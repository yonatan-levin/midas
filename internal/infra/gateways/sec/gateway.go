package sec

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// Gateway implements the SEC gateway interface
type Gateway struct {
	client *Client
	parser *Parser
	logger *zap.Logger
}

// NewGateway creates a new SEC gateway
func NewGateway(cfg *config.SECConfig, logger *zap.Logger) *Gateway {
	client := NewClient(cfg, logger)
	parser := NewParser(logger)

	return &Gateway{
		client: client,
		parser: parser,
		logger: logger.Named("sec-gateway"),
	}
}

// GetCompanyFacts retrieves company facts from SEC API
func (g *Gateway) GetCompanyFacts(ctx context.Context, cik string) (*ports.SECCompanyFacts, error) {
	return g.client.GetCompanyFacts(ctx, cik)
}

// GetTickerCIKMapping retrieves the ticker-to-CIK mapping from SEC
func (g *Gateway) GetTickerCIKMapping(ctx context.Context) (map[string]string, error) {
	return g.client.GetTickerCIKMapping(ctx)
}

// ParseFinancialData extracts financial data from SEC company facts
func (g *Gateway) ParseFinancialData(ctx context.Context, facts *ports.SECCompanyFacts) (*entities.HistoricalFinancialData, error) {
	return g.parser.ParseFinancialData(ctx, facts)
}

// NormalizeFinancialData applies normalization rules to financial data
func (g *Gateway) NormalizeFinancialData(ctx context.Context, data *entities.FinancialData) (*entities.FinancialData, error) {
	return g.parser.NormalizeFinancialData(ctx, data)
}

// GetFinancialDataForTicker fetches and parses financial data for a ticker
func (g *Gateway) GetFinancialDataForTicker(ctx context.Context, ticker, cik string) (*entities.HistoricalFinancialData, error) {
	g.logger.Info("Fetching financial data for ticker",
		zap.String("ticker", ticker),
		zap.String("cik", cik))

	// 1. Get company facts from SEC
	facts, err := g.client.GetCompanyFacts(ctx, cik)
	if err != nil {
		return nil, fmt.Errorf("failed to get company facts for %s (CIK: %s): %w", ticker, cik, err)
	}

	// 2. Parse the financial data
	historical, err := g.parser.ParseFinancialData(ctx, facts)
	if err != nil {
		return nil, fmt.Errorf("failed to parse financial data for %s: %w", ticker, err)
	}

	// 3. Set the ticker on all financial data entries
	historical.Ticker = ticker
	for _, data := range historical.Data {
		data.Ticker = ticker
	}

	// 4. Normalize each period's data
	for period, data := range historical.Data {
		normalized, err := g.parser.NormalizeFinancialData(ctx, data)
		if err != nil {
			g.logger.Warn("Failed to normalize data for period",
				zap.String("ticker", ticker),
				zap.String("period", period),
				zap.Error(err))
			continue
		}
		historical.Data[period] = normalized
	}

	g.logger.Info("Successfully processed financial data",
		zap.String("ticker", ticker),
		zap.Int("periods", len(historical.Data)))

	return historical, nil
}

// HealthCheck performs a health check on the SEC gateway
func (g *Gateway) HealthCheck(ctx context.Context) error {
	return g.client.HealthCheck(ctx)
}
