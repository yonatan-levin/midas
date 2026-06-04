// Command accuracy is an offline valuation-accuracy harness for Midas.
//
// It walks a directory of captured artifact bundles (the per-request trace
// bundles written by the server under ./artifacts/...), reads each ticker's
// 17-response.json and 14-model-selection.json, and produces a ranked report
// comparing the engine's intrinsic value against the market price, surfacing
// the systematic red flags that an accuracy review needs to see at a glance:
//
//   - NEG_INTRINSIC        intrinsic value < 0 (model breakdown, not conservatism)
//   - TERMINAL_DOMINANCE   terminal PV > 80% of enterprise value
//   - NEG_FCF_YEARS        one or more explicit-year FCF present values are negative
//   - MODEL_DIVERGENCE     router-selected model != computed model (e.g. DDM->DCF fallback)
//   - EXTREME_GAP          |intrinsic/price - 1| > 50%
//   - SANITY_BLINDSPOT     sanity-check says "reasonable" yet the price gap is extreme
//   - CLASSIFIER_MISMATCH  SIC classifier disagrees with the balance-sheet heuristic
//
// It is read-only and hermetic: it never touches the DB, network, or live
// engine — it only reads captured JSON. This makes it safe to run against any
// baseline directory, e.g.:
//
//	go run ./cmd/accuracy --dir artifacts/tier2-baseline/2026-06-03 --format md
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Response is the slim subset of handlers.FairValueResponse that the harness
// reads from 17-response.json. Fields absent for a given model (e.g. the DCF
// stage fields on a DDM ticker) decode to their zero value.
type Response struct {
	Ticker             string       `json:"ticker"`
	DCFValuePerShare   float64      `json:"dcf_value_per_share"`
	CurrentPrice       float64      `json:"current_price"`
	CalculationMethod  string       `json:"calculation_method"`
	CalculationVersion string       `json:"calculation_version"`
	AssumptionProfile  string       `json:"assumption_profile"`
	DCFPerYearPV       []float64    `json:"dcf_per_year_pv"`
	DCFTerminalPctOfEV float64      `json:"dcf_terminal_pct_of_ev"`
	DataQualityGrade   string       `json:"data_quality_grade"`
	SanityCheck        sanityCheck  `json:"sanity_check"`
	Industry           industryInfo `json:"industry"`
	// Note: only the fields above are read. Other response fields (e.g.
	// data_quality_score, growth_confidence, warnings) are intentionally not
	// decoded — unknown JSON keys are ignored, which also means a future shape
	// change to those fields can never break this harness.
}

type sanityCheck struct {
	IsReasonable bool `json:"is_reasonable"`
}

type industryInfo struct {
	SIC   string `json:"sic"`
	Match bool   `json:"match"`
}

// ModelSelection is 14-model-selection.json — the router's chosen model.
// Only Model is read (for MODEL_DIVERGENCE); other keys are ignored.
type ModelSelection struct {
	Model string `json:"model"`
}

// Flag identifiers (stable strings; consumed by dashboards/CSV).
const (
	flagNegIntrinsic       = "NEG_INTRINSIC"
	flagTerminalDominance  = "TERMINAL_DOMINANCE"
	flagNegFCFYears        = "NEG_FCF_YEARS"
	flagModelDivergence    = "MODEL_DIVERGENCE"
	flagExtremeGap         = "EXTREME_GAP"
	flagSanityBlindspot    = "SANITY_BLINDSPOT"
	flagClassifierMismatch = "CLASSIFIER_MISMATCH"
)

const (
	terminalDominanceThreshold = 0.80 // terminal PV share of EV
	extremeGapThreshold        = 50.0 // |intrinsic/price - 1| in percent
)

// Row is the analyzed, render-ready view of one ticker.
type Row struct {
	Ticker         string
	CalcVersion    string
	SelectedModel  string
	ComputedMethod string
	Profile        string
	Intrinsic      float64
	Price          float64
	GapPct         float64 // (intrinsic/price - 1) * 100; negative = market above intrinsic
	TerminalPct    float64
	NegFCFYears    int
	QualityGrade   string
	SIC            string
	Flags          []string
}

// methodFamily collapses a computed calculation_method or a router model name
// to a comparable family so DDM-vs-DCF style divergence can be detected.
func methodFamily(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case s == "":
		return ""
	case strings.Contains(s, "ddm"), strings.Contains(s, "dividend"):
		return "ddm"
	case strings.Contains(s, "ffo"), strings.Contains(s, "affo"):
		return "ffo"
	case strings.Contains(s, "revenue"), strings.Contains(s, "multiple"):
		return "revenue_multiple"
	case strings.Contains(s, "dcf"):
		return "dcf"
	default:
		return s
	}
}

// analyze computes metrics and derives the flag set for one bundle. Pure.
func analyze(resp Response, sel ModelSelection) Row {
	r := Row{
		Ticker:         resp.Ticker,
		CalcVersion:    resp.CalculationVersion,
		SelectedModel:  sel.Model,
		ComputedMethod: resp.CalculationMethod,
		Profile:        resp.AssumptionProfile,
		Intrinsic:      resp.DCFValuePerShare,
		Price:          resp.CurrentPrice,
		TerminalPct:    resp.DCFTerminalPctOfEV,
		QualityGrade:   resp.DataQualityGrade,
		SIC:            resp.Industry.SIC,
	}

	if resp.CurrentPrice > 0 {
		r.GapPct = (resp.DCFValuePerShare/resp.CurrentPrice - 1) * 100
	}
	for _, pv := range resp.DCFPerYearPV {
		if pv < 0 {
			r.NegFCFYears++
		}
	}

	if resp.DCFValuePerShare < 0 {
		r.Flags = append(r.Flags, flagNegIntrinsic)
	}
	if resp.DCFTerminalPctOfEV > terminalDominanceThreshold {
		r.Flags = append(r.Flags, flagTerminalDominance)
	}
	if r.NegFCFYears > 0 {
		r.Flags = append(r.Flags, flagNegFCFYears)
	}
	if fam := methodFamily(sel.Model); fam != "" && fam != methodFamily(resp.CalculationMethod) {
		r.Flags = append(r.Flags, flagModelDivergence)
	}
	extreme := math.Abs(r.GapPct) > extremeGapThreshold
	if extreme {
		r.Flags = append(r.Flags, flagExtremeGap)
	}
	// Sanity-check blind spot: the crosscheck rubber-stamps a value as "reasonable"
	// (implied multiples vs sector) even when it's wildly disconnected from price.
	if resp.SanityCheck.IsReasonable && extreme {
		r.Flags = append(r.Flags, flagSanityBlindspot)
	}
	if !resp.Industry.Match {
		r.Flags = append(r.Flags, flagClassifierMismatch)
	}
	return r
}

// byAbsGap sorts rows by descending absolute price gap (biggest mispricing first).
type byAbsGap []Row

func (b byAbsGap) Len() int      { return len(b) }
func (b byAbsGap) Swap(i, j int) { b[i], b[j] = b[j], b[i] }
func (b byAbsGap) Less(i, j int) bool {
	return math.Abs(b[i].GapPct) > math.Abs(b[j].GapPct)
}

// --- loading ---

// loadBundle reads the response + model-selection JSON from one bundle dir.
func loadBundle(dir string) (Response, ModelSelection, error) {
	var resp Response
	if err := readJSON(filepath.Join(dir, "17-response.json"), &resp); err != nil {
		return resp, ModelSelection{}, err
	}
	var sel ModelSelection
	// model-selection is optional (absent => no divergence detection for this row).
	_ = readJSON(filepath.Join(dir, "14-model-selection.json"), &sel)
	return resp, sel, nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// latestBundleDir returns the most-recently-modified request-bundle dir under a
// ticker directory. Bundle dirs are named "req_<uuid>"; the prefix filter keeps a
// stray non-bundle directory from being mistaken for the latest bundle.
func latestBundleDir(tickerDir string) (string, bool) {
	entries, err := os.ReadDir(tickerDir)
	if err != nil {
		return "", false
	}
	var best string
	var bestMod int64 = -1
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "req_") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if m := info.ModTime().UnixNano(); m > bestMod {
			bestMod = m
			best = filepath.Join(tickerDir, e.Name())
		}
	}
	return best, best != ""
}

// collectRows walks a baseline directory (one subdir per ticker) and analyzes
// the latest bundle for each.
func collectRows(root string) ([]Row, []string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil, err
	}
	var rows []Row
	var skipped []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		ticker := e.Name()
		bundle, ok := latestBundleDir(filepath.Join(root, ticker))
		if !ok {
			skipped = append(skipped, ticker+" (no bundle)")
			continue
		}
		resp, sel, err := loadBundle(bundle)
		if err != nil {
			skipped = append(skipped, ticker+" ("+err.Error()+")")
			continue
		}
		rows = append(rows, analyze(resp, sel))
	}
	sort.Sort(byAbsGap(rows))
	return rows, skipped, nil
}

// --- rendering ---

func renderMarkdown(rows []Row, skipped []string, root string) string {
	var b strings.Builder
	ver := calcVersionLabel(rows)
	fmt.Fprintf(&b, "# Midas accuracy report\n\n")
	fmt.Fprintf(&b, "Source: `%s`  •  tickers: %d  •  calc-version: %s\n\n", shortPath(root), len(rows), ver)

	fmt.Fprintf(&b, "| Ticker | Sel→Comp | Profile | Intrinsic | Price | Gap%% | TermPV%% | -FCFy | Grade | Flags |\n")
	fmt.Fprintf(&b, "|---|---|---|--:|--:|--:|--:|--:|:-:|---|\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "| %s | %s→%s | %s | %s | %s | %+.1f | %s | %d | %s | %s |\n",
			r.Ticker,
			dash(r.SelectedModel), dash(methodFamily(r.ComputedMethod)),
			dash(r.Profile),
			money(r.Intrinsic), money(r.Price), r.GapPct,
			pct(r.TerminalPct), r.NegFCFYears, dash(r.QualityGrade),
			strings.Join(r.Flags, ", "),
		)
	}

	b.WriteString("\n## Summary\n\n")
	writeSummary(&b, rows)

	if len(skipped) > 0 {
		fmt.Fprintf(&b, "\n_Skipped: %s_\n", strings.Join(skipped, "; "))
	}
	return b.String()
}

func writeSummary(b *strings.Builder, rows []Row) {
	if len(rows) == 0 {
		b.WriteString("_no rows_\n")
		return
	}
	flagCounts := map[string]int{}
	var sumAbsGap, below float64
	belowN := 0
	for _, r := range rows {
		for _, f := range r.Flags {
			flagCounts[f]++
		}
		sumAbsGap += math.Abs(r.GapPct)
		if r.GapPct < 0 {
			belowN++
			below += r.GapPct
		}
	}
	fmt.Fprintf(b, "- Mean absolute price gap: **%.1f%%**\n", sumAbsGap/float64(len(rows)))
	fmt.Fprintf(b, "- Intrinsic below market: **%d/%d** tickers", belowN, len(rows))
	if belowN > 0 {
		fmt.Fprintf(b, " (mean gap %.1f%%)", below/float64(belowN))
	}
	b.WriteString("\n")

	// Flag rollup, most common first.
	type fc struct {
		flag string
		n    int
	}
	var fcs []fc
	for f, n := range flagCounts {
		fcs = append(fcs, fc{f, n})
	}
	sort.Slice(fcs, func(i, j int) bool {
		if fcs[i].n != fcs[j].n {
			return fcs[i].n > fcs[j].n
		}
		return fcs[i].flag < fcs[j].flag
	})
	b.WriteString("- Flag rollup:\n")
	for _, x := range fcs {
		fmt.Fprintf(b, "  - `%s`: %d\n", x.flag, x.n)
	}
}

func renderCSV(rows []Row) string {
	var b strings.Builder
	b.WriteString("ticker,selected_model,computed_method,profile,intrinsic,price,gap_pct,terminal_pct,neg_fcf_years,quality_grade,sic,flags\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%s,%s,%s,%s,%.4f,%.4f,%.2f,%.4f,%d,%s,%s,%s\n",
			r.Ticker, r.SelectedModel, methodFamily(r.ComputedMethod), r.Profile,
			r.Intrinsic, r.Price, r.GapPct, r.TerminalPct, r.NegFCFYears,
			r.QualityGrade, r.SIC, strings.Join(r.Flags, " "),
		)
	}
	return b.String()
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func money(f float64) string { return fmt.Sprintf("%.2f", f) }

// calcVersionLabel returns the calc-version for the header. A clean baseline is
// single-version; if a directory ever mixes versions it is flagged explicitly
// rather than silently reporting only the first row's version.
func calcVersionLabel(rows []Row) string {
	if len(rows) == 0 {
		return "?"
	}
	first := rows[0].CalcVersion
	for _, r := range rows[1:] {
		if r.CalcVersion != first {
			return dash(first) + " (mixed)"
		}
	}
	return dash(first)
}

// shortPath collapses an arbitrary (possibly absolute) baseline path to its last
// two segments so the report header is stable across machines, e.g.
// "C:/.../artifacts/tier2-baseline/2026-06-03" -> "tier2-baseline/2026-06-03".
func shortPath(p string) string {
	p = filepath.ToSlash(strings.TrimRight(p, `/\`))
	parts := strings.Split(p, "/")
	if len(parts) <= 2 {
		return p
	}
	return strings.Join(parts[len(parts)-2:], "/")
}

func pct(frac float64) string {
	if frac == 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", frac*100)
}

func main() {
	dir := flag.String("dir", "artifacts/tier2-baseline/2026-06-03", "baseline directory (one subdir per ticker)")
	format := flag.String("format", "md", "output format: md | csv")
	out := flag.String("out", "", "write report to this file instead of stdout")
	flag.Parse()

	rows, skipped, err := collectRows(*dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "accuracy: %v\n", err)
		os.Exit(1)
	}

	// Surface skipped tickers on stderr for BOTH formats so a missing/malformed
	// bundle is never silently lost (the CSV body has no place for it).
	if len(skipped) > 0 {
		fmt.Fprintf(os.Stderr, "accuracy: skipped %d ticker(s): %s\n", len(skipped), strings.Join(skipped, "; "))
	}

	var report string
	switch *format {
	case "md":
		report = renderMarkdown(rows, skipped, *dir)
	case "csv":
		report = renderCSV(rows)
	default:
		fmt.Fprintf(os.Stderr, "accuracy: unknown --format %q (want md|csv)\n", *format)
		os.Exit(2)
	}

	if *out != "" {
		if err := os.WriteFile(*out, []byte(report), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "accuracy: write %s: %v\n", *out, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "wrote %s (%d tickers)\n", *out, len(rows))
		return
	}
	fmt.Print(report)
}
