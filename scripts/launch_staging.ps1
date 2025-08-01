# DCF Valuation API - Local Staging Launch Script (PowerShell)
# Phase 2.5: MVP End-to-End Validation
# Created: 2025-01-28

# Set error action preference
$ErrorActionPreference = "Stop"

# Colors for output
$Green = "Green"
$Yellow = "Yellow"
$Red = "Red"

Write-Host "DCF Valuation API - Local Staging Environment" -ForegroundColor $Green
Write-Host "============================================" -ForegroundColor $Green
Write-Host ""

# Check if .env exists, if not create from example
if (-not (Test-Path ".env")) {
    Write-Host "Creating .env file from config.env.example..." -ForegroundColor $Yellow
    Copy-Item "config.env.example" ".env"
    
    # Update specific values for local staging
    $envContent = Get-Content ".env" -Raw
    $envContent = $envContent -replace "ENV=development", "ENV=staging"
    $envContent = $envContent -replace "CACHE_TYPE=memory", "CACHE_TYPE=redis"
    
    # Add demo API key for testing
    $envContent += "`n# Demo API key for Phase 2.5 testing`n"
    $envContent += "DEMO_API_KEY=demo-key-phase-2.5-mvp`n"
    
    Set-Content ".env" $envContent
    
    Write-Host "✓ .env file created" -ForegroundColor $Green
} else {
    Write-Host "✓ Using existing .env file" -ForegroundColor $Green
}

# Check if Docker is running
try {
    docker info | Out-Null
} catch {
    Write-Host "Error: Docker is not running. Please start Docker first." -ForegroundColor $Red
    exit 1
}

# Use local docker-compose for staging
$COMPOSE_FILE = "docker-compose.yml"

Write-Host ""
Write-Host "Starting services..." -ForegroundColor $Yellow

# Stop any existing containers
docker-compose -f $COMPOSE_FILE down 2>$null

# Start services
docker-compose -f $COMPOSE_FILE up -d

# Wait for services to be ready
Write-Host ""
Write-Host "Waiting for services to be ready..." -ForegroundColor $Yellow

# Function to check service health
function Test-ServiceHealth {
    param(
        [string]$ServiceName,
        [int]$Port,
        [int]$MaxAttempts = 30
    )
    
    $attempt = 0
    while ($attempt -lt $MaxAttempts) {
        try {
            $tcpClient = New-Object System.Net.Sockets.TcpClient
            $tcpClient.Connect("localhost", $Port)
            $tcpClient.Close()
            Write-Host "✓ $ServiceName is ready" -ForegroundColor $Green
            return $true
        } catch {
            $attempt++
            Start-Sleep -Seconds 1
        }
    }
    
    Write-Host "✗ $ServiceName failed to start" -ForegroundColor $Red
    return $false
}

# Check Redis
Test-ServiceHealth "Redis" 6379

# Build and run the application
Write-Host ""
Write-Host "Building application..." -ForegroundColor $Yellow
go build -o ./bin/dcf-api ./cmd/server/main.go

# Create data directory if it doesn't exist
if (-not (Test-Path "./data")) {
    New-Item -ItemType Directory -Path "./data" -Force | Out-Null
}

# Start the application
Write-Host ""
Write-Host "Starting DCF Valuation API..." -ForegroundColor $Yellow
Start-Process -FilePath "./bin/dcf-api" -NoNewWindow -PassThru | Out-Null

# Wait for API to be ready
Start-Sleep -Seconds 3
Test-ServiceHealth "DCF API" 8080

# Test health endpoint
Write-Host ""
Write-Host "Testing health endpoint..." -ForegroundColor $Yellow
try {
    $healthResponse = Invoke-RestMethod -Uri "http://localhost:8080/health" -Method Get
    Write-Host "✓ Health check passed" -ForegroundColor $Green
    Write-Host "Response: $($healthResponse | ConvertTo-Json)"
} catch {
    Write-Host "✗ Health check failed" -ForegroundColor $Red
    Write-Host "Error: $($_.Exception.Message)"
}

Write-Host ""
Write-Host "============================================" -ForegroundColor $Green
Write-Host "Local staging environment is ready!" -ForegroundColor $Green
Write-Host ""
Write-Host "API URL: http://localhost:8080"
Write-Host "API Docs: http://localhost:8080/swagger/index.html"
Write-Host "Metrics: http://localhost:9090/metrics"
Write-Host ""
Write-Host "Demo API Key: demo-key-phase-2.5-mvp"
Write-Host ""
Write-Host "Example requests:"
Write-Host "  Invoke-RestMethod -Uri 'http://localhost:8080/api/v1/fair-value/AAPL' -Headers @{'Authorization'='Bearer demo-key-phase-2.5-mvp'}"
Write-Host ""
Write-Host "To stop the services, run: .\scripts\stop_staging.ps1"
Write-Host "" 