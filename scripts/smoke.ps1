Param(
  [string]$ApiBase = "http://localhost:8080",
  [string]$AdminKey,
  [string]$DemoKey
)

Set-StrictMode -Version Latest

function Invoke-Json([string]$Method, [string]$Url, [string]$Body = $null, [hashtable]$Headers = @{}) {
  $params = @{ Method = $Method; Uri = $Url; Headers = $Headers; ErrorAction = 'Stop' }
  if ($Body) { $params['Body'] = $Body; $params['ContentType'] = 'application/json' }
  Invoke-RestMethod @params
}

Write-Host "🔎 Health: $ApiBase/health"
Invoke-Json -Method GET -Url "$ApiBase/health" | ConvertTo-Json -Depth 6

if (-not $DemoKey) {
  if (-not $AdminKey) {
    Write-Error "ADMIN_KEY is required or provide -DemoKey. Use seed_demo_key.go to create one."
    exit 1
  }
  Write-Host "Creating demo API key via admin route..."
  $Payload = @{ user_id = 'demo'; permissions = @('read:fair_value') } | ConvertTo-Json
  $Resp = Invoke-Json -Method POST -Url "$ApiBase/api/v1/auth/keys" -Body $Payload -Headers @{ 'X-API-Key' = $AdminKey }
  $DemoKey = $Resp.key
}

Write-Host "Using DEMO_KEY $($DemoKey.Substring(0,8))..."

Write-Host "📈 GET /api/v1/fair-value/AAPL"
Invoke-Json -Method GET -Url "$ApiBase/api/v1/fair-value/AAPL" -Headers @{ 'X-API-Key' = $DemoKey } | ConvertTo-Json -Depth 6


