package leases

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// CacheKey represents a cache key for lease calculations
type CacheKey struct {
	Ticker     string    `json:"ticker"`
	FilingDate time.Time `json:"filing_date"`
	ConfigHash string    `json:"config_hash"`
	DataHash   string    `json:"data_hash"`
}

// String returns a string representation of the cache key
func (ck *CacheKey) String() string {
	return fmt.Sprintf("lease_pv_%s_%s_%s_%s",
		ck.Ticker,
		ck.FilingDate.Format("2006-01-02"),
		ck.ConfigHash[:8],
		ck.DataHash[:8])
}

// CacheEntry represents a cached calculation result
type CacheEntry struct {
	Key       *CacheKey           `json:"key"`
	Result    *PresentValueResult `json:"result"`
	CachedAt  time.Time           `json:"cached_at"`
	ExpiresAt time.Time           `json:"expires_at"`
	HitCount  int                 `json:"hit_count"`
	LastHit   time.Time           `json:"last_hit"`
}

// IsExpired checks if the cache entry has expired
func (ce *CacheEntry) IsExpired() bool {
	return time.Now().After(ce.ExpiresAt)
}

// LeaseCalculationCache provides caching for lease present value calculations
type LeaseCalculationCache struct {
	cache      map[string]*CacheEntry
	mutex      sync.RWMutex
	ttl        time.Duration
	maxSize    int
	hitCount   int64
	missCount  int64
	evictCount int64

	// TODO: Add Redis integration for distributed caching
	// redisClient *redis.Client
}

// NewLeaseCalculationCache creates a new cache instance
func NewLeaseCalculationCache(ttl time.Duration, maxSize int) *LeaseCalculationCache {
	cache := &LeaseCalculationCache{
		cache:   make(map[string]*CacheEntry),
		ttl:     ttl,
		maxSize: maxSize,
	}

	// Start background cleanup goroutine
	go cache.startCleanupWorker()

	return cache
}

// Get retrieves a cached result
func (c *LeaseCalculationCache) Get(key *CacheKey) (*PresentValueResult, bool) {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	entry, exists := c.cache[key.String()]
	if !exists {
		c.missCount++
		return nil, false
	}

	if entry.IsExpired() {
		c.missCount++
		// Don't remove here to avoid write lock, let cleanup worker handle it
		return nil, false
	}

	// Update hit statistics
	c.hitCount++
	entry.HitCount++
	entry.LastHit = time.Now()

	return entry.Result, true
}

// Set stores a calculation result in the cache
func (c *LeaseCalculationCache) Set(key *CacheKey, result *PresentValueResult) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Check if we need to evict entries
	if len(c.cache) >= c.maxSize {
		c.evictOldest()
	}

	entry := &CacheEntry{
		Key:       key,
		Result:    result,
		CachedAt:  time.Now(),
		ExpiresAt: time.Now().Add(c.ttl),
		HitCount:  0,
		LastHit:   time.Now(),
	}

	c.cache[key.String()] = entry
}

// evictOldest removes the oldest cache entry
func (c *LeaseCalculationCache) evictOldest() {
	var oldestKey string
	var oldestTime time.Time

	for key, entry := range c.cache {
		if oldestKey == "" || entry.CachedAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.CachedAt
		}
	}

	if oldestKey != "" {
		delete(c.cache, oldestKey)
		c.evictCount++
	}
}

// startCleanupWorker starts a background goroutine to clean expired entries
func (c *LeaseCalculationCache) startCleanupWorker() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.cleanupExpired()
	}
}

// cleanupExpired removes expired entries from the cache
func (c *LeaseCalculationCache) cleanupExpired() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	for key, entry := range c.cache {
		if entry.IsExpired() {
			delete(c.cache, key)
			c.evictCount++
		}
	}
}

// GetStats returns cache statistics
func (c *LeaseCalculationCache) GetStats() CacheStats {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	totalRequests := c.hitCount + c.missCount
	hitRate := 0.0
	if totalRequests > 0 {
		hitRate = float64(c.hitCount) / float64(totalRequests)
	}

	return CacheStats{
		Size:       len(c.cache),
		MaxSize:    c.maxSize,
		HitCount:   c.hitCount,
		MissCount:  c.missCount,
		EvictCount: c.evictCount,
		HitRate:    hitRate,
		TTL:        c.ttl,
	}
}

// CacheStats represents cache performance statistics
type CacheStats struct {
	Size       int           `json:"size"`
	MaxSize    int           `json:"max_size"`
	HitCount   int64         `json:"hit_count"`
	MissCount  int64         `json:"miss_count"`
	EvictCount int64         `json:"evict_count"`
	HitRate    float64       `json:"hit_rate"`
	TTL        time.Duration `json:"ttl"`
}

// Clear removes all entries from the cache
func (c *LeaseCalculationCache) Clear() {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.cache = make(map[string]*CacheEntry)
	c.hitCount = 0
	c.missCount = 0
	c.evictCount = 0
}

// CacheKeyGenerator generates cache keys for lease calculations
type CacheKeyGenerator struct {
	configVersion string
}

// NewCacheKeyGenerator creates a new cache key generator
func NewCacheKeyGenerator(configVersion string) *CacheKeyGenerator {
	return &CacheKeyGenerator{
		configVersion: configVersion,
	}
}

// GenerateKey generates a cache key for the given inputs
func (kg *CacheKeyGenerator) GenerateKey(data *entities.FinancialData, context *entities.CleaningContext, config *EstimationConfig) *CacheKey {
	// Generate hash of financial data
	dataHash := kg.hashFinancialData(data)

	// Generate hash of configuration
	configHash := kg.hashConfig(config)

	return &CacheKey{
		Ticker:     data.Ticker,
		FilingDate: data.FilingDate,
		ConfigHash: configHash,
		DataHash:   dataHash,
	}
}

// hashFinancialData generates a hash of relevant financial data fields
func (kg *CacheKeyGenerator) hashFinancialData(data *entities.FinancialData) string {
	// Include only fields that affect lease calculations
	hashInput := fmt.Sprintf("%.2f|%.2f|%.2f|%.2f|%.2f|%.2f|%.2f|%v",
		data.OperatingLeaseLiability,
		data.OperatingLeaseLiabilityCurrent,
		data.OperatingLeaseLiabilityNoncurrent,
		data.InterestExpense,
		data.TotalDebt,
		data.RiskFreeRate,
		data.Revenue,
		data.OperatingLeaseCommitments)

	hash := md5.Sum([]byte(hashInput))
	return hex.EncodeToString(hash[:])
}

// hashConfig generates a hash of the configuration
func (kg *CacheKeyGenerator) hashConfig(config *EstimationConfig) string {
	hashInput := fmt.Sprintf("%s|%s|%s|%.3f|%d|%.3f|%.3f|%.3f|%s",
		config.DiscountRateMethod,
		config.LeaseTermMethod,
		config.PaymentMethod,
		config.DefaultDiscountRate,
		config.DefaultLeaseTermYears,
		config.DefaultEscalationRate,
		config.MinimumRate,
		config.MaximumRate,
		kg.configVersion)

	hash := md5.Sum([]byte(hashInput))
	return hex.EncodeToString(hash[:])
}

// CircuitBreakerState represents the state of the circuit breaker
type CircuitBreakerState int

const (
	StateClosed CircuitBreakerState = iota
	StateOpen
	StateHalfOpen
)

// String returns the string representation of the circuit breaker state
func (s CircuitBreakerState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// CircuitBreaker provides circuit breaker pattern for lease calculations
type CircuitBreaker struct {
	config          *CircuitBreakerConfig
	state           CircuitBreakerState
	failureCount    int
	lastFailureTime time.Time
	nextRetryTime   time.Time
	halfOpenCalls   int
	mutex           sync.RWMutex

	// Statistics
	totalCalls    int64
	successCalls  int64
	failureCalls  int64
	rejectedCalls int64
}

// CircuitBreakerConfig holds configuration for the circuit breaker
type CircuitBreakerConfig struct {
	FailureThreshold int           `json:"failure_threshold"`
	RecoveryTimeout  time.Duration `json:"recovery_timeout"`
	HalfOpenMaxCalls int           `json:"half_open_max_calls"`
	CallTimeout      time.Duration `json:"call_timeout"`
}

// NewCircuitBreaker creates a new circuit breaker instance
func NewCircuitBreaker(config *CircuitBreakerConfig) *CircuitBreaker {
	return &CircuitBreaker{
		config: config,
		state:  StateClosed,
	}
}

// Call executes a function with circuit breaker protection
func (cb *CircuitBreaker) Call(ctx context.Context, fn func(context.Context) (*PresentValueResult, error)) (*PresentValueResult, error) {
	cb.mutex.Lock()
	cb.totalCalls++

	// Check if we should reject the call
	if cb.shouldReject() {
		cb.rejectedCalls++
		cb.mutex.Unlock()
		return nil, fmt.Errorf("circuit breaker is open, rejecting call")
	}

	// Allow the call
	cb.mutex.Unlock()

	// Create context with timeout
	callCtx, cancel := context.WithTimeout(ctx, cb.config.CallTimeout)
	defer cancel()

	// Execute the function
	result, err := fn(callCtx)

	// Update circuit breaker state based on result
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	if err != nil {
		cb.recordFailure()
		cb.failureCalls++
		return nil, err
	}

	cb.recordSuccess()
	cb.successCalls++
	return result, nil
}

// shouldReject determines if a call should be rejected
func (cb *CircuitBreaker) shouldReject() bool {
	switch cb.state {
	case StateClosed:
		return false
	case StateOpen:
		// Check if recovery timeout has passed
		if time.Now().After(cb.nextRetryTime) {
			cb.state = StateHalfOpen
			cb.halfOpenCalls = 0
			return false
		}
		return true
	case StateHalfOpen:
		// Allow limited calls in half-open state
		if cb.halfOpenCalls >= cb.config.HalfOpenMaxCalls {
			return true
		}
		cb.halfOpenCalls++
		return false
	default:
		return true
	}
}

// recordFailure records a failure and updates circuit breaker state
func (cb *CircuitBreaker) recordFailure() {
	cb.failureCount++
	cb.lastFailureTime = time.Now()

	switch cb.state {
	case StateClosed:
		if cb.failureCount >= cb.config.FailureThreshold {
			cb.state = StateOpen
			cb.nextRetryTime = time.Now().Add(cb.config.RecoveryTimeout)
		}
	case StateHalfOpen:
		// Return to open state on failure
		cb.state = StateOpen
		cb.nextRetryTime = time.Now().Add(cb.config.RecoveryTimeout)
	}
}

// recordSuccess records a success and updates circuit breaker state
func (cb *CircuitBreaker) recordSuccess() {
	switch cb.state {
	case StateClosed:
		cb.failureCount = 0
	case StateHalfOpen:
		// Return to closed state after successful half-open calls
		cb.state = StateClosed
		cb.failureCount = 0
	}
}

// GetState returns the current circuit breaker state
func (cb *CircuitBreaker) GetState() CircuitBreakerState {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()
	return cb.state
}

// GetStats returns circuit breaker statistics
func (cb *CircuitBreaker) GetStats() CircuitBreakerStats {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()

	successRate := 0.0
	if cb.totalCalls > 0 {
		successRate = float64(cb.successCalls) / float64(cb.totalCalls)
	}

	return CircuitBreakerStats{
		State:           cb.state,
		FailureCount:    cb.failureCount,
		TotalCalls:      cb.totalCalls,
		SuccessCalls:    cb.successCalls,
		FailureCalls:    cb.failureCalls,
		RejectedCalls:   cb.rejectedCalls,
		SuccessRate:     successRate,
		LastFailureTime: cb.lastFailureTime,
		NextRetryTime:   cb.nextRetryTime,
	}
}

// CircuitBreakerStats represents circuit breaker performance statistics
type CircuitBreakerStats struct {
	State           CircuitBreakerState `json:"state"`
	FailureCount    int                 `json:"failure_count"`
	TotalCalls      int64               `json:"total_calls"`
	SuccessCalls    int64               `json:"success_calls"`
	FailureCalls    int64               `json:"failure_calls"`
	RejectedCalls   int64               `json:"rejected_calls"`
	SuccessRate     float64             `json:"success_rate"`
	LastFailureTime time.Time           `json:"last_failure_time"`
	NextRetryTime   time.Time           `json:"next_retry_time"`
}

// Reset resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.state = StateClosed
	cb.failureCount = 0
	cb.halfOpenCalls = 0
	cb.totalCalls = 0
	cb.successCalls = 0
	cb.failureCalls = 0
	cb.rejectedCalls = 0
}

// PerformanceOptimizedCalculator wraps the present value calculator with caching and circuit breaker
type PerformanceOptimizedCalculator struct {
	calculator     *PresentValueCalculator
	cache          *LeaseCalculationCache
	circuitBreaker *CircuitBreaker
	keyGenerator   *CacheKeyGenerator
	config         *EstimationConfig
}

// NewPerformanceOptimizedCalculator creates a new performance-optimized calculator
func NewPerformanceOptimizedCalculator(config *EstimationConfig) *PerformanceOptimizedCalculator {
	calculator := NewPresentValueCalculator(config)

	// Initialize cache if enabled
	var cache *LeaseCalculationCache
	if config.CacheEnabled {
		cache = NewLeaseCalculationCache(config.CacheTTL, 1000) // TODO: Make cache size configurable
	}

	// Initialize circuit breaker
	circuitBreakerConfig := &CircuitBreakerConfig{
		FailureThreshold: 5,
		RecoveryTimeout:  30 * time.Second,
		HalfOpenMaxCalls: 3,
		CallTimeout:      config.CalculationTimeout,
	}
	circuitBreaker := NewCircuitBreaker(circuitBreakerConfig)

	keyGenerator := NewCacheKeyGenerator("1.0.0") // TODO: Get version from config

	return &PerformanceOptimizedCalculator{
		calculator:     calculator,
		cache:          cache,
		circuitBreaker: circuitBreaker,
		keyGenerator:   keyGenerator,
		config:         config,
	}
}

// CalculatePresentValue calculates present value with caching and circuit breaker protection
func (poc *PerformanceOptimizedCalculator) CalculatePresentValue(ctx context.Context, data *entities.FinancialData, cleaningContext *entities.CleaningContext) (*PresentValueResult, error) {
	// Generate cache key
	var cacheKey *CacheKey
	if poc.cache != nil {
		cacheKey = poc.keyGenerator.GenerateKey(data, cleaningContext, poc.config)

		// Check cache first
		if result, found := poc.cache.Get(cacheKey); found {
			return result, nil
		}
	}

	// Execute calculation with circuit breaker protection
	result, err := poc.circuitBreaker.Call(ctx, func(ctx context.Context) (*PresentValueResult, error) {
		return poc.calculator.CalculatePresentValue(ctx, data, cleaningContext)
	})

	if err != nil {
		return nil, err
	}

	// Cache the result
	if poc.cache != nil && cacheKey != nil {
		poc.cache.Set(cacheKey, result)
	}

	return result, nil
}

// GetCacheStats returns cache statistics
func (poc *PerformanceOptimizedCalculator) GetCacheStats() *CacheStats {
	if poc.cache == nil {
		return nil
	}
	stats := poc.cache.GetStats()
	return &stats
}

// GetCircuitBreakerStats returns circuit breaker statistics
func (poc *PerformanceOptimizedCalculator) GetCircuitBreakerStats() CircuitBreakerStats {
	return poc.circuitBreaker.GetStats()
}

// ClearCache clears the calculation cache
func (poc *PerformanceOptimizedCalculator) ClearCache() {
	if poc.cache != nil {
		poc.cache.Clear()
	}
}

// ResetCircuitBreaker resets the circuit breaker to closed state
func (poc *PerformanceOptimizedCalculator) ResetCircuitBreaker() {
	poc.circuitBreaker.Reset()
}
