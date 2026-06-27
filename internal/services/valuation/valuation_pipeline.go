package valuation

import (
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/services/datacleaner/cleaneddata"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/authority"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/params"
	"github.com/midas/dcf-valuation-api/internal/services/valuation/profile"
	"github.com/midas/dcf-valuation-api/pkg/finance/dcf"
	"github.com/midas/dcf-valuation-api/pkg/finance/wacc"
)

// valuationCtx carries the intermediate state threaded between the phases of
// performValuation (resolveValuationInputs → computeWACC → runDCF →
// assembleResult). It exists ONLY to avoid 20-arg phase functions; it holds no
// behavior. Every field maps 1:1 to a local that previously lived in
// performValuation's scope (same name), so the phase-split diff stays a pure
// move. Locals that are phase-local (do not cross a phase boundary) stay as
// locals inside their phase and are NOT fields here — see the plan §1.3.
type valuationCtx struct {
	// inputs (set by the caller / validation)
	historicalData *entities.HistoricalFinancialData
	marketData     *entities.MarketData
	macroData      *entities.MacroData
	opts           *ValuationOptions
	cleaned        *cleaneddata.CleanedFinancialData

	// resolveValuationInputs outputs (stages B–N up to model selection)
	overrides             params.Overrides
	growthEstimate        *entities.GrowthEstimate
	latestFinancialData   *entities.FinancialData
	latestPeriod          string
	tangibleValuePerShare float64
	industryCode          string
	sicLabel              string
	heuristicCode         string
	heuristicName         string
	sharesOutstanding     float64
	resolvedProfile       *profile.ResolvedProfile
	resolutionTrace       profile.ResolutionTrace
	guidanceResolution    authority.Resolution
	hasOverrides          bool
	industryExitMultiple  float64
	p                     params.EffectiveValuationParams
	selectedModel         models.ValuationModel

	// computeWACC outputs (stages J–L)
	waccRestated       *cleaneddata.FinancialDataView
	rawBeta            float64
	blumeBeta          float64
	unleveredBeta      float64
	releveredBeta      float64
	beta               float64
	riskFreeRate       float64
	countryRiskPremium float64
	waccResult         *wacc.Result
	terminalGrowthRate float64

	// runDCF outputs (stages O–S)
	baseOI                    float64
	baseNormalizationMethod   string
	ttmOperatingIncomeSource  string
	ttmOperatingIncomeWarning string
	projectionYears           int
	terminalMethodLabel       string
	usingNOPATFallback        bool
	reinvestmentWarnings      []string
	dcfResult                 *dcf.Result
	equityValue               float64
	dcfValuePerShare          float64
	forwardShares             float64
	appliedDilutionRate       float64
	dilutionWarnings          []string
	dcfFallbackWarning        string
}
