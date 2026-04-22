package sec

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
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
func (g *Gateway) GetCompanyFacts(ctx context.Context, cik string) (*entities.CompanyFactsResponse, error) {
	facts, err := g.client.GetCompanyFacts(ctx, cik)
	if err != nil {
		return nil, err
	}

	// Count total concepts across all taxonomies
	totalConcepts := 0
	for _, concepts := range facts.Facts {
		totalConcepts += len(concepts)
	}

	// Convert ports.SECCompanyFacts to entities.CompanyFactsResponse
	return &entities.CompanyFactsResponse{
		CIK:         facts.CIK.String(),
		EntityName:  facts.EntityName,
		Facts:       convertFactsToMap(facts.Facts),
		FactsCount:  totalConcepts,
		LastUpdated: facts.FilingDate,
	}, nil
}

// convertFactsToMap converts nested SEC facts to a generic interface map that
// is fully traversable via type assertions (all slices are []interface{}, not []SECFact).
func convertFactsToMap(facts map[string]map[string]ports.SECFactGroup) map[string]interface{} {
	result := make(map[string]interface{})
	for taxonomy, concepts := range facts {
		taxonomyMap := make(map[string]interface{})
		for conceptName, group := range concepts {
			// Convert units to interface-based types so downstream type assertions work.
			// Go does not allow []SECFact → []interface{} covariant conversion.
			unitsMap := make(map[string]interface{})
			for unitType, secFacts := range group.Units {
				factSlice := make([]interface{}, len(secFacts))
				for i, f := range secFacts {
					factSlice[i] = map[string]interface{}{
						"val":   f.Val,
						"end":   f.End,
						"fy":    float64(f.Fy),
						"fp":    f.Fp,
						"filed": f.Filed,
						"accn":  f.Accn,
						"form":  f.Form,
						"frame": f.Frame,
					}
				}
				unitsMap[unitType] = factSlice
			}
			taxonomyMap[conceptName] = map[string]interface{}{
				"label":       group.Label,
				"description": group.Description,
				"units":       unitsMap,
			}
		}
		result[taxonomy] = taxonomyMap
	}
	return result
}

// GetCompanyConcepts retrieves company concepts from SEC API for a specific tag
func (g *Gateway) GetCompanyConcepts(ctx context.Context, cik string, tag string) (*entities.ConceptResponse, error) {
	return g.client.GetCompanyConcepts(ctx, cik, tag)
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
	logctx.Or(ctx, g.logger).Info("Fetching financial data for ticker",
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

	// 4. Fetch SIC code from the SEC submissions endpoint for industry classification.
	// This is a lightweight call that extracts only the SIC field from the metadata.
	// Graceful degradation: if the call fails, we proceed without SIC (keyword matching
	// in IndustryClassifier still works).
	sicCode, sicErr := g.client.GetCompanySIC(ctx, cik)
	if sicErr != nil {
		logctx.Or(ctx, g.logger).Debug("Could not fetch SIC code from submissions endpoint, proceeding without",
			zap.String("ticker", ticker),
			zap.Error(sicErr))
	} else if sicCode != "" {
		historical.SICCode = sicCode
		logctx.Or(ctx, g.logger).Debug("Extracted SIC code from SEC submissions",
			zap.String("ticker", ticker),
			zap.String("sic_code", sicCode))
	}

	// 5. Normalize each period's data
	for period, data := range historical.Data {
		normalized, err := g.parser.NormalizeFinancialData(ctx, data)
		if err != nil {
			logctx.Or(ctx, g.logger).Warn("Failed to normalize data for period",
				zap.String("ticker", ticker),
				zap.String("period", period),
				zap.Error(err))
			continue
		}
		historical.Data[period] = normalized
	}

	logctx.Or(ctx, g.logger).Info("Successfully processed financial data",
		zap.String("ticker", ticker),
		zap.Int("periods", len(historical.Data)))

	return historical, nil
}

// HealthCheck performs a health check on the SEC gateway
func (g *Gateway) HealthCheck(ctx context.Context) error {
	return g.client.HealthCheck(ctx)
}
