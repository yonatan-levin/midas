package integration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Minimal contract fuzz to ensure invalid inputs do not 5xx
func TestAPIFuzz_InvalidInputs_No5xx(t *testing.T) {
	env := SetupTestEnvironment(t)
	if env == nil {
		return
	}
	defer env.Cleanup()

	// build very long path
	long := make([]byte, 512)
	for i := range long {
		long[i] = 'A'
	}
	cases := []string{
		"/api/v1/fair-value/",                     // empty
		"/api/v1/fair-value/!!!!!!!!",             // bad chars
		"/api/v1/fair-value/TOO_LONG_TICKER",      // long
		"/api/v1/fair-value/" + string(long),      // very long
		"/api/v1/fair-value/%00%00%00",            // encoded nulls
		"/api/v1/fair-value/INV@LID$",             // invalid symbols
		"/api/v1/fair-value/%F0%9F%98%80",         // emoji
		"/api/v1/fair-value/中文",                   // unicode
		"/api/v1/fair-value/CASE-SENSITIVE-CHECK", // case
		"/api/v1/fair-value/with/slash",           // extra segment
	}

	for _, path := range cases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		// unauthenticated on purpose
		w := httptest.NewRecorder()
		env.Router.ServeHTTP(w, req)

		if w.Code >= 500 {
			t.Fatalf("expected <500 for %s, got %d body=%s", path, w.Code, w.Body.String())
		}
	}
}

// Ensure wrong HTTP methods do not cause 5xx
func TestAPIFuzz_WrongMethods_No5xx(t *testing.T) {
	env := SetupTestEnvironment(t)
	if env == nil {
		return
	}
	defer env.Cleanup()

	methodCases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/fair-value/AAPL"},
		{http.MethodGet, "/api/v1/fair-value/bulk"},
	}

	for _, c := range methodCases {
		req := httptest.NewRequest(c.method, c.path, nil)
		w := httptest.NewRecorder()
		env.Router.ServeHTTP(w, req)
		if w.Code >= 500 {
			t.Fatalf("expected <500 for %s %s, got %d body=%s", c.method, c.path, w.Code, w.Body.String())
		}
	}
}

// Bulk payload fuzz: invalid/malformed input must not cause 5xx
func TestAPIFuzz_BulkInvalidPayloads_No5xx(t *testing.T) {
	env := SetupTestEnvironment(t)
	if env == nil {
		return
	}
	defer env.Cleanup()

	payloads := []string{
		``,                       // empty body
		`{`,                      // malformed JSON
		`{"tickers":null}`,       // null list
		`{"tickers":[]}`,         // empty list
		`{"tickers":[""]}`,       // empty ticker
		`{"tickers":["!!!!!!"]}`, // invalid ticker chars
	}

	for _, body := range payloads {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/fair-value/bulk", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		// unauthenticated on purpose
		w := httptest.NewRecorder()
		env.Router.ServeHTTP(w, req)
		if w.Code >= 500 {
			t.Fatalf("expected <500 for payload %q, got %d body=%s", body, w.Code, w.Body.String())
		}
	}
}
