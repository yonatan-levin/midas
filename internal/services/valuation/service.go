package valuation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
	"github.com/midas/dcf-valuation-api/internal/observability/calclog"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
	"github.com/midas/dcf-valuation-api/internal/observability/narrate"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/industry"
	"github.com/midas/dcf-valuation-api/internal/services/datafetcher"
	growthsvc "github.com/midas/dcf-valuation-api/internal/services/growth"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
	"github.com/midas/dcf-valuation-api/pkg/finance/dcf"
	"github.com/midas/dcf-valuation-api/pkg/finance/growth"
	"github.com/midas/dcf-valuation-api/pkg/finance/wacc"
)

// Service provides valuation operations
type Service struct {
	financialRepo      ports.FinancialDataRepository
	marketRepo         ports.MarketDataRepository
	macroRepo          ports.MacroDataRepository
	cache              ports.CacheRepository
	dataCleaner        datacleaner.DataCleanerService
	dataFetcher        *datafetcher.DataFetcher
	growthEstimator    *growthsvc.Estimator
	yfinanceGateway    ports.YFinanceGateway  // optional, for analyst estimates
	macroGateway       ports.MacroDataGateway // Phase B9 (IFRS-FPI): FX-rate lookups; nil = no-op FX path
	metricsService     ports.MetricsService
	modelRouter        *models.ModelRouter          // Phase 3: industry-aware model selection
	industryClassifier *industry.IndustryClassifier // Phase 3: SIC/NAICS classification
	countryRiskMap     map[string]float64           // Phase 4: ISO-2 country code -> CRP
	industryMultiples  *industryMultiplesConfig     // Phase 4: EV/EBITDA and P/E multiples for cross-checks
	adrRatios          *ADRRatios                   // Phase B8 (IFRS-FPI): ordinary-shares-per-ADR; consumed in Phase B10
	config             *config.Config
	logger             *zap.Logger
	calcEmitter        *calclog.Emitter // emits the 12 DCF stage traces (Phase M)

	// clock is the wall-clock seam introduced by Phase R0 of the
	// observability replay-tooling spec (D10). Production binds it to
	// wallClock{} (which delegates to time.Now); replay binds it to a
	// manifest-bound clock so cross-year regression replays do not silently
	// shift the FY-period key or the CalculatedAt stamp. NewService defaults
	// this to wallClock{} so every existing test call site is unaffected;
	// SetClock allows replay to override post-construction.
	clock Clock
}

// log returns the appropriate logger for the current execution context.
// When ctx carries a request-scoped logger (injected by logctx middleware), that
// logger is returned so all downstream log lines inherit request_id / user_id /
// key_id. When the service is called from the scheduler or startup (plain
// context.Background()), the fx singleton logger is returned as fallback so
// structured logging is never silenced.
func (s *Service) log(ctx context.Context) *zap.Logger {
	return logctx.Or(ctx, s.logger)
}

// NewService creates a new valuation service.
// calcEmitter is injected by the DI container and gates the 12 calculation stage
// traces (Phase M). Pass nil only in tests that do not need calc tracing.
func NewService(
	financialRepo ports.FinancialDataRepository,
	marketRepo ports.MarketDataRepository,
	macroRepo ports.MacroDataRepository,
	cache ports.CacheRepository,
	dataCleaner datacleaner.DataCleanerService,
	dataFetcher *datafetcher.DataFetcher,
	metricsService ports.MetricsService,
	cfg *config.Config,
	logger *zap.Logger,
	calcEmitter *calclog.Emitter,
) *Service {
	// Build growth estimator from valuation config.
	// Pass calcEmitter so EstimateGrowthRates can emit stage-5 "growth" trace.
	estimatorCfg := growthsvc.DefaultEstimatorConfig()
	if cfg.Valuation.DCFMaxGrowthRate > 0 {
		estimatorCfg.MaxGrowthRate = cfg.Valuation.DCFMaxGrowthRate
	}
	if cfg.Valuation.DCFMinGrowthRate != 0 {
		estimatorCfg.MinGrowthRate = cfg.Valuation.DCFMinGrowthRate
	}

	// Initialize industry classifier for SIC/NAICS-based model selection.
	// ctx is added to Classify (M.1) so the valuation service can emit stage-3
	// "industry_classification" trace after the call returns.
	classifier := industry.NewIndustryClassifier()

	// Initialize model router with all available valuation models.
	// Order matters: more specific models are listed first.
	// Pass calcEmitter so SelectModel emits stage-4 "model_selection" trace.
	allModels := []models.ValuationModel{
		models.NewDDMModel(logger),
		models.NewFFOModel(logger),
		models.NewRevenueMultipleModel(logger),
		models.NewMultiStageDCFModel(logger),
	}
	router := models.NewModelRouter(allModels, logger, calcEmitter)

	// Phase 4: Load country risk premiums for international support.
	// Graceful degradation: if config file is missing, all tickers get CRP = 0 (domestic US).
	crpMap, crpErr := LoadCountryRiskPremiums(DefaultCountryRiskConfigPath)
	if crpErr != nil {
		logger.Warn("Country risk config unavailable, defaulting CRP to 0 for all tickers",
			zap.Error(crpErr))
		crpMap = map[string]float64{"US": 0, "default": 0}
	}

	// Phase 4: Load industry multiples for exit-multiple TV and sanity cross-checks.
	// Graceful degradation: if missing, exit-multiple and cross-checks are skipped.
	indMultiples, imErr := LoadIndustryMultiples("")
	if imErr != nil {
		logger.Warn("Industry multiples config unavailable, exit-multiple TV and cross-checks disabled",
			zap.Error(imErr))
	}

	// Phase B8 (IFRS-FPI): Load ADR-to-ordinary-share ratios. Phase B10 will
	// consume this to convert SEC-reported ordinary-share counts into per-ADR
	// terms before computing per-ADR fair value. Missing file is non-fatal —
	// the loader returns an empty *ADRRatios and Get() defaults every ticker
	// to 1:1, so we boot fine but log a warning for operator visibility.
	adrRatios, adrErr := LoadADRRatios(DefaultADRRatiosConfigPath)
	if adrErr != nil {
		logger.Warn("ADR ratios config unavailable, all tickers default to 1:1",
			zap.Error(adrErr))
		adrRatios = &ADRRatios{Ratios: map[string]int{}}
	}

	return &Service{
		financialRepo:      financialRepo,
		marketRepo:         marketRepo,
		macroRepo:          macroRepo,
		cache:              cache,
		dataCleaner:        dataCleaner,
		dataFetcher:        dataFetcher,
		growthEstimator:    growthsvc.NewEstimator(estimatorCfg, logger, calcEmitter),
		metricsService:     metricsService,
		modelRouter:        router,
		industryClassifier: classifier,
		countryRiskMap:     crpMap,
		industryMultiples:  indMultiples,
		adrRatios:          adrRatios,
		config:             cfg,
		logger:             logger,
		calcEmitter:        calcEmitter,
		// Default to the production wall-clock binding. Replay overrides this
		// via SetClock after construction (D10). Existing test call sites
		// don't need to thread anything new because the default is identical
		// to a direct time.Now() read.
		clock: NewWallClock(),
	}
}

// SetClock injects a Clock implementation used for the four wall-clock reads
// in CalculateValuation (request-start, FY-period fallback, two CalculatedAt
// stamps). Production never calls this — the default wallClock{} populated by
// NewService matches pre-R0 behavior bit-for-bit. Replay uses this to bind
// the clock to manifest.started_at so cross-year regression replays remain
// deterministic. Mirrors the SetMacroGateway / SetYFinanceGateway pattern.
//
// Passing a nil Clock is a no-op — the existing default is preserved so a
// caller that always sets but occasionally passes nil cannot accidentally
// brick the service.
func (s *Service) SetClock(clk Clock) {
	if clk == nil {
		return
	}
	s.clock = clk
}

// SetYFinanceGateway injects the Yahoo Finance gateway for analyst estimates.
// This is optional — when nil, the growth estimator uses historical data only.
func (s *Service) SetYFinanceGateway(gw ports.YFinanceGateway) {
	s.yfinanceGateway = gw
}

// CalculateValuation performs a complete DCF valuation for a ticker.
// opts may be nil; when provided, overrides (e.g., beta, risk-free rate)
// replace the corresponding values fetched from data sources.
func (s *Service) CalculateValuation(ctx context.Context, ticker string, opts *ValuationOptions) (*entities.ValuationResult, error) {
	// Wall-clock read routed through the Clock seam (D10). Production binds
	// to time.Now() byte-identically; replay binds to manifest.started_at.
	// Note: the duration metrics below use time.Since(start) which is
	// wall-clock relative — replay disables the metrics service so the
	// (large, manifest-time-anchored) "duration" is harmless.
	start := s.clock.Now()
	s.log(ctx).Info("Starting valuation calculation", zap.String("ticker", ticker))

	// Skip cache for requests with overrides — they represent ad-hoc user queries
	// that should not pollute (or be served from) the default-parameter cache.
	hasOverrides := opts != nil && (opts.OverrideBeta != nil || opts.OverrideRiskFree != nil)
	cacheKey := fmt.Sprintf("valuation:v4:%s", ticker)

	em := narrate.From(ctx)

	if !hasOverrides {
		var cachedResult entities.ValuationResult
		if err := s.cache.Get(ctx, cacheKey, &cachedResult); err == nil {
			// Tier-1 narrate: cache.lookup hit. Per spec §5 row 5 outcome=ok
			// on hit; emit before the early return.
			em.Emit(ctx, narrate.PhaseCacheLookup, narrate.OutcomeOK, "",
				zap.String("cache_key", cacheKey),
				zap.Bool("hit", true),
			)
			s.log(ctx).Info("Returning cached valuation", zap.String("ticker", ticker))
			s.metricsService.RecordValuationRequest(ticker, "single", "cache_hit", time.Since(start))
			return &cachedResult, nil
		}
		// Cache miss: still emit the lookup line so the per-request story
		// shows the lookup attempt.
		em.Emit(ctx, narrate.PhaseCacheLookup, narrate.OutcomeSkipped, "miss",
			zap.String("cache_key", cacheKey),
			zap.Bool("hit", false),
		)
	} else {
		// Cache deliberately bypassed; record as skipped so the trace is honest.
		em.Emit(ctx, narrate.PhaseCacheLookup, narrate.OutcomeSkipped, "overrides bypass cache",
			zap.Bool("hit", false),
		)
	}

	// Try to load data from repositories first (for previously fetched/seeded tickers)
	historicalData, err := s.financialRepo.GetHistorical(ctx, ticker, 10)

	var marketData *entities.MarketData
	var macroData *entities.MacroData

	if err != nil || len(historicalData.Data) == 0 {
		// No data in repository — use DataFetcher to retrieve from external APIs
		s.log(ctx).Info("No historical data in repository, fetching via DataFetcher", zap.String("ticker", ticker))

		if s.dataFetcher == nil {
			return nil, fmt.Errorf("%w: no historical data and data fetcher not configured for %s", ErrTickerNotFound, ticker)
		}

		fetchResult, fetchErr := s.dataFetcher.Fetch(ctx, &entities.FetchRequest{Ticker: ticker})
		if fetchErr != nil {
			// Defensive: if a direct fetch error ever wraps a SEC sentinel,
			// classify as the appropriate user-facing error rather than a
			// generic 500. In the current flow CoordinateFetch never returns
			// non-nil, so these branches are primarily future-proofing.
			//
			// FPI check MUST come first — ErrForeignPrivateIssuer is the more
			// specific case (parsing gap, not data gap).
			if errors.Is(fetchErr, ports.ErrForeignPrivateIssuer) {
				return nil, fmt.Errorf("%w: SEC filing for %s uses ifrs-full taxonomy", ErrForeignPrivateIssuer, ticker)
			}
			if errors.Is(fetchErr, ports.ErrCompanyFactsNotFound) {
				return nil, fmt.Errorf("%w: no US-GAAP XBRL facts currently available for %s via SEC EDGAR", ErrInsufficientData, ticker)
			}
			return nil, fmt.Errorf("failed to fetch data via DataFetcher: %w", fetchErr)
		}

		// NOTE: a stray `fmt.Println(fetchResult)` lived here pre-branch
		// (commit 53717609). It violated the structured-logging rule
		// (CLAUDE.md "Use go.uber.org/zap exclusively, never fmt.Println"),
		// dumped megabytes of raw SEC XBRL to stdout on every cold-cache
		// request, and bypassed the artifact redactor. Removed by the
		// observability-narrative branch fix-up. If you need a fetch-result
		// peek, use `s.log(ctx).Debug("trace.fetch_result_summary", ...)`
		// with a SUMMARY (counts/sizes), never the raw struct.

		// Use multi-period historical data if available (from full SEC parser)
		if fetchResult.HistoricalData != nil && len(fetchResult.HistoricalData.Data) > 0 {
			historicalData = fetchResult.HistoricalData
		} else if fetchResult.FinancialData != nil {
			// Fallback: wrap single FinancialData into HistoricalFinancialData
			periodKey := fetchResult.FinancialData.FilingPeriod
			if periodKey == "" || (len(periodKey) < 2 || periodKey[len(periodKey)-2:] != "FY") {
				// Math-affecting read: a 2026 bundle replayed in 2027 must
				// pick the same FY-period key it picked at capture time
				// (D10). Routing through s.clock.Now() makes that pinning
				// possible. Production semantics unchanged.
				periodKey = fmt.Sprintf("%dFY", s.clock.Now().Year())
				fetchResult.FinancialData.FilingPeriod = periodKey
			}
			historicalData = &entities.HistoricalFinancialData{
				Ticker: ticker,
				Data:   map[string]*entities.FinancialData{periodKey: fetchResult.FinancialData},
			}
		} else {
			// No data came back from any source. Three-way classification so
			// the HTTP layer can give a clear, actionable message for each
			// case (all 422 except the last which is 404):
			//
			//   1. FPI — 20-F filer using ifrs-full taxonomy (TSM, ASML, NVO,
			//      AZN, BABA, …). Data exists, parser can't read it yet.
			//      Phase B of the IFRS-FPI spec teaches the parser to.
			//   2. CompanyFactsNotFound — clinical-stage biotech, pre-revenue
			//      issuer, or genuinely-unknown CIK. us-gaap present but
			//      missing Revenue/OperatingIncome.
			//   3. Else — ticker truly unknown across all sources.
			//
			// FPI MUST be checked first because it is the more specific case;
			// a SEC fetch error can technically wrap both sentinels, and the
			// FPI message is more useful for the user.
			if hasForeignPrivateIssuerError(fetchResult.Errors) {
				return nil, fmt.Errorf("%w: SEC filing for %s uses ifrs-full taxonomy", ErrForeignPrivateIssuer, ticker)
			}
			if hasCompanyFactsNotFoundError(fetchResult.Errors) {
				return nil, fmt.Errorf("%w: no US-GAAP XBRL facts currently available for %s via SEC EDGAR", ErrInsufficientData, ticker)
			}
			return nil, fmt.Errorf("%w: DataFetcher returned no financial data for %s", ErrTickerNotFound, ticker)
		}

		// Use market and macro data from FetchResult if available
		marketData = fetchResult.MarketData
		macroData = fetchResult.MacroData

		s.log(ctx).Info("Successfully fetched data via DataFetcher",
			zap.String("ticker", ticker),
			zap.Int("periods", len(historicalData.Data)),
			zap.Bool("has_market_data", marketData != nil),
			zap.Bool("has_macro_data", macroData != nil))
	} else if s.isFinancialDataIncomplete(historicalData) && s.dataFetcher != nil {
		// Data exists in repo but is incomplete (missing FCF fields like D&A, CapEx, Cash).
		// Re-fetch from SEC to get complete data, then persist the update.
		s.log(ctx).Info("Stored financial data incomplete (missing FCF fields), re-fetching from SEC",
			zap.String("ticker", ticker))

		fetchResult, fetchErr := s.dataFetcher.Fetch(ctx, &entities.FetchRequest{Ticker: ticker})
		if fetchErr == nil && fetchResult.HistoricalData != nil && len(fetchResult.HistoricalData.Data) > 0 {
			historicalData = fetchResult.HistoricalData
			if fetchResult.MarketData != nil {
				marketData = fetchResult.MarketData
			}
			if fetchResult.MacroData != nil {
				macroData = fetchResult.MacroData
			}

			// Persist the updated data so future requests don't re-fetch
			if s.financialRepo != nil {
				if storeErr := s.financialRepo.StoreHistorical(ctx, historicalData); storeErr != nil {
					s.log(ctx).Warn("Failed to persist re-fetched financial data",
						zap.String("ticker", ticker), zap.Error(storeErr))
				} else {
					s.log(ctx).Info("Persisted complete financial data to repository",
						zap.String("ticker", ticker),
						zap.Int("periods", len(historicalData.Data)))
				}
			}
		} else {
			s.log(ctx).Warn("Re-fetch failed or returned no data, using incomplete stored data with NOPAT fallback",
				zap.String("ticker", ticker))
		}
	}

	// Fill in market and macro data from repositories if not already set by DataFetcher
	if marketData == nil {
		marketData, err = s.marketRepo.GetLatest(ctx, ticker)
		if err != nil {
			s.log(ctx).Warn("Failed to fetch market data from repository", zap.Error(err), zap.String("ticker", ticker))
			return nil, fmt.Errorf("failed to fetch market data: %w", err)
		}
	}
	if macroData == nil {
		macroData, err = s.macroRepo.GetLatest(ctx)
		if err != nil {
			s.log(ctx).Warn("Failed to fetch macro data from repository", zap.Error(err), zap.String("ticker", ticker))
			return nil, fmt.Errorf("failed to fetch macro data: %w", err)
		}
	}

	// Phase B9 (IFRS-FPI): FX-convert any non-USD reporting-currency periods
	// to USD BEFORE WACC / growth / DCF run, so every downstream calculation
	// sees USD-denominated values. Idempotent — USD periods short-circuit.
	// Placed at the post-fetch convergence point so both the DataFetcher
	// path and the repo path get the same treatment.
	//
	// On failure, the policy is asymmetric:
	//   - If the ONLY failures were on non-USD periods (i.e., this is a
	//     foreign filer for which we have no FX coverage), surface as
	//     ErrForeignPrivateIssuer so the handler emits the existing 422
	//     FOREIGN_PRIVATE_ISSUER_UNSUPPORTED rather than a generic 500.
	//     Post-Phase-B residual case: parser successfully extracted IFRS
	//     data but the reporting currency has no FRED series AND no entry
	//     in config/fx_rates.json. Extend coverage by adding to either.
	//   - Otherwise (mixed-currency partial failure): WARN and proceed with
	//     the surviving USD + already-converted periods. Better stale-but-
	//     finite than no answer.
	if err := s.convertFinancialsToUSD(ctx, historicalData); err != nil {
		if errors.Is(err, ports.ErrFXRateUnavailable) && hasNonUSDPeriod(historicalData) {
			return nil, fmt.Errorf("%w: FX conversion failed for ticker %s reporting in non-USD currency", ErrForeignPrivateIssuer, ticker)
		}
		s.log(ctx).Warn("FX conversion partially failed; valuation may be stale",
			zap.String("ticker", ticker), zap.Error(err))
	}

	// Phase B10 (IFRS-FPI): FX conversion ran above (B9). Now divide
	// ordinary-share counts by the configured ADR ratio so per-share values
	// match the listed ADR price (e.g., TSM 25.93B ordinary / 5 = 5.19B
	// ADR-equivalent). No-op for domestic filers (ratio=1, ticker absent
	// from config/adr_ratios.json). Single-call-only by contract — the
	// function does not mark the data, so do NOT re-invoke this anywhere
	// downstream. Failures here are warnings only (no error path).
	s.applyADRRatio(ctx, ticker, historicalData, marketData)

	// Stage 1 — "data_fetch" calc trace: emit which data sources were consulted and
	// whether they succeeded. This always fires regardless of whether data came from
	// the repository or the DataFetcher, giving operators full acquisition visibility.
	if s.calcEmitter != nil {
		// sourcesTried reflects the code path taken. When DataFetcher is active,
		// the live gateways (SEC/Yahoo/FRED) are tried; otherwise the repo layer.
		var sourcesTried []string
		if s.dataFetcher != nil {
			sourcesTried = []string{"sec_edgar", "yahoo_finance", "fred"}
		} else {
			sourcesTried = []string{"financial_repo", "market_repo", "macro_repo"}
		}
		sourcesOk := []string{}
		if len(historicalData.Data) > 0 {
			if s.dataFetcher != nil {
				sourcesOk = append(sourcesOk, "sec_edgar")
			} else {
				sourcesOk = append(sourcesOk, "financial_repo")
			}
		}
		if marketData != nil {
			if s.dataFetcher != nil {
				sourcesOk = append(sourcesOk, "yahoo_finance")
			} else {
				sourcesOk = append(sourcesOk, "market_repo")
			}
		}
		if macroData != nil {
			if s.dataFetcher != nil {
				sourcesOk = append(sourcesOk, "fred")
			} else {
				sourcesOk = append(sourcesOk, "macro_repo")
			}
		}
		s.calcEmitter.Emit(ctx, "data_fetch",
			zap.String("ticker", ticker),
			zap.Bool("via_fetcher", s.dataFetcher != nil),
			zap.Strings("sources_tried", sourcesTried),
			zap.Strings("sources_ok", sourcesOk),
			zap.Int64("duration_ms", time.Since(start).Milliseconds()),
		)
	}

	// Apply data cleaning if service is available
	var cleaningResult *entities.CleaningResult
	if s.dataCleaner != nil {
		latest, latestPeriod := historicalData.GetLatestPeriod()
		if latest != nil {
			var err error
			cleaningResult, err = s.dataCleaner.CleanFinancialData(ctx, latest)
			if err != nil {
				s.log(ctx).Warn("Data cleaning failed, using original data",
					zap.Error(err),
					zap.String("ticker", ticker))
			} else {
				s.log(ctx).Info("Data cleaning applied successfully",
					zap.String("ticker", ticker),
					zap.Float64("quality_score", cleaningResult.QualityScore))
				// Update historical data with cleaned data
				historicalData.Data[latestPeriod] = cleaningResult.CleanedData
			}
		}
	} else {
		s.log(ctx).Info("DataCleaner service not available, using original data", zap.String("ticker", ticker))
	}

	// Calculate valuation using potentially cleaned data, applying any user overrides
	result, err := s.performValuation(ctx, historicalData, marketData, macroData, opts)
	if err != nil {
		s.metricsService.RecordValuationRequest(ticker, "single", "error", time.Since(start))
		s.metricsService.RecordValuationError(ticker, "calculation_failed")
		return nil, fmt.Errorf("failed to perform valuation: %w", err)
	}

	// Phase B12 (IFRS-FPI): stamp transparency fields on the result.
	//
	// ReportingCurrency is always "USD" because convertFinancialsToUSD
	// (Phase B9) ran above with the calculation-safety contract that every
	// monetary field on every period ends up in USD before any math runs.
	// Hard-coded rather than read from historicalData[*].ReportingCurrency
	// because the latter could be "" for legacy/test data and the user's
	// actual unit on DCFValuePerShare is what they care about.
	//
	// ADRRatioApplied mirrors what applyADRRatio (Phase B10) divided by:
	// the configured ratio for foreign filers (TSM=5, BABA=8, …) or 1 for
	// every other ticker. Get() is nil-safe and always returns a positive
	// int by contract, so this is unconditionally safe.
	result.ReportingCurrency = "USD"
	result.ADRRatioApplied = s.adrRatios.Get(ticker)

	// Add cleaning results if available
	if cleaningResult != nil {
		result.DataQualityScore = cleaningResult.QualityScore
		result.DataQualityGrade = entities.GetQualityGrade(cleaningResult.QualityScore)
		result.CleaningFlags = cleaningResult.Flags
		result.CleaningAdjustments = cleaningResult.Adjustments
		// Note: CleaningReport would need the full report structure to be implemented

		// W-1: heuristic GICS classification is now populated inside
		// performValuation via ClassifyIndustry (guaranteed-GICS output),
		// independent of cleaningResult.IndustryCode which may be any upstream
		// string. See docs/superpowers/specs/2026-04-23-industry-in-response-design.md.
	}

	// Only cache default (no-override) results to avoid cache poisoning
	if !hasOverrides {
		if err := s.cache.Set(ctx, cacheKey, result, s.config.Valuation.CacheTTL); err != nil {
			s.log(ctx).Warn("Failed to cache valuation result", zap.Error(err))
		}
	}

	// Record successful valuation metrics
	s.metricsService.RecordValuationRequest(ticker, "single", "success", time.Since(start))

	s.log(ctx).Info("Valuation calculation completed", zap.String("ticker", ticker), zap.Float64("dcf_value", result.DCFValuePerShare))

	// Stage 12 — "final" calc trace: emit the top-line DCF result so operators can
	// verify the intrinsic value output and its quality metadata end-to-end.
	if s.calcEmitter != nil {
		s.calcEmitter.Emit(ctx, "final",
			zap.String("ticker", ticker),
			zap.Float64("dcf_per_share", result.DCFValuePerShare),
			zap.Float64("tangible_per_share", result.TangibleValuePerShare),
			zap.String("method", result.CalculationMethod),
			zap.String("version", result.CalculationVersion),
			zap.Float64("quality_score", result.DataQualityScore),
			zap.Int("warnings_count", len(result.Warnings)),
		)
	}

	return result, nil
}

// performValuation executes the valuation calculation logic.
// opts may be nil; when provided, its fields override data-source values in the WACC calculation.
func (s *Service) performValuation(
	ctx context.Context,
	historicalData *entities.HistoricalFinancialData,
	marketData *entities.MarketData,
	macroData *entities.MacroData,
	opts *ValuationOptions,
) (*entities.ValuationResult, error) {

	// Validate minimum data requirements — need at least 1 annual period with revenue or OI
	if !historicalData.HasMinimumData(1) {
		return nil, fmt.Errorf("%w: need at least 1 year of financial data", ErrInsufficientData)
	}

	if !marketData.IsComplete() {
		return nil, fmt.Errorf("%w: incomplete market data", ErrInsufficientData)
	}

	if !macroData.IsComplete() {
		return nil, fmt.Errorf("%w: incomplete macro data", ErrInsufficientData)
	}

	// Calculate historical growth rate from SEC data.
	historicalGrowth, err := historicalData.CalculateAverageGrowthRate(5)
	if err != nil {
		historicalGrowth = &growth.CalculationResult{
			GrowthRate:  s.config.Valuation.DefaultTerminalGrowthCap,
			Method:      "default",
			DataQuality: "low",
			IsReliable:  false,
		}
	}

	// Fetch analyst consensus estimates with caching (optional, degrades gracefully).
	// Cached separately with 7-day TTL since analyst estimates change infrequently.
	analystData := s.getAnalystEstimates(ctx, historicalData.Ticker)

	// Calculate ROIC-sustainable growth ceiling
	sustainableGrowth := 0.0
	latestForROIC, _ := historicalData.GetLatestPeriod()
	if latestForROIC != nil {
		nopat := latestForROIC.NormalizedOperatingIncome * (1 - latestForROIC.TaxRate)
		investedCapital := growth.CalculateInvestedCapital(
			latestForROIC.StockholdersEquity,
			latestForROIC.InterestBearingDebt,
			latestForROIC.CashAndCashEquivalents,
		)
		sustainableGrowth = growth.CalculateSustainableGrowth(nopat, investedCapital, s.growthEstimator.Config().DefaultPayoutRatio)
	}

	// Produce multi-stage growth estimate (analyst + historical blend).
	// ctx is passed through so EstimateGrowthRates can emit stage-5 "growth" trace.
	// Ticker is threaded in so the emitted trace carries it self-describingly (M-1a).
	growthEstimate := s.growthEstimator.EstimateGrowthRates(ctx, historicalData.Ticker, analystData, historicalGrowth, sustainableGrowth)

	// Tier-1 narrate: growth.estimated. Spec §5 row 12 fields. Reports
	// year-1 + terminal growth and an indication of which inputs drove the
	// blend (analyst vs historical weights).
	{
		gYear1 := 0.0
		if rates := growthEstimate.ProjectedGrowthRates; len(rates) > 0 {
			gYear1 = rates[0]
		}
		analystWeight := 0.0
		historicalWeight := 1.0
		if analystData != nil {
			// Coarse signal — actual weighting is internal to estimator.
			analystWeight = 0.5
			historicalWeight = 0.5
		}
		gOutcome := narrate.OutcomeOK
		if !historicalGrowth.IsReliable {
			gOutcome = narrate.OutcomePartial
		}
		narrate.From(ctx).Emit(ctx, narrate.PhaseGrowthEstimated, gOutcome, "",
			zap.Int("stage_count", len(growthEstimate.ProjectedGrowthRates)),
			zap.Float64("analyst_weight", analystWeight),
			zap.Float64("historical_weight", historicalWeight),
			zap.Float64("g_year_1", gYear1),
			zap.Float64("g_terminal", growthEstimate.TerminalGrowthRate),
		)
		if b := artifact.From(ctx); b != nil {
			b.Snapshot(ctx, "growth.estimated", "12-growth-curve.json", growthEstimate)
			b.AddSchemaVersion("GrowthEstimate", 1)
		}
	}

	// Get the latest financial data for asset calculations
	latestFinancialData, latestPeriod := historicalData.GetLatestPeriod()
	if latestFinancialData == nil {
		return nil, fmt.Errorf("%w: no latest financial data available", ErrInsufficientData)
	}

	// Calculate tangible value per share
	tangibleValuePerShare := s.calculateTangibleValuePerShare(latestFinancialData, marketData)

	// Determine beta and risk-free rate, applying user overrides when provided
	beta := marketData.GetEffectiveBeta()
	riskFreeRate := macroData.GetEffectiveRiskFreeRate()
	if opts != nil {
		if opts.OverrideBeta != nil {
			beta = *opts.OverrideBeta
		}
		if opts.OverrideRiskFree != nil {
			riskFreeRate = *opts.OverrideRiskFree
		}
	}

	// Phase 4: Beta improvements — Blume adjustment + unlever/relever.
	// Track each beta stage so we can include all values in the stage-6 "wacc" trace.
	rawBeta := beta
	blumeBeta := wacc.BlumeAdjustedBeta(beta)
	beta = blumeBeta
	unleveredBeta := blumeBeta // default: same as blume if no relever conditions met
	releveredBeta := blumeBeta // default: same as blume if no relever conditions met

	// Unlever beta to remove capital structure effect, then relever at current D/E.
	// For a single company this is near-identity, but it normalizes extreme D/E betas
	// and prepares the pipeline for industry-average beta comparison.
	marketEquity := marketData.CalculateMarketValue()
	if marketEquity > 0 && latestFinancialData.InterestBearingDebt > 0 {
		debtEquityRatio := latestFinancialData.InterestBearingDebt / marketEquity
		unleveredBeta = wacc.UnleveredBeta(blumeBeta, latestFinancialData.TaxRate, debtEquityRatio)
		releveredBeta = wacc.RelleveredBeta(unleveredBeta, latestFinancialData.TaxRate, debtEquityRatio)
		beta = releveredBeta
	}

	s.log(ctx).Debug("Beta adjustments applied",
		zap.String("ticker", historicalData.Ticker),
		zap.Float64("raw_beta", rawBeta),
		zap.Float64("adjusted_beta", beta))

	// Phase 4: Look up country risk premium for international / ADR companies.
	// US-domiciled companies get CRP = 0, so the formula is backward-compatible.
	countryCode := GetCountryForTicker(historicalData.Ticker)
	countryRiskPremium := GetCountryRiskPremium(s.countryRiskMap, countryCode)
	if countryRiskPremium > 0 {
		s.log(ctx).Info("Country risk premium applied",
			zap.String("ticker", historicalData.Ticker),
			zap.String("country", countryCode),
			zap.Float64("crp", countryRiskPremium))
	}

	// Calculate WACC (with CRP for international companies)
	waccInputs := wacc.Inputs{
		RiskFreeRate:        riskFreeRate,
		MarketRiskPremium:   macroData.MarketRiskPremium,
		Beta:                beta,
		CountryRiskPremium:  countryRiskPremium,
		MarketValueOfEquity: marketData.CalculateMarketValue(),
		MarketValueOfDebt:   latestFinancialData.InterestBearingDebt,
		InterestExpense:     latestFinancialData.InterestExpense,
		TaxRate:             latestFinancialData.TaxRate,
	}

	waccResult, err := wacc.Calculate(waccInputs)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate WACC: %w", err)
	}

	// Record WACC calculation metric
	s.metricsService.IncWACCCalculations()
	s.metricsService.SetAverageWACC(waccResult.WACC)

	// Stage 6 — "wacc" calc trace: emit every WACC component so operators can audit
	// the cost-of-capital build-up (beta ladder, risk premiums, debt cost, final rate).
	if s.calcEmitter != nil {
		s.calcEmitter.Emit(ctx, "wacc",
			zap.String("ticker", historicalData.Ticker),
			zap.Float64("rf", riskFreeRate),
			zap.Float64("beta_raw", rawBeta),
			zap.Float64("beta_blume", blumeBeta),
			zap.Float64("beta_unlevered", unleveredBeta),
			zap.Float64("beta_relevered", releveredBeta),
			zap.Float64("erp", macroData.MarketRiskPremium),
			zap.Float64("crp", countryRiskPremium),
			zap.Float64("tax_rate", latestFinancialData.TaxRate),
			zap.Float64("cost_of_debt", waccResult.CostOfDebtAfterTax),
			zap.Float64("wacc", waccResult.WACC),
		)
	}

	// Tier-1 narrate: wacc.computed. Spec §5 row 13 fields. Carries the
	// final WACC and the major inputs so a reader can sanity-check it.
	narrate.From(ctx).Emit(ctx, narrate.PhaseWACCComputed, narrate.OutcomeOK, "",
		zap.Float64("cost_of_equity", waccResult.CostOfEquity),
		zap.Float64("cost_of_debt", waccResult.CostOfDebtAfterTax),
		zap.Float64("weight_equity", waccResult.WeightOfEquity),
		zap.Float64("wacc", waccResult.WACC),
		zap.Bool("country_premium_applied", countryRiskPremium > 0),
	)
	// Tier-3 artifact bundle: snapshot WACC inputs + result.
	if b := artifact.From(ctx); b != nil {
		b.Snapshot(ctx, "wacc.computed", "13-wacc.json", map[string]any{
			"inputs":         waccInputs,
			"result":         waccResult,
			"raw_beta":       rawBeta,
			"blume_beta":     blumeBeta,
			"unlevered_beta": unleveredBeta,
			"relevered_beta": releveredBeta,
		})
	}

	// Use terminal growth from the growth estimate, with WACC safety guard
	terminalGrowthRate := s.calculateTerminalGrowthRate(growthEstimate.SummaryGrowthRate(), waccResult.WACC)
	growthEstimate.TerminalGrowthRate = terminalGrowthRate

	// --- Phase 3: Industry-aware model selection ---
	// Classify industry using IndustryClassifier (SIC/NAICS/keyword matching).
	// The IndustryCode on the financial data may already be populated from
	// upstream; only invoke the classifier when it hasn't been, to avoid the
	// per-request CPU cost on the hot path (B-2: restores pre-feature gate).
	industryCode := latestFinancialData.IndustryCode
	var sicLabel string

	// classification carries the full classifier output (sector, industry,
	// sub-industry, model_hint, plus echoes of the input SIC/NAICS) so the
	// "industry_classification" calc trace below can surface every field
	// in the Phase M spec table — see docs/refactoring/observability-
	// upgrade-spec.md §374 (M-1b).
	var classification industry.ClassificationResult
	classification.SIC = historicalData.SICCode

	if industryCode == "" && s.industryClassifier != nil {
		// Use SIC code and company name from SEC data for classification.
		// Falls back to ticker if company name unavailable.
		companyName := historicalData.CompanyName
		if companyName == "" {
			companyName = historicalData.Ticker
		}
		// ctx is passed through for future context-aware tracing inside Classify.
		classified, classifyErr := s.industryClassifier.Classify(ctx, historicalData.SICCode, "", companyName)
		if classifyErr == nil && classified.Industry != "" && classified.Industry != "NA" {
			industryCode = classified.Industry
		}
		// Always retain the classification (even on error / NA) so the trace
		// below can echo the SIC/NAICS the caller asked about.
		classification = classified
	} else if industryCode != "" {
		// Pre-populated upstream — Classify was bypassed, so the trace would
		// otherwise emit empty sector/industry/model_hint while industry_code
		// holds the real value. Synthesize the struct fields from the cached
		// code so the calc trace stays self-consistent (M-1b validation
		// follow-up). Sector / SubIndustry / NAICS remain unknown on this
		// path — leave them blank rather than guess; the upstream caller
		// authoritatively chose `industryCode` and that's all we know.
		classification.Industry = industryCode
		classification.ModelHint = industryCode
	}

	// sicLabel reflects whatever value the router actually uses, so the API
	// response surface and the model-router are consistent. If upstream
	// pre-populated industryCode, we report that; if we classified just now,
	// we report the classifier output. Empty string (and "NA") flow through
	// and simply produce an omitempty-dropped field in the response.
	// See docs/superpowers/specs/2026-04-23-industry-in-response-design.md.
	sicLabel = industryCode

	// Request-correlated Debug log (uses logctx.Or so request_id flows through when
	// the call path is HTTP; falls back to singleton on scheduler/startup paths).
	s.log(ctx).Debug("Industry classification result",
		zap.String("ticker", historicalData.Ticker),
		zap.String("industry_code", industryCode),
		zap.String("sic_label", sicLabel))

	// W-1: heuristic (balance-sheet) classification is resolved by calling
	// ClassifyIndustry directly, not by reading cleaningResult.IndustryCode.
	// This guarantees IndustryHeuristicCode/Name are always GICS (matching the
	// field documentation) rather than "whatever the upstream pipeline coughed
	// up". On error or nil result the fields stay empty and the response's
	// omitempty-tagged Industry sub-fields drop.
	var heuristicCode, heuristicName string
	if s.industryClassifier != nil {
		if sectorConfig, classifyErr := s.industryClassifier.ClassifyIndustry(historicalData.Ticker, latestFinancialData); classifyErr == nil && sectorConfig != nil {
			heuristicCode = sectorConfig.SectorCode
			heuristicName = sectorConfig.SectorName
		}
	}

	// Stage 3 — "industry_classification" calc trace: always emitted from here (not
	// inside Classify) so only the valuation pipeline fires it once per valuation —
	// avoids double-emission if Classify is also called from the datacleaner path.
	// Surfaces the full Phase M spec field set (sic, naics, sector, industry,
	// sub_industry, model_hint) per docs/refactoring/observability-upgrade-spec.md
	// §374. industry_code is preserved as a back-compat alias for downstream
	// log consumers that key on the original field name.
	if s.calcEmitter != nil {
		s.calcEmitter.Emit(ctx, "industry_classification",
			zap.String("ticker", historicalData.Ticker),
			zap.String("sic", classification.SIC),
			zap.String("naics", classification.NAICS),
			zap.String("sector", classification.Sector),
			zap.String("industry", classification.Industry),
			zap.String("sub_industry", classification.SubIndustry),
			zap.String("industry_code", industryCode),
			zap.String("model_hint", classification.ModelHint),
		)
	}

	// Tier-1 narrate: classify.industry. Spec §5 row 11. Carries both
	// classifier outputs (SIC-based and the heuristic) plus a match flag so
	// a reader can spot drift between the two without opening the bundle.
	matchFlag := classification.Industry != "" && heuristicCode != ""
	narrate.From(ctx).Emit(ctx, narrate.PhaseClassifyIndustry, narrate.OutcomeOK, "",
		zap.String("sic_label", classification.Industry),
		zap.String("heuristic_label", heuristicCode),
		zap.Bool("match", matchFlag),
		zap.String("chosen", industryCode),
	)
	if b := artifact.From(ctx); b != nil {
		b.Snapshot(ctx, "classify.industry", "11-classify.json", map[string]any{
			"sic_classifier":       classification,
			"heuristic_code":       heuristicCode,
			"heuristic_name":       heuristicName,
			"chosen_industry_code": industryCode,
			"match":                matchFlag,
		})
	}

	// Resolve shares outstanding (needed by both DCF and alternative models)
	// Priority: diluted (most conservative) > market basic (most current) > financial basic
	sharesOutstanding := latestFinancialData.DilutedSharesOutstanding
	if sharesOutstanding <= 0 {
		sharesOutstanding = marketData.SharesOutstanding
	}
	if sharesOutstanding <= 0 {
		sharesOutstanding = latestFinancialData.SharesOutstanding
	}
	if sharesOutstanding <= 0 {
		return nil, fmt.Errorf("%w: shares outstanding not available", ErrInsufficientData)
	}

	// Select the appropriate valuation model based on industry and financials.
	// ctx is passed through so SelectModel emits stage-4 "model_selection" trace;
	// ticker is threaded in so that trace entry carries it self-describingly (M-1a).
	selectedModel := s.modelRouter.SelectModel(ctx, historicalData.Ticker, industryCode, latestFinancialData)

	// Tier-1 narrate: model.selected. Spec §5 row 14 fields. Reason is
	// intentionally coarse — full reasoning lives in the calclog
	// model_selection trace which fires from inside SelectModel.
	{
		modelName := "none"
		reason := "no model registered"
		if selectedModel != nil {
			modelName = selectedModel.ModelType()
			reason = "router selection"
		}
		narrate.From(ctx).Emit(ctx, narrate.PhaseModelSelected, narrate.OutcomeOK, "",
			zap.String("model", modelName),
			zap.String("reason", reason),
		)
		if b := artifact.From(ctx); b != nil {
			b.Snapshot(ctx, "model.selected", "14-model-selection.json", map[string]any{
				"model":         modelName,
				"reason":        reason,
				"industry_code": industryCode,
			})
		}
	}

	var dcfFallbackWarning string

	// If an alternative model (non-DCF) is selected, use it.
	// When the primary model fails but the company has positive OI,
	// performAlternativeValuation returns errFallbackToDCF to signal DCF fallback.
	if selectedModel != nil && selectedModel.ModelType() != "multi_stage_dcf" {
		altResult, altErr := s.performAlternativeValuation(
			ctx, selectedModel, historicalData, marketData, macroData,
			growthEstimate, waccResult, latestFinancialData, latestPeriod,
			tangibleValuePerShare, sharesOutstanding, industryCode,
		)
		if errors.Is(altErr, errFallbackToDCF) {
			s.log(ctx).Info("Falling back to standard DCF after alternative model failure",
				zap.String("ticker", historicalData.Ticker),
				zap.String("primary_model", selectedModel.ModelType()))
			dcfFallbackWarning = fmt.Sprintf("Primary model (%s) could not value this company; fell back to multi_stage_dcf", selectedModel.ModelType())
		} else if altErr != nil {
			return nil, altErr
		} else {
			// Attach SIC classification metadata on the alt-model path too, so
			// the response exposes it regardless of which valuation model ran.
			altResult.SICCodeRaw = historicalData.SICCode
			altResult.IndustrySIC = sicLabel
			// W-1: heuristic GICS plumbing is also attached on the alt-model
			// path so the API surface is identical across model selections.
			altResult.IndustryHeuristicCode = heuristicCode
			altResult.IndustryHeuristicName = heuristicName
			return altResult, nil
		}
	}

	// --- Standard multi-stage DCF path (existing logic) ---

	// Guard: standard DCF requires positive operating income.
	// Companies with negative OI are routed to revenue_multiple model above.
	baseOI := effectiveOI(latestFinancialData)
	if baseOI <= 0 {
		return nil, fmt.Errorf("%w: company has non-positive operating income (%.2f); standard DCF requires positive operating income", ErrModelNotApplicable, latestFinancialData.NormalizedOperatingIncome)
	}

	// Calculate net working capital change from historical data if available
	nwcChange := s.calculateNetWorkingCapitalChange(historicalData, latestFinancialData)

	// Perform DCF calculation with multi-stage growth rates
	projectionYears := len(growthEstimate.ProjectedGrowthRates)
	if projectionYears == 0 {
		projectionYears = 5 // fallback
	}
	dcfInputs := dcf.Inputs{
		BaseOperatingIncome: baseOI,
		GrowthRate:          growthEstimate.SummaryGrowthRate(), // backward-compatible summary
		GrowthRates:         growthEstimate.ProjectedGrowthRates,
		TerminalGrowthRate:  terminalGrowthRate,
		WACC:                waccResult.WACC,
		ProjectionYears:     projectionYears,
		TaxRate:             latestFinancialData.TaxRate,
	}

	// Use true FCF when D&A and CapEx data are available.
	// Average CapEx and D&A over available annual periods to smooth cyclical spikes
	// (e.g., MSFT's $30B AI infrastructure buildout year shouldn't define all future CapEx).
	avgDA, avgCapEx := s.averageCapExAndDA(historicalData)
	if avgDA > 0 || avgCapEx > 0 {
		dcfInputs.UseTrueFCF = true
		dcfInputs.DepreciationAndAmortization = avgDA
		dcfInputs.CapitalExpenditures = avgCapEx
		dcfInputs.NetWorkingCapitalChange = nwcChange
		s.log(ctx).Info("Using true FCF calculation (smoothed over available periods)",
			zap.String("ticker", historicalData.Ticker),
			zap.Float64("avg_da", avgDA),
			zap.Float64("avg_capex", avgCapEx),
			zap.Float64("nwc_change", nwcChange))
	} else {
		s.log(ctx).Info("Falling back to NOPAT-based FCF (D&A/CapEx unavailable)",
			zap.String("ticker", historicalData.Ticker))
	}
	usingNOPATFallback := !dcfInputs.UseTrueFCF

	// Phase 4: Wire exit-multiple terminal value from industry config.
	// When available, DCF averages Gordon Growth TV with exit-multiple TV to reduce model risk.
	if s.industryMultiples != nil && s.industryMultiples.EVEBITDAMultiples != nil {
		exitMultiple := LookupMultiple(s.industryMultiples.EVEBITDAMultiples, industryCode)
		if exitMultiple > 0 {
			dcfInputs.ExitMultiple = exitMultiple
			s.log(ctx).Debug("Exit multiple wired for terminal value averaging",
				zap.String("ticker", historicalData.Ticker),
				zap.String("industry", industryCode),
				zap.Float64("exit_multiple", exitMultiple))
		}
	}

	dcfResult, err := dcf.CalculateDCF(dcfInputs)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate DCF: %w", err)
	}

	// Record DCF calculation metrics
	s.metricsService.IncDCFCalculations()
	s.metricsService.SetAverageGrowthRate(growthEstimate.SummaryGrowthRate())

	// Stage 7 — "fcf_projection" calc trace: emit per-year growth rates and FCF
	// projections so operators can audit the explicit forecast period.
	if s.calcEmitter != nil {
		fcfSeries := make([]float64, len(dcfResult.Projections))
		for i, p := range dcfResult.Projections {
			fcfSeries[i] = p.FreeCashFlow
		}
		s.calcEmitter.Emit(ctx, "fcf_projection",
			zap.String("ticker", historicalData.Ticker),
			zap.Int("years", dcfResult.ProjectionYears),
			zap.Float64s("growth_rates", growthEstimate.ProjectedGrowthRates),
			zap.Float64s("fcf_series", fcfSeries),
		)
	}

	// Stage 8 — "terminal_value" calc trace: emit terminal-value build-up so operators
	// can see the Gordon Growth vs. exit-multiple averaging outcome.
	if s.calcEmitter != nil {
		// gordonTV = terminalYearFCF * (1 + tg) / (wacc - tg) before exit-multiple averaging.
		// Re-derive it because dcf.Result only exposes the averaged TerminalValueNominal.
		gordonTV := 0.0
		if waccResult.WACC > terminalGrowthRate {
			gordonTV = dcfResult.TerminalYearFCF * (1 + terminalGrowthRate) / (waccResult.WACC - terminalGrowthRate)
		}
		// exit_multiple_used is true when TerminalValueNominal differs from the pure
		// Gordon Growth value — i.e. pkg/finance/dcf averaged an exit-multiple TV in.
		// Post-M-1c the raw exit_multiple_tv component is also persisted on
		// dcf.Result.ExitMultipleTV (zero on the Gordon-only path), so we surface it
		// directly rather than back-calculating via 2*averaged - gordon.
		exitMultipleUsed := math.Abs(dcfResult.TerminalValueNominal-gordonTV) > 1e-6
		s.calcEmitter.Emit(ctx, "terminal_value",
			zap.String("ticker", historicalData.Ticker),
			zap.Float64("gordon_tv", gordonTV),
			zap.Float64("exit_multiple_tv", dcfResult.ExitMultipleTV),
			zap.Bool("exit_multiple_used", exitMultipleUsed),
			zap.Float64("averaged_tv", dcfResult.TerminalValueNominal),
			zap.Float64("terminal_growth", terminalGrowthRate),
		)
	}

	// Stage 9 — "discount" calc trace: emit the PV of explicit period and terminal
	// value to show how enterprise value is assembled from discounted cash flows.
	if s.calcEmitter != nil {
		s.calcEmitter.Emit(ctx, "discount",
			zap.String("ticker", historicalData.Ticker),
			zap.Float64("pv_explicit", dcfResult.ExplicitPeriodValue),
			zap.Float64("pv_terminal", dcfResult.TerminalValue),
			zap.Float64("enterprise_value", dcfResult.EnterpriseValue),
		)
	}

	// Equity value bridge: EV - Debt + Cash - MinorityInterest - PreferredEquity = Equity Value
	// M-1d: minority interest and preferred equity are subtracted to produce
	// common-shareholder claim only. Both are zero for issuers without
	// non-controlling interests or preferred stock outstanding, so the
	// per-share output for those tickers is unchanged versus the prior
	// 3-arg signature.
	equityValue := dcf.CalculateEquityValue(
		dcfResult.EnterpriseValue,
		latestFinancialData.InterestBearingDebt,
		latestFinancialData.CashAndCashEquivalents,
		latestFinancialData.MinorityInterest,
		latestFinancialData.PreferredEquity,
	)
	dcfValuePerShare := equityValue / sharesOutstanding

	// Stage 10 — "equity_bridge" calc trace: emit the bridge from enterprise value to
	// per-share intrinsic value so operators can audit the equity conversion step.
	if s.calcEmitter != nil {
		s.calcEmitter.Emit(ctx, "equity_bridge",
			zap.String("ticker", historicalData.Ticker),
			zap.Float64("cash", latestFinancialData.CashAndCashEquivalents),
			zap.Float64("debt", latestFinancialData.InterestBearingDebt),
			zap.Float64("minority_interest", latestFinancialData.MinorityInterest),
			zap.Float64("preferred", latestFinancialData.PreferredEquity),
			zap.Float64("equity_value", equityValue),
			zap.Float64("diluted_shares", sharesOutstanding),
			zap.Float64("per_share", dcfValuePerShare),
		)
	}

	// Calculate data freshness score
	dataFreshnessScore := s.calculateDataFreshnessScore(latestFinancialData, marketData, macroData)

	// Apply NOPAT fallback penalty
	if usingNOPATFallback {
		dataFreshnessScore -= 15
		if dataFreshnessScore < 0 {
			dataFreshnessScore = 0
		}
	}

	gf := calculateGrahamFloorMetrics(ctx, s.logger, historicalData.Ticker,
		latestFinancialData, sharesOutstanding, marketData.SharePrice)

	result := &entities.ValuationResult{
		Ticker: historicalData.Ticker,
		// CalculatedAt is observable in the response payload but does not
		// affect math. Routing through Clock keeps replay byte-deterministic
		// across calendar dates.
		CalculatedAt:          s.clock.Now(),
		TangibleValuePerShare: tangibleValuePerShare,
		DCFValuePerShare:      dcfValuePerShare,
		CurrentAssetsPerShare: gf.CurrentAssetsPerShare,
		NCAVPerShare:          gf.NCAVPerShare,
		GrahamFloorPerShare:   gf.GrahamFloorPerShare,
		GrahamDiscountPct:     gf.GrahamDiscountPct,
		WACC:                  waccResult.WACC,
		GrowthRate:            growthEstimate.SummaryGrowthRate(),
		GrowthRates:           growthEstimate.ProjectedGrowthRates,
		TerminalGrowthRate:    terminalGrowthRate,
		GrowthSource:          growthEstimate.Source,
		GrowthConfidence:      growthEstimate.Confidence,
		MarketRiskPremium:     macroData.MarketRiskPremium,
		EnterpriseValue:       dcfResult.EnterpriseValue,
		EquityValue:           equityValue,
		FinancialDataPeriod:   latestPeriod,
		MarketDataDate:        marketData.AsOf,
		CurrentPrice:          marketData.SharePrice,
		DataFreshnessScore:    dataFreshnessScore,
		CalculationMethod:     "multi_stage_dcf",
		CalculationVersion:    "4.1",
		// Industry metadata for the API response surface. Both the SIC label
		// and the heuristic GICS code/name flow through the valuation service
		// directly — see spec 2026-04-23-industry-in-response-design.md.
		// Heuristic values come from ClassifyIndustry (not cleaningResult.IndustryCode)
		// to guarantee GICS semantics per W-1.
		SICCodeRaw:            historicalData.SICCode,
		IndustrySIC:           sicLabel,
		IndustryHeuristicCode: heuristicCode,
		IndustryHeuristicName: heuristicName,
	}

	if usingNOPATFallback {
		result.Warnings = append(result.Warnings,
			"FCF using NOPAT approximation (D&A/CapEx unavailable from filing). Valuation may be less accurate for capital-intensive companies.")
	}

	if dcfFallbackWarning != "" {
		result.Warnings = append(result.Warnings, dcfFallbackWarning)
	}

	if len(gf.Warnings) > 0 {
		result.Warnings = append(result.Warnings, gf.Warnings...)
	}

	// Phase 4: Run multiples sanity cross-check to flag extreme divergences.
	// Uses EPS, EBITDA, and FCF from financials to compute implied P/E, EV/EBITDA,
	// and P/FCF, then compares against sector medians.
	if s.industryMultiples != nil {
		eps := 0.0
		if latestFinancialData.NetIncome > 0 && sharesOutstanding > 0 {
			eps = latestFinancialData.NetIncome / sharesOutstanding
		}
		ebitda := latestFinancialData.OperatingIncome + latestFinancialData.DepreciationAndAmortization

		// Calculate FCF per share for P/FCF cross-check.
		// Simplified FCF = NetIncome + D&A - CapEx. This intentionally omits
		// NWC change and uses NetIncome rather than NOPAT (the DCF engine's
		// "true FCF" definition), so ImpliedPFCF is a sanity-check proxy,
		// not the same FCF number driving the DCF itself.
		fcfPerShare := 0.0
		fcf := latestFinancialData.NetIncome + latestFinancialData.DepreciationAndAmortization - latestFinancialData.CapitalExpenditures
		if fcf > 0 && sharesOutstanding > 0 {
			fcfPerShare = fcf / sharesOutstanding
		}

		sectorPE := LookupMultiple(s.industryMultiples.SectorMedianPE, industryCode)
		sectorEVEBITDA := LookupMultiple(s.industryMultiples.EVEBITDAMultiples, industryCode)
		sectorPFCF := LookupMultiple(s.industryMultiples.SectorMedianPFCF, industryCode)

		sanity := CalculateSanityCheck(
			equityValue, dcfResult.EnterpriseValue,
			eps, ebitda, fcfPerShare,
			sharesOutstanding,
			industryCode,
			sectorPE, sectorEVEBITDA, sectorPFCF,
		)
		result.SanityCheck = sanity

		// Stage 11 — "cross_check" calc trace: emit implied multiples vs. sector medians
		// so operators can see whether the DCF output is in a reasonable range.
		// Only emitted when industryMultiples is configured and the check actually ran.
		if s.calcEmitter != nil {
			s.calcEmitter.Emit(ctx, "cross_check",
				zap.String("ticker", historicalData.Ticker),
				zap.Float64("implied_pe", sanity.ImpliedPE),
				zap.Float64("implied_ev_ebitda", sanity.ImpliedEVEBITDA),
				zap.Float64("sector_median_pe", sanity.SectorMedianPE),
				zap.Float64("sector_median_ev_ebitda", sanity.SectorMedianEVEBITDA),
				zap.Strings("flags", sanity.Flags),
			)
		}

		// Tier-1 narrate: crosscheck.evaluated. Spec §5 row 16 fields.
		// deviation_sigma is approximated as |1 - implied_pe/sector_pe| (no
		// proper sigma available in the current SanityCheck struct); flagged
		// is true iff any flag fired.
		flagged := len(sanity.Flags) > 0
		var devSigma float64
		if sanity.ImpliedPE > 0 && sanity.SectorMedianPE > 0 {
			r := sanity.ImpliedPE / sanity.SectorMedianPE
			if r > 1 {
				devSigma = r - 1
			} else {
				devSigma = 1 - r
			}
		}
		ccOutcome := narrate.OutcomeOK
		if flagged {
			ccOutcome = narrate.OutcomePartial
		}
		narrate.From(ctx).Emit(ctx, narrate.PhaseCrosscheckEvaluated, ccOutcome, "",
			zap.Float64("implied_pe", sanity.ImpliedPE),
			zap.Float64("sector_pe", sanity.SectorMedianPE),
			zap.Float64("deviation_sigma", devSigma),
			zap.Bool("flagged", flagged),
		)
		if b := artifact.From(ctx); b != nil {
			b.Snapshot(ctx, "crosscheck.evaluated", "16-crosscheck.json", sanity)
		}

		// Propagate sanity check flags as warnings for visibility in the API response
		if !sanity.IsReasonable {
			result.Warnings = append(result.Warnings, sanity.Flags...)
		}
	}

	// Tier-3 artifact bundle: snapshot the full DCF working result so the
	// per-year cashflows + PVs + TV can be replayed offline.
	if b := artifact.From(ctx); b != nil {
		b.Snapshot(ctx, "valuation.computed", "15-valuation.json", result)
		b.AddSchemaVersion("ValuationResult", 2)
	}

	return result, nil
}

// performAlternativeValuation executes a non-DCF valuation model (DDM, FFO, Revenue Multiple)
// and converts its result to the standard ValuationResult format.
func (s *Service) performAlternativeValuation(
	ctx context.Context,
	model models.ValuationModel,
	historicalData *entities.HistoricalFinancialData,
	marketData *entities.MarketData,
	macroData *entities.MacroData,
	growthEstimate *entities.GrowthEstimate,
	waccResult *wacc.Result,
	latestFinancialData *entities.FinancialData,
	latestPeriod string,
	tangibleValuePerShare float64,
	sharesOutstanding float64,
	industryCode string,
) (*entities.ValuationResult, error) {

	s.log(ctx).Info("Using alternative valuation model",
		zap.String("ticker", historicalData.Ticker),
		zap.String("model_type", model.ModelType()),
		zap.String("industry", industryCode))

	// Build the model input with all pre-computed values.
	// Now plumbs the *Service Clock seam through to model consumers
	// (RM-1.A: RevenueMultipleModel's staleness check). Replay binds
	// the Clock to manifest.started_at, so the staleness threshold is
	// evaluated deterministically against captured bundle time.
	modelInput := &models.ModelInput{
		HistoricalData:         historicalData,
		MarketData:             marketData,
		MacroData:              macroData,
		GrowthEstimate:         growthEstimate,
		Industry:               industryCode,
		WACC:                   waccResult.WACC,
		CostOfEquity:           waccResult.CostOfEquity,
		TaxRate:                latestFinancialData.TaxRate,
		SharesOutstanding:      sharesOutstanding,
		InterestBearingDebt:    latestFinancialData.InterestBearingDebt,
		CashAndCashEquivalents: latestFinancialData.CashAndCashEquivalents,
		Now:                    s.clock.Now,
	}

	// Execute the alternative model
	modelResult, err := model.Calculate(ctx, modelInput)
	if err != nil {
		// Primary model failed — try fallback strategies before giving up
		s.log(ctx).Warn("Primary valuation model failed, attempting fallback",
			zap.String("model", model.ModelType()),
			zap.String("ticker", historicalData.Ticker),
			zap.Error(err))

		// If company has positive OI, fall back to standard DCF path
		if effectiveOI(latestFinancialData) > 0 {
			return nil, errFallbackToDCF
		}

		// Negative OI — try revenue_multiple as last resort
		revModel := models.NewRevenueMultipleModelWithMultiples(nil, s.logger)
		modelResult, err = revModel.Calculate(ctx, modelInput)
		if err != nil {
			return nil, fmt.Errorf("%w: all models failed for this company", ErrModelNotApplicable)
		}
		modelResult.Warnings = append(modelResult.Warnings,
			fmt.Sprintf("Primary model (%s) failed, used revenue_multiple as fallback", model.ModelType()))
	}

	// Calculate data freshness score
	dataFreshnessScore := s.calculateDataFreshnessScore(latestFinancialData, marketData, macroData)

	gf := calculateGrahamFloorMetrics(ctx, s.logger, historicalData.Ticker,
		latestFinancialData, sharesOutstanding, marketData.SharePrice)

	// Convert ModelResult to ValuationResult
	result := &entities.ValuationResult{
		Ticker: historicalData.Ticker,
		// See note in DCF path above — Clock seam keeps replay deterministic.
		CalculatedAt:          s.clock.Now(),
		TangibleValuePerShare: tangibleValuePerShare,
		DCFValuePerShare:      modelResult.IntrinsicValuePerShare,
		CurrentAssetsPerShare: gf.CurrentAssetsPerShare,
		NCAVPerShare:          gf.NCAVPerShare,
		GrahamFloorPerShare:   gf.GrahamFloorPerShare,
		GrahamDiscountPct:     gf.GrahamDiscountPct,
		WACC:                  waccResult.WACC,
		GrowthRate:            growthEstimate.SummaryGrowthRate(),
		GrowthRates:           growthEstimate.ProjectedGrowthRates,
		TerminalGrowthRate:    growthEstimate.TerminalGrowthRate,
		GrowthSource:          growthEstimate.Source,
		GrowthConfidence:      modelResult.Confidence,
		MarketRiskPremium:     macroData.MarketRiskPremium,
		EnterpriseValue:       modelResult.EnterpriseValue,
		EquityValue:           modelResult.EquityValue,
		FinancialDataPeriod:   latestPeriod,
		MarketDataDate:        marketData.AsOf,
		CurrentPrice:          marketData.SharePrice,
		DataFreshnessScore:    dataFreshnessScore,
		CalculationMethod:     modelResult.ModelType,
		CalculationVersion:    "4.1",
		Warnings:              modelResult.Warnings,
	}

	if len(gf.Warnings) > 0 {
		result.Warnings = append(result.Warnings, gf.Warnings...)
	}

	// Tier-3 artifact bundle: snapshot the alt-model valuation result and
	// stamp the ValuationResult schema version. The DCF path emits the same
	// pair (service.go:1234-1235); the alt-model path previously omitted
	// them, which caused two issues for replay:
	//   1. No 15-valuation.json snapshot for alt-model bundles, breaking
	//      Stage K --diff-stages comparisons.
	//   2. ValuationResult missing from manifest.schema_versions, surfacing
	//      a false-positive schema_drift entry on every same-SHA replay of
	//      an alt-model bundle (e.g. MXL → revenue_multiple).
	if b := artifact.From(ctx); b != nil {
		b.Snapshot(ctx, "valuation.computed", "15-valuation.json", result)
		b.AddSchemaVersion("ValuationResult", 2)
	}

	return result, nil
}

// calculateTangibleValuePerShare calculates the tangible book value per share.
//
// Share-resolution priority chain uses the same chain as performValuation
// (the DCF-path share-resolution block): diluted → market basic → financial
// basic → 0.
//
// The flip from market-basic-first to diluted-first landed in v0.10.0 as PR
// #2 of the Graham-floor metrics work (graham-floor-metrics-spec.md §4.5).
// It produces a 2-5% lower per-share value for issuers with material
// option/RSU/convertible dilution (typical large-caps) and brings this field
// into line with every other per-share number in the response (DCF, NCAV,
// current_assets_per_share, graham_floor) which already use diluted shares.
func (s *Service) calculateTangibleValuePerShare(financial *entities.FinancialData, market *entities.MarketData) float64 {
	// Calculate tangible equity (total assets - intangibles - liabilities)
	tangibleEquity := financial.TangibleAssets

	// Diluted-first priority chain (consistent with DCF path).
	shares := financial.DilutedSharesOutstanding
	if shares <= 0 {
		shares = market.SharesOutstanding
	}
	if shares <= 0 {
		shares = financial.SharesOutstanding
	}

	if shares <= 0 {
		return 0
	}

	return tangibleEquity / shares
}

// calculateTerminalGrowthRate calculates a conservative terminal growth rate.
// It also enforces a minimum 2% spread below WACC to prevent terminal value explosion.
func (s *Service) calculateTerminalGrowthRate(historicalCAGR, wacc float64) float64 {
	// Conservative approach: min of 3% or half of historical CAGR
	terminalGrowth := historicalCAGR / 2
	maxTerminalGrowth := 0.03 // 3%

	if terminalGrowth > maxTerminalGrowth {
		terminalGrowth = maxTerminalGrowth
	}

	if terminalGrowth <= 0 {
		terminalGrowth = 0.02 // Minimum 2% for inflation (viable businesses grow at least with prices)
	}

	// Ensure terminal growth stays at least 2% below WACC to prevent TV explosion
	if wacc > 0 && terminalGrowth > wacc-0.02 {
		terminalGrowth = wacc - 0.02
		if terminalGrowth < 0.01 {
			terminalGrowth = 0.01
		}
	}

	return terminalGrowth
}

// averageCapExAndDA computes the average D&A and CapEx over available annual periods.
// This smooths cyclical spikes (e.g., a single year's massive AI infrastructure buildout)
// that would otherwise make projected FCF negative for all future years.
func (s *Service) averageCapExAndDA(historicalData *entities.HistoricalFinancialData) (avgDA, avgCapEx float64) {
	recentYears := historicalData.GetRecentYears(5)
	var daSum, capexSum float64
	var count float64

	for _, fd := range recentYears {
		if fd.DepreciationAndAmortization > 0 || fd.CapitalExpenditures > 0 {
			daSum += fd.DepreciationAndAmortization
			capexSum += fd.CapitalExpenditures
			count++
		}
	}

	if count == 0 {
		return 0, 0
	}
	return daSum / count, capexSum / count
}

// isFinancialDataIncomplete checks whether stored financial data is missing
// critical FCF fields (D&A, CapEx, Cash). Returns true if ALL are zero/missing,
// which indicates pre-Phase-1.2 data that should be re-fetched.
// effectiveOI returns the best available operating income (normalized preferred, raw as fallback).
func effectiveOI(fd *entities.FinancialData) float64 {
	if fd.NormalizedOperatingIncome > 0 {
		return fd.NormalizedOperatingIncome
	}
	return fd.OperatingIncome
}

func (s *Service) isFinancialDataIncomplete(data *entities.HistoricalFinancialData) bool {
	latest, _ := data.GetLatestPeriod()
	if latest == nil {
		return true
	}
	return latest.DepreciationAndAmortization == 0 &&
		latest.CapitalExpenditures == 0 &&
		latest.CashAndCashEquivalents == 0
}

// calculateDataFreshnessScore calculates a score from 0-100 based on data age.
//
// All wall-clock reads route through s.clock so the score is deterministic
// under replay (Phase R0/D7): replay binds Clock to manifest.started_at, so
// every age delta reflects the original capture's age rather than
// recomputing against the wall clock at replay time. Production binds Clock
// to wallClock{} (time.Now), so observable behavior is unchanged.
//
// We compute the market age inline rather than calling market.GetDataAge()
// because that entity helper unconditionally reads time.Now() — a third
// latent leak in the same payload-visible field that the dispatch's
// two-reads framing did not flag explicitly. Cross-year replay would
// otherwise still see the score floating with the wall clock, so we route
// market age through s.clock here for symmetry. The entity helper is left
// alone (it has other call sites and changing its signature is out of scope
// for this dispatch).
func (s *Service) calculateDataFreshnessScore(financial *entities.FinancialData, market *entities.MarketData, macro *entities.MacroData) int {
	score := 100
	now := s.clock.Now()

	// Reduce score based on financial data age
	financialAge := now.Sub(financial.AsOf)
	if financialAge > 90*24*time.Hour { // More than 90 days
		score -= 30
	} else if financialAge > 30*24*time.Hour { // More than 30 days
		score -= 15
	}

	// Reduce score based on market data age
	marketAge := now.Sub(market.AsOf)
	if marketAge > 7*24*time.Hour { // More than 7 days
		score -= 20
	} else if marketAge > 24*time.Hour { // More than 1 day
		score -= 10
	}

	// Reduce score based on macro data age
	macroAge := now.Sub(macro.AsOf)
	if macroAge > 30*24*time.Hour { // More than 30 days
		score -= 20
	} else if macroAge > 7*24*time.Hour { // More than 7 days
		score -= 10
	}

	if score < 0 {
		score = 0
	}

	return score
}

// analystEstimateCacheTTL defines the cache duration for analyst consensus estimates.
// 7 days is appropriate because analyst estimate revisions are infrequent (typically
// quarterly around earnings), and Yahoo Finance rate limits make aggressive fetching costly.
const analystEstimateCacheTTL = 168 * time.Hour // 7 days

// analystEstimateCachePrefix is the versioned cache key prefix for analyst estimates.
const analystEstimateCachePrefix = "analyst:v1:"

// getAnalystEstimates returns analyst consensus estimates for a ticker, using a 7-day
// cache to reduce external API calls. Returns nil when the gateway is not configured
// or when both cache and live fetch fail (graceful degradation).
func (s *Service) getAnalystEstimates(ctx context.Context, ticker string) *ports.YFinanceAnalystEstimates {
	if s.yfinanceGateway == nil {
		return nil
	}

	cacheKey := analystEstimateCachePrefix + ticker

	// Try cache first
	var cached ports.YFinanceAnalystEstimates
	if err := s.cache.Get(ctx, cacheKey, &cached); err == nil {
		s.log(ctx).Debug("Analyst estimates cache hit", zap.String("ticker", ticker))
		return &cached
	}

	// Cache miss — fetch from Yahoo Finance
	estimates, err := s.yfinanceGateway.GetAnalystEstimates(ctx, ticker)
	if err != nil || estimates == nil {
		s.log(ctx).Debug("Failed to fetch analyst estimates, proceeding without",
			zap.String("ticker", ticker), zap.Error(err))
		return nil
	}

	// Store in cache for future requests
	if cacheErr := s.cache.Set(ctx, cacheKey, estimates, analystEstimateCacheTTL); cacheErr != nil {
		s.log(ctx).Warn("Failed to cache analyst estimates",
			zap.String("ticker", ticker), zap.Error(cacheErr))
	}

	s.log(ctx).Debug("Analyst estimates fetched and cached",
		zap.String("ticker", ticker),
		zap.Int("analysts", estimates.NumberOfAnalysts))

	return estimates
}

// calculateNetWorkingCapitalChange computes the change in net working capital
// between the two most recent annual periods. Positive = cash consumed.
func (s *Service) calculateNetWorkingCapitalChange(
	historicalData *entities.HistoricalFinancialData,
	latest *entities.FinancialData,
) float64 {
	if latest.CurrentAssets <= 0 || latest.CurrentLiabilities <= 0 {
		return 0 // data not available
	}

	latestNWC := latest.CurrentAssets - latest.CurrentLiabilities

	// Find prior period to compute delta
	recentYears := historicalData.GetRecentYears(2)
	if len(recentYears) < 2 {
		return 0 // not enough history for delta
	}

	// recentYears[0] is most recent, [1] is prior (sorted descending by GetRecentYears)
	prior := recentYears[1]
	if prior.CurrentAssets <= 0 || prior.CurrentLiabilities <= 0 {
		return 0
	}

	priorNWC := prior.CurrentAssets - prior.CurrentLiabilities
	return latestNWC - priorNWC
}
