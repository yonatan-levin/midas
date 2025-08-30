Param(
  [string]$ApiBase = "http://localhost:8080",
  [string]$DbPath = "./data/midas.db"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

Write-Host "Applying migrations..."
go run ./cmd/migrate -db $DbPath | Out-Null

Write-Host "Starting server..."
$job = Start-Job -ScriptBlock {
  $env:ENABLE_SWAGGER = 'true'
  $env:ENABLE_PPROF   = 'true'
  $env:DATABASE_DRIVER = 'sqlite3'
  $env:DATABASE_PATH   = $using:DbPath
  go run ./cmd/server
}

try {
  for ($i=0; $i -lt 30; $i++) {
    try { Invoke-RestMethod -Method GET -Uri "$ApiBase/health" -TimeoutSec 2 | Out-Null; break } catch { Start-Sleep -Seconds 1 }
  }
  if ($i -ge 30) { throw "Server did not become healthy" }

  $DEMO_KEY = 'dcf_demo_3a4a5b6c7d8e9f00112233445566778899aabbccddeeff001122334455667788'

  function Try-Request([string]$Method, [string]$Url, [hashtable]$Headers=@{}, [string]$BodyJson=$null) {
    try {
      if ($BodyJson) {
        $resp = Invoke-RestMethod -Method $Method -Uri $Url -Headers $Headers -Body $BodyJson -ContentType 'application/json' -TimeoutSec 10
        return @{ status=200; body=$resp }
      } else {
        $resp = Invoke-RestMethod -Method $Method -Uri $Url -Headers $Headers -TimeoutSec 10
        return @{ status=200; body=$resp }
      }
    } catch {
      $code = 0
      if ($_.Exception.Response -and $_.Exception.Response.StatusCode) { $code = [int]$_.Exception.Response.StatusCode }
      return @{ status=$code; body=$null; error=$_.Exception.Message }
    }
  }

  Write-Host "QA: GET /health"
  $health = Try-Request GET "$ApiBase/health"
  Write-Host ("Status: {0}" -f $health.status)

  Write-Host "QA: GET /docs/openapi.yaml"
  $openapi = Try-Request GET "$ApiBase/docs/openapi.yaml"
  Write-Host ("Status: {0}" -f $openapi.status)

  Write-Host "QA: GET /swagger/index.html"
  $swagger = Try-Request GET "$ApiBase/swagger/index.html"
  Write-Host ("Status: {0}" -f $swagger.status)

  Write-Host "QA: GET /api/v1/fair-value/AAPL without key (expect 401)"
  $noauth = Try-Request GET "$ApiBase/api/v1/fair-value/AAPL"
  Write-Host ("Status: {0}" -f $noauth.status)

  Write-Host "QA: GET /api/v1/fair-value/AAPL with demo key"
  $fv = Try-Request GET "$ApiBase/api/v1/fair-value/AAPL" @{ 'X-API-Key'=$DEMO_KEY }
  Write-Host ("Status: {0}" -f $fv.status)
  if ($fv.body) { Write-Host ("DCF: {0}, Tangible: {1}" -f $fv.body.dcf_value_per_share, $fv.body.tangible_value_per_share) }

  Write-Host "QA: POST /api/v1/fair-value/bulk with demo key"
  $bulkBody = '{"tickers":["AAPL","MSFT","GOOGL"]}'
  $bulk = Try-Request POST "$ApiBase/api/v1/fair-value/bulk" @{ 'X-API-Key'=$DEMO_KEY } $bulkBody
  Write-Host ("Status: {0}" -f $bulk.status)
  if ($bulk.body) { Write-Host ("Results: {0}" -f $bulk.body.results.Count) }

  Write-Host "QA: GET /api/v1/health/detailed with demo key (expect 403)"
  $hd = Try-Request GET "$ApiBase/api/v1/health/detailed" @{ 'X-API-Key'=$DEMO_KEY }
  Write-Host ("Status: {0}" -f $hd.status)

  Write-Host "QA: POST /api/v1/auth/keys with demo key (expect 403)"
  $keyBody = '{"user_id":"qa","permissions":["read:fair_value"]}'
  $k = Try-Request POST "$ApiBase/api/v1/auth/keys" @{ 'X-API-Key'=$DEMO_KEY } $keyBody
  Write-Host ("Status: {0}" -f $k.status)

  Write-Host "QA: GET /metrics (public)"
  $metrics = Try-Request GET "$ApiBase/metrics"
  Write-Host ("Status: {0}" -f $metrics.status)

} finally {
  try { Stop-Job $job -Force | Out-Null } catch {}
  try { Receive-Job $job -Keep | Out-Null } catch {}
}

Write-Host "QA run complete."


