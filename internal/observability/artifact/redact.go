// Package artifact implements the Tier-3 per-request artifact bundle: a
// directory of raw + parsed payloads + before/after pipeline snapshots
// captured to disk so a single request's data flow can be replayed offline.
//
// See docs/refactoring/observability-narrative-and-artifacts-spec.md (§7).
package artifact

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// redactedValue is the sentinel string substituted in place of any value the
// redactor identifies as sensitive. Hard-coded so the JSON shape is stable
// (a length-changing replacement could break golden-file diffs).
const redactedValue = "<redacted>"

// HeaderRedactList is the spec §7.5 closed list of HTTP header names whose
// values must be replaced before any bundle file is written. Case-insensitive
// matching.
//
// Adding a new external API to Midas REQUIRES adding its auth header here AND
// extending the redactor fixture in redact_test.go (fail-on-leak invariant).
//
//nolint:gochecknoglobals // immutable enum
var HeaderRedactList = []string{
	"Authorization",
	"Cookie",
	"Set-Cookie",
	"X-API-Key",
}

// QueryRedactList is the spec §7.5 closed list of URL query-parameter names
// whose values must be redacted (case-insensitive). Yahoo's `crumb` and FRED's
// `api_key` are the two known external secrets we forward in URLs today.
//
//nolint:gochecknoglobals // immutable enum
var QueryRedactList = []string{
	"crumb",
	"api_key",
}

// secretKeyPattern matches any JSON-key (case-insensitive) that names a
// secret-bearing field. Pre-compiled at package init for hot-path cheapness.
//
//nolint:gochecknoglobals // immutable precompiled regex
var secretKeyPattern = regexp.MustCompile(`(?i)(password|secret|token|bearer)`)

// RedactedPaths records the dotted JSON paths the redactor visited and
// replaced for a single redaction pass. The bundle manifest carries this
// list under "redactions_applied" so consumers can audit what was removed.
type RedactedPaths []string

// RedactJSON walks parsed JSON (a map or slice tree as produced by
// json.Unmarshal) and replaces any value at a sensitive key with redactedValue.
// Returns the dotted paths that were redacted, sorted+deduplicated.
//
// Behaviour:
//   - Walks maps and slices recursively.
//   - On a map key that matches secretKeyPattern, the value is replaced
//     regardless of its type (string, number, nested object).
//   - Leaves all other values untouched (byte-level identity preserved).
func RedactJSON(v any) (any, RedactedPaths) {
	paths := make(map[string]struct{})
	out := redactWalk(v, "", paths)
	return out, sortedKeys(paths)
}

// RedactJSONBytes is the convenience entry point for raw JSON byte slices,
// e.g. an HTTP response body. It Unmarshal's, redacts, and re-Marshal's. If
// the input is not valid JSON it is returned unchanged with no redactions
// recorded — non-JSON payloads are someone else's problem to scrub.
func RedactJSONBytes(b []byte) ([]byte, RedactedPaths) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return b, nil
	}
	red, paths := RedactJSON(v)
	out, err := json.MarshalIndent(red, "", "  ")
	if err != nil {
		// Re-marshal failure is unexpected for valid Unmarshal'd data; fall
		// back to original bytes rather than dropping the file.
		return b, paths
	}
	return out, paths
}

// redactWalk is the recursive worker. Path is a dotted accumulator built as
// we descend (e.g. "headers.Authorization").
func redactWalk(v any, path string, paths map[string]struct{}) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			subPath := joinPath(path, k)
			if secretKeyPattern.MatchString(k) {
				t[k] = redactedValue
				paths[subPath] = struct{}{}
				continue
			}
			t[k] = redactWalk(child, subPath, paths)
		}
		return t
	case []any:
		for i, child := range t {
			t[i] = redactWalk(child, path, paths)
		}
		return t
	default:
		return v
	}
}

// RedactHeaders returns a deep-copy header map with all values for header
// names in HeaderRedactList replaced. Original is unchanged. Returns the
// list of redacted header keys (canonical form).
func RedactHeaders(h http.Header) (http.Header, RedactedPaths) {
	if h == nil {
		return nil, nil
	}
	out := make(http.Header, len(h))
	paths := make(map[string]struct{})

	// Build a quick lookup of canonical header names to redact.
	hide := make(map[string]struct{}, len(HeaderRedactList))
	for _, k := range HeaderRedactList {
		hide[http.CanonicalHeaderKey(k)] = struct{}{}
	}

	for k, vs := range h {
		canon := http.CanonicalHeaderKey(k)
		if _, drop := hide[canon]; drop {
			out[canon] = []string{redactedValue}
			paths["headers."+canon] = struct{}{}
			continue
		}
		// Copy slice to keep callers safe from aliasing.
		copied := make([]string, len(vs))
		copy(copied, vs)
		out[canon] = copied
	}
	return out, sortedKeys(paths)
}

// RedactQueryString takes a URL query string (the part after '?') and returns
// it with values for any QueryRedactList key replaced. Empty input returns
// empty output.
func RedactQueryString(raw string) (string, RedactedPaths) {
	if raw == "" {
		return "", nil
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		// On parse failure leave the input alone — better to surface a
		// possibly-leaky URL than to silently corrupt a non-conforming one.
		return raw, nil
	}

	hide := make(map[string]struct{}, len(QueryRedactList))
	for _, k := range QueryRedactList {
		hide[strings.ToLower(k)] = struct{}{}
	}

	paths := make(map[string]struct{})
	for k := range values {
		if _, drop := hide[strings.ToLower(k)]; drop {
			values.Set(k, redactedValue)
			paths["query."+k] = struct{}{}
		}
	}
	return values.Encode(), sortedKeys(paths)
}

// RedactURL canonicalises a URL string and returns it with any QueryRedactList
// query parameter redacted. Useful when capturing the request URL in a
// metadata sidecar.
func RedactURL(raw string) (string, RedactedPaths) {
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw, nil
	}
	if u.RawQuery == "" {
		return raw, nil
	}
	q, paths := RedactQueryString(u.RawQuery)
	u.RawQuery = q
	return u.String(), paths
}

// joinPath concatenates a dotted JSON path. Empty parent yields the bare
// child key.
func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

// sortedKeys returns a deterministic slice of map keys for stable golden-file
// comparisons.
func sortedKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
