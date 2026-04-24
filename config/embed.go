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

//go:embed industry_multiples.json datacleaner
var fs embed.FS

// Read returns the contents of a file packaged into the binary. The path is
// relative to the repo's config/ directory, e.g. "industry_multiples.json"
// or "datacleaner/industry_codes.json".
func Read(path string) ([]byte, error) {
	return fs.ReadFile(path)
}
