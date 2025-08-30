package valuation

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// BenchmarkValuationService_ParallelIOOperations measures the performance impact of parallel I/O
func BenchmarkValuationService_ParallelIOOperations(b *testing.B) {
	b.Run("Sequential", func(b *testing.B) {
		// Simulate sequential I/O operations as currently implemented
		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			// Simulate the current sequential approach
			start := time.Now()

			// Simulate financial data fetch (50ms)
			time.Sleep(50 * time.Millisecond)

			// Simulate market data fetch (30ms)
			time.Sleep(30 * time.Millisecond)

			// Simulate macro data fetch (20ms)
			time.Sleep(20 * time.Millisecond)

			totalTime := time.Since(start)

			// Should be around 100ms total
			if totalTime < 90*time.Millisecond {
				b.Fatalf("Expected ~100ms, got %v", totalTime)
			}
		}
	})

	b.Run("Parallel", func(b *testing.B) {
		// Simulate parallel I/O operations (proposed optimization)
		b.ResetTimer()
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			// Simulate the proposed parallel approach
			start := time.Now()

			var wg sync.WaitGroup
			wg.Add(3)

			// Simulate parallel fetches
			go func() {
				defer wg.Done()
				time.Sleep(50 * time.Millisecond) // Financial data
			}()

			go func() {
				defer wg.Done()
				time.Sleep(30 * time.Millisecond) // Market data
			}()

			go func() {
				defer wg.Done()
				time.Sleep(20 * time.Millisecond) // Macro data
			}()

			wg.Wait()
			totalTime := time.Since(start)

			// Should be around 50ms total (longest operation)
			if totalTime > 70*time.Millisecond {
				b.Fatalf("Expected ~50ms, got %v", totalTime)
			}
		}
	})
}

// BenchmarkDatabaseOperations_Sequential measures current sequential database calls
func BenchmarkDatabaseOperations_Sequential(b *testing.B) {
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Simulate current sequential database operations
		start := time.Now()

		// Financial data query - simulate complex query
		_, err := simulateFinancialDataQuery(ctx, "AAPL", 10)
		if err != nil {
			b.Fatalf("Financial data query failed: %v", err)
		}

		// Market data query - simulate quick lookup
		_, err = simulateMarketDataQuery(ctx, "AAPL")
		if err != nil {
			b.Fatalf("Market data query failed: %v", err)
		}

		// Macro data query - simulate metadata lookup
		_, err = simulateMacroDataQuery(ctx)
		if err != nil {
			b.Fatalf("Macro data query failed: %v", err)
		}

		_ = time.Since(start)
	}
}

// BenchmarkDatabaseOperations_Parallel measures proposed parallel database calls
func BenchmarkDatabaseOperations_Parallel(b *testing.B) {
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Simulate proposed parallel database operations
		start := time.Now()

		var (
			historicalData          *entities.HistoricalFinancialData
			marketData              *entities.MarketData
			macroData               *entities.MacroData
			errHist, errMkt, errMac error
		)

		var wg sync.WaitGroup
		wg.Add(3)

		// Financial data query
		go func() {
			defer wg.Done()
			historicalData, errHist = simulateFinancialDataQuery(ctx, "AAPL", 10)
		}()

		// Market data query
		go func() {
			defer wg.Done()
			marketData, errMkt = simulateMarketDataQuery(ctx, "AAPL")
		}()

		// Macro data query
		go func() {
			defer wg.Done()
			macroData, errMac = simulateMacroDataQuery(ctx)
		}()

		wg.Wait()

		// Check errors
		if errHist != nil {
			b.Fatalf("Financial data query failed: %v", errHist)
		}
		if errMkt != nil {
			b.Fatalf("Market data query failed: %v", errMkt)
		}
		if errMac != nil {
			b.Fatalf("Macro data query failed: %v", errMac)
		}

		// Validate results
		if historicalData == nil || marketData == nil || macroData == nil {
			b.Fatal("Expected all data to be present")
		}

		_ = time.Since(start)
	}
}

// BenchmarkMemoryAllocation_DataStructures measures memory allocation patterns
func BenchmarkMemoryAllocation_DataStructures(b *testing.B) {
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Create structures as they would be created in the service
		historicalData := &entities.HistoricalFinancialData{
			Ticker: "AAPL",
			Data: map[string]*entities.FinancialData{
				"2024Q3": createPerfTestFinancialDataWithDate("AAPL", "2024Q3", time.Date(2024, 9, 30, 0, 0, 0, 0, time.UTC)),
				"2024Q2": createPerfTestFinancialDataWithDate("AAPL", "2024Q2", time.Date(2024, 6, 30, 0, 0, 0, 0, time.UTC)),
				"2024Q1": createPerfTestFinancialDataWithDate("AAPL", "2024Q1", time.Date(2024, 3, 31, 0, 0, 0, 0, time.UTC)),
			},
		}

		marketData := createPerfTestMarketData("AAPL")
		macroData := createPerfTestMacroData()

		// Simulate the data processing that would happen
		latest, _ := historicalData.GetLatestPeriod()
		if latest == nil {
			b.Fatal("Expected latest data")
		}

		// Simulate calculations
		_ = marketData.CalculateMarketValue()
		_ = macroData.GetEffectiveRiskFreeRate()
	}
}

// Helper functions to simulate database operations

func simulateFinancialDataQuery(ctx context.Context, ticker string, periods int) (*entities.HistoricalFinancialData, error) {
	// Simulate realistic query time for complex financial data
	select {
	case <-time.After(2 * time.Millisecond): // Simulate query latency
		return &entities.HistoricalFinancialData{
			Ticker: ticker,
			Data: map[string]*entities.FinancialData{
				"2024Q3": createPerfTestFinancialData(ticker),
			},
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func simulateMarketDataQuery(ctx context.Context, ticker string) (*entities.MarketData, error) {
	// Simulate faster market data lookup
	select {
	case <-time.After(1 * time.Millisecond): // Simulate query latency
		return createPerfTestMarketData(ticker), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func simulateMacroDataQuery(ctx context.Context) (*entities.MacroData, error) {
	// Simulate quick macro data lookup
	select {
	case <-time.After(500 * time.Microsecond): // Simulate query latency
		return createPerfTestMacroData(), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func createPerfTestFinancialData(ticker string) *entities.FinancialData {
	return &entities.FinancialData{
		Ticker:                    ticker,
		Revenue:                   100000000000, // $100B
		OperatingIncome:           20000000000,  // $20B
		NormalizedOperatingIncome: 21000000000,  // $21B
		TotalAssets:               200000000000, // $200B
		TangibleAssets:            150000000000, // $150B
		TotalDebt:                 50000000000,  // $50B
		InterestBearingDebt:       45000000000,  // $45B
		SharesOutstanding:         1000000000,   // 1B shares
		DilutedSharesOutstanding:  1020000000,   // 1.02B shares
		InterestExpense:           2000000000,   // $2B
		TaxRate:                   0.21,         // 21%
		AsOf:                      time.Now(),
		Period:                    "2024Q3",
		HasNormalizedData:         true,
	}
}

func createPerfTestFinancialDataWithDate(ticker, period string, filingDate time.Time) *entities.FinancialData {
	return &entities.FinancialData{
		Ticker:                    ticker,
		Revenue:                   100000000000, // $100B
		OperatingIncome:           20000000000,  // $20B
		NormalizedOperatingIncome: 21000000000,  // $21B
		TotalAssets:               200000000000, // $200B
		TangibleAssets:            150000000000, // $150B
		TotalDebt:                 50000000000,  // $50B
		InterestBearingDebt:       45000000000,  // $45B
		SharesOutstanding:         1000000000,   // 1B shares
		DilutedSharesOutstanding:  1020000000,   // 1.02B shares
		InterestExpense:           2000000000,   // $2B
		TaxRate:                   0.21,         // 21%
		AsOf:                      time.Now(),
		Period:                    period,
		FilingDate:                filingDate, // This is the key field for GetLatestPeriod()
		HasNormalizedData:         true,
	}
}

func createPerfTestMarketData(ticker string) *entities.MarketData {
	return &entities.MarketData{
		Ticker:            ticker,
		SharePrice:        150.25,
		SharesOutstanding: 1000000000, // 1B shares
		Beta:              1.2,
		AsOf:              time.Now(),
		Source:            "test",
	}
}

func createPerfTestMacroData() *entities.MacroData {
	return &entities.MacroData{
		RiskFreeRate:      0.045, // 4.5%
		MarketRiskPremium: 0.05,  // 5%
		AsOf:              time.Now(),
		Source:            "test",
	}
}
