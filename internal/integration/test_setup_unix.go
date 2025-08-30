//go:build !windows
// +build !windows

package integration

import (
	"context"
	"fmt"
	"time"

	"testing"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupRedisContainer(t *testing.T, ctx context.Context) (testcontainers.Container, string) {
	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
		Cmd:          []string{"redis-server", "--appendonly", "yes"},
	}

	redisContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "Failed to start Redis container")

	mappedPort, err := redisContainer.MappedPort(ctx, "6379")
	require.NoError(t, err, "Failed to get Redis mapped port")

	host, err := redisContainer.Host(ctx)
	require.NoError(t, err, "Failed to get Redis host")

	redisURL := fmt.Sprintf("redis://%s:%s", host, mappedPort.Port())

	time.Sleep(2 * time.Second)
	return redisContainer, redisURL
}
