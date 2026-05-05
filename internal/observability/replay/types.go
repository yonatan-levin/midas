package replay

// Mode is the gateway substitution mode used by Bundle*Gateway stubs. It
// selects between two read paths into a captured artifact bundle:
//
//   - ModeRaw  — read the captured raw HTTP-response bytes from
//     `<bundleDir>/<NN-fetch-*.raw.json` and dispatch through the production
//     parser (e.g. sec.Parser.ParseFinancialData, macro.ParseFREDSeries).
//     This exercises the gateway's parser logic on each replay so a parser
//     regression surfaces immediately. Spec D3 invariant.
//
//   - ModeParsed — read the post-parse snapshot directly from
//     `<bundleDir>/<NN-fetch-*.parsed.json` and json.Unmarshal it into the
//     domain entity, bypassing the production parser entirely. Lets a user
//     isolate downstream engine-math drift from upstream-parser drift when
//     diagnosing diffs.
//
// Both modes return the same domain entity types; only the path through the
// gateway implementation differs. Default is ModeRaw — the spec NF1 invariant
// "no production behavior is altered by replay" means every replay path must
// honor the same parsing contract production uses, and ModeRaw is what
// enforces that.
type Mode int

const (
	// ModeRaw reads `<NN>.raw.json` and runs the production parser.
	ModeRaw Mode = iota
	// ModeParsed reads `<NN>.parsed.json` and json.Unmarshal's directly.
	ModeParsed
)

// Options carries replay configuration that flows from the CLI into the fx
// Module and the Replay() orchestrator. Fields are read by both module.go
// (for fx wiring) and replay.go (for policy decisions like schema-drift
// tolerance).
//
// R2 only consumes Mode. The drift-toggle fields land in R2 as well because
// Replay() needs them; the per-stage / parallelism / tolerance knobs are R3
// and intentionally absent from this struct.
type Options struct {
	// Mode selects raw vs parsed gateway behavior. Defaults to ModeRaw
	// (Go's zero value is the iota-zero, which is ModeRaw — by design so
	// callers that omit this field get the symmetric production-parser path
	// rather than silently using parsed snapshots).
	Mode Mode

	// AllowSchemaDrift, when true, downgrades a schema_versions mismatch
	// from a hard error to a warning (Result.SchemaDrift=true plus normal
	// outcome). Default false: spec D5 says drift is refused unless the
	// operator explicitly opts in.
	AllowSchemaDrift bool

	// AllowGitDrift, when true, downgrades a git_sha mismatch the same way.
	// An empty bundle git_sha is "unknown" (not drift) per F6 — drift only
	// fires when both bundle and current have non-empty git_sha values that
	// disagree.
	AllowGitDrift bool

	// ManifestStartedAt is the bundle manifest's started_at value,
	// RFC3339Nano-formatted. The replay fx Module binds the
	// *valuation.Service Clock seam to this instant so the engine's
	// wall-clock reads (FY-period fallback in service.go:285,
	// CalculatedAt stamps) reproduce the capture-time outputs even when
	// the replay calendar year differs (D10 invariant). Empty/malformed
	// values fall through to a wall-clock binding — replay still runs,
	// but cross-year determinism is no longer guaranteed.
	ManifestStartedAt string

	// Ticker is the bundle manifest's ticker value, threaded into the
	// BundleSECGateway constructor so GetTickerCIKMapping returns
	// {ticker: cik} for the actual bundle ticker. Without this the
	// gateway's mapping is empty / synthetic and the engine fails at
	// coordinator.go:342 for any bundle whose request did not carry an
	// inline CIK. VERIFIER finding MEDIUM-1.
	//
	// Replay() populates this field from the manifest before constructing
	// the fx Module; direct callers of Module() may set it explicitly.
	Ticker string

	// FloatRelTol overrides the default relative tolerance used by the
	// float-diff layer (DefaultFloatRelTol = 1e-9). Zero means "use the
	// default"; explicit non-zero values flow through to compareFairValueResponses.
	// R3 Stage L.2.
	FloatRelTol float64

	// FloatAbsTol overrides the default absolute tolerance (DefaultFloatAbsTol
	// = 1e-12). Same semantics as FloatRelTol.
	FloatAbsTol float64

	// DiffStages enables per-stage diff (10-clean-output.json,
	// 12-growth-curve.json, 13-wacc.json, 15-valuation.json) against the
	// engine's reproductions. R3 Stage K wires the diff machinery; this
	// option toggles it. False (default) preserves R0+R1+R2 behavior.
	DiffStages bool
}
