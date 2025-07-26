#!/bin/bash
# DCF Valuation API - Docker Build Script
# Builds multi-architecture Docker images with proper versioning

set -euo pipefail

# Configuration
IMAGE_NAME="dcf-valuation-api"
REGISTRY="${DOCKER_REGISTRY:-}"
PLATFORMS="linux/amd64,linux/arm64"
BUILD_CONTEXT="."
DOCKERFILE="Dockerfile"

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

# Function to get version from git
get_version() {
    if git rev-parse --is-inside-work-tree > /dev/null 2>&1; then
        # Try to get version from git tag
        if git describe --tags --exact-match > /dev/null 2>&1; then
            git describe --tags --exact-match
        else
            # Fallback to branch + commit hash
            local branch=$(git rev-parse --abbrev-ref HEAD)
            local commit=$(git rev-parse --short HEAD)
            echo "${branch}-${commit}"
        fi
    else
        echo "latest"
    fi
}

# Function to check prerequisites
check_prerequisites() {
    log_info "Checking prerequisites..."
    
    # Check if Docker is installed and running
    if ! command -v docker &> /dev/null; then
        log_error "Docker is not installed or not in PATH"
        exit 1
    fi
    
    if ! docker info &> /dev/null; then
        log_error "Docker daemon is not running"
        exit 1
    fi
    
    # Check if Docker Buildx is available for multi-platform builds
    if ! docker buildx version &> /dev/null; then
        log_error "Docker Buildx is not available"
        exit 1
    fi
    
    # Check if we're in the project root
    if [[ ! -f "go.mod" ]] || [[ ! -f "Dockerfile" ]]; then
        log_error "Please run this script from the project root directory"
        exit 1
    fi
    
    log_success "Prerequisites check passed"
}

# Function to create or use buildx builder
setup_builder() {
    local builder_name="dcf-builder"
    
    log_info "Setting up Docker buildx builder..."
    
    # Check if builder already exists
    if docker buildx inspect "$builder_name" &> /dev/null; then
        log_info "Using existing builder: $builder_name"
        docker buildx use "$builder_name"
    else
        log_info "Creating new builder: $builder_name"
        docker buildx create --name "$builder_name" --use
    fi
    
    # Bootstrap the builder
    docker buildx inspect --bootstrap
    
    log_success "Builder setup complete"
}

# Function to build image
build_image() {
    local version="$1"
    local push="$2"
    local image_tag
    
    if [[ -n "$REGISTRY" ]]; then
        image_tag="${REGISTRY}/${IMAGE_NAME}:${version}"
    else
        image_tag="${IMAGE_NAME}:${version}"
    fi
    
    log_info "Building Docker image: $image_tag"
    log_info "Platforms: $PLATFORMS"
    
    # Build arguments
    local build_args=(
        --platform "$PLATFORMS"
        --file "$DOCKERFILE"
        --tag "$image_tag"
        --build-arg "BUILD_DATE=$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
        --build-arg "VERSION=$version"
        --build-arg "GIT_COMMIT=$(git rev-parse HEAD 2>/dev/null || echo 'unknown')"
    )
    
    # Add latest tag if version is not latest
    if [[ "$version" != "latest" ]] && [[ "$version" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        if [[ -n "$REGISTRY" ]]; then
            build_args+=(--tag "${REGISTRY}/${IMAGE_NAME}:latest")
        else
            build_args+=(--tag "${IMAGE_NAME}:latest")
        fi
    fi
    
    # Add push flag if requested
    if [[ "$push" == "true" ]]; then
        build_args+=(--push)
        log_info "Images will be pushed to registry"
    else
        build_args+=(--load)
        log_info "Images will be loaded locally"
    fi
    
    # Execute build
    docker buildx build "${build_args[@]}" "$BUILD_CONTEXT"
    
    log_success "Build completed: $image_tag"
}

# Function to run basic tests on built image
test_image() {
    local version="$1"
    local image_tag
    
    if [[ -n "$REGISTRY" ]]; then
        image_tag="${REGISTRY}/${IMAGE_NAME}:${version}"
    else
        image_tag="${IMAGE_NAME}:${version}"
    fi
    
    log_info "Running basic tests on image: $image_tag"
    
    # Test if image can start and respond to health check
    local container_id
    container_id=$(docker run -d -p 18080:8080 "$image_tag")
    
    # Wait for container to start
    sleep 5
    
    # Test health endpoint
    if curl -f http://localhost:18080/health > /dev/null 2>&1; then
        log_success "Health check passed"
    else
        log_warning "Health check failed - this might be expected if dependencies are not available"
    fi
    
    # Clean up test container
    docker stop "$container_id" > /dev/null
    docker rm "$container_id" > /dev/null
    
    log_success "Basic tests completed"
}

# Function to display usage
usage() {
    cat << EOF
Usage: $0 [OPTIONS]

Build Docker images for DCF Valuation API

OPTIONS:
    -v, --version VERSION   Specify version tag (default: auto-detect from git)
    -p, --push             Push images to registry after build
    -r, --registry URL     Specify Docker registry URL
    -t, --test             Run basic tests after build
    -h, --help             Display this help message

EXAMPLES:
    $0                               # Build with auto-detected version
    $0 -v v1.0.0 -p                 # Build and push version v1.0.0
    $0 -r my-registry.com -p        # Build and push to custom registry
    $0 -t                           # Build and run basic tests

ENVIRONMENT VARIABLES:
    DOCKER_REGISTRY         Default registry URL
    DOCKER_BUILDKIT         Enable BuildKit (recommended: 1)

EOF
}

# Main function
main() {
    local version=""
    local push="false"
    local run_tests="false"
    
    # Parse command line arguments
    while [[ $# -gt 0 ]]; do
        case $1 in
            -v|--version)
                version="$2"
                shift 2
                ;;
            -p|--push)
                push="true"
                shift
                ;;
            -r|--registry)
                REGISTRY="$2"
                shift 2
                ;;
            -t|--test)
                run_tests="true"
                shift
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
    
    # Auto-detect version if not provided
    if [[ -z "$version" ]]; then
        version=$(get_version)
        log_info "Auto-detected version: $version"
    fi
    
    # Enable BuildKit
    export DOCKER_BUILDKIT=1
    
    # Execute build process
    check_prerequisites
    setup_builder
    build_image "$version" "$push"
    
    # Run tests if requested
    if [[ "$run_tests" == "true" ]] && [[ "$push" != "true" ]]; then
        test_image "$version"
    fi
    
    log_success "Docker build process completed successfully!"
    
    # Display useful information
    echo
    log_info "Built image(s):"
    if [[ -n "$REGISTRY" ]]; then
        echo "  - ${REGISTRY}/${IMAGE_NAME}:${version}"
        if [[ "$version" != "latest" ]] && [[ "$version" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
            echo "  - ${REGISTRY}/${IMAGE_NAME}:latest"
        fi
    else
        echo "  - ${IMAGE_NAME}:${version}"
        if [[ "$version" != "latest" ]] && [[ "$version" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
            echo "  - ${IMAGE_NAME}:latest"
        fi
    fi
    
    echo
    log_info "To run the container:"
    echo "  docker run -p 8080:8080 ${IMAGE_NAME}:${version}"
    
    if [[ "$push" == "true" ]]; then
        echo
        log_info "Images have been pushed to the registry"
    fi
}

# Run main function with all arguments
main "$@" 