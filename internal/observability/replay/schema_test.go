package replay

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/guidance"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
)

// TestCurrentSchemaVersions_HasAllKnownProducers is a static pin: the
// CurrentSchemaVersions map MUST contain every entity that any producer in
// the codebase calls b.AddSchemaVersion for. Discovering a missing entry
// requires a code-search through the producers, which is what this list
// re-encodes (§12 spec lint row).
//
// When a new producer is added that stamps a new entity, append it here in
// the same commit that adds the producer. The R2 integration test (which
// will run a real OpenBundle round-trip) is the ultimate check; this
// static pin is the R1 stand-in so the schema map cannot drift unnoticed
// before R2 lands.
func TestCurrentSchemaVersions_HasAllKnownProducers(t *testing.T) {
	// Order mirrors the producer list in schema.go's package doc.
	expected := []string{
		"FinancialData",     // datacleaner/service.go
		"GrowthEstimate",    // valuation/service.go
		"ValuationResult",   // valuation/service.go
		"FairValueResponse", // api/v1/handlers/fair_value.go
		"SECCompanyFacts",   // infra/gateways/sec/client.go
		"MarketData",        // infra/gateways/market/yfinance_client.go
		"MacroData",         // infra/gateways/macro/gateway.go
		// RPL-10 (#28): these two were stamped by the artifact bundle
		// (SetAssumptionProfileManifest / SetGuidanceResolution) but absent
		// from CurrentSchemaVersions, so every fresh bundle showed schema
		// drift. The static list above failed to catch the omission; the
		// dynamic round-trip guard below now does as well.
		"AssumptionProfileManifest", // observability/artifact/bundle.go
		"GuidanceResolution",        // observability/artifact/bundle.go
	}
	for _, e := range expected {
		if _, ok := CurrentSchemaVersions[e]; !ok {
			t.Errorf("CurrentSchemaVersions missing producer-stamped entity %q", e)
		}
	}
}

// TestCurrentSchemaVersions_RegistersEveryStampedEntity is the RPL-10 (#28)
// teeth: it drives the REAL artifact producers (the same SetX calls
// production code makes) through a hermetic OpenBundle round-trip, reads the
// manifest back off disk, and asserts every entity the bundle stamped is
// registered in CurrentSchemaVersions. This is the dynamic guard the static
// list above could not be — if a future producer stamps an entity without
// registering it here, this fails the build instead of silently forcing
// --allow-schema-drift on every fresh bundle.
//
// Producers exercised: SetAssumptionProfileManifest + SetGuidanceResolution
// (the two RPL-10 regression entities) plus a cleaner-style
// AddSchemaVersion("FinancialData", ...) so the round-trip also covers the
// direct stamping path.
func TestCurrentSchemaVersions_RegistersEveryStampedEntity(t *testing.T) {
	cfg := artifact.Config{Enabled: true, RootPath: t.TempDir()}
	b, err := artifact.OpenBundle(cfg, "rid-rpl10", "AMD", artifact.TriggerQuery)
	if err != nil {
		t.Fatalf("OpenBundle: %v", err)
	}
	if b == nil {
		t.Fatal("OpenBundle returned nil bundle")
	}

	ctx := context.Background()
	// Zero-value manifest/stage are sufficient: we assert on the stamped
	// schema_versions keys, not on the snapshot bodies.
	b.SetAssumptionProfileManifest(ctx, profile.AssumptionProfileManifest{})
	b.SetGuidanceResolution(ctx, guidance.BundleStage{
		Resolution: guidance.ResolutionEnvelope{Status: "absent"},
	})
	b.AddSchemaVersion("FinancialData", CurrentSchemaVersions["FinancialData"])

	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mfBody, err := os.ReadFile(filepath.Join(b.Root(), "00-manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var mf artifact.Manifest
	if err := json.Unmarshal(mfBody, &mf); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}

	if len(mf.SchemaVersions) == 0 {
		t.Fatal("manifest stamped no schema_versions; round-trip produced nothing to check")
	}
	for entity, stampedVer := range mf.SchemaVersions {
		curVer, ok := CurrentSchemaVersions[entity]
		if !ok {
			t.Errorf("bundle stamped %q (v%d) but CurrentSchemaVersions does not register it "+
				"(RPL-10 class regression — add it to the map in the same commit as the producer)",
				entity, stampedVer)
			continue
		}
		if curVer != stampedVer {
			t.Errorf("bundle stamped %q at v%d but CurrentSchemaVersions has v%d", entity, stampedVer, curVer)
		}
	}
}

// TestCurrentSchemaVersions_NonZeroVersions catches the common bug where a
// new entity is added with version 0 (a placeholder that means "missing"
// in CompareSchemaVersions). Every entry must be ≥ 1.
func TestCurrentSchemaVersions_NonZeroVersions(t *testing.T) {
	for entity, ver := range CurrentSchemaVersions {
		if ver < 1 {
			t.Errorf("CurrentSchemaVersions[%q] = %d, must be >= 1", entity, ver)
		}
	}
}

// TestCompareSchemaVersions_NoDrift verifies a manifest stamping exactly
// the current versions reports zero drift.
func TestCompareSchemaVersions_NoDrift(t *testing.T) {
	manifestVers := map[string]int{}
	maps.Copy(manifestVers, CurrentSchemaVersions)
	rpt := CompareSchemaVersions(manifestVers)
	if rpt.HasDrift() {
		t.Fatalf("expected no drift; got %d entries: %+v", len(rpt.Entries), rpt.Entries)
	}
}

// TestCompareSchemaVersions_VersionMismatch verifies the canonical drift
// case (bundle has older version, current code has newer).
func TestCompareSchemaVersions_VersionMismatch(t *testing.T) {
	manifestVers := map[string]int{}
	maps.Copy(manifestVers, CurrentSchemaVersions)
	// Roll FinancialData back by one to simulate a v6 bundle replayed
	// against v7 code.
	manifestVers["FinancialData"] = CurrentSchemaVersions["FinancialData"] - 1

	rpt := CompareSchemaVersions(manifestVers)
	if !rpt.HasDrift() {
		t.Fatal("expected drift")
	}
	if len(rpt.Entries) != 1 {
		t.Fatalf("want 1 drift entry, got %d: %+v", len(rpt.Entries), rpt.Entries)
	}
	e := rpt.Entries[0]
	if e.Entity != "FinancialData" {
		t.Errorf("Entity = %q", e.Entity)
	}
	if e.BundleVersion != CurrentSchemaVersions["FinancialData"]-1 {
		t.Errorf("BundleVersion = %d", e.BundleVersion)
	}
	if e.CurrentVersion != CurrentSchemaVersions["FinancialData"] {
		t.Errorf("CurrentVersion = %d", e.CurrentVersion)
	}
	if e.MissingFromBundle || e.MissingFromCurrent {
		t.Errorf("Missing flags should be false for a value mismatch; got %+v", e)
	}
}

// TestCompareSchemaVersions_MissingFromCurrent verifies a bundle stamping
// an entity replay's map doesn't know about is reported as drift.
func TestCompareSchemaVersions_MissingFromCurrent(t *testing.T) {
	manifestVers := map[string]int{
		"UnknownEntity": 5,
	}
	maps.Copy(manifestVers, CurrentSchemaVersions)

	rpt := CompareSchemaVersions(manifestVers)
	if !rpt.HasDrift() {
		t.Fatal("expected drift")
	}
	var found bool
	for _, e := range rpt.Entries {
		if e.Entity == "UnknownEntity" {
			found = true
			if !e.MissingFromCurrent {
				t.Errorf("MissingFromCurrent should be true; got %+v", e)
			}
			if e.BundleVersion != 5 {
				t.Errorf("BundleVersion = %d, want 5", e.BundleVersion)
			}
		}
	}
	if !found {
		t.Errorf("expected drift entry for UnknownEntity; got %+v", rpt.Entries)
	}
}

// TestCompareSchemaVersions_MissingFromBundle covers the replay-old-bundle
// case: a producer added since capture is reported as drift in the other
// direction.
func TestCompareSchemaVersions_MissingFromBundle(t *testing.T) {
	manifestVers := map[string]int{}
	maps.Copy(manifestVers, CurrentSchemaVersions)
	// Drop one entity to simulate a producer not yet present at capture
	// time.
	delete(manifestVers, "MacroData")

	rpt := CompareSchemaVersions(manifestVers)
	if !rpt.HasDrift() {
		t.Fatal("expected drift")
	}
	var found bool
	for _, e := range rpt.Entries {
		if e.Entity == "MacroData" && e.MissingFromBundle {
			found = true
		}
	}
	if !found {
		t.Errorf("expected MissingFromBundle entry for MacroData; got %+v", rpt.Entries)
	}
}

// TestCompareSchemaVersions_DeterministicSort confirms entries are sorted
// alphabetically by Entity name. Important for stable golden-file output
// once the JSON renderer pins it.
func TestCompareSchemaVersions_DeterministicSort(t *testing.T) {
	manifestVers := map[string]int{
		"Zeta":  1, // unknown to current (MissingFromCurrent)
		"Alpha": 1, // unknown
		"Beta":  1, // unknown
	}
	rpt := CompareSchemaVersions(manifestVers)
	prev := ""
	for _, e := range rpt.Entries {
		if prev != "" && e.Entity < prev {
			t.Errorf("entries not sorted: %s came after %s", e.Entity, prev)
		}
		prev = e.Entity
	}
}

// TestCompareManifestSchemas_NilManifest defends the convenience wrapper
// against a nil call site (defensive — avoids a panic if a future caller
// forgets to nil-check after a failed ReadManifest).
func TestCompareManifestSchemas_NilManifest(t *testing.T) {
	rpt := CompareManifestSchemas(nil)
	if rpt == nil {
		t.Fatal("CompareManifestSchemas(nil) returned nil")
	}
	if rpt.HasDrift() {
		t.Errorf("nil manifest should report no drift; got %+v", rpt.Entries)
	}
}

// TestCompareManifestSchemas_RealManifestStruct routes the real
// artifact.Manifest type through CompareManifestSchemas so the public
// shim stays correct under any future Manifest-field rearrangement.
func TestCompareManifestSchemas_RealManifestStruct(t *testing.T) {
	mf := &artifact.Manifest{
		SchemaVersions: map[string]int{},
	}
	maps.Copy(mf.SchemaVersions, CurrentSchemaVersions)
	rpt := CompareManifestSchemas(mf)
	if rpt.HasDrift() {
		t.Fatalf("expected no drift for current-aligned manifest; got %+v", rpt.Entries)
	}
}
