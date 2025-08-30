# DCF Valuation API Dockerfile
# Multi-stage build for optimized production image

# Stage 1: Build stage
FROM golang:1.23-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates sqlite gcc musl-dev

# Set working directory
WORKDIR /app

# Copy go mod files first for better layer caching
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application
# CGO_ENABLED=1 required for SQLite
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags="-w -s" \
    -a -installsuffix cgo \
    -o dcf-api \
    cmd/server/main.go

# Build migrate helper
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags="-w -s" \
    -a -installsuffix cgo \
    -o dcf-migrate \
    cmd/migrate/main.go

# Stage 2: Runtime stage
FROM alpine:3.19

# Install runtime dependencies
RUN apk --no-cache add \
    ca-certificates \
    sqlite \
    tzdata \
    && update-ca-certificates

# Create non-root user for security
RUN adduser -D -s /bin/sh -u 1001 appuser

# Set working directory
WORKDIR /app

# Copy binary from builder stage
COPY --from=builder /app/dcf-api .
COPY --from=builder /app/dcf-migrate .

# Copy configuration files and assets
COPY ./config ./config
COPY ./docs ./docs
COPY ./migrations ./migrations
COPY ./internal/infra/database/schema.sql ./internal/infra/database/schema.sql

# Add entrypoint script for optional migrations on start
COPY ./docker-entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Create necessary directories
RUN mkdir -p /app/data /app/logs \
    && chown -R appuser:appuser /app

# Switch to non-root user
USER appuser

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD /app/dcf-api --health-check || exit 1

# Set environment variables
ENV GIN_MODE=release
ENV LOG_LEVEL=info
ENV PORT=8080

# Run the application via entrypoint (runs migrations when enabled)
ENTRYPOINT ["/entrypoint.sh"]
CMD ["./dcf-api"] 