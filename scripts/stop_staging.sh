#!/bin/bash
# DCF Valuation API - Stop Staging Environment
# Phase 2.5: MVP End-to-End Validation

set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${YELLOW}Stopping DCF Valuation API staging environment...${NC}"

# Stop API if running
if [ -f .api.pid ]; then
    PID=$(cat .api.pid)
    if ps -p $PID > /dev/null 2>&1; then
        echo -e "${YELLOW}Stopping API (PID: $PID)...${NC}"
        kill $PID 2>/dev/null || true
        sleep 2
        # Force kill if still running
        kill -9 $PID 2>/dev/null || true
    fi
    rm -f .api.pid
    echo -e "${GREEN}✓ API stopped${NC}"
fi

# Stop Docker services
echo -e "${YELLOW}Stopping Docker services...${NC}"
docker-compose down

echo -e "${GREEN}✓ All services stopped${NC}" 