package ports

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFlexibleCIK_UnmarshalJSON guards SEC EDGAR's polymorphic CIK encoding:
// the same field arrives as a JSON number (e.g. 320193 for AAPL) or as a
// zero-padded quoted string (e.g. "0001729214" for XRTX). Before FlexibleCIK,
// the field was typed as json.Number and rejected the quoted form, which
// surfaced to users as a misleading HTTP 404 TICKER_NOT_FOUND for any affected
// ticker.
func TestFlexibleCIK_UnmarshalJSON(t *testing.T) {
	type wrapper struct {
		CIK FlexibleCIK `json:"cik"`
	}

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "quoted_zero_padded_string_xrtx",
			input: `{"cik":"0001729214"}`,
			want:  "0001729214",
		},
		{
			name:  "quoted_unpadded_string",
			input: `{"cik":"320193"}`,
			want:  "320193",
		},
		{
			name:  "unquoted_number_aapl",
			input: `{"cik":320193}`,
			want:  "320193",
		},
		{
			name:  "unquoted_zero_value",
			input: `{"cik":0}`,
			want:  "0",
		},
		{
			name:  "empty_string",
			input: `{"cik":""}`,
			want:  "",
		},
		{
			name:    "rejected_bool",
			input:   `{"cik":true}`,
			wantErr: true,
		},
		{
			name:    "rejected_object",
			input:   `{"cik":{"nested":1}}`,
			wantErr: true,
		},
		{
			name:    "rejected_array",
			input:   `{"cik":[1,2,3]}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var w wrapper
			err := json.Unmarshal([]byte(tt.input), &w)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, w.CIK.String())
			// Zero-value comparison with untyped string constant must still work
			assert.Equal(t, tt.want == "", w.CIK == "")
		})
	}
}

// TestFlexibleCIK_DecodesRealSECCompanyFactsShape confirms a realistic
// nested payload (the shape SEC actually returns for XRTX) decodes end-to-end
// without error — catching regressions if someone later reverts the type to
// json.Number. The facts map is intentionally populated only with "dei" and
// no "us-gaap"; the valuation layer's HasMinimumData gate must handle that
// case downstream (tested separately).
func TestFlexibleCIK_DecodesRealSECCompanyFactsShape(t *testing.T) {
	xrtxResponse := `{
		"cik": "0001729214",
		"entityName": "XORTX Therapeutics Inc.",
		"facts": {
			"dei": {
				"EntityCommonStockSharesOutstanding": {
					"label": "Entity Common Stock, Shares Outstanding",
					"description": "",
					"units": {
						"shares": [
							{"end": "2023-12-31", "val": 1000000, "accn": "x", "fy": 2023, "fp": "FY", "form": "20-F", "filed": "2024-03-15"}
						]
					}
				}
			}
		}
	}`

	var facts SECCompanyFacts
	err := json.Unmarshal([]byte(xrtxResponse), &facts)
	require.NoError(t, err, "quoted CIK payload must decode successfully; regression if this fails")
	assert.Equal(t, "0001729214", facts.CIK.String())
	assert.Equal(t, "XORTX Therapeutics Inc.", facts.EntityName)

	// DEI taxonomy present but no us-gaap — downstream valuation should
	// classify this as ErrInsufficientData via HasMinimumData(1) gate.
	_, hasDEI := facts.Facts["dei"]
	_, hasUSGAAP := facts.Facts["us-gaap"]
	assert.True(t, hasDEI, "dei taxonomy expected for foreign private issuer")
	assert.False(t, hasUSGAAP, "us-gaap taxonomy intentionally absent in this fixture")
}
