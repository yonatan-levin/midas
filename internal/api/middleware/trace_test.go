package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/midas/dcf-valuation-api/internal/api/middleware"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
	"github.com/midas/dcf-valuation-api/internal/observability/narrate"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// requestIDStub mimics the canonical requestIDMiddleware — it sets
// "request_id" on the gin context, the only contract trace middleware
// depends on.
func requestIDStub(rid string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("request_id", rid)
		c.Next()
	}
}

// TestTrace_NoOpWhenArtifactDisabled — flag set, but ArtifactStore.Enabled=false.
// No bundle must be created on disk; narrate emitter still attached to ctx.
func TestTrace_NoOpWhenArtifactDisabled(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-x"))
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{Enabled: false, RootPath: root},
	))

	var sawEmitter bool
	var sawBundle bool
	var sawTraceEnabled bool
	var sawTraceReason string
	r.GET("/x", func(c *gin.Context) {
		ctx := c.Request.Context()
		sawEmitter = narrate.From(ctx).Sampled() // Sampled() == true means emitter present
		sawBundle = artifact.From(ctx) != nil
		v, _ := c.Get("trace_enabled")
		sawTraceEnabled, _ = v.(bool)
		reason, _ := c.Get("trace_reason")
		sawTraceReason, _ = reason.(string)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x?trace=1", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, sawEmitter, "narrate emitter must always be on ctx")
	assert.False(t, sawBundle, "no bundle when ArtifactStore disabled")
	assert.False(t, sawTraceEnabled, "trace_enabled=false when artifact_store off")
	assert.Equal(t, "disabled", sawTraceReason)

	// Disk must be empty.
	entries, _ := os.ReadDir(root)
	assert.Empty(t, entries, "no bundle directory should have been written")
}

// TestTrace_NoOpWhenNoFlag — even with ArtifactStore enabled, no flag means
// no bundle.
func TestTrace_NoOpWhenNoFlag(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-y"))
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{Enabled: true, RootPath: root},
	))
	r.GET("/x", func(c *gin.Context) {
		assert.Nil(t, artifact.From(c.Request.Context()))
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	entries, _ := os.ReadDir(root)
	assert.Empty(t, entries)
}

// TestTrace_OpensBundleViaQuery — ?trace=1 + enabled = bundle on disk.
func TestTrace_OpensBundleViaQuery(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-q"))
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{Enabled: true, RootPath: root},
	))

	var bundleRoot string
	r.GET("/x", func(c *gin.Context) {
		b := artifact.From(c.Request.Context())
		require.NotNil(t, b, "bundle must be on ctx")
		bundleRoot = b.Root()
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x?trace=1", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NotEmpty(t, bundleRoot)
	// 00-manifest.json must exist after Close (deferred at request end).
	manifest := filepath.Join(bundleRoot, "00-manifest.json")
	_, err := os.Stat(manifest)
	assert.NoError(t, err, "manifest must be written when middleware exits")
}

// TestTrace_OpensBundleViaHeader — X-Midas-Trace: 1.
func TestTrace_OpensBundleViaHeader(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-h"))
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{Enabled: true, RootPath: root},
	))
	r.GET("/x", func(c *gin.Context) {
		assert.NotNil(t, artifact.From(c.Request.Context()))
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Midas-Trace", "1")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.NotEmpty(t, entries, "bundle must be created on disk")
}

// TestTrace_TruthyValues — 1, true, yes, on are all accepted.
func TestTrace_TruthyValues(t *testing.T) {
	cases := []string{"1", "true", "TRUE", "yes", "on", "True"}
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			root := t.TempDir()

			r := gin.New()
			r.Use(requestIDStub("req-" + v))
			r.Use(middleware.TraceMiddleware(
				narrate.Config{Enabled: true, SampleRate: 1.0},
				artifact.Config{Enabled: true, RootPath: root},
			))
			r.GET("/x", func(c *gin.Context) {
				assert.NotNil(t, artifact.From(c.Request.Context()))
				c.Status(http.StatusOK)
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/x?trace="+v, nil)
			r.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
		})
	}
}

// TestTrace_FalsyValuesIgnored — typos and unknowns must NOT enable tracing.
func TestTrace_FalsyValuesIgnored(t *testing.T) {
	cases := []string{"0", "false", "no", "off", "tru", "y", ""}
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			root := t.TempDir()

			r := gin.New()
			r.Use(requestIDStub("req-falsy"))
			r.Use(middleware.TraceMiddleware(
				narrate.Config{Enabled: true, SampleRate: 1.0},
				artifact.Config{Enabled: true, RootPath: root},
			))
			r.GET("/x", func(c *gin.Context) {
				assert.Nil(t, artifact.From(c.Request.Context()),
					"trace=%q must NOT open a bundle", v)
				c.Status(http.StatusOK)
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/x?trace="+v, nil)
			r.ServeHTTP(w, req)

			entries, _ := os.ReadDir(root)
			assert.Empty(t, entries, "no bundle for trace=%q", v)
		})
	}
}

// TestTrace_HeaderTakesPrecedenceOverQuery — both set => header wins.
func TestTrace_HeaderTakesPrecedenceOverQuery(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-pref"))
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{Enabled: true, RootPath: root},
	))

	var trigger string
	r.GET("/x", func(c *gin.Context) {
		v, _ := c.Get("trace_trigger")
		trigger, _ = v.(string)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x?trace=1", nil)
	req.Header.Set("X-Midas-Trace", "1")
	r.ServeHTTP(w, req)

	assert.Equal(t, "header", trigger, "header must take precedence over query")
}

// TestTrace_BundleClosedOnReturn — manifest must be on disk by the time
// the request handler completes.
func TestTrace_BundleClosedOnReturn(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-closed"))
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{Enabled: true, RootPath: root},
	))
	r.GET("/x", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x?trace=1", nil)
	r.ServeHTTP(w, req)

	// Walk: <root>/<date>/_no-ticker/req_*/00-manifest.json must exist.
	var found string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && filepath.Base(p) == "00-manifest.json" {
			found = p
		}
		return nil
	})
	assert.NotEmpty(t, found, "manifest must be present after request returns")
}

// TestTrace_NarrateEmitterAlwaysOnContext — even with no trace flag and no
// bundle, downstream code must be able to call narrate.From(ctx) safely.
func TestTrace_NarrateEmitterAlwaysOnContext(t *testing.T) {
	r := gin.New()
	r.Use(requestIDStub("req-emit"))
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{Enabled: false},
	))
	var hasEmitter bool
	r.GET("/x", func(c *gin.Context) {
		e := narrate.From(c.Request.Context())
		hasEmitter = e != nil
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	assert.True(t, hasEmitter)
}
