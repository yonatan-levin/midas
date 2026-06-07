package guidance

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validCapExArtifact returns a fully-valid high-confidence CapEx artifact
// (mirrors the spec §"high-confidence hit" example). ArtifactSHA256 is left
// empty for the caller to compute when a hash-stamped copy is needed.
func validCapExArtifact() *Artifact {
	return &Artifact{
		SchemaVersion: SchemaVersion,
		Status:        StatusValidated,
		Issuer:        Issuer{Ticker: "AMD", CIK: "0000002488"},
		Filing: Filing{
			Accession:  "0000002488-26-000012",
			FormType:   "10-K",
			FilingDate: "2026-02-04",
			PeriodEnd:  "2025-12-28",
			SECURL:     "https://www.sec.gov/example",
		},
		SourceSelection: &SourceSelection{
			Sections:           []string{"Item 7 MD&A", "Liquidity and Capital Resources"},
			SelectedTextSHA256: SHA256Hex("selected text"),
		},
		Extraction: &Extraction{
			CapExGuidance: &Envelope{
				ValueLow:  1.4e9,
				ValueHigh: 1.6e9,
				Unit:      UnitAbsoluteUSD,
				Period:    "FY2026",
				Basis: &Basis{
					GrossOrNet:            "gross",
					CashOrAccrual:         "cash",
					GAAPOrNonGAAP:         "gaap",
					ConsolidatedOrSegment: "consolidated",
				},
				Confidence: 0.82,
				Evidence: []Evidence{
					{Quote: "we expect capital expenditures of approximately $1.5 billion in fiscal 2026", Location: "Item 7, ¶ Liquidity"},
				},
			},
		},
		AIProvenance: &AIProvenance{
			Provider:     ProviderHandAuthored,
			ModelName:    "fixture",
			ModelVersion: "n/a",
			Temperature:  0.0,
			PromptSHA256: SHA256Hex("prompt"),
		},
		Validation: Validation{
			Status:                    string(StatusValidated),
			Confidence:                0.82,
			NormalizationRulesVersion: "1.0.0",
			ValidatorVersion:          "fixture-1.0.0",
		},
	}
}

// absentArtifact returns a valid no_explicit_guidance_found record.
func absentArtifact() *Artifact {
	return &Artifact{
		SchemaVersion: SchemaVersion,
		Status:        StatusNoGuidanceFound,
		Issuer:        Issuer{Ticker: "AMD", CIK: "0000002488"},
		Filing: Filing{
			Accession:  "0000002488-26-000099",
			FormType:   "10-Q",
			FilingDate: "2026-05-01",
			PeriodEnd:  "2026-03-29",
		},
		Validation: Validation{
			Status:                    string(StatusNoGuidanceFound),
			Confidence:                0.0,
			NormalizationRulesVersion: "1.0.0",
			ValidatorVersion:          "fixture-1.0.0",
		},
	}
}

func mustHash(t *testing.T, a *Artifact) *Artifact {
	t.Helper()
	h, err := ComputeArtifactSHA256(a)
	require.NoError(t, err)
	a.ArtifactSHA256 = h
	return a
}

func TestArtifact_RoundTrip_HighConfidence(t *testing.T) {
	a := mustHash(t, validCapExArtifact())

	raw, err := json.Marshal(a)
	require.NoError(t, err)

	var got Artifact
	require.NoError(t, json.Unmarshal(raw, &got))

	assert.Equal(t, *a, got, "lossless marshal/unmarshal round-trip")

	// Re-validate after round-trip.
	require.NoError(t, ValidateStructural(&got))

	// Hash is stable across the round-trip (the embedded hash matches a fresh
	// recompute on the unmarshalled value).
	recomputed, err := ComputeArtifactSHA256(&got)
	require.NoError(t, err)
	assert.Equal(t, a.ArtifactSHA256, recomputed)
}

func TestArtifact_RoundTrip_NoGuidanceFound(t *testing.T) {
	a := mustHash(t, absentArtifact())

	raw, err := json.Marshal(a)
	require.NoError(t, err)

	var got Artifact
	require.NoError(t, json.Unmarshal(raw, &got))

	assert.Equal(t, *a, got)
	require.NoError(t, ValidateStructural(&got))

	// Wire form must carry the mandatory absent status.
	var wire map[string]any
	require.NoError(t, json.Unmarshal(raw, &wire))
	assert.Equal(t, "no_explicit_guidance_found", wire["status"])
	_, hasExtraction := wire["extraction"]
	assert.False(t, hasExtraction, "absent record omits extraction")
}

func TestComputeArtifactSHA256_Deterministic(t *testing.T) {
	a := validCapExArtifact()

	h1, err := ComputeArtifactSHA256(a)
	require.NoError(t, err)
	h2, err := ComputeArtifactSHA256(a)
	require.NoError(t, err)

	assert.Len(t, h1, 64, "sha256 hex is 64 chars")
	assert.Equal(t, h1, h2, "identical input → identical hash")

	// The embedded ArtifactSHA256 must NOT participate in its own preimage:
	// stamping it does not change the recomputed hash.
	a.ArtifactSHA256 = h1
	h3, err := ComputeArtifactSHA256(a)
	require.NoError(t, err)
	assert.Equal(t, h1, h3, "embedded hash excluded from preimage")
}

func TestComputeArtifactSHA256_SensitiveToContent(t *testing.T) {
	a := validCapExArtifact()
	h1, err := ComputeArtifactSHA256(a)
	require.NoError(t, err)

	// Mutate a value-bearing field → different hash.
	a.Extraction.CapExGuidance.ValueHigh = 1.7e9
	h2, err := ComputeArtifactSHA256(a)
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2, "content change → hash change")
}

func TestComputeArtifactSHA256_NilArtifact(t *testing.T) {
	_, err := ComputeArtifactSHA256(nil)
	assert.Error(t, err)
}

func TestEnvelope_Midpoint(t *testing.T) {
	e := Envelope{ValueLow: 1.4e9, ValueHigh: 1.6e9}
	assert.Equal(t, 1.5e9, e.Midpoint())

	// Point estimate: low == high → midpoint == the point.
	p := Envelope{ValueLow: 0.25, ValueHigh: 0.25}
	assert.Equal(t, 0.25, p.Midpoint())
}

func TestSHA256HexStrings_OrderInsensitive(t *testing.T) {
	a := SHA256HexStrings([]string{"b", "a", "c"})
	b := SHA256HexStrings([]string{"c", "b", "a"})
	assert.Equal(t, a, b, "set-like hash is order-insensitive")
	assert.NotEqual(t, a, SHA256HexStrings([]string{"a", "b"}))
}

func TestValidateStructural_Rules(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Artifact)
		wantErr error // sentinel to errors.Is against; nil ⇒ expect no error
	}{
		{name: "valid high-confidence", mutate: func(a *Artifact) {}, wantErr: nil},
		{name: "valid absent", mutate: func(a *Artifact) { *a = *absentArtifact() }, wantErr: nil},
		{
			name:    "missing schema_version",
			mutate:  func(a *Artifact) { a.SchemaVersion = "" },
			wantErr: ErrInvalidArtifact,
		},
		{
			name:    "unknown schema major",
			mutate:  func(a *Artifact) { a.SchemaVersion = "2.0.0" },
			wantErr: ErrUnknownSchemaMajor,
		},
		{
			name:    "unknown status",
			mutate:  func(a *Artifact) { a.Status = "bananas" },
			wantErr: ErrInvalidArtifact,
		},
		{
			name:    "missing issuer ticker",
			mutate:  func(a *Artifact) { a.Issuer.Ticker = "" },
			wantErr: ErrInvalidArtifact,
		},
		{
			name:    "missing issuer cik",
			mutate:  func(a *Artifact) { a.Issuer.CIK = "" },
			wantErr: ErrInvalidArtifact,
		},
		{
			name:    "missing accession",
			mutate:  func(a *Artifact) { a.Filing.Accession = "" },
			wantErr: ErrInvalidArtifact,
		},
		{
			name:    "missing form_type",
			mutate:  func(a *Artifact) { a.Filing.FormType = "" },
			wantErr: ErrInvalidArtifact,
		},
		{
			name:    "missing filing_date",
			mutate:  func(a *Artifact) { a.Filing.FilingDate = "" },
			wantErr: ErrInvalidArtifact,
		},
		{
			name:    "malformed filing_date",
			mutate:  func(a *Artifact) { a.Filing.FilingDate = "2026/02/04" },
			wantErr: ErrInvalidArtifact,
		},
		{
			name:    "missing period_end",
			mutate:  func(a *Artifact) { a.Filing.PeriodEnd = "" },
			wantErr: ErrInvalidArtifact,
		},
		{
			name:    "value_low > value_high",
			mutate:  func(a *Artifact) { a.Extraction.CapExGuidance.ValueLow = 2e9 },
			wantErr: ErrInvalidArtifact,
		},
		{
			name:    "unknown unit",
			mutate:  func(a *Artifact) { a.Extraction.CapExGuidance.Unit = "bushels" },
			wantErr: ErrInvalidArtifact,
		},
		{
			name:    "empty period",
			mutate:  func(a *Artifact) { a.Extraction.CapExGuidance.Period = "" },
			wantErr: ErrInvalidArtifact,
		},
		{
			name:    "numeric envelope missing evidence",
			mutate:  func(a *Artifact) { a.Extraction.CapExGuidance.Evidence = nil },
			wantErr: ErrInvalidArtifact,
		},
		{
			name:    "evidence with empty quote",
			mutate:  func(a *Artifact) { a.Extraction.CapExGuidance.Evidence = []Evidence{{Quote: "", Location: "x"}} },
			wantErr: ErrInvalidArtifact,
		},
		{
			name: "margin envelope missing gaap basis",
			mutate: func(a *Artifact) {
				a.Extraction.CapExGuidance = nil
				a.Extraction.MarginGuidance = []Envelope{{
					ValueLow: 0.30, ValueHigh: 0.32, Unit: UnitPct, Period: "FY2026",
					Confidence: 0.8,
					Evidence:   []Evidence{{Quote: "gross margin ~31%", Location: "Item 7"}},
					// Basis omitted → must fail.
				}}
			},
			wantErr: ErrInvalidArtifact,
		},
		{
			name: "margin envelope with gaap basis is valid",
			mutate: func(a *Artifact) {
				a.Extraction.CapExGuidance = nil
				a.Extraction.MarginGuidance = []Envelope{{
					ValueLow: 0.30, ValueHigh: 0.32, Unit: UnitPct, Period: "FY2026",
					Basis:      &Basis{GAAPOrNonGAAP: "non_gaap"},
					Confidence: 0.8,
					Evidence:   []Evidence{{Quote: "gross margin ~31%", Location: "Item 7"}},
				}}
			},
			wantErr: nil,
		},
		{
			name: "absent record must not carry extraction",
			mutate: func(a *Artifact) {
				*a = *absentArtifact()
				a.Extraction = &Extraction{CapExGuidance: &Envelope{
					ValueLow: 1, ValueHigh: 2, Unit: UnitAbsoluteUSD, Period: "FY2026",
					Confidence: 0.5, Evidence: []Evidence{{Quote: "q", Location: "l"}},
				}}
			},
			wantErr: ErrInvalidArtifact,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := validCapExArtifact()
			tc.mutate(a)
			err := ValidateStructural(a)
			if tc.wantErr == nil {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.True(t, errors.Is(err, tc.wantErr), "want errors.Is(%v), got %v", tc.wantErr, err)
		})
	}
}

func TestValidateStructural_NilArtifact(t *testing.T) {
	err := ValidateStructural(nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidArtifact))
}
