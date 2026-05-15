//go:build goldencapture

// Package models_test (build tag: goldencapture) — one-shot helper for
// regenerating the pre-Tier-2 DDM golden fixtures.
//
// This file is excluded from the default test build (`go test ./...`) so
// the capture helper never runs in CI by accident. Run explicitly with:
//
//	go test -tags goldencapture -run TestCaptureGoldenDDM ./internal/services/valuation/models/...
//
// Reads ModelInput fixtures at testdata/golden/<ticker>_ddm_pre_tier2_input.json,
// calls DDMModel.Calculate at the current master HEAD, and writes the
// resulting ModelResult JSON to testdata/golden/<ticker>_ddm_pre_tier2_output.json.
//
// The input fixtures themselves are checked-in artifacts (built once during
// Phase Bootstrap from live captured bundles + realistic public-record DPS
// values — see commit message for Phase Bootstrap for the synthetic-DPS
// rationale). Regenerating outputs is appropriate only after a DELIBERATE
// change to the DDM math.
package models_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/services/valuation/models"
)

func TestCaptureGoldenDDM(t *testing.T) {
	tickers := []string{"jpm", "bac", "wfc"}
	for _, ticker := range tickers {
		t.Run(ticker, func(t *testing.T) {
			inputPath := filepath.Join("testdata", "golden", ticker+"_ddm_pre_tier2_input.json")
			outputPath := filepath.Join("testdata", "golden", ticker+"_ddm_pre_tier2_output.json")
			data, err := os.ReadFile(inputPath)
			if err != nil {
				t.Fatalf("input fixture missing at %s: %v (manually create from bundle first)", inputPath, err)
			}
			var input models.ModelInput
			if err := json.Unmarshal(data, &input); err != nil {
				t.Fatalf("unmarshal input: %v", err)
			}
			ddm := models.NewDDMModel(zap.NewNop())
			result, err := ddm.Calculate(context.Background(), &input)
			if err != nil {
				t.Fatalf("DDM calculate: %v", err)
			}
			out, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				t.Fatalf("marshal output: %v", err)
			}
			if err := os.WriteFile(outputPath, out, 0o644); err != nil {
				t.Fatalf("write output: %v", err)
			}
			fmt.Printf("Captured golden for %s: %s (intrinsic=%.6f)\n",
				ticker, outputPath, result.IntrinsicValuePerShare)
		})
	}
}
