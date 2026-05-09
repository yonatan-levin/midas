// Package replay — stage_diff.go.
//
// Stage K (Phase 2.D R3b) — per-stage JSON diff support for the
// `--diff-stages` CLI flag. When enabled, replay reads the bundle's
// recorded intermediate-stage JSON files and diffs them against the
// engine's freshly-computed snapshots from the current run.
//
// Design choices (per R3b plan §3 Stage K, Decisions K.1 and K.2):
//
//   - Bundle side: `os.ReadFile(<bundleDir>/<stageFile>)`. Reading the
//     pre-captured files matches the user's mental model — "what's saved
//     in this bundle vs what the engine produces today" — and decouples
//     stage-diff from entity-shape evolution. If a stage file added a new
//     field next quarter, the bundle's saved JSON would still parse via
//     `map[string]any` and the new field surfaces as a `current_only`
//     StringDiff, NOT a parser error.
//
//   - Current side: bytes passed in by the caller. The Replay()
//     orchestrator captures snapshots into a tempdir bundle (in-memory in
//     spirit — created and removed within Replay()) and reads them back.
//     This preserves the spec D7 invariant ("replay produces no bundles
//     of bundles") because the tempdir is ephemeral.
//
//   - Asymmetric absences (file present on one side, missing on the
//     other) are recorded as a single StringDiff at path
//     `stages.<filename>.bundle_missing` or `stages.<filename>.current_missing`.
//     This matches the Pre-K.A check finding: not every bundle ships every
//     stage file (e.g. non-DCF model paths skip 15-valuation.json), so the
//     diff path treats absent-on-both as "no diff" and absent-on-one as a
//     surfaced asymmetry.
//
//   - The walker is a hand-written `map[string]any` traversal rather than
//     go-cmp because the JSON shapes drift between stages and across
//     versions. go-cmp over heterogeneous shapes over-reports nil-vs-zero
//     and empty-string-vs-omitted distinctions that are not meaningful
//     drift here.
package replay

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
)

// StageDiffInventory enumerates the bundle JSON files Stage K diffs.
// Order is significant for output rendering; tests pin the slice
// contents.
//
// Producers (verified against `b.Snapshot(...)` call sites at the
// master HEAD as of 2026-05-08):
//   - `10-clean-output.json` — internal/services/datacleaner/service.go:283
//   - `12-growth-curve.json` — internal/services/valuation/service.go:625
//   - `13-wacc.json` — internal/services/valuation/service.go:736
//   - `15-valuation.json` — internal/services/valuation/service.go:1234
//
// Pre-K.A check: 15-valuation.json is sometimes absent (non-DCF model
// paths skip it) — handled via the asymmetric-absence marker convention.
var StageDiffInventory = []string{
	"10-clean-output.json",
	"12-growth-curve.json",
	"13-wacc.json",
	"15-valuation.json",
}

// StageDiff is the per-stage diff record. Embedded into Result.StageDiffs.
// Mirrors Result's own diff-field shape so renderers can reuse helpers.
//
// Float drift inside tolerance lands in DriftedWithinTolerance; outside
// tolerance lands in Floats. String/bool/int mismatches and asymmetric
// absences land in Strings.
type StageDiff struct {
	Floats                 []FloatDiff  `json:"floats,omitempty"`
	Strings                []StringDiff `json:"strings,omitempty"`
	DriftedWithinTolerance []FloatDiff  `json:"drifted_within_tolerance,omitempty"`
}

// HasMismatch reports whether the StageDiff carries any out-of-tolerance
// float diff or any string-typed diff (which includes the asymmetric-
// absence markers). DriftedWithinTolerance entries do NOT count.
func (sd StageDiff) HasMismatch() bool {
	return len(sd.Floats) > 0 || len(sd.Strings) > 0
}

// diffStage compares <bundleDir>/<stageFile> against the engine-produced
// `current` bytes. Returns the per-field diff record.
//
// Asymmetric absences: when bundle file is missing but current bytes are
// present (or vice versa), the diff records ONE StringDiff at path
// `stages.<filename>.bundle_missing` / `.current_missing` so the operator
// sees the asymmetry without being buried under a synthetic per-field
// dump. Both-sides-absent yields an empty StageDiff (no drift to report).
//
// Both inputs are JSON byte payloads, NOT structured types, so this
// function is decoupled from entity-shape evolution. A new field on either
// side surfaces as an asymmetric `*_only` StringDiff rather than a parse
// error.
//
// relTol/absTol of 0 mean "use the diff layer's defaults"
// (DefaultFloatRelTol / DefaultFloatAbsTol). This mirrors the Replay()
// orchestrator's tolerance-resolution logic.
func diffStage(bundleDir, stageFile string, current []byte, relTol, absTol float64) StageDiff {
	if relTol == 0 {
		relTol = DefaultFloatRelTol
	}
	if absTol == 0 {
		absTol = DefaultFloatAbsTol
	}

	bundlePath := filepath.Join(bundleDir, stageFile)
	bundleBytes, bundleErr := os.ReadFile(bundlePath)
	bundleAbsent := errors.Is(bundleErr, os.ErrNotExist)
	currentAbsent := len(current) == 0

	// Both sides absent: no drift. Common for non-DCF model paths that
	// skip `15-valuation.json`.
	if bundleAbsent && currentAbsent {
		return StageDiff{}
	}

	// Bundle side missing but current produced one: asymmetric absence.
	if bundleAbsent && !currentAbsent {
		return StageDiff{
			Strings: []StringDiff{{
				Path: fmt.Sprintf("stages.%s.bundle_missing", stageFile),
				Old:  "absent",
				New:  "present",
			}},
		}
	}

	// Current side missing but bundle has one: asymmetric absence.
	if !bundleAbsent && currentAbsent {
		return StageDiff{
			Strings: []StringDiff{{
				Path: fmt.Sprintf("stages.%s.current_missing", stageFile),
				Old:  "present",
				New:  "absent",
			}},
		}
	}

	// Bundle file exists but read failed for some other reason (perms,
	// I/O error). Surface as a StringDiff so the operator sees the
	// failure rather than a silent zero-diff.
	if bundleErr != nil {
		return StageDiff{
			Strings: []StringDiff{{
				Path: fmt.Sprintf("stages.%s.read_error", stageFile),
				Old:  bundleErr.Error(),
				New:  "",
			}},
		}
	}

	// Both sides present — parse and walk.
	var bundleVal, currentVal any
	if err := json.Unmarshal(bundleBytes, &bundleVal); err != nil {
		return StageDiff{
			Strings: []StringDiff{{
				Path: fmt.Sprintf("stages.%s.bundle_parse_error", stageFile),
				Old:  err.Error(),
				New:  "",
			}},
		}
	}
	if err := json.Unmarshal(current, &currentVal); err != nil {
		return StageDiff{
			Strings: []StringDiff{{
				Path: fmt.Sprintf("stages.%s.current_parse_error", stageFile),
				Old:  err.Error(),
				New:  "",
			}},
		}
	}

	w := &stageWalker{
		stageFile: stageFile,
		relTol:    relTol,
		absTol:    absTol,
	}
	w.walk("", bundleVal, currentVal)

	// Stable sort for deterministic output.
	sort.Slice(w.floats, func(i, j int) bool { return w.floats[i].Path < w.floats[j].Path })
	sort.Slice(w.strings, func(i, j int) bool { return w.strings[i].Path < w.strings[j].Path })
	sort.Slice(w.driftedWithin, func(i, j int) bool { return w.driftedWithin[i].Path < w.driftedWithin[j].Path })

	return StageDiff{
		Floats:                 w.floats,
		Strings:                w.strings,
		DriftedWithinTolerance: w.driftedWithin,
	}
}

// stageWalker accumulates per-field diffs while recursively walking a
// pair of `map[string]any` JSON trees. Path prefix keeps a dotted
// breadcrumb so diff entries are grep-able against the source JSON.
type stageWalker struct {
	stageFile     string
	relTol        float64
	absTol        float64
	floats        []FloatDiff
	strings       []StringDiff
	driftedWithin []FloatDiff
}

// pathFor builds a path string of the form
// `stages.<stageFile>.<dotted-field-path>`. Empty fieldPath produces
// `stages.<stageFile>` (the root); recursive callers extend via
// childPath.
func (w *stageWalker) pathFor(fieldPath string) string {
	if fieldPath == "" {
		return fmt.Sprintf("stages.%s", w.stageFile)
	}
	return fmt.Sprintf("stages.%s.%s", w.stageFile, fieldPath)
}

// childPath joins a parent dotted path with a child segment. Empty
// parent yields the child unchanged.
func childPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

// walk recursively compares two JSON values at fieldPath. Type drift
// (e.g. number became string) is recorded as a single StringDiff at
// the field's path; it does NOT recurse further into the mismatched
// branch.
func (w *stageWalker) walk(fieldPath string, bundleVal, currentVal any) {
	// Type-mismatched leaves: surface a StringDiff and stop recursing
	// into the mismatched branch.
	if !sameJSONKind(bundleVal, currentVal) {
		w.strings = append(w.strings, StringDiff{
			Path: w.pathFor(fieldPath),
			Old:  fmt.Sprintf("%v", bundleVal),
			New:  fmt.Sprintf("%v", currentVal),
		})
		return
	}

	switch b := bundleVal.(type) {
	case map[string]any:
		c := currentVal.(map[string]any)
		w.walkMap(fieldPath, b, c)
	case []any:
		c := currentVal.([]any)
		w.walkSlice(fieldPath, b, c)
	case float64:
		w.compareFloat(fieldPath, b, currentVal.(float64))
	case string:
		if b != currentVal.(string) {
			w.strings = append(w.strings, StringDiff{
				Path: w.pathFor(fieldPath),
				Old:  b,
				New:  currentVal.(string),
			})
		}
	case bool:
		if b != currentVal.(bool) {
			w.strings = append(w.strings, StringDiff{
				Path: w.pathFor(fieldPath),
				Old:  fmt.Sprintf("%t", b),
				New:  fmt.Sprintf("%t", currentVal.(bool)),
			})
		}
	case nil:
		// Both nil — equal; no diff.
	default:
		// Unknown leaf kind — fall back to stringification.
		if !genericEqual(bundleVal, currentVal) {
			w.strings = append(w.strings, StringDiff{
				Path: w.pathFor(fieldPath),
				Old:  fmt.Sprintf("%v", bundleVal),
				New:  fmt.Sprintf("%v", currentVal),
			})
		}
	}
}

// walkMap walks two object trees. Keys present on only one side are
// recorded as `<path>.<key>.bundle_only` / `.current_only` StringDiffs
// so a new field on either side surfaces explicitly rather than as
// silent drift.
func (w *stageWalker) walkMap(fieldPath string, bundle, current map[string]any) {
	// Walk shared keys + bundle-only keys.
	for k, bv := range bundle {
		if cv, ok := current[k]; ok {
			w.walk(childPath(fieldPath, k), bv, cv)
		} else {
			w.strings = append(w.strings, StringDiff{
				Path: w.pathFor(childPath(fieldPath, k+".bundle_only")),
				Old:  fmt.Sprintf("%v", bv),
				New:  "absent",
			})
		}
	}
	// Walk current-only keys.
	for k, cv := range current {
		if _, ok := bundle[k]; !ok {
			w.strings = append(w.strings, StringDiff{
				Path: w.pathFor(childPath(fieldPath, k+".current_only")),
				Old:  "absent",
				New:  fmt.Sprintf("%v", cv),
			})
		}
	}
}

// walkSlice walks two array branches. Length mismatches are recorded as
// a single StringDiff at the parent path; matching positions recurse.
// We do NOT attempt structural alignment on length differences because
// stage files are typically scalar maps; arrays appearing here are
// growth curves and similar small fixed-length sequences.
func (w *stageWalker) walkSlice(fieldPath string, bundle, current []any) {
	if len(bundle) != len(current) {
		w.strings = append(w.strings, StringDiff{
			Path: w.pathFor(childPath(fieldPath, "length")),
			Old:  fmt.Sprintf("%d", len(bundle)),
			New:  fmt.Sprintf("%d", len(current)),
		})
		// Still walk the prefix in case a per-element drift is the more
		// useful diagnostic; it covers the common case where one side
		// just added a new tail element.
	}
	n := min(len(bundle), len(current))
	for i := 0; i < n; i++ {
		w.walk(childPath(fieldPath, fmt.Sprintf("[%d]", i)), bundle[i], current[i])
	}
}

// compareFloat invokes the package's float-comparison primitive and
// classifies the result. Drift inside tolerance lands in
// DriftedWithinTolerance; outside-tolerance lands in Floats. NaN-vs-NaN
// is treated as equal to match CompareFloat's contract.
func (w *stageWalker) compareFloat(fieldPath string, bundle, current float64) {
	if math.IsNaN(bundle) && math.IsNaN(current) {
		return
	}
	fd := FloatDiffOf(w.pathFor(fieldPath), bundle, current, w.relTol, w.absTol)
	if !fd.WithinTolerance {
		w.floats = append(w.floats, fd)
		return
	}
	if fd.AbsDrift > 0 {
		w.driftedWithin = append(w.driftedWithin, fd)
	}
}

// sameJSONKind reports whether two JSON-decoded values share the same
// underlying JSON kind (object / array / number / string / bool / null).
// Used to short-circuit type-drift cases where recursion would emit
// noisy/uninformative leaf diffs.
func sameJSONKind(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	switch a.(type) {
	case map[string]any:
		_, ok := b.(map[string]any)
		return ok
	case []any:
		_, ok := b.([]any)
		return ok
	case float64:
		_, ok := b.(float64)
		return ok
	case string:
		_, ok := b.(string)
		return ok
	case bool:
		_, ok := b.(bool)
		return ok
	default:
		return false
	}
}

// genericEqual is the catch-all leaf comparator for kinds outside
// the canonical JSON-decoder set (which should be unreachable for
// `encoding/json` output but exists as a defensive fallback).
func genericEqual(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}
