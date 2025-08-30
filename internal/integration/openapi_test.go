package integration

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenAPIEndpoint_SwaggerEnabled verifies that the OpenAPI spec is served when swagger is enabled
func TestOpenAPIEndpoint_SwaggerEnabled(t *testing.T) {
	// Set up test environment with swagger enabled
	testEnv := setupTestEnvironmentWithSwagger(t, true)
	if testEnv == nil {
		return
	}
	defer testEnv.Cleanup()

	t.Run("openapi_yaml_endpoint_returns_spec", func(t *testing.T) {
		// Create request to OpenAPI endpoint
		req, err := http.NewRequest("GET", "/docs/openapi.yaml", nil)
		require.NoError(t, err)

		// Execute request using test router
		w := httptest.NewRecorder()
		testEnv.Router.ServeHTTP(w, req)

		// Verify response status
		assert.Equal(t, http.StatusOK, w.Code, "OpenAPI endpoint should return 200 OK")

		// Read response body
		content := w.Body.String()

		// Verify it contains OpenAPI spec content
		assert.Contains(t, content, "openapi:", "Response should contain OpenAPI spec")
		assert.Contains(t, content, "Midas DCF Valuation API", "Response should contain API title")
		assert.Contains(t, content, "/api/v1/fair-value", "Response should contain API paths")
		assert.Contains(t, content, "ApiKeyAuth", "Response should contain security definitions")
	})

	t.Run("openapi_yaml_structure_is_valid", func(t *testing.T) {
		// Create request to OpenAPI endpoint
		req, err := http.NewRequest("GET", "/docs/openapi.yaml", nil)
		require.NoError(t, err)

		// Execute request using test router
		w := httptest.NewRecorder()
		testEnv.Router.ServeHTTP(w, req)

		// Verify response status
		require.Equal(t, http.StatusOK, w.Code, "Should return 200 OK")

		// Read response body
		content := w.Body.String()

		// Verify required OpenAPI 3.x structure
		assert.Contains(t, content, "openapi: 3.0", "Should be OpenAPI 3.x format")
		assert.Contains(t, content, "info:", "Should have info section")
		assert.Contains(t, content, "paths:", "Should have paths section")
		assert.Contains(t, content, "components:", "Should have components section")

		// Verify no templating/placeholder content
		assert.NotContains(t, content, "{{", "Should not contain template placeholders")
		assert.NotContains(t, content, "}}", "Should not contain template placeholders")
		assert.NotContains(t, content, "TODO", "Should not contain TODO placeholders")
	})
}

// TestOpenAPIEndpoint_SwaggerDisabled verifies that the OpenAPI endpoint is not available when swagger is disabled
func TestOpenAPIEndpoint_SwaggerDisabled(t *testing.T) {
	// Set up test environment with swagger disabled
	testEnv := setupTestEnvironmentWithSwagger(t, false)
	if testEnv == nil {
		return
	}
	defer testEnv.Cleanup()

	t.Run("openapi_yaml_endpoint_not_available", func(t *testing.T) {
		// Create request to OpenAPI endpoint
		req, err := http.NewRequest("GET", "/docs/openapi.yaml", nil)
		require.NoError(t, err)

		// Execute request using test router
		w := httptest.NewRecorder()
		testEnv.Router.ServeHTTP(w, req)

		// Verify response status is 404 when swagger is disabled
		assert.Equal(t, http.StatusNotFound, w.Code, "OpenAPI endpoint should return 404 when swagger is disabled")
	})
}

// TestOpenAPIContent_StaticValidation validates the content of the static OpenAPI spec file
func TestOpenAPIContent_StaticValidation(t *testing.T) {
	// This test validates the static file content without server dependency
	t.Run("static_file_exists_and_valid", func(t *testing.T) {
		// Read the static OpenAPI file directly
		content, err := readStaticFile("docs/openapi.yaml")
		require.NoError(t, err, "OpenAPI spec file should exist")

		// Validate basic structure
		assert.Contains(t, content, "openapi: 3.0", "Should be OpenAPI 3.x")
		assert.Contains(t, content, "title: Midas DCF Valuation API", "Should have correct title")
		assert.Contains(t, content, "version:", "Should have version")

		// Validate core endpoints are documented
		assert.Contains(t, content, "/api/v1/fair-value/{ticker}", "Should document fair value endpoint")
		assert.Contains(t, content, "/api/v1/fair-value/bulk", "Should document bulk endpoint")
		assert.Contains(t, content, "/api/v1/health/detailed", "Should document health endpoint")

		// Validate security is documented
		assert.Contains(t, content, "ApiKeyAuth", "Should have API key auth")
		assert.Contains(t, content, "X-API-Key", "Should specify X-API-Key header")

		// Validate schemas are present
		assert.Contains(t, content, "FairValueResponse", "Should have response schemas")
		assert.Contains(t, content, "ErrorResponse", "Should have error schemas")

		// Ensure no development artifacts
		assert.NotContains(t, content, "localhost:3000", "Should not hardcode development URLs")
		assert.NotContains(t, content, "127.0.0.1", "Should not hardcode local IPs")
		assert.NotContains(t, content, "test-api-key", "Should not contain test credentials")
	})
}

// Helper function to read static files (exists in test environment)
func readStaticFile(path string) (string, error) {
	// Try local development path first
	if content, err := readFileIfExists(path); err == nil {
		return content, nil
	}

	// Try relative path from integration test directory
	if content, err := readFileIfExists("../../" + path); err == nil {
		return content, nil
	}

	// Try container path
	if content, err := readFileIfExists("/app/" + path); err == nil {
		return content, nil
	}

	// If none work, return error
	return "", ErrFileNotFound{Path: path}
}

type ErrFileNotFound struct {
	Path string
}

func (e ErrFileNotFound) Error() string {
	return "file not found: " + e.Path
}

func readFileIfExists(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// setupTestEnvironmentWithSwagger creates a test environment with swagger enabled/disabled
func setupTestEnvironmentWithSwagger(t *testing.T, enableSwagger bool) *TestContainer {
	// Create a temporary OpenAPI file for testing when swagger is enabled
	if enableSwagger {
		// Create a minimal test OpenAPI spec
		testOpenAPIContent := `openapi: 3.0.3
info:
  title: Midas DCF Valuation API
  version: 0.1.0
  description: Test OpenAPI spec for integration testing
paths:
  /api/v1/fair-value/{ticker}:
    get:
      summary: Get fair value for a ticker
      parameters:
        - name: ticker
          in: path
          required: true
          schema:
            type: string
components:
  securitySchemes:
    ApiKeyAuth:
      type: apiKey
      in: header
      name: X-API-Key
security:
  - ApiKeyAuth: []
`
		// Ensure docs directory exists
		if err := os.MkdirAll("docs", 0755); err != nil {
			t.Fatalf("Failed to create docs directory: %v", err)
		}

		// Write test OpenAPI file
		if err := os.WriteFile("docs/openapi.yaml", []byte(testOpenAPIContent), 0644); err != nil {
			t.Fatalf("Failed to write test OpenAPI file: %v", err)
		}

		// Clean up after test
		t.Cleanup(func() {
			_ = os.Remove("docs/openapi.yaml")
		})

		t.Setenv("ENABLE_SWAGGER", "true")
	} else {
		t.Setenv("ENABLE_SWAGGER", "false")
	}

	// Set up normal test environment - it will pick up the env var
	return SetupTestEnvironment(t)
}
