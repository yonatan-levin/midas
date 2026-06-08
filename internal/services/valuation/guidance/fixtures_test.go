package guidance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// B7 — AMD guidance fixtures under testdata/guidance/0000002488/.
//
// The fixtures are AUTHORED IN CODE here (the canonical Artifact values) and
// emitted to disk with a CORRECT artifact_sha256 stamped via ComputeArtifactSHA256
// — NOT via a production tool (spec B7). Regenerate after a deliberate fixture
// change with:
//
//	UPDATE_GUIDANCE_FIXTURES=1 go test ./internal/services/valuation/guidance/ -run TestGuidanceFixtures_Generate
//
// then `git diff` to review. The verification tests below load the on-disk
// fixtures and assert they round-trip, validate, and drive the loader's
// deterministic conflict/staleness behavior — so a hand-edit that breaks the
// hash or the contract fails CI without the -update flag.

const fixtureCIK = "0000002488"

// fixtureSet returns the five canonical AMD fixtures keyed by their on-disk
// filename (<accession>.json). Hashes are stamped by stampFixtureHashes.
func fixtureSet() map[string]*Artifact {
	highConf := &Artifact{
		SchemaVersion: SchemaVersion,
		Status:        StatusValidated,
		Issuer:        Issuer{Ticker: "AMD", CIK: fixtureCIK},
		Filing: Filing{
			Accession: "0000002488-26-000012", FormType: "10-K",
			FilingDate: "2026-02-04", PeriodEnd: "2025-12-28",
			SECURL: "https://www.sec.gov/cgi-bin/browse-edgar?action=getcompany&CIK=0000002488",
		},
		SourceSelection: &SourceSelection{
			Sections:           []string{"Item 7 MD&A", "Liquidity and Capital Resources"},
			SelectedTextSHA256: SHA256Hex("AMD FY2026 capex guidance — selected MD&A text"),
		},
		Extraction: &Extraction{
			CapExGuidance: &Envelope{
				ValueLow: Float(1.4e9), ValueHigh: Float(1.6e9), Unit: UnitAbsoluteUSD, Period: "FY2026",
				Basis: &Basis{
					GrossOrNet: "gross", CashOrAccrual: "cash",
					GAAPOrNonGAAP: "gaap", ConsolidatedOrSegment: "consolidated",
				},
				Confidence: 0.82,
				Evidence: []Evidence{{
					Quote:    "we expect capital expenditures of approximately $1.5 billion in fiscal 2026",
					Location: "Item 7, ¶ Liquidity and Capital Resources",
				}},
			},
		},
		AIProvenance: &AIProvenance{
			Provider: ProviderHandAuthored, ModelName: "fixture", ModelVersion: "n/a", Temperature: 0.0,
			PromptSHA256:      SHA256Hex("hand-authored AMD capex prompt fingerprint"),
			SchemaSHA256:      SHA256Hex("guidance-schema-1.0.0"),
			RawResponseSHA256: SHA256Hex("AMD capex raw extracted region"),
		},
		Validation: Validation{
			Status: string(StatusValidated), Confidence: 0.82,
			NormalizationRulesVersion: "1.0.0", ValidatorVersion: "fixture-1.0.0",
		},
	}

	lowConf := &Artifact{
		SchemaVersion: SchemaVersion,
		Status:        StatusValidated,
		Issuer:        Issuer{Ticker: "AMD", CIK: fixtureCIK},
		Filing: Filing{
			Accession: "0000002488-26-000020", FormType: "10-Q",
			FilingDate: "2026-04-29", PeriodEnd: "2026-03-28",
		},
		Extraction: &Extraction{
			CapExGuidance: &Envelope{
				ValueLow: Float(1.2e9), ValueHigh: Float(1.8e9), Unit: UnitAbsoluteUSD, Period: "FY2026",
				Basis:      &Basis{GrossOrNet: "gross", CashOrAccrual: "cash", GAAPOrNonGAAP: "gaap"},
				Confidence: 0.45, // below the default 0.70 anchor threshold ⇒ context only
				Evidence: []Evidence{{
					Quote:    "capital spending is expected to remain elevated through fiscal 2026",
					Location: "Item 2, ¶ Liquidity",
				}},
			},
		},
		Validation: Validation{
			Status: string(StatusValidated), Confidence: 0.45,
			Warnings:                  []string{"low per-envelope confidence; not anchor-eligible"},
			NormalizationRulesVersion: "1.0.0", ValidatorVersion: "fixture-1.0.0",
		},
	}

	noGuidance := &Artifact{
		SchemaVersion: SchemaVersion,
		Status:        StatusNoGuidanceFound,
		Issuer:        Issuer{Ticker: "AMD", CIK: fixtureCIK},
		Filing: Filing{
			Accession: "0000002488-26-000099", FormType: "10-Q",
			FilingDate: "2026-05-01", PeriodEnd: "2026-03-29",
		},
		Validation: Validation{
			Status: string(StatusNoGuidanceFound), Confidence: 0.0,
			NormalizationRulesVersion: "1.0.0", ValidatorVersion: "fixture-1.0.0",
		},
	}

	// Conflict pair: a 10-K and a LATER 10-Q both speaking to FY2027 (period_end
	// 2026-12-26). The newer 10-Q wins by filing_date.
	conflict10K := &Artifact{
		SchemaVersion: SchemaVersion,
		Status:        StatusValidated,
		Issuer:        Issuer{Ticker: "AMD", CIK: fixtureCIK},
		Filing: Filing{
			Accession: "0000002488-27-000005", FormType: "10-K",
			FilingDate: "2027-02-03", PeriodEnd: "2026-12-26",
		},
		Extraction: &Extraction{
			CapExGuidance: &Envelope{
				ValueLow: Float(1.8e9), ValueHigh: Float(2.0e9), Unit: UnitAbsoluteUSD, Period: "FY2027",
				Basis:      &Basis{GrossOrNet: "gross", CashOrAccrual: "cash", GAAPOrNonGAAP: "gaap"},
				Confidence: 0.80,
				Evidence:   []Evidence{{Quote: "fiscal 2027 capex of roughly $1.9 billion", Location: "Item 7"}},
			},
		},
		Validation: Validation{
			Status: string(StatusValidated), Confidence: 0.80,
			NormalizationRulesVersion: "1.0.0", ValidatorVersion: "fixture-1.0.0",
		},
	}
	conflict10Q := &Artifact{
		SchemaVersion: SchemaVersion,
		Status:        StatusValidated,
		Issuer:        Issuer{Ticker: "AMD", CIK: fixtureCIK},
		Filing: Filing{
			Accession: "0000002488-27-000040", FormType: "10-Q",
			FilingDate: "2027-04-28", PeriodEnd: "2026-12-26", // same period_end as the 10-K
		},
		Extraction: &Extraction{
			CapExGuidance: &Envelope{
				ValueLow: Float(2.0e9), ValueHigh: Float(2.2e9), Unit: UnitAbsoluteUSD, Period: "FY2027",
				Basis:      &Basis{GrossOrNet: "gross", CashOrAccrual: "cash", GAAPOrNonGAAP: "gaap"},
				Confidence: 0.78,
				Evidence:   []Evidence{{Quote: "we now expect fiscal 2027 capex near $2.1 billion", Location: "Item 2"}},
			},
		},
		Validation: Validation{
			Status: string(StatusValidated), Confidence: 0.78,
			NormalizationRulesVersion: "1.0.0", ValidatorVersion: "fixture-1.0.0",
		},
	}

	// Stale: a FY2024 capex artifact whose referenced period has long lapsed by
	// any plausible 2026+ as-of.
	stale := &Artifact{
		SchemaVersion: SchemaVersion,
		Status:        StatusValidated,
		Issuer:        Issuer{Ticker: "AMD", CIK: fixtureCIK},
		Filing: Filing{
			Accession: "0000002488-24-000003", FormType: "10-K",
			FilingDate: "2024-01-31", PeriodEnd: "2023-12-30",
		},
		Extraction: &Extraction{
			CapExGuidance: &Envelope{
				ValueLow: Float(0.9e9), ValueHigh: Float(1.1e9), Unit: UnitAbsoluteUSD, Period: "FY2024",
				Basis:      &Basis{GrossOrNet: "gross", CashOrAccrual: "cash", GAAPOrNonGAAP: "gaap"},
				Confidence: 0.85,
				Evidence:   []Evidence{{Quote: "fiscal 2024 capex of about $1.0 billion", Location: "Item 7"}},
			},
		},
		Validation: Validation{
			Status: string(StatusValidated), Confidence: 0.85,
			NormalizationRulesVersion: "1.0.0", ValidatorVersion: "fixture-1.0.0",
		},
	}

	set := map[string]*Artifact{
		highConf.Filing.Accession + ".json":    highConf,
		lowConf.Filing.Accession + ".json":     lowConf,
		noGuidance.Filing.Accession + ".json":  noGuidance,
		conflict10K.Filing.Accession + ".json": conflict10K,
		conflict10Q.Filing.Accession + ".json": conflict10Q,
		stale.Filing.Accession + ".json":       stale,
	}
	stampFixtureHashes(set)
	return set
}

// stampFixtureHashes computes and stamps the content hash on each fixture so the
// emitted JSON is self-consistent (hash verifies on load).
func stampFixtureHashes(set map[string]*Artifact) {
	for _, a := range set {
		h, err := ComputeArtifactSHA256(a)
		if err != nil {
			panic(err)
		}
		a.ArtifactSHA256 = h
	}
}

func fixtureDir() string {
	return filepath.Join("testdata", "guidance", fixtureCIK)
}

// TestGuidanceFixtures_Generate (re)writes the on-disk fixtures with correct
// hashes when UPDATE_GUIDANCE_FIXTURES=1. It is a generator, not a gate — it is
// a no-op assertion otherwise.
func TestGuidanceFixtures_Generate(t *testing.T) {
	if os.Getenv("UPDATE_GUIDANCE_FIXTURES") != "1" {
		t.Skip("set UPDATE_GUIDANCE_FIXTURES=1 to regenerate the AMD guidance fixtures")
	}
	dir := fixtureDir()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	for name, a := range fixtureSet() {
		body, err := json.MarshalIndent(a, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), append(body, '\n'), 0o644))
	}
}

// TestGuidanceFixtures_OnDisk_MatchAuthored asserts every on-disk fixture is
// byte-equal to the in-code authored artifact (so a hand-edit that drifts from
// the source — or a stale hash — fails without the -update flag).
func TestGuidanceFixtures_OnDisk_MatchAuthored(t *testing.T) {
	for name, want := range fixtureSet() {
		t.Run(name, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(fixtureDir(), name))
			require.NoError(t, err, "fixture missing — run TestGuidanceFixtures_Generate with UPDATE_GUIDANCE_FIXTURES=1")

			var got Artifact
			require.NoError(t, json.Unmarshal(body, &got))
			assert.Equal(t, *want, got, "on-disk fixture drifted from the authored source")

			// Structural validity + self-consistent hash.
			require.NoError(t, ValidateStructural(&got))
			require.NoError(t, verifyArtifactHash(&got), "fixture hash does not verify")
		})
	}
}

// TestGuidanceFixtures_LoaderBehavior drives the real Loader against the on-disk
// AMD fixtures and pins the documented deterministic outcomes.
func TestGuidanceFixtures_LoaderBehavior(t *testing.T) {
	root := filepath.Join("testdata", "guidance")
	l := NewLoader(root)

	t.Run("high-confidence hit selected in early-2026 window", func(t *testing.T) {
		// as-of 2026-03-01: the high-confidence 10-K (filing 2026-02-04) is the
		// newest eligible filing (the low-conf 10-Q files 2026-04-29, the
		// no-guidance files 2026-05-01, the FY2027 pair files in 2027, the stale
		// FY2024 files 2024). Newest-eligible-filing total order selects it.
		res, err := l.Load(fixtureCIK, mustDate(t, "2026-03-01"))
		require.NoError(t, err)
		require.NotNil(t, res.Artifact)
		assert.Equal(t, "0000002488-26-000012", res.Trace.SelectedAccession)
		assert.Equal(t, StatusValidated, res.Artifact.Status)
		assert.False(t, res.Trace.Stale)
	})

	t.Run("conflict pair: newer 10-Q wins over same-period 10-K", func(t *testing.T) {
		// as-of 2027-05-01: the FY2027 10-Q (filing 2027-04-28) and 10-K (filing
		// 2027-02-03) share period_end 2026-12-26; newest filing_date wins.
		res, err := l.Load(fixtureCIK, mustDate(t, "2027-05-01"))
		require.NoError(t, err)
		require.NotNil(t, res.Artifact)
		assert.Equal(t, "0000002488-27-000040", res.Trace.SelectedAccession, "newer 10-Q beats same-period 10-K")
		assert.Contains(t, res.Trace.RejectedAccessions, "0000002488-27-000005")
	})

	t.Run("stale FY2024 artifact flagged when it would be selected", func(t *testing.T) {
		// A tightly-scoped loader rooted at a stale-only dir would select the
		// FY2024 artifact and flag it stale. Here we assert the staleness helper
		// directly on the fixture to keep the assertion independent of selection.
		set := fixtureSet()
		staleArt := set["0000002488-24-000003.json"]
		assert.True(t, isStale(staleArt, mustDate(t, "2026-03-01")), "FY2024 lapsed by 2026 ⇒ stale")
	})

	t.Run("low-confidence fixture loads but is not anchor-eligible", func(t *testing.T) {
		// as-of 2026-04-30: the low-conf 10-Q (filing 2026-04-29) is now the
		// newest eligible filing. It loads as a hit, but its envelope confidence
		// (0.45) is below the default anchor threshold — the resolver (B3) treats
		// it as context-only. Here we assert the loader returns it as a hit and
		// the envelope confidence is sub-threshold.
		res, err := l.Load(fixtureCIK, mustDate(t, "2026-04-30"))
		require.NoError(t, err)
		require.NotNil(t, res.Artifact)
		assert.Equal(t, "0000002488-26-000020", res.Trace.SelectedAccession)
		require.NotNil(t, res.Artifact.Extraction.CapExGuidance)
		assert.Less(t, res.Artifact.Extraction.CapExGuidance.Confidence, 0.70)
	})

	t.Run("no_explicit_guidance_found fixture loads as a positive absence", func(t *testing.T) {
		// as-of 2026-05-02: the no-guidance 10-Q (filing 2026-05-01) is the
		// newest eligible filing and resolves as a first-class absence record.
		res, err := l.Load(fixtureCIK, mustDate(t, "2026-05-02"))
		require.NoError(t, err)
		require.NotNil(t, res.Artifact)
		assert.Equal(t, StatusNoGuidanceFound, res.Artifact.Status)
		assert.Equal(t, "no_explicit_guidance_found", res.Trace.Reason)
	})
}
