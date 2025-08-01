# DCF Valuation API - Stop Staging Environment (PowerShell)
# Phase 2.5: MVP End-to-End Validation

# Set error action preference
$ErrorActionPreference = "Stop"

# Colors for output
$Green = "Green"
$Yellow = "Yellow"
$Red = "Red"

Write-Host "Stopping DCF Valuation API staging environment..." -ForegroundColor $Yellow

# Stop Docker services
Write-Host "Stopping Docker services..." -ForegroundColor $Yellow
docker-compose down

# Kill any running API processes
Write-Host "Stopping API processes..." -ForegroundColor $Yellow
Get-Process -Name "dcf-api" -ErrorAction SilentlyContinue | Stop-Process -Force

# Remove PID file if it exists
if (Test-Path ".api.pid") {
    Remove-Item ".api.pid" -Force
}

Write-Host "✓ Staging environment stopped" -ForegroundColor $Green
Write-Host ""
Write-Host "To restart, run: .\scripts\launch_staging.ps1"
Write-Host "" 