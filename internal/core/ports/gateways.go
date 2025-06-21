package ports

import (
	"context"
	"time"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// SECGateway defines the interface for SEC data operations
type SECGateway interface {
	// GetCompanyFacts retrieves company facts from SEC API
	GetCompanyFacts(ctx context.Context, cik string) (*SECCompanyFacts, error)

	// GetTickerCIKMapping retrieves the ticker-to-CIK mapping from SEC
	GetTickerCIKMapping(ctx context.Context) (map[string]string, error)

	// ParseFinancialData extracts financial data from SEC company facts
	ParseFinancialData(ctx context.Context, facts *SECCompanyFacts) (*entities.HistoricalFinancialData, error)

	// NormalizeFinancialData applies normalization rules to financial data
	NormalizeFinancialData(ctx context.Context, data *entities.FinancialData) (*entities.FinancialData, error)
}

// MarketDataGateway defines the interface for market data operations
type MarketDataGateway interface {
	// GetMarketData retrieves current market data for a ticker
	GetMarketData(ctx context.Context, ticker string) (*entities.MarketData, error)

	// GetBatchMarketData retrieves market data for multiple tickers
	GetBatchMarketData(ctx context.Context, tickers []string) (map[string]*entities.MarketData, error)

	// GetBeta retrieves beta for a ticker
	GetBeta(ctx context.Context, ticker string) (float64, error)

	// GetSharePrice retrieves current share price for a ticker
	GetSharePrice(ctx context.Context, ticker string) (float64, error)

	// GetSharesOutstanding retrieves shares outstanding for a ticker
	GetSharesOutstanding(ctx context.Context, ticker string) (float64, error)
}

// MacroDataGateway defines the interface for macro-economic data operations
type MacroDataGateway interface {
	// GetTreasuryRate retrieves current Treasury rate (risk-free rate)
	GetTreasuryRate(ctx context.Context, maturity string) (float64, error)

	// GetMacroData retrieves comprehensive macro data
	GetMacroData(ctx context.Context) (*entities.MacroData, error)

	// GetInflationRate retrieves current inflation rate
	GetInflationRate(ctx context.Context) (float64, error)
}

// SECCompanyFacts represents the structure of SEC Company Facts API response
type SECCompanyFacts struct {
	CIK        string                  `json:"cik"`
	EntityName string                  `json:"entityName"`
	Facts      map[string]SECFactGroup `json:"facts"`
	FilingDate time.Time               `json:"-"` // Derived from facts
}

// SECFactGroup represents a group of facts by taxonomy
type SECFactGroup struct {
	Label       string               `json:"label"`
	Description string               `json:"description"`
	Units       map[string][]SECFact `json:"units"`
}

// SECFact represents a single fact from SEC data
type SECFact struct {
	End   string  `json:"end"`   // End date (YYYY-MM-DD)
	Val   float64 `json:"val"`   // Value
	Accn  string  `json:"accn"`  // Accession number
	Fy    int     `json:"fy"`    // Fiscal year
	Fp    string  `json:"fp"`    // Fiscal period (FY, Q1, Q2, Q3, Q4)
	Form  string  `json:"form"`  // Form type (10-K, 10-Q, etc.)
	Filed string  `json:"filed"` // Filed date (YYYY-MM-DD)
	Frame string  `json:"frame"` // Frame identifier
}

// YFinanceGateway defines the interface for Yahoo Finance-style data operations
type YFinanceGateway interface {
	// GetQuote retrieves current quote data for a ticker
	GetQuote(ctx context.Context, ticker string) (*YFinanceQuote, error)

	// GetBatchQuotes retrieves quotes for multiple tickers
	GetBatchQuotes(ctx context.Context, tickers []string) (map[string]*YFinanceQuote, error)

	// GetKeyStatistics retrieves key statistics including beta and shares outstanding
	GetKeyStatistics(ctx context.Context, ticker string) (*YFinanceKeyStats, error)

	// GetHistoricalPrices retrieves historical price data for beta calculation
	GetHistoricalPrices(ctx context.Context, ticker string, days int) ([]YFinancePricePoint, error)
}

// YFinanceQuote represents quote data from Yahoo Finance
type YFinanceQuote struct {
	Symbol               string  `json:"symbol"`
	RegularMarketPrice   float64 `json:"regularMarketPrice"`
	MarketCap            float64 `json:"marketCap"`
	SharesOutstanding    float64 `json:"sharesOutstanding"`
	RegularMarketVolume  float64 `json:"regularMarketVolume"`
	AverageDailyVolume3M float64 `json:"averageDailyVolume3Month"`
	Beta                 float64 `json:"beta"`
	Currency             string  `json:"currency"`
	MarketState          string  `json:"marketState"`
	RegularMarketTime    int64   `json:"regularMarketTime"`
}

// YFinanceKeyStats represents key statistics from Yahoo Finance
type YFinanceKeyStats struct {
	Beta                     float64 `json:"beta"`
	Beta3Year                float64 `json:"beta3Year"`
	SharesOutstanding        float64 `json:"sharesOutstanding"`
	SharesFloat              float64 `json:"floatShares"`
	ImpliedSharesOutstanding float64 `json:"impliedSharesOutstanding"`
	BookValue                float64 `json:"bookValue"`
	PriceToBook              float64 `json:"priceToBook"`
	MarketCap                float64 `json:"marketCap"`
	EnterpriseValue          float64 `json:"enterpriseValue"`
	TotalCash                float64 `json:"totalCash"`
	TotalDebt                float64 `json:"totalDebt"`
}

// YFinancePricePoint represents a single price point for historical data
type YFinancePricePoint struct {
	Date   time.Time `json:"date"`
	Open   float64   `json:"open"`
	High   float64   `json:"high"`
	Low    float64   `json:"low"`
	Close  float64   `json:"close"`
	Volume float64   `json:"volume"`
}

// FinziveGateway defines the interface for Finzive scraping operations
type FinziveGateway interface {
	// GetFinancialData retrieves financial data from Finzive
	GetFinancialData(ctx context.Context, ticker string) (*FinziveFinancialData, error)

	// GetMarketData retrieves market data from Finzive as fallback
	GetMarketData(ctx context.Context, ticker string) (*entities.MarketData, error)

	// IsAvailable checks if Finzive data is available for a ticker
	IsAvailable(ctx context.Context, ticker string) (bool, error)
}

// FinziveFinancialData represents financial data scraped from Finzive
type FinziveFinancialData struct {
	Ticker            string    `json:"ticker"`
	CompanyName       string    `json:"company_name"`
	Revenue           float64   `json:"revenue"`
	OperatingIncome   float64   `json:"operating_income"`
	NetIncome         float64   `json:"net_income"`
	TotalAssets       float64   `json:"total_assets"`
	TotalDebt         float64   `json:"total_debt"`
	SharesOutstanding float64   `json:"shares_outstanding"`
	BookValue         float64   `json:"book_value"`
	MarketCap         float64   `json:"market_cap"`
	Beta              float64   `json:"beta"`
	ReportDate        time.Time `json:"report_date"`
	Source            string    `json:"source"`
	DataQuality       string    `json:"data_quality"`
}
