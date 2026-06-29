package models

import (
	"testing"
	"time"
)

// TestSICToDamodaran_ReferentialIntegrity is the RM-2 Phase 2 CI integrity gate.
// It enforces, at build time, the invariant the runtime lookup guard only
// degrades around: every industry name the SIC crosswalk points at MUST exist as
// a key in the Damodaran multiples table. A dangling reference (typo, or an
// industry renamed by a refresh) would silently fall back to the Phase 1 bucket
// for an entire SIC class; this test fails loudly instead.
//
// Deviation note (RM-2.2.7): the tracker's original acceptance criterion was a
// check that every SIC seen in the last 60 days of request logs is covered. We
// have no request-log corpus in CI, so this static referential-integrity check
// substitutes for it — it guarantees the crosswalk can never reference a
// non-existent Damodaran industry, which is the load-bearing safety property.
// Partial SIC coverage is by design (unmapped SICs degrade to Phase 1); this
// gate does NOT assert completeness, only that what IS mapped resolves.
func TestSICToDamodaran_ReferentialIntegrity(t *testing.T) {
	table, _, err := loadDamodaranMultiples()
	if err != nil {
		t.Fatalf("loadDamodaranMultiples error: %v", err)
	}
	xwalk, err := loadSICToDamodaran()
	if err != nil {
		t.Fatalf("loadSICToDamodaran error: %v", err)
	}
	if len(table) == 0 {
		t.Fatal("Damodaran multiples table is empty")
	}
	if len(xwalk) == 0 {
		t.Fatal("SIC->Damodaran crosswalk is empty")
	}

	for sic, industry := range xwalk {
		if _, ok := table[industry]; !ok {
			t.Errorf("dangling crosswalk entry: SIC %q -> %q, which is not a key in damodaran_sector_multiples.json", sic, industry)
		}
	}
}

// TestDamodaranDatasetDate_Parses asserts the committed dataset_date is present
// and parses as YYYY-MM-DD, so the provenance string surfaced on the response
// (industry.multiple_source = "Damodaran <date>") is always a real date.
func TestDamodaranDatasetDate_Parses(t *testing.T) {
	_, date, err := loadDamodaranMultiples()
	if err != nil {
		t.Fatalf("loadDamodaranMultiples error: %v", err)
	}
	if date == "" {
		t.Fatal("dataset_date is empty")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		t.Errorf("dataset_date %q does not parse as YYYY-MM-DD: %v", date, err)
	}
}
