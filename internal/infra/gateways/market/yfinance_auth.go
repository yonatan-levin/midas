package market

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
)

// YFinanceAuth manages Yahoo Finance cookie + crumb authentication.
//
// Yahoo Finance requires a session cookie (obtained from fc.yahoo.com) and a
// crumb token (obtained from query2.finance.yahoo.com/v1/test/getcrumb) on all
// API requests. This struct handles fetching, caching, and refreshing these
// credentials in a thread-safe manner.
type YFinanceAuth struct {
	httpClient *http.Client
	cookieURL  string
	crumbURL   string
	logger     *zap.Logger

	mu        sync.Mutex
	cookies   []*http.Cookie
	crumb     string
	expiresAt time.Time
	authTTL   time.Duration
}

// NewYFinanceAuth creates a new auth manager for Yahoo Finance cookie+crumb flow.
func NewYFinanceAuth(httpClient *http.Client, cookieURL, crumbURL string, authTTL time.Duration, logger *zap.Logger) *YFinanceAuth {
	if authTTL <= 0 {
		authTTL = 6 * time.Hour
	}
	return &YFinanceAuth{
		httpClient: httpClient,
		cookieURL:  cookieURL,
		crumbURL:   crumbURL,
		authTTL:    authTTL,
		logger:     logger.Named("yfinance-auth"),
	}
}

// EnsureAuth ensures valid auth credentials are available.
// It fetches new credentials if none exist or if they have expired.
// Thread-safe: concurrent callers will wait for a single fetch.
func (a *YFinanceAuth) EnsureAuth(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.isValid() {
		return nil
	}

	return a.fetchAuth(ctx)
}

// GetCrumb returns the current crumb token.
func (a *YFinanceAuth) GetCrumb() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.crumb
}

// ApplyCookies sets the auth cookies on an HTTP request.
func (a *YFinanceAuth) ApplyCookies(req *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, cookie := range a.cookies {
		req.AddCookie(cookie)
	}
}

// Invalidate marks auth credentials as expired, forcing a refresh on next EnsureAuth call.
//
// Intentionally uses the singleton logger, not logctx.Or — Invalidate has no
// ctx parameter and adding one would be a public API change for a method that
// is mostly called from non-request paths (scheduler refresh). The surrounding
// request-path methods correctly use logctx.Or.
func (a *YFinanceAuth) Invalidate() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.expiresAt = time.Time{}
	a.crumb = ""
	a.cookies = nil
	a.logger.Debug("Auth credentials invalidated")
}

// isValid reports whether current credentials are still usable.
// Caller must hold a.mu.
func (a *YFinanceAuth) isValid() bool {
	return a.crumb != "" && len(a.cookies) > 0 && time.Now().Before(a.expiresAt)
}

// fetchAuth performs the two-step cookie + crumb retrieval.
// Caller must hold a.mu.
func (a *YFinanceAuth) fetchAuth(ctx context.Context) error {
	logctx.Or(ctx, a.logger).Debug("Fetching new Yahoo Finance auth credentials",
		zap.String("cookie_url", a.cookieURL),
		zap.String("crumb_url", a.crumbURL))

	// Step 1: GET cookie URL to obtain session cookies
	cookies, err := a.fetchCookies(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch Yahoo Finance cookies: %w", err)
	}

	// Step 2: GET crumb URL with the session cookies
	crumb, err := a.fetchCrumb(ctx, cookies)
	if err != nil {
		return fmt.Errorf("failed to fetch Yahoo Finance crumb: %w", err)
	}

	a.cookies = cookies
	a.crumb = crumb
	a.expiresAt = time.Now().Add(a.authTTL)

	logctx.Or(ctx, a.logger).Info("Yahoo Finance auth credentials obtained",
		zap.Int("cookies_count", len(cookies)),
		zap.Time("expires_at", a.expiresAt))

	return nil
}

// fetchCookies retrieves session cookies from Yahoo's consent/cookie page.
func (a *YFinanceAuth) fetchCookies(ctx context.Context) ([]*http.Cookie, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", a.cookieURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie request: %w", err)
	}

	req.Header.Set("User-Agent", yahooUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cookie request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Drain the body so the connection can be reused
	_, _ = io.Copy(io.Discard, resp.Body)

	cookies := resp.Cookies()
	if len(cookies) == 0 {
		return nil, fmt.Errorf("no cookies received from %s (status %d)", a.cookieURL, resp.StatusCode)
	}

	logctx.Or(ctx, a.logger).Debug("Cookies obtained",
		zap.Int("count", len(cookies)),
		zap.Int("status", resp.StatusCode))

	return cookies, nil
}

// fetchCrumb retrieves the crumb token using the session cookies.
func (a *YFinanceAuth) fetchCrumb(ctx context.Context, cookies []*http.Cookie) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", a.crumbURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create crumb request: %w", err)
	}

	req.Header.Set("User-Agent", yahooUserAgent)
	req.Header.Set("Accept", "*/*")

	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("crumb request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("crumb request returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read crumb response: %w", err)
	}

	crumb := strings.TrimSpace(string(body))
	if crumb == "" {
		return "", fmt.Errorf("empty crumb received from %s", a.crumbURL)
	}

	logctx.Or(ctx, a.logger).Debug("Crumb obtained", zap.String("crumb", crumb))

	return crumb, nil
}

// yahooUserAgent is a realistic browser User-Agent to satisfy Yahoo's anti-bot checks.
const yahooUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
