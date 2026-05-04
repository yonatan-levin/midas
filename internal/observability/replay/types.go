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

// String returns a stable lowercase name suitable for log fields and CLI
// flag values ("raw" / "parsed"). Stable contract — values flow through
// `--from=raw|parsed` and into the manifest's notes when replay annotates
// the chosen mode.
func (m Mode) String() string {
	switch m {
	case ModeRaw:
		return "raw"
	case ModeParsed:
		return "parsed"
	default:
		return "unknown"
	}
}

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
}
