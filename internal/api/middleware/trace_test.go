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
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/midas/dcf-valuation-api/internal/api/middleware"
	"github.com/midas/dcf-valuation-api/internal/observability/artifact"
	"github.com/midas/dcf-valuation-api/internal/observability/logctx"
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

// TestTrace_OpenBundleFailureWarnsAndDegrades — when artifact.OpenBundle
// fails (here: RootPath is a regular file, so MkdirAll errors), the
// middleware MUST:
//   - emit a Warn log line so operators can find the cause (HIGH 1 fix);
//   - set trace_enabled=false on the gin context;
//   - record trace_reason=open_failed so the request narrative isn't silent.
//
// Pre-fix this path silently swallowed the error and the user saw no bundle
// with no log line explaining why.
func TestTrace_OpenBundleFailureWarnsAndDegrades(t *testing.T) {
	// Make RootPath a FILE rather than a directory — every MkdirAll under
	// it will fail with "not a directory" on POSIX and ENOTDIR on Windows.
	tmpDir := t.TempDir()
	rootAsFile := filepath.Join(tmpDir, "artifacts-but-actually-a-file")
	require.NoError(t, os.WriteFile(rootAsFile, []byte("not a dir"), 0o644))

	// Capture log output via a zap observer so we can assert on the Warn line.
	core, recorded := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)

	r := gin.New()
	r.Use(requestIDStub("req-openfail"))
	// Inject the observer logger via logctx so trace.go's
	// logctx.From(c.Request.Context()) picks it up.
	r.Use(func(c *gin.Context) {
		ctx := logctx.Inject(c.Request.Context(), logger)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{Enabled: true, RootPath: rootAsFile},
	))

	var sawBundle bool
	var sawTraceEnabled bool
	var sawTraceReason string
	r.GET("/x", func(c *gin.Context) {
		sawBundle = artifact.From(c.Request.Context()) != nil
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
	assert.False(t, sawBundle, "bundle must be absent on OpenBundle failure")
	assert.False(t, sawTraceEnabled,
		"trace_enabled must downgrade to false when bundle fails to open")
	assert.Equal(t, "open_failed", sawTraceReason,
		"trace_reason must explain the silent absence of the bundle")

	// And the Warn line must have been emitted with the open error.
	logs := recorded.FilterMessage("trace.bundle.open_failed").All()
	require.Len(t, logs, 1, "expected exactly one open_failed Warn line")
	assert.Equal(t, zapcore.WarnLevel, logs[0].Level)
	// The error field must be present and non-empty.
	fields := logs[0].ContextMap()
	assert.NotEmpty(t, fields["error"], "Warn line must carry the underlying error")
	assert.Equal(t, "req-openfail", fields["request_id"])
}

// TestTrace_BundleSinkInstalledAndCaptures — when ?trace=1 opens a bundle,
// the trace middleware must wrap the request-scoped logger with a
// BundleSink so subsequent narrate + Debug entries land in 99-narrate.jsonl
// / 99-debug-trace.jsonl on disk. Forwarding to the host log stream must
// also still work (the wrapper is transparent).
//
// This is the contract behind QA finding MINOR-1 (2026-04-25): the spec
// promised these JSONL files live in the bundle and the test pins that
// promise at the middleware layer.
func TestTrace_BundleSinkInstalledAndCaptures(t *testing.T) {
	root := t.TempDir()

	// Observer logger at Debug level so the BundleSink wrapper can see Debug
	// entries (production typically runs at Info; the integration test's
	// debug-stream branch covers that case).
	core, recorded := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	r := gin.New()
	r.Use(requestIDStub("req-sink-cap"))
	// Inject the observer logger so trace.go's wrap-the-request-scoped-logger
	// path has a non-nop logger to wrap.
	r.Use(func(c *gin.Context) {
		ctx := logctx.Inject(c.Request.Context(), logger)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{Enabled: true, RootPath: root, QueueSize: 64},
	))

	var bundleRoot string
	r.GET("/x", func(c *gin.Context) {
		b := artifact.From(c.Request.Context())
		require.NotNil(t, b)
		bundleRoot = b.Root()

		// Emit one narrate line and one Debug line via logctx — the wrapped
		// logger from trace middleware should tee both to bundle JSONL
		// streams while still forwarding to the observer.
		l := logctx.From(c.Request.Context())
		l.Info("trace.handler.narrate",
			zap.String("event", "narrate"),
			zap.String("phase", "handler.entry"),
		)
		l.Debug("trace.handler.debug",
			zap.String("phase", "compute"),
		)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x?trace=1", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.NotEmpty(t, bundleRoot)

	// Forwarding still works: the observer must have seen both entries plus
	// trace middleware's own request.received + response.sent narrate lines.
	allEntries := recorded.All()
	require.NotEmpty(t, allEntries, "observer must receive entries (forwarding works)")
	sawHandlerNarrate := false
	sawHandlerDebug := false
	for _, e := range allEntries {
		if e.Message == "trace.handler.narrate" {
			sawHandlerNarrate = true
		}
		if e.Message == "trace.handler.debug" {
			sawHandlerDebug = true
		}
	}
	assert.True(t, sawHandlerNarrate, "observer must receive the narrate entry")
	assert.True(t, sawHandlerDebug, "observer must receive the debug entry")

	// Bundle JSONL streams must contain the entries.
	narrateBody, err := os.ReadFile(filepath.Join(bundleRoot, "99-narrate.jsonl"))
	require.NoError(t, err, "99-narrate.jsonl must be on disk")
	// Multiple narrate lines: trace middleware's request.received + response.sent
	// + the handler's emit. Just assert "at least one" so future trace phases
	// don't break this test.
	assert.Contains(t, string(narrateBody), `"phase":"handler.entry"`,
		"narrate stream must contain the handler's narrate entry")

	debugBody, err := os.ReadFile(filepath.Join(bundleRoot, "99-debug-trace.jsonl"))
	require.NoError(t, err, "99-debug-trace.jsonl must be on disk when Debug entries emitted")
	assert.Contains(t, string(debugBody), `trace.handler.debug`,
		"debug stream must contain the handler's debug entry")
}

// TestTrace_NoBundleSink_WhenDisabled — when artifact store is disabled,
// the trace middleware MUST NOT wrap the logger (the wrapper is nil-bundle
// transparent, but skipping the wrap entirely keeps the hot path slim).
// We assert by emitting a narrate line and confirming no JSONL file lands
// anywhere on disk.
func TestTrace_NoBundleSink_WhenDisabled(t *testing.T) {
	root := t.TempDir()

	core, _ := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	r := gin.New()
	r.Use(requestIDStub("req-no-sink"))
	r.Use(func(c *gin.Context) {
		ctx := logctx.Inject(c.Request.Context(), logger)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{Enabled: false, RootPath: root},
	))
	r.GET("/x", func(c *gin.Context) {
		l := logctx.From(c.Request.Context())
		l.Info("phase",
			zap.String("event", "narrate"),
			zap.String("phase", "handler.entry"),
		)
		l.Debug("dbg")
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x?trace=1", nil)
	r.ServeHTTP(w, req)

	// Disk must remain empty: no bundle was ever opened.
	entries, _ := os.ReadDir(root)
	assert.Empty(t, entries, "no bundle dir when artifact store disabled")
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
