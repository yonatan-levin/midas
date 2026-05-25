package adjustments

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/ai"
)

// sha256Hex returns the SHA-256 hex digest of s. Result is a 64-char
// lowercase string. Used as the AIProvenance.SourceDocHash helper —
// hashing the raw footnote text directly so an identical footnote
// produces an identical hash regardless of upstream prompt-template
// version.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// sha256HexPromptCanonical hashes a deterministic, timestamp-free
// canonical serialization of an ai.FootnoteAnalysisRequest so the
// resulting digest is reproducible across runs of the same fixture.
//
// Why the canonical form (rather than plain JSON of the struct):
//
//   - request.RequestTimestamp moves with wall-clock and MUST be
//     excluded — otherwise replay tooling would see spurious drift on
//     every re-run.
//   - request.Context is a map[string]interface{} whose Go-runtime
//     iteration order is non-deterministic; we sort keys before
//     serialization so two structurally-equal Context maps hash
//     identically.
//
// The hash IS the prompt-as-sent identity, not the prompt template
// alone — substituted inputs (ticker, period, footnote) participate.
// Two calls on the same fixture produce the same hash; a different
// ticker or different footnote text produces a different hash.
func sha256HexPromptCanonical(request *ai.FootnoteAnalysisRequest) string {
	if request == nil {
		return sha256Hex("")
	}

	type canonical struct {
		Ticker        string            `json:"ticker"`
		FilingType    string            `json:"filing_type"`
		FootnoteText  string            `json:"footnote_text"`
		AnalysisType  string            `json:"analysis_type"`
		PriorityLevel string            `json:"priority_level"`
		Context       map[string]string `json:"context"`
	}

	// Stable string serialization of the Context map values. Numeric
	// values render via %v (Go's default) so 1234.5 → "1234.5"; this
	// matches Go's JSON encoding for float64 well enough for hash
	// stability (the exact byte representation matters only to the
	// hash, not the AI service).
	ctx := make(map[string]string, len(request.Context))
	keys := make([]string, 0, len(request.Context))
	for k := range request.Context {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		// Encode each value via JSON so int / float / string all produce
		// a stable, type-aware string. encoding/json on a single value
		// is total for the types B3 stuffs into Context today (string,
		// float64, int).
		//
		// Phase 3 followup (LOW-1 fix): if a future caller stuffs a
		// channel, func, or cyclic structure into Context, encoding/json
		// returns a typed error and the raw value would otherwise hash
		// as an empty string — silently colliding across structurally-
		// distinct inputs. Tag the value with its type instead so the
		// canonical-request fingerprint stays sensitive to the
		// unsupported value's identity even when its content cannot be
		// serialized.
		b, err := json.Marshal(request.Context[k])
		if err != nil {
			ctx[k] = fmt.Sprintf("<unsupported:%T>", request.Context[k])
			continue
		}
		ctx[k] = string(b)
	}

	c := canonical{
		Ticker:        request.Ticker,
		FilingType:    request.FilingType,
		FootnoteText:  request.FootnoteText,
		AnalysisType:  string(request.AnalysisType),
		PriorityLevel: string(request.PriorityLevel),
		Context:       ctx,
	}
	// Map keys in `ctx` are sorted by Go's JSON encoder (alphabetical),
	// locking determinism. The outer Marshal CANNOT fail on this shape
	// (canonical struct fields are scalar strings + map[string]string)
	// — if Go's stdlib ever regresses that invariant we want a LOUD
	// panic at hash time, NOT a silent hash collision downstream.
	buf, err := json.Marshal(c)
	if err != nil {
		panic(fmt.Sprintf("hash.go: encoding/json.Marshal failed on canonical hash input: %v", err))
	}
	return sha256Hex(string(buf))
}
