package middleware_test

import (
	"encoding/json"
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
	// (This middleware test doesn't run a handler that calls SetTicker, so the
	// dir stays at _no-ticker — by design. End-to-end ticker rename is covered
	// by TestSetTicker_RenamesDirectory in internal/observability/artifact.)
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

// TestTrace_HostStreamHasRequestIDExactlyOnce pins the fix for REVIEWER
// finding 2026-04-25 (HIGH): the previous workaround re-applied request_id
// via .With() AFTER WrapCore, which routed request_id through both the
// BundleSink (good — JSONL needed it) AND the wrapped core's internal
// With-state (bad — request_id was already there from requestIDMiddleware).
// zap's JSON encoder does NOT dedupe duplicate field keys, so every host log
// line emitted during a traced request carried "request_id" twice. The fix
// passes request_id as a baseField directly to NewBundleSink, bypassing the
// wrapped core's .With() chain entirely.
//
// This test would have FAILED on commit 5 (with the duplicate). It MUST
// PASS post-fix on commit 6.
func TestTrace_HostStreamHasRequestIDExactlyOnce(t *testing.T) {
	root := t.TempDir()

	// Observer captures every entry the wrapped core sees, including its
	// accumulated With-state via Entry.Context.
	core, recorded := observer.New(zapcore.DebugLevel)
	// Bake request_id into the request-scoped logger BEFORE trace middleware
	// runs — this mirrors what the real requestIDMiddleware does in
	// internal/api/middleware/request_id.go.
	requestID := "req-no-dup"
	baseLogger := zap.New(core).With(zap.String("request_id", requestID))

	r := gin.New()
	r.Use(requestIDStub(requestID))
	// Inject the request-scoped logger so trace.go's logctx.From picks it up.
	r.Use(func(c *gin.Context) {
		ctx := logctx.Inject(c.Request.Context(), baseLogger)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	// ?trace=1 installs the BundleSink wrapper — this is the path that used
	// to double-up request_id.
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{Enabled: true, RootPath: root, QueueSize: 64},
	))
	r.GET("/x", func(c *gin.Context) {
		// Emit one log line through the request-scoped logger so it goes
		// through the BundleSink-wrapped core.
		l := logctx.From(c.Request.Context())
		l.Info("handler.work", zap.String("phase", "compute"))
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x?trace=1", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	allEntries := recorded.All()
	require.NotEmpty(t, allEntries, "observer must receive at least one entry")

	// For every entry the wrapped core saw, count how many times "request_id"
	// appears in its accumulated context. Pre-fix this was 2 for entries
	// emitted via the request-scoped logger; post-fix it MUST be exactly 1.
	for i, e := range allEntries {
		count := 0
		for _, f := range e.Context {
			if f.Key == "request_id" {
				count++
			}
		}
		assert.Equalf(t, 1, count,
			"entry %d (%q) must carry request_id EXACTLY once; got %d in fields=%v",
			i, e.Message, count, e.Context)
	}
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

// ---------------------------------------------------------------------------
// Phase 2.A — auto-on-error trigger tests
// ---------------------------------------------------------------------------
//
// These pin the behaviour matrix from the Phase 2.A brief:
//
//   manual flag | on_error cfg | status   | bundle?  | trigger
//   ------------|--------------|----------|----------|------------
//   yes         | any          | any      | yes      | header/query
//   no          | true         | >=500    | yes      | on_error
//   no          | true         | <500     | no       | —
//   no          | false        | any      | no       | —
//
// One test per non-trivial row.

// findManifest walks `root` and returns the bundle's 00-manifest.json path
// (empty if none exists). Used by the auto-trigger tests because the
// directory layout is artifacts/<UTC-date>/<TICKER-or-_no-ticker>/req_<id>/
// and the date partition isn't predictable in a unit-test seam.
func findManifest(root string) string {
	var found string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && info != nil && filepath.Base(p) == "00-manifest.json" {
			found = p
		}
		return nil
	})
	return found
}

// readManifestTrigger returns the trigger value from the bundle's manifest
// at root, or empty when no manifest exists. Defensive helper so the auto-
// trigger tests can assert on trigger without rebuilding manifest plumbing
// each time.
func readManifestTrigger(t *testing.T, root string) string {
	t.Helper()
	mfPath := findManifest(root)
	if mfPath == "" {
		return ""
	}
	body, err := os.ReadFile(mfPath)
	require.NoError(t, err)
	var m artifact.Manifest
	require.NoError(t, json.Unmarshal(body, &m))
	return m.Trigger
}

// TestTrace_OnError_AutoBundle_When500 — handler returns 500, on_error=true,
// no manual flag. A bundle MUST appear on disk and the manifest's trigger
// MUST be "on_error". This is the row-2 happy path.
func TestTrace_OnError_AutoBundle_When500(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-onerror-500"))
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{
			Enabled:  true,
			RootPath: root,
			Triggers: artifact.TriggerConfig{OnError: true},
		},
	))
	r.GET("/x", func(c *gin.Context) {
		// Synthetic 500 — the on_error trigger only cares about Writer.Status().
		c.AbortWithStatus(http.StatusInternalServerError)
	})

	w := httptest.NewRecorder()
	// NB: NO ?trace=1 and NO X-Midas-Trace header — pin the auto-trigger.
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)

	// Bundle must exist on disk.
	mfPath := findManifest(root)
	require.NotEmpty(t, mfPath, "bundle must be on disk for 500 + on_error=true")

	// Trigger must be on_error.
	assert.Equal(t, "on_error", readManifestTrigger(t, root),
		"auto-triggered bundle must record trigger=on_error")
}

// TestTrace_OnError_NoBundle_When200 — handler returns 200, on_error=true,
// no manual flag. NO bundle directory may be created. This is the dominant
// production path; getting it wrong floods the disk.
func TestTrace_OnError_NoBundle_When200(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-onerror-200"))
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{
			Enabled:  true,
			RootPath: root,
			Triggers: artifact.TriggerConfig{OnError: true},
		},
	))
	r.GET("/x", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	// Disk must remain pristine — no bundle dir, no date partition.
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Empty(t, entries,
		"non-erroring request with on_error=true must NOT leave files on disk; got %v", entries)
}

// TestTrace_OnError_Disabled_NoBundle_When500 — handler returns 500,
// on_error=false (default), no manual flag. NO bundle must be created.
// Pins the default-off invariant: enabling artifact_store alone (without
// flipping on_error) does NOT auto-capture errors.
func TestTrace_OnError_Disabled_NoBundle_When500(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-onerror-disabled"))
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{
			Enabled:  true,
			RootPath: root,
			// Triggers.OnError NOT set — defaults to false.
		},
	))
	r.GET("/x", func(c *gin.Context) {
		c.AbortWithStatus(http.StatusInternalServerError)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)

	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Empty(t, entries,
		"500 with on_error=false must NOT auto-capture; got %v", entries)
}

// TestTrace_Manual_StillWorks — regression pin for Phase 1: a request with
// ?trace=1 + on_error=false + status 200 must still create a bundle with
// trigger=query. This is row 1 of the matrix and verifies Phase 2.A did not
// break Phase 1's manual path.
func TestTrace_Manual_StillWorks(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-manual"))
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{
			Enabled:  true,
			RootPath: root,
			// on_error explicitly off — manual path must be self-sufficient.
			Triggers: artifact.TriggerConfig{OnError: false},
		},
	))
	r.GET("/x", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x?trace=1", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	mfPath := findManifest(root)
	require.NotEmpty(t, mfPath, "manual ?trace=1 must still create a bundle")
	assert.Equal(t, "query", readManifestTrigger(t, root),
		"manual via query string must record trigger=query")
}

// TestTrace_Manual_PrecedenceOverOnError — when BOTH the manual flag AND
// on_error fire (manual flag set + 500 returned + on_error=true), the
// manifest's trigger MUST stay "header" or "query" (NOT "on_error").
// Manual takes precedence so debug sessions stay attributable to whoever
// flipped the flag, even when the request happens to error.
func TestTrace_Manual_PrecedenceOverOnError(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-precedence"))
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{
			Enabled:  true,
			RootPath: root,
			Triggers: artifact.TriggerConfig{OnError: true},
		},
	))
	r.GET("/x", func(c *gin.Context) {
		// Erroring handler with the manual flag also set — both triggers
		// COULD fire. Manual must win.
		c.AbortWithStatus(http.StatusInternalServerError)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x?trace=1", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)

	trigger := readManifestTrigger(t, root)
	require.NotEmpty(t, trigger, "bundle must exist (eager manual path)")
	// Specifically NOT on_error — manual wins.
	assert.NotEqual(t, "on_error", trigger,
		"manifest trigger must NOT be on_error when manual flag was also set")
	assert.Contains(t, []string{"query", "header"}, trigger,
		"manifest trigger must be the manual source (query/header)")
}

// TestTrace_OnError_BundleSinkInstalledForDeferred — pins that the
// BundleSink is installed even for deferred bundles, so log entries emitted
// before the trigger fires are buffered into the deferred stream and end up
// in 99-narrate.jsonl AFTER promote. Without this, the bundle would only
// contain post-promote entries — useless for understanding the request that
// errored.
func TestTrace_OnError_BundleSinkInstalledForDeferred(t *testing.T) {
	root := t.TempDir()

	core, _ := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	r := gin.New()
	r.Use(requestIDStub("req-deferred-sink"))
	r.Use(func(c *gin.Context) {
		ctx := logctx.Inject(c.Request.Context(), logger)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{
			Enabled:  true,
			RootPath: root,
			Triggers: artifact.TriggerConfig{OnError: true},
		},
	))

	r.GET("/x", func(c *gin.Context) {
		l := logctx.From(c.Request.Context())
		// Emit a narrate-tagged entry mid-handler — must be buffered into the
		// deferred bundle's stream and survive the promote at request-end.
		l.Info("trace.handler.narrate",
			zap.String("event", "narrate"),
			zap.String("phase", "handler.entry"),
		)
		c.AbortWithStatus(http.StatusInternalServerError)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)

	// Walk for the bundle dir and read 99-narrate.jsonl.
	mfPath := findManifest(root)
	require.NotEmpty(t, mfPath, "deferred bundle must promote on 500")
	bundleDir := filepath.Dir(mfPath)

	streamBody, err := os.ReadFile(filepath.Join(bundleDir, "99-narrate.jsonl"))
	require.NoError(t, err, "99-narrate.jsonl must be on disk after promote")
	// The buffered handler.entry line must be present in the post-promote
	// JSONL — that is the whole point of "buffer through request, decide at
	// end" vs. Phase 1's "decide at start, write through".
	assert.Contains(t, string(streamBody), `"phase":"handler.entry"`,
		"narrate stream must contain the handler-emitted line that fired BEFORE the 500")
}

// TestTrace_OnError_PromoteFailure_NoArtifactPathOnNarrate pins QA finding
// MINOR-NEW (2026-04-26). Pre-fix: when a deferred bundle was opened on the
// auto-on-error path and Promote() failed at request-end (e.g. mkdir error
// because the destination is unwritable), the response.sent narrate line
// still emitted artifact_path pointing at the now-non-existent bundle
// directory. Log readers chasing the path hit a missing-dir dead end with
// no signal that promotion ever failed.
//
// Post-fix: artifact_path is gated on promoteSucceeded (true only when
// Promote returns nil). On promote failure the field is OMITTED from
// response.sent and the existing trace.bundle.promote_failed Warn line
// remains the operator's correlation point.
//
// Setup: RootPath is set to a regular FILE so OpenDeferredBundle (which
// does NOT mkdir at construction — see internal/observability/artifact/
// bundle.go:281) succeeds, then Promote()'s os.MkdirAll(b.root) fails with
// "not a directory" (ENOTDIR / Windows equivalent) because the parent isn't
// a directory. This exercises the Promote-failure branch specifically; if
// OpenDeferredBundle had failed instead, we'd be testing a different code
// path (the open_failed branch already covered by
// TestTrace_OpenBundleFailureWarnsAndDegrades).
func TestTrace_OnError_PromoteFailure_NoArtifactPathOnNarrate(t *testing.T) {
	tmpDir := t.TempDir()
	// Make RootPath a FILE rather than a directory. OpenDeferredBundle just
	// computes the path string at construction and won't notice; Promote()'s
	// MkdirAll on <file>/<date>/<ticker>/req_<id> fails with ENOTDIR because
	// the immediate parent (<file>) cannot host children.
	rootAsFile := filepath.Join(tmpDir, "artifacts-but-actually-a-file")
	require.NoError(t, os.WriteFile(rootAsFile, []byte("not a dir"), 0o644))

	// Capture every entry the request-scoped logger emits. Narrate uses
	// Info level (l.Info("narrate", ...)) and the Warn line uses Warn
	// level — DebugLevel observer captures both.
	core, recorded := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	r := gin.New()
	r.Use(requestIDStub("req-promote-fail"))
	// Inject the observer logger via logctx so trace.go's
	// logctx.From(c.Request.Context()) picks it up for the Warn line AND the
	// narrate emitter (the narrate emitter resolves logctx.From at Emit-time).
	r.Use(func(c *gin.Context) {
		ctx := logctx.Inject(c.Request.Context(), logger)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{
			Enabled:  true,
			RootPath: rootAsFile,
			Triggers: artifact.TriggerConfig{OnError: true},
		},
	))
	r.GET("/x", func(c *gin.Context) {
		// Synthetic 500 — triggers the deferred-bundle Promote attempt at
		// request-end. Promote will fail because RootPath is a file.
		c.AbortWithStatus(http.StatusInternalServerError)
	})

	w := httptest.NewRecorder()
	// NB: NO ?trace=1 and NO X-Midas-Trace header — must exercise the
	// auto-on-error / deferred path, not the eager path.
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	// The Promote failure must NOT propagate to the client — the request
	// completes normally with the handler's chosen status.
	require.Equal(t, http.StatusInternalServerError, w.Code,
		"Promote failure must not change the HTTP response")

	// Disk must be empty under tmpDir aside from the file we created.
	// Specifically, no bundle directory was created (Promote's MkdirAll failed).
	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "tmpDir should only contain the file we created")
	require.Equal(t, "artifacts-but-actually-a-file", entries[0].Name())
	require.False(t, entries[0].IsDir(), "the entry must still be the file, not a directory")

	// The Warn line MUST have been emitted with the underlying error and
	// request_id — that's the operator's correlation point now that
	// artifact_path is omitted from the narrate stream.
	warnLogs := recorded.FilterMessage("trace.bundle.promote_failed").All()
	require.Len(t, warnLogs, 1, "expected exactly one promote_failed Warn line")
	assert.Equal(t, zapcore.WarnLevel, warnLogs[0].Level)
	warnFields := warnLogs[0].ContextMap()
	assert.NotEmpty(t, warnFields["error"], "Warn line must carry the underlying error")
	assert.Equal(t, "req-promote-fail", warnFields["request_id"])
	assert.Equal(t, "on_error", warnFields["trigger"])

	// The response.sent narrate line MUST exist (every request emits one)
	// but MUST NOT carry artifact_path — that is the bug being pinned.
	narrateLogs := recorded.FilterMessage("narrate").All()
	var responseSent *observer.LoggedEntry
	for i := range narrateLogs {
		fields := narrateLogs[i].ContextMap()
		if phase, _ := fields["phase"].(string); phase == "response.sent" {
			responseSent = &narrateLogs[i]
			break
		}
	}
	require.NotNil(t, responseSent, "response.sent narrate line must be emitted on every request")

	respFields := responseSent.ContextMap()
	_, hasArtifactPath := respFields["artifact_path"]
	assert.False(t, hasArtifactPath,
		"response.sent must NOT carry artifact_path when Promote failed (got fields=%v)", respFields)
	// Sanity: the standard narrate fields must still be present so we're sure
	// we found the right line and the regression is specifically about
	// artifact_path, not a broader emit failure.
	assert.Equal(t, "error", respFields["outcome"],
		"response.sent on a 500 must carry outcome=error")
	assert.EqualValues(t, http.StatusInternalServerError, respFields["status"],
		"response.sent must carry the status field")
}

// TestTrace_OnError_HandlerPanic_StillBundles pins REVIEWER HIGH-4. With
// the trace middleware registered AFTER the recovery middleware (i.e.,
// recovery is OUTSIDE trace in the call stack), a handler panic propagates
// THROUGH trace's c.Next() back up to recovery. Pre-fix, trace's
// post-c.Next() block — which contains the deferred-bundle Promote/Close
// logic — was skipped entirely, leaking the in-memory buffers and (much
// worse) silently dropping the on-error bundle for the very requests most
// in need of post-mortem visibility.
//
// Post-fix the post-c.Next() block lives inside a defer with a recover()
// at the top; the bundle is finalised, the panic is re-raised so any
// outer recovery middleware still observes it, and the on-disk bundle
// directory exists with trigger="on_error".
//
// Verifies:
//   - The bundle directory exists on disk (the most important part —
//     pre-fix it did not).
//   - The manifest's trigger is "on_error".
//   - The manifest's outcome is "error" (panicked → forced error outcome
//     even before any outer recovery sets status to 500).
//   - gin's recovery middleware did its job and returned 500 to the client.
func TestTrace_OnError_HandlerPanic_StillBundles(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-panic-onerror"))
	// Recovery is registered FIRST, which means it runs FIRST in the chain
	// and therefore wraps every middleware registered after it. Any panic
	// from a downstream middleware or handler propagates back through
	// trace's c.Next() to here, where Recovery catches it.
	r.Use(gin.Recovery())
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{
			Enabled:  true,
			RootPath: root,
			Triggers: artifact.TriggerConfig{OnError: true},
		},
	))
	r.GET("/boom", func(c *gin.Context) {
		// Synthetic panic — simulates a nil pointer or any unrecovered
		// runtime fault inside the handler.
		panic("simulated handler panic for trace HIGH-4")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	// The defer-wrap re-raises the panic AFTER bundle finalisation so the
	// outer gin.Recovery() can still translate it into a 500. The test
	// relies on that re-raise to keep gin's recovery semantics intact.
	r.ServeHTTP(w, req)

	// gin.Recovery translates the panic into a 500.
	require.Equal(t, http.StatusInternalServerError, w.Code,
		"outer Recovery middleware must still see the panic after trace finalises the bundle")

	// Pre-fix: this assertion FAILED — no bundle on disk because trace's
	// post-c.Next() block was skipped by the panic.
	mfPath := findManifest(root)
	require.NotEmpty(t, mfPath,
		"on-error bundle must exist on disk even when the handler panics")

	// Manifest must reflect the on_error auto-trigger and the error outcome.
	body, err := os.ReadFile(mfPath)
	require.NoError(t, err)
	var mf artifact.Manifest
	require.NoError(t, json.Unmarshal(body, &mf))
	assert.Equal(t, "on_error", mf.Trigger,
		"panicking handler must promote the deferred bundle with trigger=on_error")
	assert.Equal(t, "error", mf.Outcome,
		"panicked request must record outcome=error in the manifest")
}

// ---------------------------------------------------------------------------
// Phase 2.B — auto-on-quality-flag trigger tests
// ---------------------------------------------------------------------------
//
// Behaviour matrix for Phase 2.B:
//
//   manual flag | qty_flag thr | qty count | on_error | status   | bundle?  | trigger
//   ------------|--------------|-----------|----------|----------|----------|----------------
//   yes         | any          | any       | any      | any      | yes      | header/query
//   no          | "warning"    | >=1       | any      | any      | yes      | on_quality_flag
//   no          | "warning"    | 0         | false    | <500     | no       | —
//   no          | "warning"    | 0         | true     | >=500    | yes      | on_error
//   no          | "warning"    | >=1       | true     | >=500    | yes      | on_quality_flag
//   no          | ""           | any       | any      | any      | no/eager | (no on_qf)
//
// The tests below pin the non-trivial rows. The on_error rows from Phase
// 2.A continue to apply unchanged.

// TestTrace_OnQualityFlag_AutoBundle_WhenThresholdExceeded — handler
// records 3 qualifying flags via the bundle's RecordQualityFlagCount API.
// Bundle MUST appear on disk and the manifest's trigger MUST be
// "on_quality_flag". Status is 200 (no on_error contribution); this row
// pins that quality_flag fires independently of the response code.
func TestTrace_OnQualityFlag_AutoBundle_WhenThresholdExceeded(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-qflag-fires"))
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{
			Enabled:  true,
			RootPath: root,
			Triggers: artifact.TriggerConfig{
				QualityFlagThreshold: "warning",
			},
		},
	))
	r.GET("/x", func(c *gin.Context) {
		// Synthetic cleaner hook — record 3 qualifying flags.
		b := artifact.From(c.Request.Context())
		require.NotNil(t, b, "deferred bundle must be on ctx when threshold configured")
		b.RecordQualityFlagCount(3)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	mfPath := findManifest(root)
	require.NotEmpty(t, mfPath, "bundle must be on disk when threshold exceeded")
	assert.Equal(t, "on_quality_flag", readManifestTrigger(t, root),
		"auto-triggered bundle must record trigger=on_quality_flag")
}

// TestTrace_OnQualityFlag_HandlerPanic_StillBundles is the on_quality_flag
// twin of TestTrace_OnError_HandlerPanic_StillBundles (REVIEWER HIGH-4).
// The Phase 2.A defer+recover() that wraps the post-c.Next() block is
// structurally trigger-agnostic: it finalises the deferred bundle no matter
// which auto-trigger ends up winning the precedence ladder. This test pins
// that contract for the on_quality_flag path so a future refactor that
// (e.g.) accidentally moves the Promote/Close logic out of the defer is
// caught for BOTH triggers, not just on_error. QA flagged this as
// MINOR Finding #1 — implicit coverage exists via the trigger-agnostic
// defer, but a dedicated test documents intent and tightens the seam.
//
// Setup mirrors the on_error panic test EXCEPT:
//   - Only QualityFlagThreshold is configured (OnError is OFF). Otherwise
//     the on_error fallback branch in trace.go's precedence ladder could
//     mask a regression in the on_quality_flag branch — both branches
//     would fire on a panic and on_quality_flag would still "win" by
//     precedence, hiding the on_error fallback even working.
//   - The handler records 2 qualifying flags via the bundle API BEFORE
//     panicking. Without a non-zero count the on_quality_flag branch
//     short-circuits (count==0) and no auto-trigger fires, so we'd be
//     testing the dissolve path instead of the promote path.
//
// Verifies:
//   - The bundle directory exists on disk (the panic must NOT skip Promote).
//   - The manifest's trigger is "on_quality_flag" (NOT "on_error" — with
//     OnError off there is no on_error fallback to win precedence; this
//     pins that the panic forcing respOutcome=error doesn't accidentally
//     route to a non-existent on_error trigger).
//   - The manifest's outcome is "error" (panic forces it via the defer's
//     `panicked || status>=500` check, even when no outer recovery has yet
//     translated the panic to a 500 response).
//   - gin.Recovery — registered OUTSIDE trace, mirroring the on_error
//     panic test — still translates the re-raised panic into a 500.
func TestTrace_OnQualityFlag_HandlerPanic_StillBundles(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-panic-onqualityflag"))
	// Recovery is registered FIRST (outside trace), so a panic from the
	// handler propagates back through trace's c.Next() to here. Pre-fix,
	// trace's post-c.Next() block — including Promote — was skipped.
	// Post-fix, the defer captures the panic, runs Promote, then re-raises
	// for Recovery to translate into a 500.
	r.Use(gin.Recovery())
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{
			Enabled:  true,
			RootPath: root,
			Triggers: artifact.TriggerConfig{
				// ONLY on_quality_flag — see test doc above for why we
				// deliberately leave OnError off.
				QualityFlagThreshold: "warning",
			},
		},
	))
	r.GET("/boom", func(c *gin.Context) {
		// Record qualifying flags BEFORE panicking so the deferred bundle
		// has a non-zero count when the defer's precedence ladder runs.
		// Without this the on_quality_flag branch's `count > 0` guard
		// would short-circuit and no auto-trigger would fire.
		if b := artifact.From(c.Request.Context()); b != nil {
			b.RecordQualityFlagCount(2)
		}
		// Synthetic panic — same shape as the on_error panic test.
		panic("synthetic handler panic for trace HIGH-4 (on_quality_flag)")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	r.ServeHTTP(w, req)

	// gin.Recovery translates the re-raised panic into a 500.
	require.Equal(t, http.StatusInternalServerError, w.Code,
		"outer Recovery middleware must still see the re-raised panic")

	// Bundle directory must exist on disk despite the panic.
	mfPath := findManifest(root)
	require.NotEmpty(t, mfPath,
		"on_quality_flag bundle must exist on disk even when the handler panics")

	// Manifest must reflect the on_quality_flag auto-trigger and the error
	// outcome forced by the panic.
	body, err := os.ReadFile(mfPath)
	require.NoError(t, err)
	var mf artifact.Manifest
	require.NoError(t, json.Unmarshal(body, &mf))
	assert.Equal(t, "on_quality_flag", mf.Trigger,
		"panicking handler that pre-recorded flags must promote with trigger=on_quality_flag")
	assert.Equal(t, "error", mf.Outcome,
		"panicked request must record outcome=error in the manifest")
}

// TestTrace_OnQualityFlag_NoBundle_WhenUnderThreshold — handler records
// zero qualifying flags, status 200, on_error off. NO bundle directory may
// be created. This is the dominant production path when the trigger is
// configured: most requests see flag counts below threshold and the
// deferred bundle dissolves cleanly.
func TestTrace_OnQualityFlag_NoBundle_WhenUnderThreshold(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-qflag-zero"))
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{
			Enabled:  true,
			RootPath: root,
			Triggers: artifact.TriggerConfig{
				QualityFlagThreshold: "warning",
			},
		},
	))
	r.GET("/x", func(c *gin.Context) {
		// Cleaner ran but found nothing severe enough — count=0.
		b := artifact.From(c.Request.Context())
		require.NotNil(t, b, "deferred bundle must be on ctx even when count stays 0")
		// Intentionally no RecordQualityFlagCount call — equivalent to count=0.
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Empty(t, entries,
		"non-flagging request must NOT leave files on disk; got %v", entries)
}

// TestTrace_OnQualityFlag_Disabled_NoBundle — threshold is empty (default).
// Handler records 5 flags but the trigger is disabled. NO bundle must be
// created. Pins the off-by-default invariant: enabling artifact_store alone
// (without setting quality_flag_threshold) does NOT auto-capture on flags.
func TestTrace_OnQualityFlag_Disabled_NoBundle(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-qflag-disabled"))
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{
			Enabled:  true,
			RootPath: root,
			// QualityFlagThreshold NOT set — defaults to "".
		},
	))
	r.GET("/x", func(c *gin.Context) {
		// No deferred bundle exists, so artifact.From returns nil.
		// RecordQualityFlagCount on nil is a no-op (per nil-safety contract).
		b := artifact.From(c.Request.Context())
		assert.Nil(t, b, "no bundle expected when no trigger is configured")
		b.RecordQualityFlagCount(5) // must not panic on nil
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	assert.Empty(t, entries,
		"disabled trigger must NOT auto-capture; got %v", entries)
}

// TestTrace_OnQualityFlag_PrecedenceOverOnError — request returns 500 AND
// quality_flag_count exceeds threshold. Both auto-triggers fire; quality
// flag MUST win because it's more diagnostic (the flag list points at the
// suspicious upstream data, the 5xx alone just says "something failed").
//
// Pre-test invariant: bundle is opened deferred (no manual flag), the
// handler records flags AND returns 500.
func TestTrace_OnQualityFlag_PrecedenceOverOnError(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-precedence-qf"))
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{
			Enabled:  true,
			RootPath: root,
			Triggers: artifact.TriggerConfig{
				OnError:              true,
				QualityFlagThreshold: "warning",
			},
		},
	))
	r.GET("/x", func(c *gin.Context) {
		b := artifact.From(c.Request.Context())
		require.NotNil(t, b)
		b.RecordQualityFlagCount(2)
		c.AbortWithStatus(http.StatusInternalServerError)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)

	trigger := readManifestTrigger(t, root)
	require.NotEmpty(t, trigger, "bundle must exist (both triggers fired)")
	assert.Equal(t, "on_quality_flag", trigger,
		"on_quality_flag must outrank on_error in the precedence ladder")
}

// TestTrace_Manual_PrecedenceOverOnQualityFlag — manual flag + 5+ flags
// recorded + threshold configured. Manual must STILL win so debug
// sessions stay attributable to the operator who flipped the flag, even
// when the request happens to also tickle the quality-flag trigger.
func TestTrace_Manual_PrecedenceOverOnQualityFlag(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-manual-vs-qf"))
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{
			Enabled:  true,
			RootPath: root,
			Triggers: artifact.TriggerConfig{
				QualityFlagThreshold: "warning",
			},
		},
	))
	r.GET("/x", func(c *gin.Context) {
		b := artifact.From(c.Request.Context())
		require.NotNil(t, b)
		b.RecordQualityFlagCount(5)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x?trace=1", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	trigger := readManifestTrigger(t, root)
	require.NotEmpty(t, trigger, "bundle must exist (eager manual path)")
	assert.NotEqual(t, "on_quality_flag", trigger,
		"manifest trigger must NOT be on_quality_flag when manual flag was also set")
	assert.Contains(t, []string{"query", "header"}, trigger,
		"manifest trigger must be the manual source (query/header)")
}

// TestTrace_OnQualityFlag_PromoteOnce — both on_quality_flag and on_error
// fire on the same request. Promote must be called EXACTLY ONCE, not twice.
// We assert this indirectly by checking the manifest's trigger is the
// higher-precedence value (on_quality_flag) — a double-call would either
// overwrite the trigger or no-op via the deferred-flag check; either way
// the FIRST trigger value chosen at the precedence ladder must be the one
// that lands in the manifest.
//
// This also incidentally pins that the bundle directory exists exactly
// once (not duplicated under two timestamps), which a buggy double-promote
// could conceivably produce if paths weren't deterministic.
func TestTrace_OnQualityFlag_PromoteOnce(t *testing.T) {
	root := t.TempDir()

	r := gin.New()
	r.Use(requestIDStub("req-promote-once"))
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{
			Enabled:  true,
			RootPath: root,
			Triggers: artifact.TriggerConfig{
				OnError:              true,
				QualityFlagThreshold: "warning",
			},
		},
	))
	r.GET("/x", func(c *gin.Context) {
		b := artifact.From(c.Request.Context())
		require.NotNil(t, b)
		b.RecordQualityFlagCount(7)
		c.AbortWithStatus(http.StatusInternalServerError)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)

	// EXACTLY ONE manifest must exist on disk — not two.
	manifestCount := 0
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && info != nil && filepath.Base(p) == "00-manifest.json" {
			manifestCount++
		}
		return nil
	})
	assert.Equal(t, 1, manifestCount,
		"a single Promote call must produce exactly one bundle directory + manifest")

	// And that single manifest must carry the precedence-winning trigger.
	assert.Equal(t, "on_quality_flag", readManifestTrigger(t, root),
		"single Promote call must use the precedence-winning trigger")
}

// TestTrace_OnQualityFlag_PromotedLogLine pins REVIEWER MEDIUM-2: after a
// successful auto-trigger Promote, the trace middleware MUST emit a host-log
// Info line keyed "trace.bundle.promoted" so operators tailing the host log
// stream can see WHICH requests created bundles today and WHY without
// walking the artifacts directory or grepping 99-narrate.jsonl files inside
// each bundle. The line must carry request_id, trigger, and artifact_path
// — symmetrical with the existing trace.bundle.promote_failed Warn shape.
func TestTrace_OnQualityFlag_PromotedLogLine(t *testing.T) {
	root := t.TempDir()

	// Capture every entry the request-scoped logger emits at Info+ so we
	// see the new promoted line. Debug level is fine — Info entries pass through.
	core, recorded := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)

	r := gin.New()
	r.Use(requestIDStub("req-promoted-line"))
	// Inject the observer logger via logctx so trace.go's
	// logctx.From(c.Request.Context()) picks it up for the Info line.
	r.Use(func(c *gin.Context) {
		ctx := logctx.Inject(c.Request.Context(), logger)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{
			Enabled:  true,
			RootPath: root,
			Triggers: artifact.TriggerConfig{
				QualityFlagThreshold: "warning",
			},
		},
	))
	r.GET("/x", func(c *gin.Context) {
		// Fire the on_quality_flag auto-trigger by recording flags via the
		// bundle API directly (cleaner integration is exercised in the
		// datacleaner package tests).
		b := artifact.From(c.Request.Context())
		require.NotNil(t, b, "deferred bundle must be on ctx when threshold configured")
		b.RecordQualityFlagCount(2)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	// Bundle must exist on disk (sanity — without this the promoted line
	// wouldn't be expected to fire).
	mfPath := findManifest(root)
	require.NotEmpty(t, mfPath, "bundle must be on disk for the promote-success path")

	// The promoted Info line MUST have been emitted with request_id,
	// trigger=on_quality_flag, and artifact_path. We FilterMessage by the
	// stable greppable identifier — the symmetric shape with
	// trace.bundle.promote_failed (Warn) gives operators one field set to
	// learn for both outcomes.
	infoLogs := recorded.FilterMessage("trace.bundle.promoted").All()
	require.Len(t, infoLogs, 1,
		"expected exactly one trace.bundle.promoted Info line on successful auto-promote")
	entry := infoLogs[0]
	assert.Equal(t, zapcore.InfoLevel, entry.Level,
		"trace.bundle.promoted must be Info (not Debug) so default-level operators see it")

	fields := entry.ContextMap()
	assert.Equal(t, "req-promoted-line", fields["request_id"],
		"trace.bundle.promoted must carry request_id for correlation")
	assert.Equal(t, "on_quality_flag", fields["trigger"],
		"trace.bundle.promoted must carry the auto-trigger name")
	artifactPath, ok := fields["artifact_path"].(string)
	require.True(t, ok, "artifact_path must be a string field; got %T", fields["artifact_path"])
	assert.NotEmpty(t, artifactPath,
		"trace.bundle.promoted must carry artifact_path so operators can navigate to the bundle directory")
}

// TestTrace_OnError_PromotedLogLine — symmetry pin: the promoted Info
// line MUST also fire for the on_error auto-trigger (not just
// on_quality_flag). Otherwise operators would only see promote events
// for the newer trigger and silently miss every on_error capture, which
// is the more common production trigger.
func TestTrace_OnError_PromotedLogLine(t *testing.T) {
	root := t.TempDir()

	core, recorded := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)

	r := gin.New()
	r.Use(requestIDStub("req-promoted-onerror"))
	r.Use(func(c *gin.Context) {
		ctx := logctx.Inject(c.Request.Context(), logger)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{
			Enabled:  true,
			RootPath: root,
			Triggers: artifact.TriggerConfig{OnError: true},
		},
	))
	r.GET("/x", func(c *gin.Context) {
		c.AbortWithStatus(http.StatusInternalServerError)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.NotEmpty(t, findManifest(root), "bundle must be on disk for the on_error promote-success path")

	infoLogs := recorded.FilterMessage("trace.bundle.promoted").All()
	require.Len(t, infoLogs, 1,
		"expected exactly one trace.bundle.promoted Info line on successful on_error auto-promote")
	fields := infoLogs[0].ContextMap()
	assert.Equal(t, "on_error", fields["trigger"],
		"on_error path must record trigger=on_error in trace.bundle.promoted")
}

// TestTrace_OnError_PromoteFailed_NoPromotedLogLine pins the negative
// case: when Promote() FAILS (mkdir error), the promoted Info line MUST
// NOT fire — the existing trace.bundle.promote_failed Warn line is the
// correlation point for that case. Emitting both would mislead operators
// into thinking the bundle landed on disk.
func TestTrace_OnError_PromoteFailed_NoPromotedLogLine(t *testing.T) {
	tmpDir := t.TempDir()
	rootAsFile := filepath.Join(tmpDir, "artifacts-but-actually-a-file")
	require.NoError(t, os.WriteFile(rootAsFile, []byte("not a dir"), 0o644))

	core, recorded := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	r := gin.New()
	r.Use(requestIDStub("req-promoted-fail"))
	r.Use(func(c *gin.Context) {
		ctx := logctx.Inject(c.Request.Context(), logger)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Use(middleware.TraceMiddleware(
		narrate.Config{Enabled: true, SampleRate: 1.0},
		artifact.Config{
			Enabled:  true,
			RootPath: rootAsFile,
			Triggers: artifact.TriggerConfig{OnError: true},
		},
	))
	r.GET("/x", func(c *gin.Context) {
		c.AbortWithStatus(http.StatusInternalServerError)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)

	// promote_failed Warn MUST have fired (sanity — confirms the
	// promote-failure branch was actually exercised).
	require.NotEmpty(t, recorded.FilterMessage("trace.bundle.promote_failed").All(),
		"promote-failure branch must have fired for this test to be meaningful")
	// And the promoted Info line MUST NOT have fired — otherwise log
	// readers would chase a non-existent artifact_path.
	assert.Empty(t, recorded.FilterMessage("trace.bundle.promoted").All(),
		"trace.bundle.promoted must NOT fire when Promote failed")
}
