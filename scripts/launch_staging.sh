#!/bin/bash
# DCF Valuation API - Local Staging Launch Script
# Phase 2.5: MVP End-to-End Validation
# Created: 2025-01-28

set -e  # Exit on error

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${GREEN}DCF Valuation API - Local Staging Environment${NC}"
echo -e "${GREEN}============================================${NC}"
echo ""

# Check if .env exists, if not create from example
if [ ! -f ".env" ]; then
    echo -e "${YELLOW}Creating .env file from config.env.example...${NC}"
    cp config.env.example .env
    
    # Update specific values for local staging
    sed -i 's/ENV=development/ENV=staging/g' .env 2>/dev/null || sed -i '' 's/ENV=development/ENV=staging/g' .env
    sed -i 's/CACHE_TYPE=memory/CACHE_TYPE=redis/g' .env 2>/dev/null || sed -i '' 's/CACHE_TYPE=memory/CACHE_TYPE=redis/g' .env
    
    # Add demo API key for testing
    echo "" >> .env
    echo "# Demo API key for Phase 2.5 testing" >> .env
    echo "DEMO_API_KEY=demo-key-phase-2.5-mvp" >> .env
    
    echo -e "${GREEN}✓ .env file created${NC}"
else
    echo -e "${GREEN}✓ Using existing .env file${NC}"
fi

# Export environment variables
export $(grep -v '^#' .env | xargs)

# Check if Docker is running
if ! docker info > /dev/null 2>&1; then
    echo -e "${RED}Error: Docker is not running. Please start Docker first.${NC}"
    exit 1
fi

# Use local docker-compose for staging (not production compose)
COMPOSE_FILE="docker-compose.yml"

echo ""
echo -e "${YELLOW}Starting services...${NC}"

# Stop any existing containers
docker-compose -f $COMPOSE_FILE down 2>/dev/null || true

# Start services
docker-compose -f $COMPOSE_FILE up -d

# Wait for services to be ready
echo ""
echo -e "${YELLOW}Waiting for services to be ready...${NC}"

# Function to check service health
check_service() {
    local service=$1
    local port=$2
    local max_attempts=30
    local attempt=0
    
    while [ $attempt -lt $max_attempts ]; do
        if nc -z localhost $port 2>/dev/null; then
            echo -e "${GREEN}✓ $service is ready${NC}"
            return 0
        fi
        attempt=$((attempt + 1))
        sleep 1
    done
    
    echo -e "${RED}✗ $service failed to start${NC}"
    return 1
}

# Check Redis
check_service "Redis" 6379

# Build and run the application
echo ""
echo -e "${YELLOW}Building application...${NC}"
go build -o ./bin/dcf-api ./cmd/server/main.go

# Run database migrations
echo ""
echo -e "${YELLOW}Running database migrations...${NC}"
# TODO: Add migration command when available

# Seed demo API key
echo ""
echo -e "${YELLOW}Seeding demo data...${NC}"
# TODO: Add seed script when SQL seed is created

# Start the application
echo ""
echo -e "${YELLOW}Starting DCF Valuation API...${NC}"
./bin/dcf-api &
API_PID=$!

# Save PID for cleanup
echo $API_PID > .api.pid

# Wait for API to be ready
sleep 3
check_service "DCF API" 8080

# Test health endpoint
echo ""
echo -e "${YELLOW}Testing health endpoint...${NC}"
HEALTH_RESPONSE=$(curl -s http://localhost:8080/health || echo "FAILED")

if [[ "$HEALTH_RESPONSE" == *"\"status\":\"ok\""* ]]; then
    echo -e "${GREEN}✓ Health check passed${NC}"
    echo "Response: $HEALTH_RESPONSE"
else
    echo -e "${RED}✗ Health check failed${NC}"
    echo "Response: $HEALTH_RESPONSE"
fi

echo ""
echo -e "${GREEN}============================================${NC}"
echo -e "${GREEN}Local staging environment is ready!${NC}"
echo ""
echo "API URL: http://localhost:8080"
echo "API Docs: http://localhost:8080/swagger/index.html"
echo "Metrics: http://localhost:9090/metrics"
echo ""
echo "Demo API Key: demo-key-phase-2.5-mvp"
echo ""
echo "Example requests:"
echo "  curl -H 'Authorization: Bearer demo-key-phase-2.5-mvp' http://localhost:8080/api/v1/fair-value/AAPL"
echo ""
echo "To stop the services, run: ./scripts/stop_staging.sh"
echo "" 