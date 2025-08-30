Param(
  [Parameter(Mandatory=$true)] [string]$DemoKey,
  [string]$ApiBase = "http://localhost:8080",
  [string]$DbPath = "./data/midas.db",
  [switch]$InstallSchemathesis
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Apply schema & migrations first
go run ./cmd/migrate -db $DbPath | Out-Null

# Start server in a background job with required env vars
$job = Start-Job -ScriptBlock {
  $env:ENABLE_SWAGGER = 'true'
  $env:DATABASE_DRIVER  = 'sqlite3'
  $env:DATABASE_PATH  = $using:DbPath
  go run ./cmd/server
}

try {
  # Wait for health
  for ($i = 0; $i -lt 30; $i++) {
    try {
      Invoke-RestMethod -Method GET -Uri "$ApiBase/health" -TimeoutSec 2 | Out-Null
      break
    } catch {
      Start-Sleep -Seconds 1
    }
  }
  if ($i -ge 30) { throw "Server did not become healthy" }

  # Ensure Schemathesis installed if requested
  if ($InstallSchemathesis) {
    if (Get-Command python -ErrorAction SilentlyContinue) {
      python -m pip install --user --upgrade schemathesis | Out-Null
    }
  }

  # Run Schemathesis using python -m for path-agnostic execution
  $openapi = "$ApiBase/docs/openapi.yaml"
  try {
    if (Get-Command python -ErrorAction SilentlyContinue) {
      python -m schemathesis.cli run $openapi --checks all --header "X-API-Key: $DemoKey"
    } elseif (Get-Command schemathesis -ErrorAction SilentlyContinue) {
      schemathesis run $openapi --checks all --header "X-API-Key: $DemoKey"
    } else {
      Write-Warning "Schemathesis not installed and Python not found; skipping fuzz run."
    }
  } catch {
    Write-Warning "Schemathesis run failed: $($_.Exception.Message)"
  }

  # Smoke: fair-value for AAPL
  $resp = Invoke-RestMethod -Method GET -Uri "$ApiBase/api/v1/fair-value/AAPL" -Headers @{ 'X-API-Key' = $DemoKey }
  $json = $resp | ConvertTo-Json -Depth 8
  Write-Host $json
}
finally {
  try { Stop-Job $job -Force | Out-Null } catch {}
  try { Receive-Job $job -Keep | Out-Null } catch {}
}


