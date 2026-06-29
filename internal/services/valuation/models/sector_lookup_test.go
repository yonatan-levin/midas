package models

import "testing"

// TestLookupDamodaranMultiple covers the pure SIC -> Damodaran EV/Sales lookup
// across the four contract cases: mapped, unmapped, empty SIC, and a dangling
// crosswalk entry (SIC mapped to an industry absent from the table).
func TestLookupDamodaranMultiple(t *testing.T) {
	table := map[string]float64{
		"Semiconductor":                   15.7006,
		"Software (System & Application)": 11.4088,
		"Drugs (Biotechnology)":           7.918,
	}
	xwalk := map[string]string{
		"3674": "Semiconductor",
		"7372": "Software (System & Application)",
		"2836": "Drugs (Biotechnology)",
		"9999": "Nonexistent Industry", // dangling: not present in table
	}

	tests := []struct {
		name         string
		sic          string
		wantMultiple float64
		wantIndustry string
		wantOK       bool
	}{
		{name: "mapped SIC resolves multiple and industry", sic: "3674", wantMultiple: 15.7006, wantIndustry: "Semiconductor", wantOK: true},
		{name: "mapped software SIC", sic: "7372", wantMultiple: 11.4088, wantIndustry: "Software (System & Application)", wantOK: true},
		{name: "unmapped SIC returns ok=false", sic: "1234", wantMultiple: 0, wantIndustry: "", wantOK: false},
		{name: "empty SIC returns ok=false", sic: "", wantMultiple: 0, wantIndustry: "", wantOK: false},
		{name: "dangling crosswalk entry returns ok=false (runtime guard)", sic: "9999", wantMultiple: 0, wantIndustry: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mult, ind, ok := lookupDamodaranMultiple(tt.sic, xwalk, table)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if mult != tt.wantMultiple {
				t.Errorf("multiple = %v, want %v", mult, tt.wantMultiple)
			}
			if ind != tt.wantIndustry {
				t.Errorf("industry = %q, want %q", ind, tt.wantIndustry)
			}
		})
	}
}

// TestLookupDamodaranMultiple_NilMaps confirms nil maps (Damodaran tables
// absent / load failure) degrade to ok=false without panicking.
func TestLookupDamodaranMultiple_NilMaps(t *testing.T) {
	if _, _, ok := lookupDamodaranMultiple("3674", nil, nil); ok {
		t.Error("expected ok=false for nil maps")
	}
}

// TestLoadDamodaranMultiples_FromEmbed asserts the embedded table loads with a
// non-empty dataset date and at least the highest-value sectors present.
func TestLoadDamodaranMultiples_FromEmbed(t *testing.T) {
	table, date, err := loadDamodaranMultiples()
	if err != nil {
		t.Fatalf("loadDamodaranMultiples error: %v", err)
	}
	if date == "" {
		t.Error("dataset date is empty")
	}
	if len(table) == 0 {
		t.Fatal("table is empty")
	}
	if _, ok := table["Semiconductor"]; !ok {
		t.Error("Semiconductor missing from loaded table")
	}
}

// TestLoadSICToDamodaran_FromEmbed asserts the embedded crosswalk loads and
// carries the semiconductor special-case.
func TestLoadSICToDamodaran_FromEmbed(t *testing.T) {
	xwalk, err := loadSICToDamodaran()
	if err != nil {
		t.Fatalf("loadSICToDamodaran error: %v", err)
	}
	if len(xwalk) == 0 {
		t.Fatal("crosswalk is empty")
	}
	if got := xwalk["3674"]; got != "Semiconductor" {
		t.Errorf("xwalk[3674] = %q, want Semiconductor", got)
	}
}
