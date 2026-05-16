package profile

// ResolverVersion is the semver of the resolver logic itself. Bumped on any
// change to the resolver algorithm (Stage 1/2/3 logic, override rules, etc.).
// Stamps onto ResolutionTrace and AssumptionProfileManifest for replay
// determinism per spec §7.3.
//
// Distinct from ConfigVersion, which tracks the on-disk assumption_profiles.json
// schema. Resolver-version drift between capture and replay is the signal
// that the algorithm itself changed, not just the calibration data.
const ResolverVersion = "1.0.0"
