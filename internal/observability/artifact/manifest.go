package artifact

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ManifestVersion is the on-disk schema version of 00-manifest.json. Bump
// when adding/renaming fields OR when the bundle file layout changes so
// old bundles can be detected and read with the right decoder.
//
// Version history:
//   - 1.0 — initial release (Phase 2.D R0)
//   - 1.1 — adds 05-fetch-sec-submissions.{raw,parsed}.json (SIC code
//     capture) and 06-fetch-market-analyst.{raw,parsed}.json (Yahoo
//     earningsTrend capture). Closes replay-drift gaps for industry
//     classification and analyst-blend growth. Pre-1.1 bundles still
//     replay; the missing files trigger backward-compat fallbacks in the
//     replay-side bundle gateways.
const ManifestVersion = "1.1"

// Manifest is the bundle's self-describing index. Written to
// 00-manifest.json at the root of every bundle directory.
//
// Fields mirror spec §7.2; new fields MUST be additive (downstream readers
// should ignore unknowns).
type Manifest struct {
	BundleVersion     string         `json:"bundle_version"`
	RequestID         string         `json:"request_id"`
	Ticker            string         `json:"ticker"`
	Trigger           string         `json:"trigger"` // "header" | "query"
	StartedAt         string         `json:"started_at"`
	FinishedAt        string         `json:"finished_at,omitempty"`
	Outcome           string         `json:"outcome"` // ok | partial | error
	PhasesRecorded    []PhaseRecord  `json:"phases_recorded"`
	RedactionsApplied []string       `json:"redactions_applied"`
	SchemaVersions    map[string]int `json:"schema_versions"`
	GitSHA            string         `json:"git_sha,omitempty"`
	BuildVersion      string         `json:"build_version,omitempty"`
	// Notes is a free-form annotation populated by the bundle when outcome
	// degrades to "partial" — e.g. "write_failures=3 queue_drops=1". Empty
	// (and omitted from JSON) for clean bundles. Consumers should treat the
	// value as opaque; the format is stable enough for grep but not a
	// machine contract.
	Notes string `json:"notes,omitempty"`
}

// PhaseRecord is one row in the manifest's phases_recorded[] index. Tells a
// consumer which files belong to a given phase plus how many bytes were
// captured (handy for sizing decisions before reading).
type PhaseRecord struct {
	Phase string   `json:"phase"`
	Files []string `json:"files"`
	Bytes int64    `json:"bytes"`
}

// ManifestBuilder is the thread-safe accumulator for in-flight bundles. As
// snapshot writes complete, the worker calls AddPhase / AddRedactions /
// SetOutcome; at request end Finalize marshals the JSON and writes it to
// 00-manifest.json.
type ManifestBuilder struct {
	mu sync.Mutex

	manifest Manifest
	// phasesByName preserves insertion order (the order phases fired) so the
	// manifest reads naturally top-to-bottom.
	phaseOrder []string
	phaseMap   map[string]*PhaseRecord
	redactions map[string]struct{}
}

// NewManifestBuilder seeds a builder with the immutable identity fields. The
// outcome defaults to "ok" and is overridden later if any phase records an
// error.
func NewManifestBuilder(requestID, ticker, trigger, gitSHA, buildVersion string) *ManifestBuilder {
	return &ManifestBuilder{
		manifest: Manifest{
			BundleVersion:  ManifestVersion,
			RequestID:      requestID,
			Ticker:         ticker,
			Trigger:        trigger,
			StartedAt:      time.Now().UTC().Format(time.RFC3339Nano),
			Outcome:        "ok",
			SchemaVersions: map[string]int{},
			GitSHA:         gitSHA,
			BuildVersion:   buildVersion,
		},
		phaseMap:   make(map[string]*PhaseRecord),
		redactions: make(map[string]struct{}),
	}
}

// AddPhase records or extends a phase row. Idempotent for the same phase
// name — repeated calls append files and accumulate bytes.
func (b *ManifestBuilder) AddPhase(phase string, files []string, bytes int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	rec, exists := b.phaseMap[phase]
	if !exists {
		rec = &PhaseRecord{Phase: phase}
		b.phaseMap[phase] = rec
		b.phaseOrder = append(b.phaseOrder, phase)
	}
	rec.Files = append(rec.Files, files...)
	rec.Bytes += bytes
}

// AddRedactions merges a slice of redaction-paths into the unique set kept
// for the manifest's redactions_applied[] field.
func (b *ManifestBuilder) AddRedactions(paths []string) {
	if len(paths) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range paths {
		b.redactions[p] = struct{}{}
	}
}

// SetSchemaVersion records the on-disk schema version of a domain entity
// captured in the bundle (e.g. "FinancialData" -> 7).
func (b *ManifestBuilder) SetSchemaVersion(name string, version int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.manifest.SchemaVersions[name] = version
}

// SetOutcome records the bundle-level outcome. "error" overrides "ok"; "ok"
// never overrides a prior "error" (failures are sticky).
func (b *ManifestBuilder) SetOutcome(outcome string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.manifest.Outcome == "error" && outcome == "ok" {
		return
	}
	b.manifest.Outcome = outcome
}

// SetNotes records a free-form note on the manifest. Used by Bundle.Close()
// to annotate why outcome degraded to "partial" (e.g. write failures or
// queue overflow). Last writer wins; empty string clears the annotation.
func (b *ManifestBuilder) SetNotes(notes string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.manifest.Notes = notes
}

// Finalize stamps finished_at, freezes the phase index + redaction list, and
// writes the manifest as 00-manifest.json under root.
func (b *ManifestBuilder) Finalize(root string) error {
	b.mu.Lock()
	b.manifest.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)

	// Materialise phases in insertion order.
	phases := make([]PhaseRecord, 0, len(b.phaseOrder))
	for _, name := range b.phaseOrder {
		rec := b.phaseMap[name]
		// Sort files within a phase for stable output.
		sort.Strings(rec.Files)
		phases = append(phases, *rec)
	}
	b.manifest.PhasesRecorded = phases

	// Materialise redactions in sorted order so the JSON is stable.
	red := make([]string, 0, len(b.redactions))
	for p := range b.redactions {
		red = append(red, p)
	}
	sort.Strings(red)
	b.manifest.RedactionsApplied = red

	// Snapshot the manifest under the lock; the marshal happens after we
	// release it to keep the critical section short.
	snap := b.manifest
	b.mu.Unlock()

	body, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	path := filepath.Join(root, "00-manifest.json")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return fmt.Errorf("write manifest %s: %w", path, err)
	}
	return nil
}
