package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// TestMemoryCacheRepository_FinancialDataPlugFields_RoundTrip pins the DC-1
// Phase 0 contract for the cache persistence path: the four Other*Assets /
// Other*Liabilities plug fields populated by the SEC parser MUST survive a
// Set → Get cycle through the in-memory cache.
//
// The memory cache uses generic encoding/json (memory_cache_repository.go:63)
// so the JSON tags added in Phase 0 task 0.1 cover the new fields without any
// repository code change. This test pins that contract — a future change to
// the cache's serialization format (e.g., gob, msgpack) must keep the plug
// fields intact.
//
// Phase 0 invariant: no production consumer reads the plug fields yet, so a
// cache hit on a pre-Phase-0 row simply yields zero plugs (harmless). This
// test pins the post-Phase-0 round-trip; the pre-Phase-0 case is exercised
// implicitly by the JSON decoder treating missing keys as zero values.
//
// DC-1 spec reference:
// docs/refactoring/spec/datacleaner-component-primitive-and-parallel-views-spec.md
func TestMemoryCacheRepository_FinancialDataPlugFields_RoundTrip(t *testing.T) {
	repo := NewMemoryCacheRepository()
	ctx := context.Background()

	// AAPL-shape values across the four plug fields. Non-zero across all
	// four so a serialization bug that drops one cleanly fails the assertion.
	const (
		wantOtherCA  = 107_270_000_000.0
		wantOtherNCA = 209_189_000_000.0
		wantOtherCL  = 143_898_000_000.0
		wantOtherNCL = 23_491_000_000.0
	)

	data := &entities.FinancialData{
		Ticker:                     "AAPL",
		CIK:                        "0000320193",
		FilingPeriod:               "2023FY",
		FilingDate:                 time.Date(2023, 11, 3, 0, 0, 0, 0, time.UTC),
		OtherCurrentAssets:         wantOtherCA,
		OtherNonCurrentAssets:      wantOtherNCA,
		OtherCurrentLiabilities:    wantOtherCL,
		OtherNonCurrentLiabilities: wantOtherNCL,
	}

	const cacheKey = "financial_data:AAPL:2023FY"
	require.NoError(t, repo.Set(ctx, cacheKey, data, 1*time.Hour))

	var got entities.FinancialData
	require.NoError(t, repo.Get(ctx, cacheKey, &got))

	assert.Equal(t, wantOtherCA, got.OtherCurrentAssets,
		"OtherCurrentAssets must survive Set->Get through the cache")
	assert.Equal(t, wantOtherNCA, got.OtherNonCurrentAssets,
		"OtherNonCurrentAssets must survive Set->Get through the cache")
	assert.Equal(t, wantOtherCL, got.OtherCurrentLiabilities,
		"OtherCurrentLiabilities must survive Set->Get through the cache")
	assert.Equal(t, wantOtherNCL, got.OtherNonCurrentLiabilities,
		"OtherNonCurrentLiabilities must survive Set->Get through the cache")

	// Sanity: pre-Phase-0 fields also still round-trip, so we know the test
	// isn't accidentally pinning ONLY the new fields.
	assert.Equal(t, "AAPL", got.Ticker)
	assert.Equal(t, "0000320193", got.CIK)
	assert.Equal(t, "2023FY", got.FilingPeriod)
}
