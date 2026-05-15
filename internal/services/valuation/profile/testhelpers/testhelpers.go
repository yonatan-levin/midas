// Package testhelpers provides shared test fixtures and helpers for Tier 2
// AssumptionProfile work. Helpers are defined ONCE here and consumed by
// every phase (P1/P2/P3/P4) so each worktree-isolated BACKEND agent uses
// identical fixtures.
//
// File layout:
//   - fixtures.go: synthetic ModelInput builders (BuildMXLModelInput,
//     BuildSyntheticAAPLishModelInput, BuildSyntheticDataCenterREITInput)
//     plus the buildHistoricalFromLatest / buildMarketData / buildMacroData
//     / buildGrowthEstimate helpers used by all of them.
//   - profile_registry.go: MustLoadFullFixture loads a richer
//     profile.Registry fixture for resolver tests.
//   - service.go: Service-level builders (BuildTestService,
//     BuildTestServiceWithFixedProfile, RunValuation) that are deliberately
//     Phase-Bootstrap-stubbed via t.Skip; P1-P4 fill them in inside their
//     consuming worktree. LoadGoldenJPMPrimaryValue is fully implemented in
//     Phase Bootstrap because the underlying fixture is captured in Task B.2.
//
// IMPORTANT: the entity field names here track the actual midas codebase
// (FinancialData.AsOf, HistoricalFinancialData.Data, MarketData.SharePrice,
// MacroData.RiskFreeRate / MarketRiskPremium). Any drift between the Tier 2
// plan pseudo-code and the real entity shapes is intentionally resolved
// here in favor of the real codebase; do not change those signatures
// without updating every consuming worktree.
package testhelpers
