package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/infra/gateways/sec"
)

// secRawFile and secParsedFile are the canonical bundle filenames for the
// SEC fetch phase. They mirror the producers in
// internal/infra/gateways/sec/client.go (SnapshotRaw/Snapshot calls). Stable
// across producers and replay readers; bumping either requires a coordinated
// change in both packages plus a manifest schema_version bump.
//
// secSubmissionsRawFile / secSubmissionsParsedFile capture the SIC code via
// the submissions endpoint. Added to the bundle layout in bundle_version 1.1;
// pre-1.1 bundles do not contain these files and the replay-side reader falls
// back to an empty SIC (matching the prior "graceful classifier fallback"
// behavior, preserving backward compat).
const (
	secRawFile               = "05-fetch-sec.raw.json"
	secParsedFile            = "05-fetch-sec.parsed.json"
	secSubmissionsRawFile    = "05-fetch-sec-submissions.raw.json"
	secSubmissionsParsedFile = "05-fetch-sec-submissions.parsed.json"
)

// BundleSECGateway is the bundle-backed replay implementation of
// ports.SECGateway. It satisfies the same interface production's SEC gateway
// satisfies but reads bytes from disk (the captured bundle) instead of from
// data.sec.gov.
//
// Two modes (Mode field):
//
//   - ModeRaw — reads `05-fetch-sec.raw.json` and dispatches through the
//     production parser (sec.Parser.ParseFinancialData). This is the
//     symmetric path that exercises the gateway parser per spec D3, so a
//     replay run with `--from=raw` catches parser regressions. The gateway
//     constructs its own *sec.Parser via sec.NewParser(logger) — no DI needed
//     because the parser holds no mutable state beyond a logger.
//
//   - ModeParsed — reads `05-fetch-sec.parsed.json` and json.Unmarshal's
//     directly into *entities.CompanyFactsResponse. Skips the production
//     parser entirely; lets a user isolate engine-math drift from
//     parser drift when diagnosing diffs.
//
// Goroutine-safety: `internal/services/datafetcher/coordinator.go` invokes
// gateway methods inside `go func()` workers under sync.WaitGroup. All
// fields are immutable post-construction (bundleDir + mode + parser), and
// os.ReadFile is concurrency-safe; calls runs hits counter is monotonic via
// atomics for test observability. NO internal mutex is required — and adding
// one would harm replay throughput.
//
// F11 invariant: every "missing payload" path returns
// ErrBundleMissingPayload (NEVER panics). A panic in a coordinator worker
// goroutine is not recovered by cmd/replay/main.go's top-level recover, so a
// panic would crash the whole replay; structured-error returns let the
// orchestration layer surface an "errored" Result and continue.
type BundleSECGateway struct {
	bundleDir string
	mode      Mode
	parser    *sec.Parser

	// ticker is the manifest ticker for this bundle. Threaded in at
	// construction so GetTickerCIKMapping returns {ticker: cik} for the
	// actual bundle ticker — supports any bundle, not just the prior
	// hardcoded 8-element list of mega-caps. VERIFIER finding MEDIUM-1.
	// Empty is permitted; in that case GetTickerCIKMapping returns an
	// empty map (engine path then surfaces a "ticker not found" error).
	ticker string

	// callsCount is the per-method invocation counter, incremented atomically
	// by every exported method. Test-only observability — replay's
	// concurrency-safety regression tests assert on these values; production
	// has no consumer.
	callsCount uint64
}

// NewBundleSECGateway constructs a replay-mode SEC gateway over the supplied
// bundle directory. The directory must already contain
// `05-fetch-sec.<raw|parsed>.json` for the gateway to satisfy GetCompanyFacts;
// missing files are reported via ErrBundleMissingPayload at call time, not
// at construction.
//
// ticker is the manifest's ticker for this bundle; it is reflected back in
// GetTickerCIKMapping so the engine's coordinator (coordinator.go:342)
// resolves the bundle's ticker → captured CIK regardless of what equity
// the bundle was captured against.
//
// logger is forwarded to the production sec.Parser via sec.NewParser; pass
// zap.NewNop() in tests to silence parser-side logs.
func NewBundleSECGateway(bundleDir string, mode Mode, ticker string, logger *zap.Logger) *BundleSECGateway {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &BundleSECGateway{
		bundleDir: bundleDir,
		mode:      mode,
		ticker:    ticker,
		parser:    sec.NewParser(logger),
	}
}

// CallsCount returns the total number of method invocations across all
// exported methods of this gateway. Test-only — used by concurrency-safety
// tests to assert the gateway saw the expected fan-out without coupling on
// per-method counters.
func (g *BundleSECGateway) CallsCount() uint64 {
	return atomic.LoadUint64(&g.callsCount)
}

// GetCompanyFacts returns the bundled CompanyFacts response. Behavior depends
// on the gateway's Mode:
//   - ModeRaw    → reads raw bytes, unmarshals into ports.SECCompanyFacts,
//     and runs sec.Parser.ParseFinancialData → re-shapes into
//     entities.CompanyFactsResponse mirroring sec.Gateway.GetCompanyFacts.
//   - ModeParsed → unmarshals 05-fetch-sec.parsed.json directly into
//     entities.CompanyFactsResponse.
//
// The cik argument is accepted for interface conformance but ignored — a
// bundle is single-ticker so the captured payload IS the response for
// whatever CIK the bundle was opened against. Mismatched calls (e.g.
// requesting CIK=12345 when the bundle captured CIK=320193) silently get
// AAPL's data; this is consistent with replay's hermeticity contract.
func (g *BundleSECGateway) GetCompanyFacts(ctx context.Context, cik string) (*entities.CompanyFactsResponse, error) {
	atomic.AddUint64(&g.callsCount, 1)

	switch g.mode {
	case ModeRaw:
		body, err := g.readPayload(secRawFile)
		if err != nil {
			return nil, err
		}
		var facts ports.SECCompanyFacts
		if err := json.Unmarshal(body, &facts); err != nil {
			return nil, fmt.Errorf("replay: BundleSECGateway GetCompanyFacts: parse raw payload: %w", err)
		}
		// Run the production parser on the bytes so any future parser change
		// is exercised by replay. This call is parser.ParseFinancialData but
		// the production sec.Gateway.GetCompanyFacts returns a converted
		// CompanyFactsResponse rather than the parser output. We mirror that
		// conversion here so callers see the same shape they see in
		// production.
		_, parseErr := g.parser.ParseFinancialData(ctx, &facts)
		if parseErr != nil {
			// Non-fatal in many cases (e.g., FPI taxonomy) — production
			// surfaces the same error to the caller. Pass through unchanged
			// so replay reproduces production semantics exactly.
			return nil, parseErr
		}
		return convertSECFactsToResponse(&facts), nil

	case ModeParsed:
		body, err := g.readPayload(secParsedFile)
		if err != nil {
			return nil, err
		}
		var resp entities.CompanyFactsResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("replay: BundleSECGateway GetCompanyFacts: parse parsed payload: %w", err)
		}
		return &resp, nil

	default:
		return nil, fmt.Errorf("replay: BundleSECGateway: unknown mode %d", g.mode)
	}
}

// GetCompanyConcepts returns ErrBundleMissingPayload — the per-concept SEC
// endpoint is not snapshotted by today's bundle producers (sec/client.go
// only snapshots the company-facts response). The valuation engine's path
// reaches Concepts only via a fallback that the bundle's primary path never
// triggers, so this is safe per F11. If a future producer captures concepts,
// extend this method instead of removing the gateway abstraction.
func (g *BundleSECGateway) GetCompanyConcepts(ctx context.Context, cik string, tag string) (*entities.ConceptResponse, error) {
	atomic.AddUint64(&g.callsCount, 1)
	return nil, NewBundleMissingPayloadError(g.bundleDir, fmt.Sprintf("05-fetch-sec-concepts-%s.raw.json", tag), nil)
}

// GetTickerCIKMapping returns the bundle's single-entry {ticker: cik}
// mapping derived from the captured SEC raw payload. Bundles do not
// snapshot the SEC global ticker→CIK index, but the engine's coordinator
// path (coordinator.go:342) consults this method to resolve the bundle's
// ticker to its CIK before calling GetFinancialDataForTicker.
//
// Resolution strategy:
//  1. Read the bundle's SEC raw (or parsed) payload to extract the
//     captured CIK.
//  2. Return {g.ticker: cik}. The bundle is single-ticker by design;
//     the manifest ticker is wired through the constructor (VERIFIER
//     finding MEDIUM-1) so any bundle works — not just the previously
//     hardcoded 8-element AAPL/MSFT/GOOG/GOOGL/AMZN/NVDA/TSLA/META set.
//
// When g.ticker is empty (constructor was passed ""), the mapping uses
// a synthetic "*" key — the coordinator's lookup `tickerMapping[ticker]`
// won't hit it, surfacing a clean "ticker not found" error rather than
// silently mis-resolving.
func (g *BundleSECGateway) GetTickerCIKMapping(ctx context.Context) (map[string]string, error) {
	atomic.AddUint64(&g.callsCount, 1)

	// Try raw mode first, fall back to parsed.
	body, err := g.readPayload(secRawFile)
	if err != nil {
		body, err = g.readPayload(secParsedFile)
		if err != nil {
			// Bundle has no SEC payload at all — this is a genuine missing-
			// payload condition. Surface it cleanly.
			return nil, NewBundleMissingPayloadError(g.bundleDir, secRawFile, nil)
		}
	}

	var cik string
	var facts ports.SECCompanyFacts
	if err := json.Unmarshal(body, &facts); err != nil {
		// Try the parsed-mode shape (entities.CompanyFactsResponse) instead.
		var parsed entities.CompanyFactsResponse
		if err2 := json.Unmarshal(body, &parsed); err2 != nil {
			return nil, fmt.Errorf("replay: BundleSECGateway GetTickerCIKMapping: parse payload: %w", err)
		}
		cik = parsed.CIK
	} else {
		cik = facts.CIK.String()
	}

	key := g.ticker
	if key == "" {
		// No manifest ticker provided — fall through to a synthetic key
		// the coordinator won't hit. Engine then surfaces "ticker not
		// found" rather than silently mis-resolving.
		key = "*"
	}
	return map[string]string{key: cik}, nil
}

// GetFinancialDataForTicker is the high-level entry the production gateway
// exposes (fetch + parse + normalize + SIC fetch). For replay we read both
// the SEC company-facts snapshot AND, when present, the SIC stamp from the
// captured submissions endpoint payload.
//
// SIC code resolution:
//   - Read `05-fetch-sec-submissions.parsed.json` from the bundle directory.
//     When found, decode the {"sic": "..."} shape and stamp it onto every
//     period of historical.Data (mirroring production's
//     sec.Gateway.GetFinancialDataForTicker behavior).
//   - When the file is absent OR malformed, leave SICCode empty. This
//     preserves backward compat with pre-bundle_version-1.1 bundles that
//     never captured the submissions endpoint. The valuation engine handles
//     a missing SIC gracefully (industry classifier falls back to keyword
//     matching), but the replay will then drift on industry-classified
//     fields versus a bundle that DID capture SIC. That drift is expected
//     for old bundles and load-bearing for new ones.
func (g *BundleSECGateway) GetFinancialDataForTicker(ctx context.Context, ticker, cik string) (*entities.HistoricalFinancialData, error) {
	atomic.AddUint64(&g.callsCount, 1)

	body, err := g.readPayload(secRawFile)
	if err != nil {
		return nil, err
	}
	var facts ports.SECCompanyFacts
	if err := json.Unmarshal(body, &facts); err != nil {
		return nil, fmt.Errorf("replay: BundleSECGateway GetFinancialDataForTicker: parse raw payload: %w", err)
	}
	historical, err := g.parser.ParseFinancialData(ctx, &facts)
	if err != nil {
		return nil, fmt.Errorf("replay: BundleSECGateway GetFinancialDataForTicker: parser: %w", err)
	}
	historical.Ticker = ticker
	for _, period := range historical.Data {
		period.Ticker = ticker
	}

	// Best-effort SIC lookup: read the submissions snapshot if present.
	// Decoding into a minimal {SIC string} shape — the full submissions
	// JSON has many fields but only `sic` matters here. Errors (missing
	// file or malformed JSON) leave SIC empty without failing the call.
	// SICCode lives only on HistoricalFinancialData (not on the per-period
	// FinancialData), matching the entity layout in financial_data.go.
	if sicBody, sicErr := g.readPayload(secSubmissionsParsedFile); sicErr == nil {
		var sub struct {
			SIC string `json:"sic"`
		}
		if jsonErr := json.Unmarshal(sicBody, &sub); jsonErr == nil && sub.SIC != "" {
			historical.SICCode = sub.SIC
		}
	}

	// Normalize each period through the parser, mirroring production
	// sec.Gateway.GetFinancialDataForTicker step 5. NormalizeFinancialData is
	// pure — no I/O — so safe in replay.
	for periodKey, data := range historical.Data {
		normalized, nErr := g.parser.NormalizeFinancialData(ctx, data)
		if nErr != nil {
			// Match production behavior: log and continue. Replay disables
			// logging by default (NopLogger) so this is silent in tests.
			continue
		}
		historical.Data[periodKey] = normalized
	}
	return historical, nil
}

// HealthCheck always succeeds in replay. Production exercises this against
// a live SEC endpoint to surface degraded upstreams; replay has no such
// concern — the bundle either has the payload or it doesn't, and that is
// detected at the GetCompanyFacts call site.
func (g *BundleSECGateway) HealthCheck(ctx context.Context) error {
	atomic.AddUint64(&g.callsCount, 1)
	return nil
}

// readPayload reads <bundleDir>/<relativePath> from disk and returns the
// bytes. Missing-file errors are converted to ErrBundleMissingPayload so
// callers can errors.Is on the sentinel; other errors wrap the underlying
// fs error so callers retain the failure detail.
func (g *BundleSECGateway) readPayload(relativePath string) ([]byte, error) {
	full := filepath.Join(g.bundleDir, relativePath)
	body, err := os.ReadFile(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, NewBundleMissingPayloadError(g.bundleDir, relativePath, err)
		}
		return nil, fmt.Errorf("replay: BundleSECGateway: read %s: %w", relativePath, err)
	}
	return body, nil
}

// convertSECFactsToResponse mirrors sec.Gateway.GetCompanyFacts's conversion
// from ports.SECCompanyFacts to entities.CompanyFactsResponse so replay
// callers see the same shape production emits. Logic is intentionally a
// duplicate of sec/gateway.go::convertFactsToMap behavior at a higher
// granularity; pulling production's helper into a shared location is out of
// scope for R2 and would risk a public-API surface change.
//
// The Facts map keys (taxonomy → concept) are preserved; the value shape is
// the SECFactGroup struct directly because callers in valuation/engine path
// access through the entities.CompanyFactsResponse abstraction, not via type
// assertion on map[string]interface{}. Production also returns interface{}
// values, but that conversion is only required for legacy JSON marshalling
// paths — the replay path consumes the response struct typed.
func convertSECFactsToResponse(facts *ports.SECCompanyFacts) *entities.CompanyFactsResponse {
	totalConcepts := 0
	for _, concepts := range facts.Facts {
		totalConcepts += len(concepts)
	}
	// Build the same nested-interface shape the production conversion does
	// (convertFactsToMap in sec/gateway.go). This keeps replay's response
	// indistinguishable from production's at the byte level.
	factsMap := make(map[string]interface{}, len(facts.Facts))
	for taxonomy, concepts := range facts.Facts {
		taxonomyMap := make(map[string]interface{}, len(concepts))
		for conceptName, group := range concepts {
			unitsMap := make(map[string]interface{}, len(group.Units))
			for unitType, secFacts := range group.Units {
				factSlice := make([]interface{}, len(secFacts))
				for i, f := range secFacts {
					factSlice[i] = map[string]interface{}{
						"val":   f.Val,
						"end":   f.End,
						"fy":    float64(f.Fy),
						"fp":    f.Fp,
						"filed": f.Filed,
						"accn":  f.Accn,
						"form":  f.Form,
						"frame": f.Frame,
					}
				}
				unitsMap[unitType] = factSlice
			}
			taxonomyMap[conceptName] = map[string]interface{}{
				"label":       group.Label,
				"description": group.Description,
				"units":       unitsMap,
			}
		}
		factsMap[taxonomy] = taxonomyMap
	}
	return &entities.CompanyFactsResponse{
		CIK:         facts.CIK.String(),
		EntityName:  facts.EntityName,
		Facts:       factsMap,
		FactsCount:  totalConcepts,
		LastUpdated: facts.FilingDate,
	}
}
