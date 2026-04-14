package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// FinancialDataRepository implements the FinancialDataRepository interface for SQLite.
// All Phase 2/3 columns (D&A, CapEx, cash flow, dividends, equity, etc.)
// are persisted and round-tripped through the database.
type FinancialDataRepository struct {
	db *sqlx.DB
}

// NewFinancialDataRepository creates a new SQLite financial data repository
func NewFinancialDataRepository(db *sqlx.DB) ports.FinancialDataRepository {
	return &FinancialDataRepository{
		db: db,
	}
}

// Store stores financial data for a company
func (r *FinancialDataRepository) Store(ctx context.Context, data *entities.FinancialData) error {
	if data == nil {
		return fmt.Errorf("financial data cannot be nil")
	}

	// Convert missing fields to JSON
	missingFieldsJSON, err := json.Marshal(data.MissingFields)
	if err != nil {
		return fmt.Errorf("failed to marshal missing fields: %w", err)
	}

	query := `
		INSERT INTO financial_data (
			ticker, cik, filing_period, filing_date, as_of_date,
			operating_income, normalized_operating_income, revenue,
			interest_expense, tax_rate,
			total_assets, tangible_assets, goodwill, other_intangibles,
			total_debt, interest_bearing_debt,
			inventory, inventory_turnover, dead_inventory_writedown,
			dividends_per_share, net_income, gain_on_property_sales,
			depreciation_and_amortization, capital_expenditures, operating_cash_flow,
			current_assets, current_liabilities,
			cash_and_cash_equivalents, stockholders_equity,
			shares_outstanding, diluted_shares_outstanding,
			has_normalized_data, missing_fields, created_at, updated_at
		) VALUES (
			:ticker, :cik, :filing_period, :filing_date, :as_of_date,
			:operating_income, :normalized_operating_income, :revenue,
			:interest_expense, :tax_rate,
			:total_assets, :tangible_assets, :goodwill, :other_intangibles,
			:total_debt, :interest_bearing_debt,
			:inventory, :inventory_turnover, :dead_inventory_writedown,
			:dividends_per_share, :net_income, :gain_on_property_sales,
			:depreciation_and_amortization, :capital_expenditures, :operating_cash_flow,
			:current_assets, :current_liabilities,
			:cash_and_cash_equivalents, :stockholders_equity,
			:shares_outstanding, :diluted_shares_outstanding,
			:has_normalized_data, :missing_fields, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)`

	args := map[string]interface{}{
		"ticker":                        data.Ticker,
		"cik":                           data.CIK,
		"filing_period":                 data.FilingPeriod,
		"filing_date":                   data.FilingDate,
		"as_of_date":                    data.AsOf,
		"operating_income":              data.OperatingIncome,
		"normalized_operating_income":   data.NormalizedOperatingIncome,
		"revenue":                       data.Revenue,
		"interest_expense":              data.InterestExpense,
		"tax_rate":                      data.TaxRate,
		"total_assets":                  data.TotalAssets,
		"tangible_assets":               data.TangibleAssets,
		"goodwill":                      data.Goodwill,
		"other_intangibles":             data.OtherIntangibles,
		"total_debt":                    data.TotalDebt,
		"interest_bearing_debt":         data.InterestBearingDebt,
		"inventory":                     data.Inventory,
		"inventory_turnover":            data.InventoryTurnover,
		"dead_inventory_writedown":      data.DeadInventoryWritedown,
		"dividends_per_share":           data.DividendsPerShare,
		"net_income":                    data.NetIncome,
		"gain_on_property_sales":        data.GainOnPropertySales,
		"depreciation_and_amortization": data.DepreciationAndAmortization,
		"capital_expenditures":          data.CapitalExpenditures,
		"operating_cash_flow":           data.OperatingCashFlow,
		"current_assets":                data.CurrentAssets,
		"current_liabilities":           data.CurrentLiabilities,
		"cash_and_cash_equivalents":     data.CashAndCashEquivalents,
		"stockholders_equity":           data.StockholdersEquity,
		"shares_outstanding":            data.SharesOutstanding,
		"diluted_shares_outstanding":    data.DilutedSharesOutstanding,
		"has_normalized_data":           data.HasNormalizedData,
		"missing_fields":                string(missingFieldsJSON),
	}

	_, err = r.db.NamedExecContext(ctx, query, args)
	if err != nil {
		return fmt.Errorf("failed to store financial data: %w", err)
	}

	return nil
}

// GetLatest retrieves the most recent financial data for a ticker
func (r *FinancialDataRepository) GetLatest(ctx context.Context, ticker string) (*entities.FinancialData, error) {
	query := `
		SELECT
			ticker, cik, filing_period, filing_date, as_of_date,
			operating_income, normalized_operating_income, revenue,
			interest_expense, tax_rate,
			total_assets, tangible_assets, goodwill, other_intangibles,
			total_debt, interest_bearing_debt,
			inventory, inventory_turnover, dead_inventory_writedown,
			dividends_per_share, net_income, gain_on_property_sales,
			depreciation_and_amortization, capital_expenditures, operating_cash_flow,
			current_assets, current_liabilities,
			cash_and_cash_equivalents, stockholders_equity,
			shares_outstanding, diluted_shares_outstanding,
			has_normalized_data, missing_fields
		FROM financial_data
		WHERE ticker = ?
		ORDER BY filing_date DESC, as_of_date DESC
		LIMIT 1`

	var data entities.FinancialData
	var missingFieldsJSON string

	err := r.db.QueryRowxContext(ctx, query, ticker).Scan(
		&data.Ticker, &data.CIK, &data.FilingPeriod, &data.FilingDate, &data.AsOf,
		&data.OperatingIncome, &data.NormalizedOperatingIncome, &data.Revenue,
		&data.InterestExpense, &data.TaxRate,
		&data.TotalAssets, &data.TangibleAssets, &data.Goodwill, &data.OtherIntangibles,
		&data.TotalDebt, &data.InterestBearingDebt,
		&data.Inventory, &data.InventoryTurnover, &data.DeadInventoryWritedown,
		&data.DividendsPerShare, &data.NetIncome, &data.GainOnPropertySales,
		&data.DepreciationAndAmortization, &data.CapitalExpenditures, &data.OperatingCashFlow,
		&data.CurrentAssets, &data.CurrentLiabilities,
		&data.CashAndCashEquivalents, &data.StockholdersEquity,
		&data.SharesOutstanding, &data.DilutedSharesOutstanding,
		&data.HasNormalizedData, &missingFieldsJSON,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no financial data found for ticker %s", ticker)
		}
		return nil, fmt.Errorf("failed to get latest financial data: %w", err)
	}

	// Unmarshal missing fields
	if missingFieldsJSON != "" && missingFieldsJSON != "null" {
		err = json.Unmarshal([]byte(missingFieldsJSON), &data.MissingFields)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal missing fields: %w", err)
		}
	}

	return &data, nil
}

// GetHistorical retrieves historical financial data for a ticker
func (r *FinancialDataRepository) GetHistorical(ctx context.Context, ticker string, periods int) (*entities.HistoricalFinancialData, error) {
	query := `
		SELECT
			ticker, cik, filing_period, filing_date, as_of_date,
			operating_income, normalized_operating_income, revenue,
			interest_expense, tax_rate,
			total_assets, tangible_assets, goodwill, other_intangibles,
			total_debt, interest_bearing_debt,
			inventory, inventory_turnover, dead_inventory_writedown,
			dividends_per_share, net_income, gain_on_property_sales,
			depreciation_and_amortization, capital_expenditures, operating_cash_flow,
			current_assets, current_liabilities,
			cash_and_cash_equivalents, stockholders_equity,
			shares_outstanding, diluted_shares_outstanding,
			has_normalized_data, missing_fields
		FROM financial_data
		WHERE ticker = ?
		ORDER BY filing_date DESC, as_of_date DESC
		LIMIT ?`

	rows, err := r.db.QueryxContext(ctx, query, ticker, periods)
	if err != nil {
		return nil, fmt.Errorf("failed to query historical data: %w", err)
	}
	defer func() { _ = rows.Close() }()

	historicalData := &entities.HistoricalFinancialData{
		Ticker: ticker,
		Data:   make(map[string]*entities.FinancialData),
	}

	for rows.Next() {
		var data entities.FinancialData
		var missingFieldsJSON string

		err := rows.Scan(
			&data.Ticker, &data.CIK, &data.FilingPeriod, &data.FilingDate, &data.AsOf,
			&data.OperatingIncome, &data.NormalizedOperatingIncome, &data.Revenue,
			&data.InterestExpense, &data.TaxRate,
			&data.TotalAssets, &data.TangibleAssets, &data.Goodwill, &data.OtherIntangibles,
			&data.TotalDebt, &data.InterestBearingDebt,
			&data.Inventory, &data.InventoryTurnover, &data.DeadInventoryWritedown,
			&data.DividendsPerShare, &data.NetIncome, &data.GainOnPropertySales,
			&data.DepreciationAndAmortization, &data.CapitalExpenditures, &data.OperatingCashFlow,
			&data.CurrentAssets, &data.CurrentLiabilities,
			&data.CashAndCashEquivalents, &data.StockholdersEquity,
			&data.SharesOutstanding, &data.DilutedSharesOutstanding,
			&data.HasNormalizedData, &missingFieldsJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan financial data: %w", err)
		}

		// Unmarshal missing fields
		if missingFieldsJSON != "" && missingFieldsJSON != "null" {
			err = json.Unmarshal([]byte(missingFieldsJSON), &data.MissingFields)
			if err != nil {
				return nil, fmt.Errorf("failed to unmarshal missing fields: %w", err)
			}
		}

		historicalData.Data[data.FilingPeriod] = &data
	}

	if len(historicalData.Data) == 0 {
		return nil, fmt.Errorf("no historical data found for ticker %s", ticker)
	}

	return historicalData, nil
}

// GetByPeriod retrieves financial data for a specific period
func (r *FinancialDataRepository) GetByPeriod(ctx context.Context, ticker, period string) (*entities.FinancialData, error) {
	query := `
		SELECT
			ticker, cik, filing_period, filing_date, as_of_date,
			operating_income, normalized_operating_income, revenue,
			interest_expense, tax_rate,
			total_assets, tangible_assets, goodwill, other_intangibles,
			total_debt, interest_bearing_debt,
			inventory, inventory_turnover, dead_inventory_writedown,
			dividends_per_share, net_income, gain_on_property_sales,
			depreciation_and_amortization, capital_expenditures, operating_cash_flow,
			current_assets, current_liabilities,
			cash_and_cash_equivalents, stockholders_equity,
			shares_outstanding, diluted_shares_outstanding,
			has_normalized_data, missing_fields
		FROM financial_data
		WHERE ticker = ? AND filing_period = ?`

	var data entities.FinancialData
	var missingFieldsJSON string

	err := r.db.QueryRowxContext(ctx, query, ticker, period).Scan(
		&data.Ticker, &data.CIK, &data.FilingPeriod, &data.FilingDate, &data.AsOf,
		&data.OperatingIncome, &data.NormalizedOperatingIncome, &data.Revenue,
		&data.InterestExpense, &data.TaxRate,
		&data.TotalAssets, &data.TangibleAssets, &data.Goodwill, &data.OtherIntangibles,
		&data.TotalDebt, &data.InterestBearingDebt,
		&data.Inventory, &data.InventoryTurnover, &data.DeadInventoryWritedown,
		&data.DividendsPerShare, &data.NetIncome, &data.GainOnPropertySales,
		&data.DepreciationAndAmortization, &data.CapitalExpenditures, &data.OperatingCashFlow,
		&data.CurrentAssets, &data.CurrentLiabilities,
		&data.CashAndCashEquivalents, &data.StockholdersEquity,
		&data.SharesOutstanding, &data.DilutedSharesOutstanding,
		&data.HasNormalizedData, &missingFieldsJSON,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no financial data found for ticker %s period %s", ticker, period)
		}
		return nil, fmt.Errorf("failed to get financial data by period: %w", err)
	}

	// Unmarshal missing fields
	if missingFieldsJSON != "" && missingFieldsJSON != "null" {
		err = json.Unmarshal([]byte(missingFieldsJSON), &data.MissingFields)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal missing fields: %w", err)
		}
	}

	return &data, nil
}

// StoreHistorical stores multiple periods of financial data atomically within a transaction.
// If any period fails to store, the entire batch is rolled back.
func (r *FinancialDataRepository) StoreHistorical(ctx context.Context, data *entities.HistoricalFinancialData) error {
	if data == nil {
		return fmt.Errorf("historical financial data cannot be nil")
	}

	if len(data.Data) == 0 {
		return fmt.Errorf("historical financial data must contain at least one period")
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() // no-op after Commit

	for _, periodData := range data.Data {
		if err := r.storeInTx(ctx, tx, periodData); err != nil {
			return fmt.Errorf("failed to store period %s: %w", periodData.FilingPeriod, err)
		}
	}

	return tx.Commit()
}

// storeInTx inserts a single period's data using the given transaction handle.
func (r *FinancialDataRepository) storeInTx(ctx context.Context, tx *sqlx.Tx, data *entities.FinancialData) error {
	if data == nil {
		return fmt.Errorf("financial data cannot be nil")
	}

	missingFieldsJSON, err := json.Marshal(data.MissingFields)
	if err != nil {
		return fmt.Errorf("failed to marshal missing fields: %w", err)
	}

	query := `
		INSERT INTO financial_data (
			ticker, cik, filing_period, filing_date, as_of_date,
			operating_income, normalized_operating_income, revenue,
			interest_expense, tax_rate,
			total_assets, tangible_assets, goodwill, other_intangibles,
			total_debt, interest_bearing_debt,
			inventory, inventory_turnover, dead_inventory_writedown,
			dividends_per_share, net_income, gain_on_property_sales,
			depreciation_and_amortization, capital_expenditures, operating_cash_flow,
			current_assets, current_liabilities,
			cash_and_cash_equivalents, stockholders_equity,
			shares_outstanding, diluted_shares_outstanding,
			has_normalized_data, missing_fields, created_at, updated_at
		) VALUES (
			:ticker, :cik, :filing_period, :filing_date, :as_of_date,
			:operating_income, :normalized_operating_income, :revenue,
			:interest_expense, :tax_rate,
			:total_assets, :tangible_assets, :goodwill, :other_intangibles,
			:total_debt, :interest_bearing_debt,
			:inventory, :inventory_turnover, :dead_inventory_writedown,
			:dividends_per_share, :net_income, :gain_on_property_sales,
			:depreciation_and_amortization, :capital_expenditures, :operating_cash_flow,
			:current_assets, :current_liabilities,
			:cash_and_cash_equivalents, :stockholders_equity,
			:shares_outstanding, :diluted_shares_outstanding,
			:has_normalized_data, :missing_fields, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)`

	args := map[string]interface{}{
		"ticker":                        data.Ticker,
		"cik":                           data.CIK,
		"filing_period":                 data.FilingPeriod,
		"filing_date":                   data.FilingDate,
		"as_of_date":                    data.AsOf,
		"operating_income":              data.OperatingIncome,
		"normalized_operating_income":   data.NormalizedOperatingIncome,
		"revenue":                       data.Revenue,
		"interest_expense":              data.InterestExpense,
		"tax_rate":                      data.TaxRate,
		"total_assets":                  data.TotalAssets,
		"tangible_assets":               data.TangibleAssets,
		"goodwill":                      data.Goodwill,
		"other_intangibles":             data.OtherIntangibles,
		"total_debt":                    data.TotalDebt,
		"interest_bearing_debt":         data.InterestBearingDebt,
		"inventory":                     data.Inventory,
		"inventory_turnover":            data.InventoryTurnover,
		"dead_inventory_writedown":      data.DeadInventoryWritedown,
		"dividends_per_share":           data.DividendsPerShare,
		"net_income":                    data.NetIncome,
		"gain_on_property_sales":        data.GainOnPropertySales,
		"depreciation_and_amortization": data.DepreciationAndAmortization,
		"capital_expenditures":          data.CapitalExpenditures,
		"operating_cash_flow":           data.OperatingCashFlow,
		"current_assets":                data.CurrentAssets,
		"current_liabilities":           data.CurrentLiabilities,
		"cash_and_cash_equivalents":     data.CashAndCashEquivalents,
		"stockholders_equity":           data.StockholdersEquity,
		"shares_outstanding":            data.SharesOutstanding,
		"diluted_shares_outstanding":    data.DilutedSharesOutstanding,
		"has_normalized_data":           data.HasNormalizedData,
		"missing_fields":                string(missingFieldsJSON),
	}

	_, err = tx.NamedExecContext(ctx, query, args)
	if err != nil {
		return fmt.Errorf("failed to store financial data: %w", err)
	}

	return nil
}

// GetLastUpdated returns when the data was last updated for a ticker
func (r *FinancialDataRepository) GetLastUpdated(ctx context.Context, ticker string) (time.Time, error) {
	query := `
		SELECT updated_at 
		FROM financial_data 
		WHERE ticker = ? 
		ORDER BY updated_at DESC 
		LIMIT 1`

	var updatedAt time.Time
	err := r.db.QueryRowxContext(ctx, query, ticker).Scan(&updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return time.Time{}, fmt.Errorf("no financial data found for ticker %s", ticker)
		}
		return time.Time{}, fmt.Errorf("failed to get last updated time: %w", err)
	}

	return updatedAt, nil
}
