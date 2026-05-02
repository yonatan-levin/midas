package replay

import (
	"sort"

	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
)

// CurrentSchemaVersions enumerates the on-disk schema versions the current
// build stamps into produced bundles. This map is the source of truth that
// replay compares the bundle's manifest.schema_versions against.
//
// Hand-maintained: when a domain entity's serialized shape changes (e.g.
// FinancialData v7 -> v8 because a new field was added), the producer that
// stamps the version (e.g. internal/services/datacleaner/service.go) and
// this map must be updated together. Spec §5 D5 + §11 REVIEWER tasks
// require a CI test that round-trips a real bundle through OpenBundle and
// asserts the keys/values stamped by production are a subset of this map —
// so a producer-side bump that forgets the replay-side bump fails the
// build.
//
// Current entries reflect the producers that call b.AddSchemaVersion in
// the codebase as of Phase R1:
//   - internal/services/datacleaner/service.go            -> FinancialData=7
//   - internal/services/valuation/service.go              -> GrowthEstimate=1, ValuationResult=2
//   - internal/api/v1/handlers/fair_value.go              -> FairValueResponse=1
//   - internal/infra/gateways/sec/client.go               -> SECCompanyFacts=3
//   - internal/infra/gateways/market/yfinance_client.go   -> MarketData=1
//   - internal/infra/gateways/macro/gateway.go            -> MacroData=1
//
// If a future producer stamps a new entity, append it here in the same
// commit.
var CurrentSchemaVersions = map[string]int{
	"FinancialData":     7,
	"GrowthEstimate":    1,
	"ValuationResult":   2,
	"FairValueResponse": 1,
	"SECCompanyFacts":   3,
	"MarketData":        1,
	"MacroData":         1,
}

// SchemaDriftEntry describes a single mismatch between a bundle's stamped
// schema_versions map and CurrentSchemaVersions. Used by the text/JSON
// renderers to format the drift table; also surfaced individually so
// callers can decide policy per entity.
//
// JSON keys are snake_case to match the rest of the §7 contract — emitted
// as part of Result.SchemaDriftEntries[]. Pre-R1-follow-up these fields
// were untagged and serialized as PascalCase, locking in a broken JSON
// contract; the tags below are mandatory and the snake_case test in
// output_test.go pins it.
type SchemaDriftEntry struct {
	// Entity is the domain entity name (e.g. "FinancialData"). Stable
	// across producer versions.
	Entity string `json:"entity"`
	// BundleVersion is the value the manifest stamped at capture time. 0
	// means the manifest did not stamp this entity at all (treated as
	// drift unless OnlyMismatch is true).
	BundleVersion int `json:"bundle_version"`
	// CurrentVersion is the value the running binary would stamp today.
	// 0 means the running binary doesn't know this entity at all (the
	// bundle stamps something replay's CurrentSchemaVersions map omits) —
	// also drift, in the opposite direction.
	CurrentVersion int `json:"current_version"`
	// MissingFromCurrent is true when the bundle stamps an entity that
	// CurrentSchemaVersions doesn't track. Indicates a producer was added
	// since this replay binary was built.
	MissingFromCurrent bool `json:"missing_from_current"`
	// MissingFromBundle is true when the running binary stamps an entity
	// the bundle's manifest does not. Indicates the producer existed at
	// build time but didn't yet at bundle-capture time.
	MissingFromBundle bool `json:"missing_from_bundle"`
}

// SchemaDriftReport is the full drift comparison between a bundle's
// schema_versions and CurrentSchemaVersions. Empty Entries means the
// bundle is consistent with the current build.
type SchemaDriftReport struct {
	// Entries are the per-entity mismatches, sorted by Entity name for
	// deterministic stdout / golden tests.
	Entries []SchemaDriftEntry
}

// HasDrift returns true when any entry indicates a mismatch.
func (r *SchemaDriftReport) HasDrift() bool {
	return len(r.Entries) > 0
}

// CompareSchemaVersions diffs the bundle's manifest.schema_versions against
// CurrentSchemaVersions. Returns a report with one entry per mismatch.
//
// Symmetry: we report drift in BOTH directions —
//   - bundle has entity X at version A, current code stamps X at version B
//     where A != B → version mismatch entry.
//   - bundle stamps entity X but current code doesn't track it →
//     MissingFromCurrent (likely a producer was retired or replay's map is
//     stale).
//   - current code stamps entity X but the bundle doesn't →
//     MissingFromBundle (likely the producer didn't exist at capture time;
//     legitimate when replaying old bundles against new code).
//
// The replay binary's policy decision on each kind of drift (refuse vs
// warn) is in §5 D5 / Replay() and is gated by --allow-schema-drift.
// CompareSchemaVersions itself is policy-free — it reports facts.
func CompareSchemaVersions(manifestVersions map[string]int) *SchemaDriftReport {
	rpt := &SchemaDriftReport{}

	// Bundle-side entities: did current code change or drop this entity?
	for entity, bundleVer := range manifestVersions {
		curVer, ok := CurrentSchemaVersions[entity]
		if !ok {
			rpt.Entries = append(rpt.Entries, SchemaDriftEntry{
				Entity:             entity,
				BundleVersion:      bundleVer,
				MissingFromCurrent: true,
			})
			continue
		}
		if curVer != bundleVer {
			rpt.Entries = append(rpt.Entries, SchemaDriftEntry{
				Entity:         entity,
				BundleVersion:  bundleVer,
				CurrentVersion: curVer,
			})
		}
	}

	// Current-side entities: did the producer for this entity not exist
	// when the bundle was captured?
	for entity, curVer := range CurrentSchemaVersions {
		if _, ok := manifestVersions[entity]; ok {
			continue
		}
		rpt.Entries = append(rpt.Entries, SchemaDriftEntry{
			Entity:            entity,
			CurrentVersion:    curVer,
			MissingFromBundle: true,
		})
	}

	// Sort for deterministic output. Stable across runs simplifies golden
	// tests and CI diffs.
	sort.Slice(rpt.Entries, func(i, j int) bool {
		return rpt.Entries[i].Entity < rpt.Entries[j].Entity
	})
	return rpt
}

// CompareManifestSchemas is a thin wrapper over CompareSchemaVersions that
// takes the parsed *artifact.Manifest directly. Convenience for callers
// holding the manifest after ReadManifest.
func CompareManifestSchemas(mf *artifact.Manifest) *SchemaDriftReport {
	if mf == nil {
		return &SchemaDriftReport{}
	}
	return CompareSchemaVersions(mf.SchemaVersions)
}
