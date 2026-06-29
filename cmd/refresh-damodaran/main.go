// Command refresh-damodaran fetches Aswath Damodaran's NYU-Stern annual sector
// table (psdata.xls, "Price and Value to Sales") and regenerates the committed
// config/damodaran_sector_multiples.json used by the revenue_multiple valuation
// model (RM-2 Phase 2).
//
// It is an OFFLINE operator tool — NOT imported by cmd/server and not on the
// request path. The only consumer of github.com/extrame/xls in the repo.
//
// Usage:
//
//	go run ./cmd/refresh-damodaran -ua "midas-dcf/refresh-damodaran (you@example.com)"
//
// The workbook shape is validated before any write: a Damodaran layout change
// (renamed sheet / moved columns) exits non-zero rather than writing garbage.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/extrame/xls"
)

const (
	defaultSourceURL = "https://pages.stern.nyu.edu/~adamodar/pc/datasets/psdata.xls"
	defaultUserAgent = "midas-dcf/refresh-damodaran (contact: set-via--ua-flag@example.com)"

	// Expected psdata.xls layout (verified for the 2026.01 file). The shape
	// guard fails loudly if any of these drift.
	expectedSheetName  = "Industry Averages"
	headerRowIndex     = 7 // 0-based; data begins at headerRowIndex+1
	colIndustryName    = 0
	colEVSales         = 4
	dateCellRow        = 0
	dateCellCol        = 1 // B1 — dataset date as a "YYYY.MM" string
	expectedHeaderName = "Industry Name"
	expectedHeaderEV   = "EV/Sales"

	// evSalesDecimals matches the precision of the committed table so the tool
	// deterministically regenerates the same JSON (the raw workbook carries
	// full float64 precision, which is noise below the 4th decimal).
	evSalesDecimals = 4
)

// parseDatasetDate decodes the workbook's "Date updated:" cell. The real file
// carries a "YYYY.MM" string (e.g. "2026.01"), NOT an Excel serial as the
// original plan assumed — the day component is not present in the source, so we
// canonicalize to the first of the month → "YYYY-MM-01".
func parseDatasetDate(cell string) (string, error) {
	cell = strings.TrimSpace(cell)
	t, err := time.Parse("2006.01", cell)
	if err != nil {
		return "", fmt.Errorf("date cell %q is not in YYYY.MM form: %w", cell, err)
	}
	return t.Format("2006-01-02"), nil
}

// sectorMultiplesConfig is the on-disk shape of damodaran_sector_multiples.json.
type sectorMultiplesConfig struct {
	DatasetDate string             `json:"dataset_date"`
	SourceURL   string             `json:"source_url"`
	Industries  map[string]float64 `json:"industries"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "refresh-damodaran: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ua := flag.String("ua", defaultUserAgent, "User-Agent header (include a contact email, SEC-style)")
	sourceURL := flag.String("url", defaultSourceURL, "psdata.xls source URL")
	outConfig := flag.String("out", filepath.Join("config", "damodaran_sector_multiples.json"), "output JSON config path")
	dataDir := flag.String("data-dir", "data", "root directory for the local raw .xls snapshot (gitignored)")
	flag.Parse()

	raw, err := fetch(*sourceURL, *ua)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}

	wb, err := xls.OpenReader(bytes.NewReader(raw), "utf-8")
	if err != nil {
		return fmt.Errorf("open workbook: %w", err)
	}
	if wb == nil {
		return fmt.Errorf("open workbook: nil workbook (corrupt or non-BIFF8 file)")
	}

	sheet, err := findSheet(wb, expectedSheetName)
	if err != nil {
		return err
	}

	datasetDate, err := readDatasetDate(sheet)
	if err != nil {
		return fmt.Errorf("read dataset date: %w", err)
	}

	if err := verifyHeader(sheet); err != nil {
		return fmt.Errorf("workbook shape guard: %w", err)
	}

	industries, err := parseIndustries(sheet)
	if err != nil {
		return err
	}
	if len(industries) == 0 {
		return fmt.Errorf("no industry rows parsed — refusing to write an empty table")
	}

	// Snapshot the raw .xls under data/damodaran/<date>/ for local provenance
	// (data/ is gitignored). Best-effort: a snapshot failure must not block the
	// config regeneration, but we surface it.
	if err := snapshot(*dataDir, datasetDate, raw); err != nil {
		fmt.Fprintf(os.Stderr, "refresh-damodaran: warning: raw snapshot failed: %v\n", err)
	}

	cfg := sectorMultiplesConfig{
		DatasetDate: datasetDate,
		SourceURL:   *sourceURL,
		Industries:  industries,
	}
	if err := writeConfig(*outConfig, cfg); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	fmt.Printf("refresh-damodaran: wrote %d industries (dataset_date=%s) to %s\n",
		len(industries), datasetDate, *outConfig)
	fmt.Println("refresh-damodaran: NOTE — sic_to_damodaran.json is hand-maintained and was NOT touched.")
	fmt.Println("refresh-damodaran: run `go test ./internal/services/valuation/models/ -run Integrity` to catch any industry-name renames that orphan crosswalk entries.")
	return nil
}

func fetch(url, ua string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", ua)
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// findSheet returns the named worksheet or an error if it is absent (shape guard).
func findSheet(wb *xls.WorkBook, name string) (*xls.WorkSheet, error) {
	for i := 0; i < wb.NumSheets(); i++ {
		s := wb.GetSheet(i)
		if s != nil && s.Name == name {
			return s, nil
		}
	}
	return nil, fmt.Errorf("workbook shape guard: sheet %q not found (Damodaran layout may have changed)", name)
}

// readDatasetDate decodes the Excel serial in cell B1 to a YYYY-MM-DD string.
func readDatasetDate(sheet *xls.WorkSheet) (string, error) {
	row := sheet.Row(dateCellRow)
	if row == nil {
		return "", fmt.Errorf("date cell row %d is empty", dateCellRow)
	}
	cell := row.Col(dateCellCol)
	if strings.TrimSpace(cell) == "" {
		return "", fmt.Errorf("date cell B1 is empty")
	}
	return parseDatasetDate(cell)
}

// verifyHeader confirms the header row carries the expected column labels so a
// silent column re-order cannot corrupt the regenerated table.
func verifyHeader(sheet *xls.WorkSheet) error {
	row := sheet.Row(headerRowIndex)
	if row == nil {
		return fmt.Errorf("header row %d is empty", headerRowIndex)
	}
	name := strings.TrimSpace(row.Col(colIndustryName))
	ev := strings.TrimSpace(row.Col(colEVSales))
	if !strings.EqualFold(name, expectedHeaderName) {
		return fmt.Errorf("header col %d = %q, want %q", colIndustryName, name, expectedHeaderName)
	}
	if !strings.EqualFold(ev, expectedHeaderEV) {
		return fmt.Errorf("header col %d = %q, want %q", colEVSales, ev, expectedHeaderEV)
	}
	return nil
}

// parseIndustries reads industry-name -> EV/Sales from the data rows. Rows with
// an empty industry name or an unparseable EV/Sales cell are skipped.
func parseIndustries(sheet *xls.WorkSheet) (map[string]float64, error) {
	out := make(map[string]float64)
	for i := headerRowIndex + 1; i <= int(sheet.MaxRow); i++ {
		row := sheet.Row(i)
		if row == nil {
			continue
		}
		name := strings.TrimSpace(row.Col(colIndustryName))
		// Skip blanks and the aggregate "Total Market" / "Total Market (without
		// financials)" rows — they are not industries a SIC maps to.
		if name == "" || strings.HasPrefix(name, "Total Market") {
			continue
		}
		evStr := strings.TrimSpace(row.Col(colEVSales))
		ev, err := strconv.ParseFloat(evStr, 64)
		if err != nil {
			// A non-numeric EV/Sales cell (blank, "NA") is a soft skip; the
			// crosswalk only ever references industries we actually wrote.
			continue
		}
		out[name] = roundTo(ev, evSalesDecimals)
	}
	return out, nil
}

// roundTo rounds v to the given number of decimal places.
func roundTo(v float64, decimals int) float64 {
	pow := math.Pow(10, float64(decimals))
	return math.Round(v*pow) / pow
}

// snapshot writes the raw .xls under <dataDir>/damodaran/<date>/psdata.xls.
func snapshot(dataDir, date string, raw []byte) error {
	dir := filepath.Join(dataDir, "damodaran", date)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "psdata.xls"), raw, 0o644)
}

// writeConfig marshals the config with 2-space indentation and a trailing
// newline, matching the committed file's shape. encoding/json sorts the
// industries map keys alphabetically, so the output is deterministic.
func writeConfig(path string, cfg sectorMultiplesConfig) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}
