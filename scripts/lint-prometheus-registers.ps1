# lint-prometheus-registers.ps1 — R3 Stage I.0 (v2 Addition #1) CI guard (Windows)
#
# Fails (exit 1) if any Go file outside the allowlist registers Prometheus
# collectors against the process-global DefaultRegisterer (or a promauto
# constructor variant that uses it). The per-instance-registry pattern
# established by PREX-1 (internal/services/metrics/service.go) is the only
# acceptable shape; this lint prevents silent reintroduction of the global
# registry hazard.
#
# Hazards detected:
#   - prometheus.MustRegister(...)          — global side-effect registration
#   - prometheus.Register(...)              — same, returns error variant
#   - prometheus.DefaultRegisterer          — direct reference to the global
#   - promauto.NewCounter / NewGauge / ...  — promauto top-level constructors
#                                             default to DefaultRegisterer
#
# Safe (NOT flagged):
#   - registry.MustRegister(...)            — registers on a service-owned
#                                             *prometheus.Registry instance
#   - promauto.With(registry).NewXXX(...)   — explicit per-instance scope
#
# Exit codes:
#   0 — clean (no stray registrations outside allowlist)
#   1 — stray registration found
#   2 — ripgrep not installed
#
# Usage: .\scripts\lint-prometheus-registers.ps1
#        (run from repo root)

$ErrorActionPreference = "Continue"

# Verify rg (ripgrep) is available
if (-not (Get-Command rg -ErrorAction SilentlyContinue)) {
    Write-Error "ripgrep (rg) is required but not found. Install via: choco install ripgrep"
    exit 2
}

# Patterns to flag (regex alternation via |).
$Patterns = @(
    'prometheus\.MustRegister\(',
    'prometheus\.Register\(',
    'prometheus\.DefaultRegisterer',
    'promauto\.New[A-Z]'
)
$PatternOr = ($Patterns -join '|')

# Allowlist: literal paths (relative to repo root) where matches are acceptable.
#
# internal/observability/replay/module.go is allowlisted for an audit
# doc-comment only (Stage I.0): the comment block near line 309 references
# `prometheus.DefaultRegisterer` to explain why metrics.NewService allocates
# a fresh per-service registry instead. The replay module wires
# *metrics.Service via metrics.NewService — verified by the same Stage I.0
# audit — so allowlisting only documents the avoidance, never an actual
# global registration.
$Allowlist = @(
    'internal/services/metrics/service.go',
    'internal/services/metrics/service_test.go',
    'internal/observability/replay/module.go'
)

Write-Host "lint-prometheus-registers: scanning for stray DefaultRegisterer registrations..."

$RgOutput = & rg `
    --no-heading `
    --line-number `
    --color never `
    --type go `
    $PatternOr `
    . 2>&1

$ExitCode = $LASTEXITCODE

if ($ExitCode -eq 1) {
    Write-Host "OK: No prometheus DefaultRegisterer references found anywhere."
    exit 0
} elseif ($ExitCode -ne 0) {
    Write-Error "ripgrep encountered an error (exit $ExitCode):"
    $RgOutput | ForEach-Object { Write-Error "  $_" }
    exit 2
}

# Filter against allowlist. ripgrep output format on Windows uses backslash
# OR forward-slash depending on shell context — normalize before comparison.
$Stray = @()
foreach ($line in $RgOutput) {
    $lineStr = [string]$line
    if ([string]::IsNullOrWhiteSpace($lineStr)) { continue }
    # path is everything up to the first ':' that follows a non-drive-letter
    # character. On Windows rg might emit C:\path\file:line:match — handle it.
    $colonIdx = -1
    for ($i = 2; $i -lt $lineStr.Length; $i++) {
        if ($lineStr[$i] -eq ':') { $colonIdx = $i; break }
    }
    if ($colonIdx -lt 0) { continue }
    $path = $lineStr.Substring(0, $colonIdx)
    # Normalize: strip leading "./", convert backslashes to forward slashes.
    $path = $path -replace '^\.[\\/]', ''
    $path = $path -replace '\\', '/'
    if ($Allowlist -notcontains $path) {
        $Stray += $lineStr
    }
}

if ($Stray.Count -gt 0) {
    Write-Host "FAIL: Stray prometheus DefaultRegisterer references found outside allowlist:"
    Write-Host ""
    foreach ($s in $Stray) { Write-Host "  $s" }
    Write-Host ""
    Write-Host "Fix: register collectors on a service-owned *prometheus.Registry via"
    Write-Host "     promauto.With(registry).NewXXX(...) or registry.MustRegister(...)."
    Write-Host "     Avoid prometheus.DefaultRegisterer / promauto.NewXXX — they leak"
    Write-Host "     into the process-global registry and break per-instance hermeticity"
    Write-Host "     (the property R3 Stage I parallel replay relies on)."
    Write-Host ""
    Write-Host "     If a new path legitimately needs to be allowlisted, add it to the"
    Write-Host "     `$Allowlist array in this script with PR-level rationale."
    exit 1
}

Write-Host "OK: All prometheus references confined to allowlist."
exit 0
