// Package configfs exposes the repo's config/ directory as an embed.FS so
// production code never depends on the process working directory. Tests that
// need a custom override construct objects directly via With-style constructors
// — they do not go through this FS.
//
// The package lives at `config/` (not `config/configfs/`) because go:embed
// patterns cannot traverse upward with "..", and the target files
// (industry_multiples.json, datacleaner/) are siblings of this file, not
// children of any subdirectory.
package configfs

import "embed"

// Tier 2 P0b: assumption_profiles.json is embedded so the AssumptionProfile
// registry loads regardless of process cwd (integration tests, scheduler-
// background jobs, etc.). Production reads the same bytes as the file the
// binary was built with — this avoids the "wrong cwd → can't find config"
// failure mode that bit early replay builds (RPL-2k).
//
// RM-2 Phase 2: damodaran_sector_multiples.json (Damodaran NYU-Stern annual
// EV/Sales table) + sic_to_damodaran.json (SIC → Damodaran-industry crosswalk)
// are embedded so the revenue_multiple model can resolve a SIC-driven sector
// multiple regardless of process cwd, identical to industry_multiples.json. A
// file omitted from this directive is unreadable in production/replay.
//
//go:embed industry_multiples.json datacleaner assumption_profiles.json damodaran_sector_multiples.json sic_to_damodaran.json
var fs embed.FS

// Read returns the contents of a file packaged into the binary. The path is
// relative to the repo's config/ directory, e.g. "industry_multiples.json"
// or "datacleaner/industry_codes.json".
func Read(path string) ([]byte, error) {
	return fs.ReadFile(path)
}
