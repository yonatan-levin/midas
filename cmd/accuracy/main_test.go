package main

import (
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// hasFlag reports whether flag f is present in the row's flag set.
func hasFlag(flags []string, f string) bool {
	return slices.Contains(flags, f)
}

func TestAnalyze(t *testing.T) {
	tests := []struct {
		name          string
		resp          Response
		sel           ModelSelection
		wantGapApprox float64 // (intrinsic/price - 1) * 100
		wantNegYears  int
		wantFlags     []string // flags that MUST be present
		denyFlags     []string // flags that MUST be absent
	}{
		{
			// NVDA signature: every explicit-year FCF PV negative, terminal > 100% of EV,
			// DCF says ~$29.6 vs $215.5 market, sanity-check still "reasonable".
			name: "nvda_terminal_dominance_negative_fcf",
			resp: Response{
				Ticker:             "NVDA",
				DCFValuePerShare:   29.595174836237682,
				CurrentPrice:       215.539,
				CalculationMethod:  "multi_stage_dcf",
				DCFPerYearPV:       []float64{-3.85e9, -4.91e9, -6.26e9, -7.58e9, -8.68e9},
				DCFTerminalPctOfEV: 1.043370510504372,
				SanityCheck:        sanity(true),
				Industry:           industry("MFG_SEMI", true),
			},
			sel:           ModelSelection{Model: "dcf"},
			wantGapApprox: -86.27,
			wantNegYears:  5,
			wantFlags:     []string{flagTerminalDominance, flagNegFCFYears, flagExtremeGap, flagSanityBlindspot},
			denyFlags:     []string{flagNegIntrinsic, flagModelDivergence, flagClassifierMismatch},
		},
		{
			// JPM signature: router selects DDM but engine computes multi-stage DCF
			// (the T2-BS-1 DividendsPerShare=0 fallback). Model divergence must fire.
			name: "jpm_model_divergence_ddm_vs_dcf",
			resp: Response{
				Ticker:             "JPM",
				DCFValuePerShare:   116.85564068363777,
				CurrentPrice:       300.79,
				CalculationMethod:  "multi_stage_dcf",
				DCFTerminalPctOfEV: 0.7969095027664843,
				SanityCheck:        sanity(true),
				Industry:           industry("FIN_BANK", true),
			},
			sel:           ModelSelection{Model: "ddm"},
			wantGapApprox: -61.15,
			wantNegYears:  0,
			wantFlags:     []string{flagModelDivergence, flagExtremeGap, flagSanityBlindspot},
			denyFlags:     []string{flagTerminalDominance, flagNegIntrinsic, flagNegFCFYears},
		},
		{
			// KO mirrors the real 4.4 KO bundle: negative intrinsic value with every
			// explicit-year FCF negative (a model breakdown — see BUG-014), and the
			// sanity check still rates it "reasonable" → SANITY_BLINDSPOT. The
			// dcf_per_year_pv values are KO's actual captured values.
			name: "ko_negative_intrinsic_real_shape",
			resp: Response{
				Ticker:            "KO",
				DCFValuePerShare:  -14.76851362892328,
				CurrentPrice:      78.68,
				CalculationMethod: "multi_stage_dcf",
				DCFPerYearPV:      []float64{-4253919224.12, -4109708816.87, -3970387228.71, -3835788725.77, -3705753192.62},
				SanityCheck:       sanity(true),
				Industry:          industry("MFG", true),
			},
			sel:           ModelSelection{Model: "dcf"},
			wantGapApprox: -118.77,
			wantNegYears:  5,
			wantFlags:     []string{flagNegIntrinsic, flagNegFCFYears, flagExtremeGap, flagSanityBlindspot},
			denyFlags:     []string{flagTerminalDominance, flagModelDivergence, flagClassifierMismatch},
		},
		{
			// Synthetic: negative intrinsic but sanity NOT reasonable → SANITY_BLINDSPOT
			// must NOT fire. Preserves the is_reasonable=false branch (no real basket
			// ticker exercises it — KO/AMD both report is_reasonable=true).
			name: "negative_unreasonable_no_blindspot",
			resp: Response{
				Ticker:            "SYN",
				DCFValuePerShare:  -10,
				CurrentPrice:      50,
				CalculationMethod: "multi_stage_dcf",
				SanityCheck:       sanity(false),
				Industry:          industry("TECH", true),
			},
			sel:           ModelSelection{Model: "dcf"},
			wantGapApprox: -120,
			wantNegYears:  0,
			wantFlags:     []string{flagNegIntrinsic, flagExtremeGap},
			denyFlags:     []string{flagSanityBlindspot, flagModelDivergence, flagNegFCFYears},
		},
		{
			// Healthy control: intrinsic close to price, positive FCF, terminal well-bounded,
			// model agreement, classifier match. No flags should fire.
			name: "healthy_no_flags",
			resp: Response{
				Ticker:             "GOOD",
				DCFValuePerShare:   100,
				CurrentPrice:       95,
				CalculationMethod:  "multi_stage_dcf",
				DCFPerYearPV:       []float64{1e9, 1.1e9, 1.2e9, 1.3e9, 1.4e9},
				DCFTerminalPctOfEV: 0.60,
				SanityCheck:        sanity(true),
				Industry:           industry("TECH", true),
			},
			sel:           ModelSelection{Model: "dcf"},
			wantGapApprox: 5.26,
			wantNegYears:  0,
			wantFlags:     nil,
			denyFlags: []string{
				flagNegIntrinsic, flagTerminalDominance, flagNegFCFYears,
				flagModelDivergence, flagExtremeGap, flagSanityBlindspot, flagClassifierMismatch,
			},
		},
		{
			// Classifier disagreement (SIC vs heuristic) must surface independent of valuation.
			name: "classifier_mismatch",
			resp: Response{
				Ticker:            "AMBR",
				DCFValuePerShare:  50,
				CurrentPrice:      48,
				CalculationMethod: "multi_stage_dcf",
				SanityCheck:       sanity(true),
				Industry:          industry("MFG_SEMI", false),
			},
			sel:           ModelSelection{Model: "dcf"},
			wantGapApprox: 4.17,
			wantFlags:     []string{flagClassifierMismatch},
			denyFlags:     []string{flagExtremeGap},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyze(tt.resp, tt.sel)

			if math.Abs(got.GapPct-tt.wantGapApprox) > 0.1 {
				t.Errorf("GapPct = %.2f, want ~%.2f", got.GapPct, tt.wantGapApprox)
			}
			if got.NegFCFYears != tt.wantNegYears {
				t.Errorf("NegFCFYears = %d, want %d", got.NegFCFYears, tt.wantNegYears)
			}
			for _, f := range tt.wantFlags {
				if !hasFlag(got.Flags, f) {
					t.Errorf("missing expected flag %q (got %v)", f, got.Flags)
				}
			}
			for _, f := range tt.denyFlags {
				if hasFlag(got.Flags, f) {
					t.Errorf("unexpected flag %q present (got %v)", f, got.Flags)
				}
			}
		})
	}
}

func TestMethodFamily(t *testing.T) {
	cases := map[string]string{
		"multi_stage_dcf":         "dcf",
		"dcf":                     "dcf",
		"ddm":                     "ddm",
		"dividend_discount_model": "ddm",
		"ffo":                     "ffo",
		"revenue_multiple":        "revenue_multiple",
		"":                        "",
	}
	for in, want := range cases {
		if got := methodFamily(in); got != want {
			t.Errorf("methodFamily(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSortByAbsGap(t *testing.T) {
	rows := []Row{
		{Ticker: "A", GapPct: -10},
		{Ticker: "B", GapPct: -90},
		{Ticker: "C", GapPct: 50},
	}
	sort.Sort(byAbsGap(rows))
	if rows[0].Ticker != "B" || rows[1].Ticker != "C" || rows[2].Ticker != "A" {
		t.Errorf("byAbsGap order = %s,%s,%s; want B,C,A", rows[0].Ticker, rows[1].Ticker, rows[2].Ticker)
	}
}

// TestCollectRows_LoadPath exercises the filesystem load path end-to-end on a
// synthetic baseline: a good bundle is analyzed, a malformed response is skipped
// (and surfaced), and a price==0 response yields GapPct==0 with no EXTREME_GAP.
func TestCollectRows_LoadPath(t *testing.T) {
	root := t.TempDir()

	writeBundle := func(ticker, reqResponse, modelSel string) {
		bundle := filepath.Join(root, ticker, "req_"+ticker)
		if err := os.MkdirAll(bundle, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bundle, "17-response.json"), []byte(reqResponse), 0o644); err != nil {
			t.Fatal(err)
		}
		if modelSel != "" {
			if err := os.WriteFile(filepath.Join(bundle, "14-model-selection.json"), []byte(modelSel), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Good DCF bundle, intrinsic far below price.
	writeBundle("GOOD", `{"ticker":"GOOD","dcf_value_per_share":30,"current_price":200,"calculation_version":"4.4","calculation_method":"multi_stage_dcf","sanity_check":{"is_reasonable":false},"industry":{"sic":"TECH","match":true}}`,
		`{"model":"dcf"}`)
	// Malformed response -> must be skipped, not panic.
	writeBundle("BADJSON", `{"ticker":"BADJSON", not json`, "")
	// price==0 -> GapPct must stay 0, no EXTREME_GAP, no divide-by-zero.
	writeBundle("ZEROPX", `{"ticker":"ZEROPX","dcf_value_per_share":42,"current_price":0,"calculation_version":"4.4","calculation_method":"multi_stage_dcf","sanity_check":{"is_reasonable":true},"industry":{"sic":"TECH","match":true}}`,
		`{"model":"dcf"}`)
	// Fractional data_quality_score (handler contract is float64) must NOT break
	// the decode and drop the ticker — the field is intentionally not decoded.
	writeBundle("FRAC", `{"ticker":"FRAC","dcf_value_per_share":90,"current_price":100,"data_quality_score":85.5,"calculation_version":"4.4","calculation_method":"multi_stage_dcf","sanity_check":{"is_reasonable":true},"industry":{"sic":"TECH","match":true}}`,
		`{"model":"dcf"}`)

	rows, skipped, err := collectRows(root)
	if err != nil {
		t.Fatalf("collectRows: %v", err)
	}

	byTicker := map[string]Row{}
	for _, r := range rows {
		byTicker[r.Ticker] = r
	}

	if _, ok := byTicker["GOOD"]; !ok {
		t.Errorf("GOOD bundle not analyzed; rows=%v", rows)
	}
	if !slices.ContainsFunc(skipped, func(s string) bool { return len(s) >= 7 && s[:7] == "BADJSON" }) {
		t.Errorf("malformed BADJSON not surfaced in skipped: %v", skipped)
	}
	z, ok := byTicker["ZEROPX"]
	if !ok {
		t.Fatalf("ZEROPX not analyzed")
	}
	if z.GapPct != 0 {
		t.Errorf("price==0 GapPct = %v, want 0", z.GapPct)
	}
	if hasFlag(z.Flags, flagExtremeGap) {
		t.Errorf("price==0 should not trip EXTREME_GAP; flags=%v", z.Flags)
	}
	if _, ok := byTicker["FRAC"]; !ok {
		t.Errorf("fractional data_quality_score dropped the ticker; rows=%v skipped=%v", rows, skipped)
	}
}

func TestShortPath(t *testing.T) {
	cases := map[string]string{
		`C:/Users/x/midas/artifacts/tier2-baseline/2026-06-03`: "tier2-baseline/2026-06-03",
		`artifacts/tier2-baseline/2026-06-03`:                  "tier2-baseline/2026-06-03",
		`C:\Users\x\midas\artifacts\tier2-baseline\2026-06-03`: "tier2-baseline/2026-06-03",
		`tier2-baseline/2026-06-03/`:                           "tier2-baseline/2026-06-03",
		`single`:                                               "single",
		`a/b`:                                                  "a/b",
	}
	for in, want := range cases {
		if got := shortPath(in); got != want {
			t.Errorf("shortPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCalcVersionLabel(t *testing.T) {
	if got := calcVersionLabel(nil); got != "?" {
		t.Errorf("empty = %q, want ?", got)
	}
	single := []Row{{CalcVersion: "4.4"}, {CalcVersion: "4.4"}}
	if got := calcVersionLabel(single); got != "4.4" {
		t.Errorf("single-version = %q, want 4.4", got)
	}
	mixed := []Row{{CalcVersion: "4.4"}, {CalcVersion: "4.1"}}
	if got := calcVersionLabel(mixed); got != "4.4 (mixed)" {
		t.Errorf("mixed-version = %q, want '4.4 (mixed)'", got)
	}
	blank := []Row{{CalcVersion: ""}}
	if got := calcVersionLabel(blank); got != "—" {
		t.Errorf("blank-version = %q, want —", got)
	}
}

func TestRenderMarkdownAndCSV(t *testing.T) {
	rows := []Row{
		{Ticker: "AAA", CalcVersion: "4.4", SelectedModel: "ddm", ComputedMethod: "multi_stage_dcf",
			Intrinsic: 100, Price: 200, GapPct: -50.0, NegFCFYears: 3, QualityGrade: "A",
			SIC: "TECH", Flags: []string{flagModelDivergence, flagNegFCFYears}},
		{Ticker: "BBB", CalcVersion: "4.4", SelectedModel: "dcf", ComputedMethod: "multi_stage_dcf",
			Intrinsic: 50, Price: 48, GapPct: 4.2, QualityGrade: "B", SIC: "MFG", Flags: nil},
	}

	md := renderMarkdown(rows, []string{"ZZZ (no bundle)"}, "artifacts/tier2-baseline/2026-06-03")
	for _, want := range []string{
		"# Midas accuracy report", "calc-version: 4.4", "| AAA |", "MODEL_DIVERGENCE",
		"ddm→dcf", "Mean absolute price gap", "Skipped: ZZZ (no bundle)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n%s", want, md)
		}
	}

	csv := renderCSV(rows)
	if !strings.HasPrefix(csv, "ticker,selected_model,") {
		t.Errorf("csv header missing; got %q", csv[:min(40, len(csv))])
	}
	for _, want := range []string{"AAA,ddm,dcf,", "MODEL_DIVERGENCE NEG_FCF_YEARS", "BBB,dcf,dcf,"} {
		if !strings.Contains(csv, want) {
			t.Errorf("csv missing %q\n%s", want, csv)
		}
	}
}

// --- test helpers ---

func sanity(reasonable bool) sanityCheck {
	return sanityCheck{IsReasonable: reasonable}
}

func industry(sic string, match bool) industryInfo {
	return industryInfo{SIC: sic, Match: match}
}
