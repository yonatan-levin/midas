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
#   - internal/services/growth/estimator.go
#       1 singleton-logger call kept for back-compat with pre-Phase-M callers.
#       Full migration tracked in docs/reviewer/M1.
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
} elseif ($ExitCode -ne 1) {
    # rg exit 2 = error
    Write-Error "ripgrep encountered an error (exit $ExitCode):"
    $RgOutput | ForEach-Object { Write-Error "  $_" }
    exit 2
}
# rg exit 1 (no matches) → fall through to the second check.

# ----------------------------------------------------------------------------
# Phase 1 (observability narrative & artifacts spec §6) — Debug-tracer prefix.
# Any Debug("…", …) call inside request-path code must use the message
# convention: logger.Debug("trace.<area>.<op>", …).
# ----------------------------------------------------------------------------
Write-Host ""
Write-Host "lint-logs: scanning Debug() messages for trace.<area>.<op> prefix..."
$DebugPattern = '\.Debug\("(?!trace\.)'

# Whitelist of files containing legacy Debug calls predating the convention.
$DebugWhitelist = @(
    "!internal/services/scheduler/**",
    "!internal/services/auth/**",
    "!internal/services/watchlist/**",
    "!internal/services/alerting/**",
    "!internal/services/metrics/**",
    "!internal/services/ratelimit/**",
    "!internal/services/datacleaner/ai/**",
    "!internal/services/growth/estimator.go",
    "!internal/services/valuation/service.go",
    "!internal/services/valuation/models/**",
    "!internal/infra/gateways/**"
)
$DebugGlobArgs = $DebugWhitelist | ForEach-Object { "--glob"; $_ }

$DebugOutput = & rg `
    --no-heading `
    --line-number `
    --color never `
    --type go `
    --pcre2 `
    @DebugGlobArgs `
    $DebugPattern `
    @ScanDirs 2>&1
$DebugExit = $LASTEXITCODE

if ($DebugExit -eq 0) {
    Write-Host "FAIL: Found Debug() calls in request-path code missing trace.<area>.<op> prefix:"
    Write-Host ""
    $DebugOutput | ForEach-Object { Write-Host "  $_" }
    Write-Host ""
    Write-Host "Fix: change message to 'trace.<area>.<op>' (e.g. trace.gateway.sec.fetch)"
    Write-Host "     or whitelist the file in scripts/lint-logs.ps1."
    exit 1
} elseif ($DebugExit -eq 1) {
    Write-Host "OK: All non-whitelisted Debug() calls follow trace.<area>.<op> prefix."
} else {
    Write-Error "ripgrep PCRE2 check failed (exit $DebugExit):"
    $DebugOutput | ForEach-Object { Write-Error "  $_" }
    exit 2
}

Write-Host "OK: No unguarded singleton logger calls found in request-path code."
exit 0
