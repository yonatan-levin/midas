#!/bin/bash
# DCF Valuation API - Docker Run Script
# Quick development and testing script

set -euo pipefail

# Configuration
IMAGE_NAME="dcf-valuation-api"
CONTAINER_NAME="dcf-api-dev"
PORT="${PORT:-8080}"
REDIS_PORT="${REDIS_PORT:-6379}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to display usage
usage() {
    cat << EOF
Usage: $0 [COMMAND] [OPTIONS]

Run DCF Valuation API in Docker containers

COMMANDS:
    up              Start all services (default)
    down            Stop and remove all containers
    restart         Restart all services
    logs            Show logs from all services
    build           Build and start services
    clean           Remove all containers and volumes
    status          Show status of all services

OPTIONS:
    -d, --detach    Run in detached mode
    -p, --port      Specify API port (default: 8080)
    --redis-port    Specify Redis port (default: 6379)
    -h, --help      Display this help message

EXAMPLES:
    $0                     # Start all services
    $0 up -d               # Start in detached mode
    $0 build               # Build and start
    $0 logs                # Show logs
    $0 down                # Stop all services

EOF
}

# Function to check if Docker is available
check_docker() {
    if ! command -v docker &> /dev/null; then
        log_error "Docker is not installed or not in PATH"
        exit 1
    fi
    
    if ! docker info &> /dev/null; then
        log_error "Docker daemon is not running"
        exit 1
    fi
}

# Function to check if docker-compose is available
check_compose() {
    if docker compose version &> /dev/null; then
        echo "docker compose"
    elif command -v docker-compose &> /dev/null; then
        echo "docker-compose"
    else
        log_error "Docker Compose is not available"
        exit 1
    fi
}

# Function to start services
start_services() {
    local detach="$1"
    local compose_cmd=$(check_compose)
    
    log_info "Starting DCF Valuation API services..."
    
    # Set environment variables
    export PORT="$PORT"
    export REDIS_PORT="$REDIS_PORT"
    
    if [[ "$detach" == "true" ]]; then
        $compose_cmd up -d
        log_success "Services started in detached mode"
        log_info "API available at: http://localhost:$PORT"
        log_info "Redis available at: localhost:$REDIS_PORT"
        log_info "Use '$0 logs' to view logs"
    else
        $compose_cmd up
    fi
}

# Function to stop services
stop_services() {
    local compose_cmd=$(check_compose)
    
    log_info "Stopping DCF Valuation API services..."
    $compose_cmd down
    log_success "Services stopped"
}

# Function to restart services
restart_services() {
    local compose_cmd=$(check_compose)
    
    log_info "Restarting DCF Valuation API services..."
    $compose_cmd restart
    log_success "Services restarted"
}

# Function to show logs
show_logs() {
    local compose_cmd=$(check_compose)
    
    log_info "Showing logs from all services..."
    $compose_cmd logs -f
}

# Function to build and start
build_and_start() {
    local compose_cmd=$(check_compose)
    
    log_info "Building and starting DCF Valuation API services..."
    
    # Set environment variables
    export PORT="$PORT"
    export REDIS_PORT="$REDIS_PORT"
    
    $compose_cmd up --build
}

# Function to clean up
clean_up() {
    local compose_cmd=$(check_compose)
    
    log_warning "This will remove all containers and volumes. Are you sure? (y/N)"
    read -r response
    
    if [[ "$response" =~ ^[Yy]$ ]]; then
        log_info "Cleaning up DCF Valuation API services..."
        $compose_cmd down -v --remove-orphans
        
        # Remove images
        if docker images -q "$IMAGE_NAME" &> /dev/null; then
            docker rmi $(docker images -q "$IMAGE_NAME") 2>/dev/null || true
        fi
        
        log_success "Cleanup completed"
    else
        log_info "Cleanup cancelled"
    fi
}

# Function to show status
show_status() {
    local compose_cmd=$(check_compose)
    
    log_info "Status of DCF Valuation API services:"
    $compose_cmd ps
    
    echo
    log_info "Docker images:"
    docker images | grep -E "(dcf-valuation-api|redis)" || echo "No DCF-related images found"
    
    echo
    log_info "Docker volumes:"
    docker volume ls | grep -E "(dcf|redis)" || echo "No DCF-related volumes found"
}

# Main function
main() {
    local command="up"
    local detach="false"
    
    # Parse command line arguments
    while [[ $# -gt 0 ]]; do
        case $1 in
            up|down|restart|logs|build|clean|status)
                command="$1"
                shift
                ;;
            -d|--detach)
                detach="true"
                shift
                ;;
            -p|--port)
                PORT="$2"
                shift 2
                ;;
            --redis-port)
                REDIS_PORT="$2"
                shift 2
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            *)
                log_error "Unknown option: $1"
                usage
                exit 1
                ;;
        esac
    done
    
    # Check prerequisites
    check_docker
    
    # Execute command
    case $command in
        up)
            start_services "$detach"
            ;;
        down)
            stop_services
            ;;
        restart)
            restart_services
            ;;
        logs)
            show_logs
            ;;
        build)
            build_and_start
            ;;
        clean)
            clean_up
            ;;
        status)
            show_status
            ;;
        *)
            log_error "Unknown command: $command"
            usage
            exit 1
            ;;
    esac
}

# Run main function with all arguments
main "$@" 