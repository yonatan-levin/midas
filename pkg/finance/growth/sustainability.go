package growth

// CalculateInvestedCapital computes the total capital deployed in the business.
// Invested Capital = Stockholders' Equity + Interest-Bearing Debt - Cash
func CalculateInvestedCapital(totalEquity, interestBearingDebt, cash float64) float64 {
	ic := totalEquity + interestBearingDebt - cash
	if ic < 0 {
		return 0
	}
	return ic
}

// CalculateROIC computes Return on Invested Capital.
// ROIC = NOPAT / Invested Capital
func CalculateROIC(nopat, investedCapital float64) float64 {
	if investedCapital <= 0 {
		return 0
	}
	return nopat / investedCapital
}

// CalculateSustainableGrowth computes the maximum growth rate a company can
// sustain without changing its capital structure or profitability.
// Sustainable Growth = ROIC × Reinvestment Rate
// where Reinvestment Rate = 1 - Payout Ratio
func CalculateSustainableGrowth(nopat, investedCapital, payoutRatio float64) float64 {
	if investedCapital <= 0 {
		return 0
	}
	if payoutRatio < 0 {
		payoutRatio = 0
	}
	if payoutRatio > 1 {
		payoutRatio = 1
	}

	roic := nopat / investedCapital
	reinvestmentRate := 1 - payoutRatio

	sustainableGrowth := roic * reinvestmentRate
	if sustainableGrowth < 0 {
		return 0
	}
	return sustainableGrowth
}
