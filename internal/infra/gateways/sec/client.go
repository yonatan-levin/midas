package sec

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/time/rate"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// Client implements the SEC gateway interface
type Client struct {
	httpClient     *http.Client
	config         *config.SECConfig
	logger         *zap.Logger
	rateLimiter    *rate.Limiter
	normalizedBase *url.URL
}

// NewClient creates a new SEC API client
func NewClient(cfg *config.SECConfig, logger *zap.Logger) *Client {
	// Create rate limiter (SEC allows max 10 requests per second)
	limiter := rate.NewLimiter(rate.Limit(cfg.RateLimit), 1)

	// Create HTTP client with timeout
	httpClient := &http.Client{
		Timeout: cfg.RequestTimeout,
		Transport: &http.Transport{
			MaxIdleConns:       10,
			IdleConnTimeout:    30 * time.Second,
			DisableCompression: false,
		},
	}

	// Normalize base URL once
	base, _ := url.Parse(cfg.BaseURL)
	if base == nil {
		base = &url.URL{Scheme: "https", Host: "data.sec.gov"}
	}

	return &Client{
		httpClient:     httpClient,
		config:         cfg,
		logger:         logger.Named("sec-client"),
		rateLimiter:    limiter,
		normalizedBase: base,
	}
}

// secXBRLURL builds a fully-qualified URL ensuring /api/xbrl prefix.
func (c *Client) secXBRLURL(segments ...string) (string, error) {
	base := *c.normalizedBase // shallow copy
	path := base.Path
	trim := func(p string) string {
		if p == "" || p == "/" {
			return ""
		}
		return strings.TrimSuffix(p, "/")
	}
	path = trim(path)
	if !strings.HasSuffix(path, "/api/xbrl") {
		if path == "" {
			path = "/api/xbrl"
		} else if strings.HasSuffix(path, "/api") {
			path = path + "/xbrl"
		} else if strings.Contains(path, "/api/xbrl") {
			// leave as-is
		} else {
			path = path + "/api/xbrl"
		}
	}
	joined, err := url.JoinPath(base.Scheme+"://"+base.Host+path, segments...)
	if err != nil {
		return "", err
	}
	return joined, nil
}

// formatCIK converts input to zero-padded 10-digit with CIK prefix.
func formatCIK(cik string) (string, error) {
	if cik == "" {
		return "", fmt.Errorf("CIK cannot be empty")
	}
	n, err := strconv.Atoi(cik)
	if err != nil {
		return "", fmt.Errorf("invalid CIK: %s", cik)
	}
	return fmt.Sprintf("CIK%010d", n), nil
}

// GetCompanyFacts retrieves company facts from SEC API
func (c *Client) GetCompanyFacts(ctx context.Context, cik string) (*ports.SECCompanyFacts, error) {
	// Wait for rate limiter
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter error: %w", err)
	}

	// Format CIK with leading zeros (SEC requires 10 digits)
	formattedCIK, errFmt := formatCIK(cik)
	if errFmt != nil {
		return nil, errFmt
	}

	urlStr, errURL := c.secXBRLURL("companyfacts", formattedCIK+".json")
	if errURL != nil {
		return nil, errURL
	}

	c.logger.Debug("Fetching company facts",
		zap.String("cik", cik),
		zap.String("url", urlStr))

	var facts *ports.SECCompanyFacts
	var err error

	// Implement retry logic
	for attempt := 0; attempt < c.config.MaxRetries; attempt++ {
		facts, err = c.makeRequest(ctx, urlStr)
		if err == nil {
			break
		}

		if attempt < c.config.MaxRetries-1 {
			backoff := time.Duration(attempt+1) * c.config.RetryBackoffBase
			c.logger.Warn("Request failed, retrying",
				zap.String("cik", cik),
				zap.Int("attempt", attempt+1),
				zap.Duration("backoff", backoff),
				zap.Error(err))

			select {
			case <-time.After(backoff):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	if err != nil {
		c.logger.Error("Failed to fetch company facts after retries",
			zap.String("cik", cik),
			zap.Int("max_retries", c.config.MaxRetries),
			zap.Error(err))
		return nil, fmt.Errorf("failed to fetch company facts for CIK %s: %w", cik, err)
	}

	// Count total concepts across all taxonomies for accurate logging
	totalConcepts := 0
	for _, concepts := range facts.Facts {
		totalConcepts += len(concepts)
	}
	c.logger.Info("Successfully fetched company facts",
		zap.String("cik", cik),
		zap.String("entity_name", facts.EntityName),
		zap.Int("taxonomy_count", len(facts.Facts)),
		zap.Int("concept_count", totalConcepts))

	return facts, nil
}

// GetCompanyConcepts retrieves company concepts from SEC API for a specific tag
func (c *Client) GetCompanyConcepts(ctx context.Context, cik string, tag string) (*entities.ConceptResponse, error) {
	// Wait for rate limiter
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter error: %w", err)
	}

	// Format CIK with leading zeros (SEC requires 10 digits)
	formattedCIK, errFmt := formatCIK(cik)
	if errFmt != nil {
		return nil, errFmt
	}
	urlStr, errURL := c.secXBRLURL("companyconcept", formattedCIK, "us-gaap", tag+".json")
	if errURL != nil {
		return nil, errURL
	}

	c.logger.Debug("Fetching company concepts",
		zap.String("cik", cik),
		zap.String("tag", tag),
		zap.String("url", urlStr))

	var conceptResponse *entities.ConceptResponse
	var err error

	// Implement retry logic
	for attempt := 0; attempt < c.config.MaxRetries; attempt++ {
		conceptResponse, err = c.makeConceptRequest(ctx, urlStr)
		if err == nil {
			break
		}

		if attempt < c.config.MaxRetries-1 {
			backoff := time.Duration(attempt+1) * c.config.RetryBackoffBase
			c.logger.Warn("Concept request failed, retrying",
				zap.String("cik", cik),
				zap.String("tag", tag),
				zap.Int("attempt", attempt+1),
				zap.Duration("backoff", backoff),
				zap.Error(err))

			select {
			case <-time.After(backoff):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	if err != nil {
		c.logger.Error("Failed to fetch company concepts after retries",
			zap.String("cik", cik),
			zap.String("tag", tag),
			zap.Int("max_retries", c.config.MaxRetries),
			zap.Error(err))
		return nil, fmt.Errorf("failed to fetch company concepts for CIK %s, tag %s: %w", cik, tag, err)
	}

	c.logger.Info("Successfully fetched company concepts",
		zap.String("cik", cik),
		zap.String("tag", tag),
		zap.String("entity_name", conceptResponse.EntityName))

	return conceptResponse, nil
}

// GetTickerCIKMapping retrieves the ticker-to-CIK mapping from SEC
func (c *Client) GetTickerCIKMapping(ctx context.Context) (map[string]string, error) {
	// Wait for rate limiter
	if err := c.rateLimiter.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter error: %w", err)
	}

	url := c.config.TickerMappingURL

	c.logger.Debug("Fetching ticker-CIK mapping", zap.String("url", url))

	var mapping map[string]string
	var err error

	// Implement retry logic
	for attempt := 0; attempt < c.config.MaxRetries; attempt++ {
		mapping, err = c.makeTickerMappingRequest(ctx, url)
		if err == nil {
			break
		}

		if attempt < c.config.MaxRetries-1 {
			backoff := time.Duration(attempt+1) * c.config.RetryBackoffBase
			c.logger.Warn("Ticker mapping request failed, retrying",
				zap.String("url", url),
				zap.Int("attempt", attempt+1),
				zap.Duration("backoff", backoff),
				zap.Error(err))

			select {
			case <-time.After(backoff):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	if err != nil {
		c.logger.Error("Failed to fetch ticker-CIK mapping after retries",
			zap.String("url", url),
			zap.Int("max_retries", c.config.MaxRetries),
			zap.Error(err))
		return nil, fmt.Errorf("failed to fetch ticker-CIK mapping: %w", err)
	}

	c.logger.Info("Successfully fetched ticker-CIK mapping",
		zap.Int("mapping_count", len(mapping)))

	return mapping, nil
}

// makeRequest executes an HTTP request to SEC API
func (c *Client) makeRequest(ctx context.Context, url string) (*ports.SECCompanyFacts, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set required headers for SEC API
	req.Header.Set("User-Agent", c.config.UserAgent)
	req.Header.Set("Accept", "application/json")
	//req.Header.Set("Host", "data.sec.gov")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Handle different HTTP status codes
	switch resp.StatusCode {
	case http.StatusOK:
		// Success, continue to parse response
	case http.StatusNotFound:
		return nil, fmt.Errorf("company facts not found (404)")
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("rate limited by SEC API (429)")
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
		return nil, fmt.Errorf("SEC API server error (%d)", resp.StatusCode)
	default:
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("SEC API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse the JSON response
	var facts ports.SECCompanyFacts
	if err := json.NewDecoder(resp.Body).Decode(&facts); err != nil {

		return nil, fmt.Errorf("failed to decode SEC response: %w", err)
	}

	// Validate the response
	if facts.CIK == "" {
		return nil, fmt.Errorf("invalid response: missing CIK")
	}

	if facts.EntityName == "" {
		return nil, fmt.Errorf("invalid response: missing entity name")
	}

	if len(facts.Facts) == 0 {
		return nil, fmt.Errorf("invalid response: no facts found")
	}

	return &facts, nil
}

// makeConceptRequest executes an HTTP request to SEC Company Concept API
func (c *Client) makeConceptRequest(ctx context.Context, url string) (*entities.ConceptResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set required headers for SEC API
	req.Header.Set("User-Agent", c.config.UserAgent)
	req.Header.Set("Accept", "application/json")

	//req.Host = "data.sec.gov"  if we really need to force it

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Handle different HTTP status codes
	switch resp.StatusCode {
	case http.StatusOK:
		// Success, continue to parse response
	case http.StatusNotFound:
		return nil, fmt.Errorf("company concept not found (404)")
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("rate limited by SEC API (429)")
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
		return nil, fmt.Errorf("SEC API server error (%d)", resp.StatusCode)
	default:
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("SEC API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse the JSON response
	var conceptResponse entities.ConceptResponse
	if err := json.NewDecoder(resp.Body).Decode(&conceptResponse); err != nil {
		return nil, fmt.Errorf("failed to decode SEC concept response: %w", err)
	}

	// Validate the response
	if conceptResponse.CIK == "" {
		return nil, fmt.Errorf("invalid response: missing CIK")
	}

	if conceptResponse.Tag == "" {
		return nil, fmt.Errorf("invalid response: missing tag")
	}

	return &conceptResponse, nil
}

// makeTickerMappingRequest executes an HTTP request to SEC ticker mapping API
func (c *Client) makeTickerMappingRequest(ctx context.Context, url string) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set required headers for SEC API
	req.Header.Set("User-Agent", c.config.UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Handle different HTTP status codes
	switch resp.StatusCode {
	case http.StatusOK:
		// Success, continue to parse response
	case http.StatusNotFound:
		return nil, fmt.Errorf("ticker mapping not found (404)")
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("rate limited by SEC API (429)")
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable:
		return nil, fmt.Errorf("SEC API server error (%d)", resp.StatusCode)
	default:
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("SEC API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse the ticker mapping JSON (top-level object keyed by numeric strings).
	// UseNumber avoids float rounding for large integers.
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	var rawMapping map[string]map[string]interface{}
	if err := decoder.Decode(&rawMapping); err != nil {
		return nil, fmt.Errorf("failed to decode ticker mapping: %w", err)
	}

	// Convert to ticker -> CIK mapping (uppercase tickers, CIK as string without padding)
	mapping := make(map[string]string)
	for _, entry := range rawMapping {
		// ticker
		var ticker string
		if v, ok := entry["ticker"].(string); ok {
			ticker = v
		}
		if ticker == "" {
			continue
		}

		// cik_str can be json.Number, string, or number
		var cik string
		if num, ok := entry["cik_str"].(json.Number); ok {
			cik = num.String()
		} else if s, ok := entry["cik_str"].(string); ok {
			cik = s
		} else if f, ok := entry["cik_str"].(float64); ok {
			cik = strconv.FormatInt(int64(f), 10)
		}
		if cik == "" {
			continue
		}

		mapping[strings.ToUpper(ticker)] = cik
	}

	return mapping, nil
}

// HealthCheck performs a health check on the SEC API
func (c *Client) HealthCheck(ctx context.Context) error {
	// Try to fetch ticker mapping as a simple health check
	_, err := c.GetTickerCIKMapping(ctx)
	if err != nil {
		return fmt.Errorf("SEC API health check failed: %w", err)
	}
	return nil
}
