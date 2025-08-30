//go:build windows
// +build windows

package integration

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go"
)

// On Windows we do not start Redis; DI will fall back to memory cache
func setupRedisContainer(t *testing.T, ctx context.Context) (testcontainers.Container, string) {
	return nil, "redis://127.0.0.1:0"
}
