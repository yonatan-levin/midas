package artifact_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
)

// TestRedactJSON_FailOnLeak is the spec §7.5 fail-on-leak guard. Every
// fixture entry exercises one entry in the redaction list. If a future
// refactor breaks the redactor, this test fails loudly.
//
// Fixture authoring rule: each new external API integrated into Midas MUST
// add a row here AND a corresponding entry in HeaderRedactList /
// QueryRedactList / secretKeyPattern.
func TestRedactJSON_FailOnLeak(t *testing.T) {
	cases := []struct {
		name        string
		input       map[string]any
		mustNotLeak []string // substrings that must NOT appear in marshalled output
		wantPath    string   // dotted path expected in RedactedPaths
	}{
		{
			name:        "password-key-at-root",
			input:       map[string]any{"password": "hunter2", "ok": "data"},
			mustNotLeak: []string{"hunter2"},
			wantPath:    "password",
		},
		{
			name: "secret-key-nested",
			input: map[string]any{
				"client": map[string]any{
					"client_secret": "shhh",
					"id":            "pub-123",
				},
			},
			mustNotLeak: []string{"shhh"},
			wantPath:    "client.client_secret",
		},
		{
			name: "token-key-anywhere-case-insensitive",
			input: map[string]any{
				"AccessToken": "deadbeef",
			},
			mustNotLeak: []string{"deadbeef"},
			wantPath:    "AccessToken",
		},
		{
			name: "bearer-token",
			input: map[string]any{
				"bearer_token": "abc.def.ghi",
			},
			mustNotLeak: []string{"abc.def.ghi"},
			wantPath:    "bearer_token",
		},
		{
			name: "deeply-nested-secret",
			input: map[string]any{
				"data": map[string]any{
					"auth": map[string]any{
						"api_secret": "leakme",
					},
				},
			},
			mustNotLeak: []string{"leakme"},
			wantPath:    "data.auth.api_secret",
		},
		{
			name: "secret-inside-array-element",
			input: map[string]any{
				"items": []any{
					map[string]any{"name": "ok"},
					map[string]any{"password": "hidden"},
				},
			},
			mustNotLeak: []string{"hidden"},
			wantPath:    "items.password",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, paths := artifact.RedactJSON(tc.input)

			// Marshal output and grep for forbidden substrings.
			b, err := json.Marshal(out)
			require.NoError(t, err)
			s := string(b)
			for _, leak := range tc.mustNotLeak {
				assert.NotContains(t, s, leak,
					"redactor leaked %q in output: %s", leak, s)
			}
			// json.Marshal HTML-escapes the angle brackets, so the on-wire
			// form is the unicode-escaped <redacted>. We check
			// for "redacted" (the unambiguous core of the sentinel) which
			// is encoding-agnostic.
			assert.Contains(t, s, "redacted",
				"sentinel keyword must be present: %s", s)
			assert.Contains(t, paths, tc.wantPath,
				"redacted-paths must record %s; got %v", tc.wantPath, paths)
		})
	}
}

// TestRedactJSON_LeavesNonSecretKeysAlone — pure non-regression: no false positives.
func TestRedactJSON_LeavesNonSecretKeysAlone(t *testing.T) {
	in := map[string]any{
		"name":     "Apple Inc.",
		"ticker":   "AAPL",
		"revenue":  394328000000.0,
		"nested":   map[string]any{"category": "tech"},
		"safelist": []any{"a", "b", "c"},
	}
	out, paths := artifact.RedactJSON(in)
	b, err := json.Marshal(out)
	require.NoError(t, err)
	s := string(b)

	assert.Contains(t, s, "Apple Inc.")
	assert.Contains(t, s, "AAPL")
	assert.Contains(t, s, "394328000000")
	assert.NotContains(t, s, "<redacted>")
	assert.Empty(t, paths, "no redactions should be recorded")
}

// TestRedactJSONBytes_InvalidJSONReturnsInputUnchanged — non-JSON payloads
// pass through unmodified (the redactor is JSON-only by design).
func TestRedactJSONBytes_InvalidJSONReturnsInputUnchanged(t *testing.T) {
	in := []byte("not-json: some=password=value")
	out, paths := artifact.RedactJSONBytes(in)
	assert.Equal(t, in, out)
	assert.Nil(t, paths)
}

// TestRedactJSONBytes_RedactsAndReformats — happy path for the bytes wrapper.
func TestRedactJSONBytes_RedactsAndReformats(t *testing.T) {
	in := []byte(`{"password":"x","ok":1}`)
	out, paths := artifact.RedactJSONBytes(in)

	assert.NotContains(t, string(out), `"x"`)
	assert.Contains(t, string(out), "redacted",
		"sentinel keyword must be present in marshalled output: %s", string(out))
	assert.Contains(t, paths, "password")
}

// TestRedactHeaders pins every entry in HeaderRedactList.
func TestRedactHeaders(t *testing.T) {
	h := http.Header{
		"Authorization":   []string{"Bearer abc"},
		"Cookie":          []string{"sid=xyz"},
		"Set-Cookie":      []string{"sid=xyz; Path=/"},
		"X-Api-Key":       []string{"key-123"},
		"Content-Type":    []string{"application/json"},
		"X-Custom-Header": []string{"value"},
	}

	out, paths := artifact.RedactHeaders(h)
	require.NotNil(t, out)

	// All listed redacts must produce <redacted>.
	for _, k := range []string{"Authorization", "Cookie", "Set-Cookie", "X-Api-Key"} {
		got := out.Get(k)
		assert.Equal(t, "<redacted>", got, "header %s must be redacted", k)
	}

	// Non-listed headers must pass through unchanged.
	assert.Equal(t, "application/json", out.Get("Content-Type"))
	assert.Equal(t, "value", out.Get("X-Custom-Header"))

	// All four redacted paths must be reported.
	for _, want := range []string{
		"headers.Authorization",
		"headers.Cookie",
		"headers.Set-Cookie",
		"headers.X-Api-Key",
	} {
		assert.Contains(t, paths, want)
	}
}

// TestRedactHeaders_OriginalUnchanged is the immutability invariant.
func TestRedactHeaders_OriginalUnchanged(t *testing.T) {
	h := http.Header{"Authorization": []string{"Bearer abc"}}
	_, _ = artifact.RedactHeaders(h)
	assert.Equal(t, "Bearer abc", h.Get("Authorization"),
		"input headers must not be mutated")
}

// TestRedactHeaders_NilSafe — never panic on nil input.
func TestRedactHeaders_NilSafe(t *testing.T) {
	out, paths := artifact.RedactHeaders(nil)
	assert.Nil(t, out)
	assert.Nil(t, paths)
}

// TestRedactQueryString covers Yahoo crumb and FRED api_key.
func TestRedactQueryString(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		mustHide  []string // substrings that must NOT appear
		wantPaths []string
	}{
		{
			name:      "yahoo-crumb",
			in:        "symbols=AAPL&crumb=abc123",
			mustHide:  []string{"abc123"},
			wantPaths: []string{"query.crumb"},
		},
		{
			name:      "fred-api-key",
			in:        "series_id=DGS10&api_key=topsecretkey&file_type=json",
			mustHide:  []string{"topsecretkey"},
			wantPaths: []string{"query.api_key"},
		},
		{
			name:      "no-secret-pass-through",
			in:        "ticker=AAPL&fields=price",
			mustHide:  nil,
			wantPaths: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, paths := artifact.RedactQueryString(tc.in)
			for _, h := range tc.mustHide {
				assert.False(t, strings.Contains(out, h),
					"must hide %q in output: %s", h, out)
			}
			for _, p := range tc.wantPaths {
				assert.Contains(t, paths, p)
			}
		})
	}
}

// TestRedactURL_FullURL — the URL form is what gets captured into bundles.
func TestRedactURL_FullURL(t *testing.T) {
	in := "https://api.stlouisfed.org/fred/series/observations?series_id=DGS10&api_key=secret123"
	out, paths := artifact.RedactURL(in)

	assert.NotContains(t, out, "secret123")
	assert.Contains(t, out, "api_key=%3Credacted%3E") // url.Values.Encode escapes <>
	assert.Contains(t, paths, "query.api_key")
}

// TestRedactURL_EmptyAndNoQuery — defensive paths.
func TestRedactURL_EmptyAndNoQuery(t *testing.T) {
	out, paths := artifact.RedactURL("")
	assert.Empty(t, out)
	assert.Nil(t, paths)

	out, paths = artifact.RedactURL("https://example.com/path")
	assert.Equal(t, "https://example.com/path", out)
	assert.Nil(t, paths)
}
