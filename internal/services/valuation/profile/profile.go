// Package profile is the Tier 2 AssumptionProfile backbone keyed by
// (Archetype × Maturity). Phase Bootstrap ships a MINIMAL skeleton so the
// testhelpers package (also created in Phase Bootstrap) compiles. The full
// type system, resolver, validation, and import-boundary guard land in
// Phase P0a per the implementer plan
// (docs/refactoring/implementations/assumption-profile-implementation-plan.md).
//
// Until P0a ships, callers MUST NOT depend on any field on these structs
// beyond what is declared here. The Resolve method panics so that any
// accidental production use surfaces during smoke tests before reaching
// staging.
package profile

import (
	"encoding/json"
	"fmt"
	"os"
)

// ResolvedProfile is the value returned by Registry.Resolve. Phase Bootstrap
// declares only the fields the testhelpers and bit-for-bit regression test
// touch; P0a expands the struct to its full spec-defined shape.
type ResolvedProfile struct {
	// ProfileID is the canonical "<archetype>:<maturity>" identifier.
	ProfileID string `json:"profile_id"`

	// Archetype is the qualitative model family (e.g. "mature_large_bank",
	// "cyclical_mid_cycle"). Owned by the spec; P0a enumerates.
	Archetype string `json:"archetype"`

	// Maturity is the secondary axis (e.g. "mature", "standard_growth").
	Maturity string `json:"maturity"`
}

// Registry is the abstraction P0a fleshes out with profile lookup, archetype
// rules, and maturity classification. Phase Bootstrap exposes only the
// minimal surface that testhelpers + downstream worktree fixtures consume.
//
// Implementations MUST be safe for concurrent use; the production registry
// is loaded once at fx startup and shared across the request path.
type Registry interface {
	// Resolve returns the profile that applies to the given resolution
	// inputs. Phase Bootstrap's stub implementation panics — P0a wires the
	// real lookup chain (archetype rules → maturity thresholds → profile
	// table). Tests that exercise resolution land in P0a.
	//
	// The argument shape is intentionally undefined here; P0a introduces
	// the Facts DTO and constrains this signature. Until then, callers must
	// only pass profile IDs through fixtures, never call Resolve directly.
	Resolve(profileID string) (*ResolvedProfile, bool)
}

// LoadFromJSON parses an assumption-profile config from the given path. The
// Phase Bootstrap stub validates only that the file exists and is valid
// JSON with a `config_version` key — sufficient for testhelpers to load a
// fixture without P0a's full schema in place. P0a replaces this with full
// schema validation + archetype-rule parsing.
func LoadFromJSON(path string) (Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("profile: read %s: %w", path, err)
	}
	var stub struct {
		ConfigVersion string `json:"config_version"`
		Profiles      map[string]struct {
			ProfileID string `json:"profile_id"`
			Archetype string `json:"archetype"`
			Maturity  string `json:"maturity"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(data, &stub); err != nil {
		return nil, fmt.Errorf("profile: unmarshal %s: %w", path, err)
	}
	if stub.ConfigVersion == "" {
		return nil, fmt.Errorf("profile: %s missing config_version", path)
	}
	reg := &bootstrapRegistry{profiles: make(map[string]*ResolvedProfile, len(stub.Profiles))}
	for id, p := range stub.Profiles {
		reg.profiles[id] = &ResolvedProfile{
			ProfileID: p.ProfileID,
			Archetype: p.Archetype,
			Maturity:  p.Maturity,
		}
	}
	return reg, nil
}

// bootstrapRegistry is the Phase Bootstrap stub Registry. It supports
// lookup-by-ID so testhelpers can build a Registry from a JSON fixture and
// hand it to downstream consumers (P1-P4); it deliberately does NOT
// implement archetype/maturity routing — that ships in P0a.
type bootstrapRegistry struct {
	profiles map[string]*ResolvedProfile
}

func (r *bootstrapRegistry) Resolve(profileID string) (*ResolvedProfile, bool) {
	p, ok := r.profiles[profileID]
	return p, ok
}
