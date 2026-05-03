package replay

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeManifest is a small test helper that writes a JSON object as
// 00-manifest.json under dir. Used to drive ReadManifest table tests without
// importing the artifact-writer machinery (which would be circular).
func writeManifest(t *testing.T, dir string, body map[string]any) string {
	t.Helper()
	path := filepath.Join(dir, ManifestFileName)
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal test manifest: %v", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write test manifest %s: %v", path, err)
	}
	return path
}

// validBaseManifest returns the minimum fields ReadManifest accepts.
// Tests mutate or drop fields to drive the negative cases.
func validBaseManifest() map[string]any {
	return map[string]any{
		"bundle_version":     "1.0",
		"request_id":         "01HW8ZQXKRTEST",
		"ticker":             "AAPL",
		"started_at":         "2026-04-25T12:34:56Z",
		"trigger":            "header",
		"outcome":            "ok",
		"phases_recorded":    []any{},
		"redactions_applied": []any{},
		"schema_versions": map[string]any{
			"FinancialData": 7,
		},
	}
}

func TestReadManifest_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, dir, validBaseManifest())

	mf, err := ReadManifest(dir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if mf.BundleVersion != "1.0" {
		t.Errorf("BundleVersion = %q, want 1.0", mf.BundleVersion)
	}
	if mf.RequestID != "01HW8ZQXKRTEST" {
		t.Errorf("RequestID = %q", mf.RequestID)
	}
	if mf.Ticker != "AAPL" {
		t.Errorf("Ticker = %q", mf.Ticker)
	}
	if mf.SchemaVersions["FinancialData"] != 7 {
		t.Errorf("SchemaVersions[FinancialData] = %d, want 7", mf.SchemaVersions["FinancialData"])
	}
}

func TestReadManifest_OptionalNotesField(t *testing.T) {
	dir := t.TempDir()
	body := validBaseManifest()
	body["notes"] = "write_failures=3 queue_drops=1"
	body["finished_at"] = "2026-04-25T12:35:01Z"
	body["build_version"] = "v0.9.0-rc1"
	writeManifest(t, dir, body)

	mf, err := ReadManifest(dir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if mf.Notes != "write_failures=3 queue_drops=1" {
		t.Errorf("Notes = %q", mf.Notes)
	}
	if mf.FinishedAt == "" {
		t.Errorf("FinishedAt should be populated")
	}
	if mf.BuildVersion != "v0.9.0-rc1" {
		t.Errorf("BuildVersion = %q", mf.BuildVersion)
	}
}

func TestReadManifest_MissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadManifest(dir)
	if err == nil {
		t.Fatal("ReadManifest of empty dir should fail")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("error should wrap os.ErrNotExist; got: %v", err)
	}
}

func TestReadManifest_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ManifestFileName)
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := ReadManifest(dir)
	if err == nil {
		t.Fatal("ReadManifest of malformed JSON should fail")
	}
	if errors.Is(err, ErrUnsupportedBundleVersion) {
		t.Errorf("unrelated error: %v", err)
	}
}

func TestReadManifest_RejectsUnsupportedVersion(t *testing.T) {
	dir := t.TempDir()
	body := validBaseManifest()
	body["bundle_version"] = "2.0"
	writeManifest(t, dir, body)
	_, err := ReadManifest(dir)
	if err == nil {
		t.Fatal("ReadManifest should reject 2.0 until parser is updated")
	}
	if !errors.Is(err, ErrUnsupportedBundleVersion) {
		t.Errorf("error should match ErrUnsupportedBundleVersion; got: %v", err)
	}
}

func TestReadManifest_RejectsMissingRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		drop string // field to remove
	}{
		{"missing_bundle_version", "bundle_version"},
		{"missing_request_id", "request_id"},
		{"missing_ticker", "ticker"},
		{"missing_started_at", "started_at"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			body := validBaseManifest()
			delete(body, tt.drop)
			writeManifest(t, dir, body)
			_, err := ReadManifest(dir)
			if err == nil {
				t.Fatalf("ReadManifest should reject manifest missing %q", tt.drop)
			}
		})
	}
}
