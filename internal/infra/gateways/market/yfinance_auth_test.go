package market

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newAuthTestServer creates a mock server that handles both cookie and crumb endpoints.
// cookiePath is served with Set-Cookie headers; crumbPath returns the crumb string.
func newAuthTestServer(t *testing.T, crumb string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cookie":
			http.SetCookie(w, &http.Cookie{
				Name:  "A1",
				Value: "test-session-cookie",
			})
			http.SetCookie(w, &http.Cookie{
				Name:  "A3",
				Value: "test-consent-cookie",
			})
			w.WriteHeader(http.StatusOK)
		case "/crumb":
			// Verify cookies are sent
			cookies := r.Cookies()
			if len(cookies) == 0 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(crumb))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestYFinanceAuth_FetchAuth_Success(t *testing.T) {
	server := newAuthTestServer(t, "test-crumb-abc123")
	defer server.Close()

	auth := NewYFinanceAuth(
		server.Client(),
		server.URL+"/cookie",
		server.URL+"/crumb",
		6*time.Hour,
		zap.NewNop(),
	)

	err := auth.EnsureAuth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "test-crumb-abc123", auth.GetCrumb())
	assert.True(t, auth.isValidPublic())
}

func TestYFinanceAuth_FetchAuth_CookieFailure(t *testing.T) {
	// Server returns no cookies
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // 200 but no Set-Cookie header
	}))
	defer server.Close()

	auth := NewYFinanceAuth(
		server.Client(),
		server.URL+"/cookie",
		server.URL+"/crumb",
		6*time.Hour,
		zap.NewNop(),
	)

	err := auth.EnsureAuth(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no cookies received")
}

func TestYFinanceAuth_FetchAuth_CrumbFailure(t *testing.T) {
	// Server serves cookies but crumb endpoint returns 500
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cookie":
			http.SetCookie(w, &http.Cookie{Name: "A1", Value: "session"})
			w.WriteHeader(http.StatusOK)
		case "/crumb":
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	auth := NewYFinanceAuth(
		server.Client(),
		server.URL+"/cookie",
		server.URL+"/crumb",
		6*time.Hour,
		zap.NewNop(),
	)

	err := auth.EnsureAuth(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "crumb request returned status 500")
}

func TestYFinanceAuth_FetchAuth_EmptyCrumb(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cookie":
			http.SetCookie(w, &http.Cookie{Name: "A1", Value: "session"})
			w.WriteHeader(http.StatusOK)
		case "/crumb":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("   ")) // whitespace-only crumb
		}
	}))
	defer server.Close()

	auth := NewYFinanceAuth(
		server.Client(),
		server.URL+"/cookie",
		server.URL+"/crumb",
		6*time.Hour,
		zap.NewNop(),
	)

	err := auth.EnsureAuth(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty crumb")
}

func TestYFinanceAuth_EnsureAuth_Caching(t *testing.T) {
	var fetchCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cookie":
			fetchCount.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "A1", Value: "session"})
			w.WriteHeader(http.StatusOK)
		case "/crumb":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("cached-crumb"))
		}
	}))
	defer server.Close()

	auth := NewYFinanceAuth(
		server.Client(),
		server.URL+"/cookie",
		server.URL+"/crumb",
		6*time.Hour,
		zap.NewNop(),
	)

	// First call should fetch
	err := auth.EnsureAuth(context.Background())
	require.NoError(t, err)

	// Second call should use cached credentials
	err = auth.EnsureAuth(context.Background())
	require.NoError(t, err)

	// Third call should still use cache
	err = auth.EnsureAuth(context.Background())
	require.NoError(t, err)

	assert.Equal(t, int32(1), fetchCount.Load(), "auth should only be fetched once")
	assert.Equal(t, "cached-crumb", auth.GetCrumb())
}

func TestYFinanceAuth_EnsureAuth_ConcurrentAccess(t *testing.T) {
	var fetchCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cookie":
			fetchCount.Add(1)
			// Small delay to simulate network latency
			time.Sleep(10 * time.Millisecond)
			http.SetCookie(w, &http.Cookie{Name: "A1", Value: "session"})
			w.WriteHeader(http.StatusOK)
		case "/crumb":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("concurrent-crumb"))
		}
	}))
	defer server.Close()

	auth := NewYFinanceAuth(
		server.Client(),
		server.URL+"/cookie",
		server.URL+"/crumb",
		6*time.Hour,
		zap.NewNop(),
	)

	// Launch 10 concurrent EnsureAuth calls
	var wg sync.WaitGroup
	errCh := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := auth.EnsureAuth(context.Background()); err != nil {
				errCh <- err
			}
		}()
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent EnsureAuth failed: %v", err)
	}

	// Due to mutex, auth should be fetched a small number of times (ideally 1)
	assert.LessOrEqual(t, fetchCount.Load(), int32(2),
		"auth should not be fetched many times concurrently")
	assert.Equal(t, "concurrent-crumb", auth.GetCrumb())
}

func TestYFinanceAuth_Invalidate(t *testing.T) {
	var fetchCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cookie":
			fetchCount.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "A1", Value: "session"})
			w.WriteHeader(http.StatusOK)
		case "/crumb":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("fresh-crumb"))
		}
	}))
	defer server.Close()

	auth := NewYFinanceAuth(
		server.Client(),
		server.URL+"/cookie",
		server.URL+"/crumb",
		6*time.Hour,
		zap.NewNop(),
	)

	// Initial fetch
	err := auth.EnsureAuth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(1), fetchCount.Load())

	// Invalidate
	auth.Invalidate()
	assert.Equal(t, "", auth.GetCrumb())

	// Should re-fetch after invalidation
	err = auth.EnsureAuth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(2), fetchCount.Load())
	assert.Equal(t, "fresh-crumb", auth.GetCrumb())
}

func TestYFinanceAuth_Expiry(t *testing.T) {
	var fetchCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cookie":
			fetchCount.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "A1", Value: "session"})
			w.WriteHeader(http.StatusOK)
		case "/crumb":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("expiry-crumb"))
		}
	}))
	defer server.Close()

	// Use 1ms TTL so it expires immediately
	auth := NewYFinanceAuth(
		server.Client(),
		server.URL+"/cookie",
		server.URL+"/crumb",
		1*time.Millisecond,
		zap.NewNop(),
	)

	err := auth.EnsureAuth(context.Background())
	require.NoError(t, err)

	// Wait for expiry
	time.Sleep(5 * time.Millisecond)

	// Should re-fetch because credentials expired
	err = auth.EnsureAuth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(2), fetchCount.Load())
}

func TestYFinanceAuth_ApplyCookies(t *testing.T) {
	server := newAuthTestServer(t, "apply-cookies-crumb")
	defer server.Close()

	auth := NewYFinanceAuth(
		server.Client(),
		server.URL+"/cookie",
		server.URL+"/crumb",
		6*time.Hour,
		zap.NewNop(),
	)

	err := auth.EnsureAuth(context.Background())
	require.NoError(t, err)

	// Create a request and apply cookies
	req, _ := http.NewRequest("GET", "https://example.com/test", nil)
	auth.ApplyCookies(req)

	cookies := req.Cookies()
	assert.GreaterOrEqual(t, len(cookies), 2, "should have at least 2 cookies applied")

	cookieNames := make([]string, len(cookies))
	for i, c := range cookies {
		cookieNames[i] = c.Name
	}
	assert.Contains(t, cookieNames, "A1")
	assert.Contains(t, cookieNames, "A3")
}

func TestYFinanceAuth_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		time.Sleep(1 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	auth := NewYFinanceAuth(
		server.Client(),
		server.URL+"/cookie",
		server.URL+"/crumb",
		6*time.Hour,
		zap.NewNop(),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := auth.EnsureAuth(ctx)
	assert.Error(t, err)
}

// isValidPublic exposes isValid for testing only.
func (a *YFinanceAuth) isValidPublic() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.isValid()
}
