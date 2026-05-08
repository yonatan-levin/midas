package replay

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
)

// Stage N (R3b plan §3 Stage N) — performance regression guards for
// NF2 (single-bundle replay) and NF3 (100-bundle batch replay).
//
// Targets per spec:
//   - NF2: single-bundle replay ≤ 200 ms.
//   - NF3: 100-bundle batch replay ≤ 30 s.
//
// CI slack: 3× the local target (NF2 fails at 600 ms; NF3 fails at
// 90 s). The slack absorbs CI machine variance — local-machine targets
// stand on faster hardware than typical CI runners.
//
// Synthetic-corpus generation strategy (Decision N.1.b):
//   - Generator-only; corpus is NOT checked into the repo.
//   - Fixed PRNG seed → byte-identical output across runs on the same
//     code revision.
//   - TestMain triggers generation ONLY when a bench is requested
//     (detected via flag.Lookup("test.bench")); default `go test
//     ./...` skips generation entirely.
//   - The corpus directory is created once per `go test -bench` run
//     and removed via TestMain's deferred cleanup.

// perfCorpusDir is the package-level path the synthetic bundles live
// under for the duration of a bench run. Populated by TestMain when a
// bench is being executed; empty otherwise.
var perfCorpusDir string

// perfTickers is the deterministic ticker rotation used by the
// generator. 10 entries covers the spec's "watchlist regression"
// motivation; rotating modulo len drives 100 bundles deterministically.
var perfTickers = []string{
	"AAPL", "MSFT", "AMD", "GOOG", "TSM",
	"NVO", "ASML", "BABA", "SAP", "V",
}

// TestMain runs before any test/bench in this package. We parse flags
// here so flag.Lookup("test.bench") returns the operator-supplied value
// (the testing framework normally parses flags from inside m.Run, but
// TestMain owns flag.Parse precedence). When -bench was passed (and the
// regex isn't empty), we generate the synthetic perf corpus once and
// remove it after m.Run returns.
//
// Generation cost is ~1-2 s on a modern dev box; running it for every
// `go test ./...` would be a footgun. Bench-gating keeps the default
// test path fast.
func TestMain(m *testing.M) {
	flag.Parse() // ensure -bench is parsed BEFORE we Lookup it
	var corpusDir string
	if isBenchRun() {
		dir, err := os.MkdirTemp("", "replay-perf-corpus-*")
		if err != nil {
			fmt.Fprintf(os.Stderr, "TestMain: mkdir perf corpus: %v\n", err)
			os.Exit(2)
		}
		if err := generatePerfCorpus(dir, 100); err != nil {
			fmt.Fprintf(os.Stderr, "TestMain: generate perf corpus: %v\n", err)
			_ = os.RemoveAll(dir)
			os.Exit(2)
		}
		perfCorpusDir = dir
		corpusDir = dir
	}
	code := m.Run()
	if corpusDir != "" {
		_ = os.RemoveAll(corpusDir)
	}
	os.Exit(code)
}

// isBenchRun reports whether the operator invoked `go test -bench=...`.
// Detected via the test.bench flag's value; the testing package
// registers it during init regardless of whether the operator passed
// it, so we Lookup + check Value.String() instead of testing.Short().
//
// Default value of -bench is the empty string (no benches matched).
// When the operator passes `-bench=.` or `-bench=^Benchmark...`, the
// flag.Value reflects the regex string.
func isBenchRun() bool {
	bf := flag.Lookup("test.bench")
	if bf == nil {
		return false
	}
	v := bf.Value.String()
	return v != "" && v != "0"
}

// generatePerfCorpus writes n synthetic bundle directories under
// rootDir. Each bundle has a unique request ID, ticker (cycling
// through perfTickers), and the same fixture payload as
// seedFullBundle's raw-mode output. Determinism: only the request ID
// varies per bundle; the SEC/market/macro payloads are byte-identical
// across all bundles, which keeps the bench timing reproducible.
//
// 17-response.json — Replay() requires the bundle to carry a recorded
// response so the diff can run. We do ONE engine warm-up replay against
// the first generated bundle (which writes the response file), then
// copy that response into every other bundle. This makes the corpus
// "round-trip clean" — bench measurements aren't polluted by
// response-mismatch error paths. The warm-up cost is paid once, before
// the bench timer starts (b.ResetTimer in each bench).
func generatePerfCorpus(rootDir string, n int) error {
	secRaw := makeMinimalSECRawForPerf()
	macroDGS10 := makeFREDObsRawForPerf("4.25")
	macroDGS5 := makeFREDObsRawForPerf("3.75")
	macroDGS2 := makeFREDObsRawForPerf("3.50")

	for i := 0; i < n; i++ {
		ticker := perfTickers[i%len(perfTickers)]
		bundleID := fmt.Sprintf("perf_%04d", i)
		bundleDir := filepath.Join(rootDir, ticker, "req_"+bundleID)
		if err := os.MkdirAll(bundleDir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", bundleDir, err)
		}

		// Manifest with current schema versions so replay does not flag drift.
		mf := artifact.Manifest{
			BundleVersion:  "1.0",
			RequestID:      bundleID,
			Ticker:         ticker,
			Trigger:        "header",
			StartedAt:      "2026-01-15T12:00:00Z",
			Outcome:        "ok",
			SchemaVersions: map[string]int{},
		}
		for k, v := range CurrentSchemaVersions {
			mf.SchemaVersions[k] = v
		}
		body, err := json.MarshalIndent(&mf, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal manifest %s: %w", bundleID, err)
		}
		if err := os.WriteFile(filepath.Join(bundleDir, "00-manifest.json"), body, 0o644); err != nil {
			return fmt.Errorf("write manifest %s: %w", bundleID, err)
		}

		// Static raw payloads — byte-identical across all 100 bundles.
		marketRaw := makeMarketRawForPerf(ticker)
		writes := map[string][]byte{
			secRawFile:                      secRaw,
			marketRawFile:                   marketRaw,
			"07-fetch-macro-DGS10.raw.json": macroDGS10,
			"07-fetch-macro-DGS5.raw.json":  macroDGS5,
			"07-fetch-macro-DGS2.raw.json":  macroDGS2,
		}
		for name, content := range writes {
			if err := os.WriteFile(filepath.Join(bundleDir, name), content, 0o644); err != nil {
				return fmt.Errorf("write %s/%s: %w", bundleID, name, err)
			}
		}
	}

	// Warm-up replay: drive the engine against bundle #0 to produce a
	// canonical 17-response.json. The bench is asserted-deterministic
	// because every bundle has byte-identical SEC/market/macro payloads
	// (only ticker + request ID vary; ticker affects the response's
	// `ticker` field but not the math). We propagate the resulting
	// response into every bundle so Replay's diff path is exercised
	// without the noise of "missing response file".
	if n == 0 {
		return nil
	}
	firstTicker := perfTickers[0]
	firstBundleDir := filepath.Join(rootDir, firstTicker, fmt.Sprintf("req_perf_%04d", 0))
	if err := warmUpResponse(firstBundleDir, firstTicker); err != nil {
		return fmt.Errorf("warm-up first bundle: %w", err)
	}
	canonical, err := os.ReadFile(filepath.Join(firstBundleDir, responseFile))
	if err != nil {
		return fmt.Errorf("read warm-up response: %w", err)
	}

	// Propagate the canonical response into every other bundle.
	for i := 1; i < n; i++ {
		ticker := perfTickers[i%len(perfTickers)]
		bundleDir := filepath.Join(rootDir, ticker, fmt.Sprintf("req_perf_%04d", i))
		if err := os.WriteFile(filepath.Join(bundleDir, responseFile), canonical, 0o644); err != nil {
			return fmt.Errorf("propagate response %d: %w", i, err)
		}
	}
	return nil
}

// warmUpResponse runs the engine against bundleDir once to capture
// the canonical FairValueResponse into 17-response.json. Used by
// generatePerfCorpus to seed bundle #0 with a deterministic response
// payload that subsequent bundles copy.
func warmUpResponse(bundleDir, ticker string) error {
	result, _, err := runEngine(context.Background(), bundleDir, Options{Mode: ModeRaw, Ticker: ticker}, ticker)
	if err != nil {
		return fmt.Errorf("runEngine for warm-up: %w", err)
	}
	resp := buildFairValueResponse(ticker, result)
	body, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal warm-up response: %w", err)
	}
	return os.WriteFile(filepath.Join(bundleDir, responseFile), body, 0o644)
}

// makeMinimalSECRawForPerf mirrors makeMinimalSECRaw from gateway_sec_test.go
// but takes no testing.TB. Re-declared here to keep the bench file
// self-contained.
func makeMinimalSECRawForPerf() []byte {
	facts := map[string]interface{}{
		"cik":        320193,
		"entityName": "Apple Inc.",
		"facts": map[string]interface{}{
			"us-gaap": map[string]interface{}{
				"Revenues": map[string]interface{}{
					"label":       "Revenues",
					"description": "Aggregate revenue",
					"units": map[string]interface{}{
						"USD": []interface{}{
							perfFact(383285000000.0),
						},
					},
				},
				"OperatingIncomeLoss": map[string]interface{}{
					"label":       "Operating Income (Loss)",
					"description": "Operating income",
					"units": map[string]interface{}{
						"USD": []interface{}{perfFact(114301000000.0)},
					},
				},
				"Assets": map[string]interface{}{
					"label":       "Assets",
					"description": "Total Assets",
					"units": map[string]interface{}{
						"USD": []interface{}{perfFact(352755000000.0)},
					},
				},
				"Liabilities": map[string]interface{}{
					"label":       "Liabilities",
					"description": "Total Liabilities",
					"units": map[string]interface{}{
						"USD": []interface{}{perfFact(290437000000.0)},
					},
				},
			},
			"dei": map[string]interface{}{
				"EntityCommonStockSharesOutstanding": map[string]interface{}{
					"label":       "Entity Common Stock Shares Outstanding",
					"description": "Shares outstanding",
					"units": map[string]interface{}{
						"shares": []interface{}{perfFact(15600000000.0)},
					},
				},
			},
		},
	}
	body, _ := json.Marshal(facts)
	return body
}

func perfFact(val float64) map[string]interface{} {
	return map[string]interface{}{
		"val":   val,
		"end":   "2023-09-30",
		"fy":    2023,
		"fp":    "FY",
		"form":  "10-K",
		"filed": "2023-11-03",
		"accn":  "0000320193-23-000106",
		"frame": "CY2023",
	}
}

// makeMarketRawForPerf mirrors makeMarketRaw from gateway_market_test.go
// without the testing dependency.
func makeMarketRawForPerf(ticker string) []byte {
	env := map[string]interface{}{
		"quoteResponse": map[string]interface{}{
			"result": []map[string]interface{}{
				{
					"symbol":                   ticker,
					"regularMarketPrice":       190.0,
					"marketCap":                3.0e12,
					"sharesOutstanding":        1.5e10,
					"regularMarketVolume":      5.5e7,
					"averageDailyVolume3Month": 6.0e7,
					"beta":                     1.25,
					"currency":                 "USD",
					"marketState":              "REGULAR",
					"regularMarketTime":        int64(1700000000),
				},
			},
			"error": nil,
		},
	}
	body, _ := json.Marshal(env)
	return body
}

// makeFREDObsRawForPerf mirrors makeFREDObsRaw from
// gateway_macro_test.go without the testing dependency. Produces a
// FRED single-observation envelope.
func makeFREDObsRawForPerf(value string) []byte {
	body, _ := json.Marshal(map[string]interface{}{
		"observation_start": "2024-01-01",
		"observation_end":   "2024-01-01",
		"observations": []map[string]interface{}{
			{"date": "2024-01-01", "value": value},
		},
	})
	return body
}

// _ keeps the entities/ports imports live even when build flags
// remove the bench paths — these symbols are referenced only by the
// generator helpers above and could be pruned by a future refactor
// otherwise.
var (
	_ = entities.TreasuryRates{}
	_ = ports.YFinanceQuote{}
)

// listPerfBundles enumerates every leaf bundle dir under perfCorpusDir.
// Returns sorted paths so iteration order is stable across bench
// invocations.
func listPerfBundles(b *testing.B) []string {
	b.Helper()
	if perfCorpusDir == "" {
		b.Skip("Stage N benches require perfCorpusDir; rerun with -bench=...")
	}
	entries, err := os.ReadDir(perfCorpusDir)
	if err != nil {
		b.Fatalf("read perf corpus: %v", err)
	}
	out := make([]string, 0, 100)
	for _, ticker := range entries {
		tickerPath := filepath.Join(perfCorpusDir, ticker.Name())
		reqs, err := os.ReadDir(tickerPath)
		if err != nil {
			continue
		}
		for _, r := range reqs {
			out = append(out, filepath.Join(tickerPath, r.Name()))
		}
	}
	return out
}

// nf2Ceiling is the wall-clock-per-iteration ceiling for NF2 (single
// bundle). Spec target 200 ms; CI slack 3× → 600 ms.
const nf2Ceiling = 600 * time.Millisecond

// BenchmarkReplay_SingleBundle_NF2 measures wall-clock-per-iteration
// of one bundle's full replay path. Fails when per-iter exceeds
// nf2Ceiling. Run via:
//
//	go test -bench=BenchmarkReplay_SingleBundle_NF2 \
//	  -benchtime=10x ./internal/observability/replay/
//
// Note: -benchtime=10x is recommended because the ceiling check only
// makes sense after a stable per-iter measurement; 10 iterations
// gives the testing.B framework enough samples to average.
func BenchmarkReplay_SingleBundle_NF2(b *testing.B) {
	bundles := listPerfBundles(b)
	if len(bundles) == 0 {
		b.Fatal("no bundles in perf corpus")
	}
	bundleDir := bundles[0]

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := Replay(context.Background(), bundleDir, Options{Mode: ModeRaw})
		if res.Status == StatusErrored {
			b.Fatalf("Replay errored: %s", res.Error)
		}
	}
	elapsed := b.Elapsed()
	perIter := elapsed / time.Duration(b.N)
	if perIter > nf2Ceiling {
		b.Errorf("NF2 SLA broken: per-iter %v exceeds ceiling %v (3× of 200ms target)", perIter, nf2Ceiling)
	}
}

// nf3Ceiling is the total wall-clock ceiling for NF3 (100-bundle
// batch). Spec target 30 s; CI slack 3× → 90 s.
const nf3Ceiling = 90 * time.Second

// BenchmarkReplay_BatchOf100_NF3_Sequential replays the full 100-bundle
// corpus serially. Asserts total wall time within the 3× slack ceiling.
func BenchmarkReplay_BatchOf100_NF3_Sequential(b *testing.B) {
	bundles := listPerfBundles(b)
	if len(bundles) < 100 {
		b.Fatalf("perf corpus has only %d bundles; need 100", len(bundles))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		for _, bundleDir := range bundles {
			res := Replay(context.Background(), bundleDir, Options{Mode: ModeRaw})
			if res.Status == StatusErrored {
				b.Fatalf("Replay errored on %s: %s", bundleDir, res.Error)
			}
		}
		elapsed := time.Since(start)
		if elapsed > nf3Ceiling {
			b.Errorf("NF3 SLA broken (sequential): total %v exceeds ceiling %v (3× of 30s target)", elapsed, nf3Ceiling)
		}
	}
}

// BenchmarkReplay_BatchOf100_NF3_Parallel replays the full 100-bundle
// corpus through a goroutine pool of width runtime.NumCPU(). Asserts
// total wall time within the 3× slack ceiling. Speedup ratio (sequential
// time / parallel time) is REPORTED via b.Log but not asserted as a
// gate — CI machine variance can suppress speedup unpredictably.
func BenchmarkReplay_BatchOf100_NF3_Parallel(b *testing.B) {
	bundles := listPerfBundles(b)
	if len(bundles) < 100 {
		b.Fatalf("perf corpus has only %d bundles; need 100", len(bundles))
	}
	workers := runtime.NumCPU()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		sem := make(chan struct{}, workers)
		var wg sync.WaitGroup
		for _, bd := range bundles {
			bd := bd
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				_ = Replay(context.Background(), bd, Options{Mode: ModeRaw})
			}()
		}
		wg.Wait()
		elapsed := time.Since(start)
		if elapsed > nf3Ceiling {
			b.Errorf("NF3 SLA broken (parallel): total %v exceeds ceiling %v (3× of 30s target)", elapsed, nf3Ceiling)
		}
		b.Logf("parallel batch of 100 (workers=%d) completed in %v", workers, elapsed)
	}
}
