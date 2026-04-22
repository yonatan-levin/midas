# lint-logs.ps1 — Phase S observability CI guard (Windows)
#
# Fails (exit 1) if any request-path service or gateway file still uses a
# singleton receiver-logger call (e.g. s.logger.Info, c.logger.Warn) where
# logctx.Or(ctx, ...) should be used instead.
#
# Scan scope: internal/services/**, internal/infra/gateways/**, internal/api/v1/handlers/**
#
# Whitelist (dirs/files intentionally skipped — future migration phases or
# singleton-only contexts):
#   - internal/services/scheduler/          (background jobs: always singleton)
#   - internal/services/auth/               (Phase T migration)
#   - internal/services/watchlist/          (Phase T migration)
#   - internal/services/alerting/           (Phase T migration)
#   - internal/services/metrics/            (Phase T migration)
#   - internal/services/ratelimit/          (Phase T migration)
#   - internal/services/datacleaner/ai/     (Phase T migration)
#   - internal/services/valuation/models/router.go
#       SelectModel has no ctx parameter — tracked concern for Phase M
#   - internal/services/growth/estimator.go
#       EstimateGrowthRates has no ctx parameter — tracked concern for Phase M
#
# Usage: .\scripts\lint-logs.ps1
#        (run from repo root)

$ErrorActionPreference = "Continue"

# Repo root: the directory containing this script's parent
$RepoRoot = Split-Path -Parent $PSScriptRoot

# Verify rg (ripgrep) is available
if (-not (Get-Command rg -ErrorAction SilentlyContinue)) {
    Write-Error "ripgrep (rg) is required but not found. Install via: choco install ripgrep"
    exit 1
}

# Pattern: receiver-scoped logger calls using single-letter variables in (s,c,r,e,g,h)
# This matches the primary request-path service receivers used in this codebase.
$Pattern = '(s|c|r|e|g|h)\.logger\.(Info|Warn|Error|Debug)\('

# Directories to scan
$ScanDirs = @(
    "internal/services",
    "internal/infra/gateways",
    "internal/api/v1/handlers"
)

# Glob patterns to exclude from scanning (whitelisted paths).
# ripgrep --glob '!pattern' excludes matching paths.
$Whitelist = @(
    "!internal/services/scheduler/**",
    "!internal/services/auth/**",
    "!internal/services/watchlist/**",
    "!internal/services/alerting/**",
    "!internal/services/metrics/**",
    "!internal/services/ratelimit/**",
    "!internal/services/datacleaner/ai/**",
    "!internal/services/valuation/models/router.go",
    "!internal/services/growth/estimator.go"
)

# Build rg glob args
$GlobArgs = $Whitelist | ForEach-Object { "--glob"; $_ }

Write-Host "lint-logs: scanning for unguarded singleton logger calls..."
Write-Host "  Pattern : $Pattern"
Write-Host "  Dirs    : $($ScanDirs -join ', ')"
Write-Host ""

# Run ripgrep
$RgOutput = & rg `
    --no-heading `
    --line-number `
    --color never `
    --type go `
    @GlobArgs `
    $Pattern `
    @ScanDirs 2>&1

$ExitCode = $LASTEXITCODE

if ($ExitCode -eq 0) {
    # rg exit 0 = matches found
    Write-Host "FAIL: Found unguarded singleton logger calls that should use logctx.Or(ctx, ...):"
    Write-Host ""
    $RgOutput | ForEach-Object { Write-Host "  $_" }
    Write-Host ""
    Write-Host "Fix: replace <receiver>.logger.<Level>(...) with logctx.Or(ctx, <receiver>.logger).<Level>(...)"
    Write-Host "     or add the file to the whitelist in scripts/lint-logs.ps1 if it is a background-only path."
    exit 1
} elseif ($ExitCode -eq 1) {
    # rg exit 1 = no matches found
    Write-Host "OK: No unguarded singleton logger calls found in request-path code."
    exit 0
} else {
    # rg exit 2 = error
    Write-Error "ripgrep encountered an error (exit $ExitCode):"
    $RgOutput | ForEach-Object { Write-Error "  $_" }
    exit 2
}
