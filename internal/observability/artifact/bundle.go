package artifact

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Trigger names how a bundle was opened. Used in the manifest to let
// consumers tell apart manual debugging from future auto-triggered captures.
type Trigger string

const (
	// TriggerHeader — request had X-Midas-Trace: 1.
	TriggerHeader Trigger = "header"
	// TriggerQuery — request had ?trace=1.
	TriggerQuery Trigger = "query"
)

// Config holds artifact-store knobs mirrored from
// LoggingConfig.ArtifactStore in config/config.yaml.
type Config struct {
	// Enabled is the master switch. When false, OpenBundle returns nil and
	// every Snapshot is a no-op even if the request had ?trace=1.
	Enabled bool

	// RootPath is the directory under which dated bundle subtrees are
	// created (default ./artifacts).
	RootPath string

	// RetentionDays is the maximum age of bundle directories before the
	// reaper sweeps them. 0 disables the age-based sweep.
	RetentionDays int

	// MaxTotalBytes is the soft cap for the entire bundle root tree.
	// When exceeded, the reaper evicts oldest bundles first. 0 disables.
	MaxTotalBytes int64

	// QueueSize is the bounded channel used by the snapshot worker. Bursty
	// traces will drop snapshots (logged + recorded as bundle outcome=partial)
	// rather than block the request thread. Default 256.
	QueueSize int

	// GitSHA / BuildVersion are stamped into every manifest so old bundles
	// can be replayed against the matching code revision. Read from build
	// flags / config at startup.
	GitSHA       string
	BuildVersion string
}

// Bundle is the per-request, on-disk capture context. Created at request
// entry by the trace middleware (when triggered), attached to ctx, and
// finalised at request exit. All Snapshot calls dispatch to a background
// goroutine via a bounded queue — the request thread never blocks on disk.
type Bundle struct {
	root     string
	manifest *ManifestBuilder
	queue    chan snapshotJob
	worker   sync.WaitGroup
	closed   atomic.Bool
	// dropped counts snapshots discarded BEFORE reaching disk (queue full).
	// writeErrors counts snapshots that reached the worker but failed
	// os.WriteFile (disk-full, permission, removed root, etc).
	// Both are factored into the manifest's outcome at Close(): any non-zero
	// count downgrades a clean "ok" to "partial" and is annotated in
	// manifest.Notes so a reader of the bundle directory immediately knows
	// why the capture is incomplete.
	dropped     atomic.Int64
	writeErrors atomic.Int64
	requestID   string

	// mu protects the streams cache. AppendStream uses cached file handles
	// so we don't pay open() per line for the ~17 narrate lines + potentially
	// hundreds of debug lines per request.
	mu      sync.Mutex
	streams map[string]*os.File
}

// snapshotJob is the unit of work passed from Snapshot() (request-thread,
// non-blocking) to the bundle's background worker (writes to disk).
type snapshotJob struct {
	phase    string // narrate phase name, used for the manifest row
	filename string // file basename, e.g. "10-clean-input.json"
	data     []byte // ready-to-write bytes (already redacted/marshalled)
	pathsRed []string
}

// OpenBundle creates the on-disk directory for a request and returns the
// Bundle handle. Returns nil + nil error when cfg.Enabled is false (so callers
// can blindly defer Close on a nil bundle).
//
// Path layout: <root>/<UTC-date>/<TICKER>/req_<request_id>/
// When ticker is empty (request.received fires before parsing) it falls back
// to "_no-ticker"; the trace middleware can rename the directory once the
// handler stamps the ticker via SetTicker.
func OpenBundle(cfg Config, requestID, ticker string, trigger Trigger) (*Bundle, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.RootPath == "" {
		return nil, errors.New("artifact: empty RootPath")
	}
	if requestID == "" {
		return nil, errors.New("artifact: empty requestID")
	}

	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = 256
	}

	tickerDir := ticker
	if tickerDir == "" {
		tickerDir = "_no-ticker"
	}
	// Sanitise ticker: replace path separators so a malicious ticker can't
	// escape the bundle root. Tickers are URL path params so rare in practice.
	tickerDir = strings.ReplaceAll(tickerDir, "/", "_")
	tickerDir = strings.ReplaceAll(tickerDir, "\\", "_")
	tickerDir = strings.ReplaceAll(tickerDir, "..", "_")

	// safeID drops any characters that aren't safe on common filesystems.
	safeID := safeRequestID(requestID)
	dateDir := time.Now().UTC().Format("2006-01-02")
	root := filepath.Join(cfg.RootPath, dateDir, tickerDir, "req_"+safeID)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("artifact: mkdir %s: %w", root, err)
	}

	b := &Bundle{
		root:      root,
		manifest:  NewManifestBuilder(requestID, ticker, string(trigger), cfg.GitSHA, cfg.BuildVersion),
		queue:     make(chan snapshotJob, queueSize),
		requestID: requestID,
	}

	// Single background worker keeps the file-write order deterministic and
	// bounds the goroutine count regardless of bundle count.
	b.worker.Add(1)
	go b.runWorker()
	return b, nil
}

// Root returns the on-disk directory of the bundle.
func (b *Bundle) Root() string {
	if b == nil {
		return ""
	}
	return b.root
}

// SetTicker updates the ticker on the manifest after URL parsing. The on-disk
// directory is NOT renamed — that would invalidate paths captured in narrate
// lines. The mismatch is rare (ticker is parsed inside a few microseconds of
// the bundle being opened) and the manifest is the authoritative record.
func (b *Bundle) SetTicker(ticker string) {
	if b == nil {
		return
	}
	b.manifest.mu.Lock()
	b.manifest.manifest.Ticker = ticker
	b.manifest.mu.Unlock()
}

// SetOutcome records the request-level outcome (ok/partial/error) on the
// manifest. Sticky — a later "ok" never overrides an earlier "error".
func (b *Bundle) SetOutcome(outcome string) {
	if b == nil {
		return
	}
	b.manifest.SetOutcome(outcome)
}

// AddSchemaVersion records the on-disk schema version of a domain entity.
func (b *Bundle) AddSchemaVersion(name string, version int) {
	if b == nil {
		return
	}
	b.manifest.SetSchemaVersion(name, version)
}

// Snapshot enqueues a JSON-serialised snapshot of v under filename, attributed
// to phase. Returns immediately — actual marshal+write happens on the bundle
// worker goroutine. When the queue is full the snapshot is dropped (and the
// bundle outcome later degrades to "partial").
//
// filename should follow the spec convention "NN-name.json" so the directory
// reads in pipeline order under `ls`.
func (b *Bundle) Snapshot(_ context.Context, phase, filename string, v any) {
	if b == nil || b.closed.Load() {
		return
	}

	// Marshal happens here so a serialisation error surfaces synchronously
	// (the caller can react). Disk I/O is what we offload, not encoding.
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		// Snapshot failures are observability noise, not request errors.
		// Record a sentinel file so the bundle still names the failed phase.
		body = []byte(fmt.Sprintf(`{"snapshot_error":%q}`, err.Error()))
	}

	job := snapshotJob{
		phase:    phase,
		filename: filename,
		data:     body,
	}
	select {
	case b.queue <- job:
		// queued
	default:
		// Drop on overflow rather than block. The dropped counter feeds into
		// the bundle outcome at Close().
		b.dropped.Add(1)
	}
}

// SnapshotRaw enqueues raw bytes (no Marshal) under filename. Used by gateway
// adapters to capture upstream response bodies after JSON-key redaction. The
// pathsRed list is merged into the manifest's redactions_applied[].
func (b *Bundle) SnapshotRaw(_ context.Context, phase, filename string, body []byte, pathsRed []string) {
	if b == nil || b.closed.Load() {
		return
	}

	job := snapshotJob{
		phase:    phase,
		filename: filename,
		data:     body,
		pathsRed: pathsRed,
	}
	select {
	case b.queue <- job:
	default:
		b.dropped.Add(1)
	}
}

// AppendStream appends a single JSONL line to <bundleDir>/<filename>. Used
// by BundleSink (the zapcore.Core wrapper) to tee narrate + debug log
// entries into the bundle without going through the snapshot machinery,
// which assumes one-shot per-phase writes with manifest registration.
//
// The file handle is cached in b.streams so we don't pay open() per line:
// each request emits ~17 narrate lines and potentially hundreds of debug
// lines, so the cache is meaningful.
//
// Behaviour:
//   - No-op when b == nil (nil-receiver safety).
//   - No-op when bundle is closed (matches Snapshot's contract).
//   - Returns the underlying error on os.OpenFile / Write failure and
//     increments writeErrors so Close() can downgrade outcome to "partial"
//     and annotate the manifest.
//   - Appends a trailing newline if line doesn't already end in one (zap's
//     JSON encoder adds the newline, but defensive in case other callers
//     pass raw bytes).
func (b *Bundle) AppendStream(filename string, line []byte) error {
	if b == nil || b.closed.Load() {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// Re-check closed under the lock to avoid racing with Close(), which
	// also takes mu before draining the cache.
	if b.closed.Load() {
		return nil
	}

	f, ok := b.streams[filename]
	if !ok {
		path := filepath.Join(b.root, filename)
		opened, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			b.writeErrors.Add(1)
			return fmt.Errorf("artifact: open stream %s: %w", filename, err)
		}
		if b.streams == nil {
			b.streams = make(map[string]*os.File)
		}
		b.streams[filename] = opened
		f = opened
	}

	if _, err := f.Write(line); err != nil {
		b.writeErrors.Add(1)
		return fmt.Errorf("artifact: write stream %s: %w", filename, err)
	}
	if len(line) == 0 || line[len(line)-1] != '\n' {
		if _, err := f.Write([]byte{'\n'}); err != nil {
			b.writeErrors.Add(1)
			return fmt.Errorf("artifact: write newline %s: %w", filename, err)
		}
	}
	return nil
}

// runWorker is the bundle's background writer. Drains the queue serially so
// disk I/O ordering is deterministic.
func (b *Bundle) runWorker() {
	defer b.worker.Done()
	for job := range b.queue {
		path := filepath.Join(b.root, job.filename)
		err := os.WriteFile(path, job.data, 0o644)
		if err != nil {
			// Track write failures so Close() can mark the bundle outcome
			// "partial" and annotate the manifest. Pre-fix this branch
			// silently dropped the failure: a disk-full or permission
			// error left outcome="ok" with zero phases on disk, which
			// turned bundles into liars. We still don't have a zap.Logger
			// here (worker is goroutine-scoped); runtime visibility for
			// these failures is a follow-up — see TODO below.
			b.writeErrors.Add(1)
			// TODO: thread a *zap.Logger into OpenBundle so worker errors
			// can be Warn-logged at runtime, not just postmortem in the
			// manifest. Tracked as a follow-up — see REVIEWER notes for
			// HIGH 2 in the observability-narrative branch.
			continue
		}
		b.manifest.AddPhase(job.phase, []string{job.filename}, int64(len(job.data)))
		if len(job.pathsRed) > 0 {
			b.manifest.AddRedactions(job.pathsRed)
		}
	}
}

// Close stops the worker, flushes any queued jobs, finalises the manifest,
// and writes 00-manifest.json. Idempotent — safe to defer multiple times.
// Always returns nil; manifest write errors are absorbed (we'd have nowhere
// useful to surface them at request end).
func (b *Bundle) Close() error {
	if b == nil {
		return nil
	}
	if !b.closed.CompareAndSwap(false, true) {
		return nil
	}

	// Closing the queue lets the worker drain remaining jobs and exit.
	close(b.queue)
	b.worker.Wait()

	// Flush + close any cached AppendStream file handles. Done AFTER the
	// snapshot worker has drained so we don't race with in-flight Snapshots,
	// and BEFORE we read writeErrors so any close-time errors are reflected
	// in the outcome. Held under mu so a late AppendStream caller racing
	// with Close cleanly observes closed=true and no-ops.
	b.mu.Lock()
	for name, f := range b.streams {
		if err := f.Sync(); err != nil {
			b.writeErrors.Add(1)
			_ = err // best-effort: nowhere useful to surface this
		}
		if err := f.Close(); err != nil {
			b.writeErrors.Add(1)
			_ = err
		}
		delete(b.streams, name)
	}
	b.mu.Unlock()

	// Read the loss counters AFTER worker.Wait() AND stream flush so all
	// increments are observed. Queue-overflow drops, snapshot write
	// failures, and stream flush errors all indicate an incomplete capture;
	// any degrades the outcome.
	dropped := b.dropped.Load()
	writeErrors := b.writeErrors.Load()
	if dropped > 0 || writeErrors > 0 {
		b.manifest.SetOutcome("partial")
		// Annotate the manifest so a reader of the bundle directory
		// immediately understands why outcome is "partial". The format is
		// stable so log-greppers and tooling can parse it.
		b.manifest.SetNotes(fmt.Sprintf("write_failures=%d queue_drops=%d", writeErrors, dropped))
	}

	// Best effort — failure to write the manifest is logged only via the
	// returned-error swallow point. Caller has nowhere good to report it.
	_ = b.manifest.Finalize(b.root)
	return nil
}

// Dropped returns the count of snapshot jobs the worker discarded due to
// queue overflow. Zero means a clean bundle. Useful for tests and for
// surfacing partial captures in narrate lines.
func (b *Bundle) Dropped() int64 {
	if b == nil {
		return 0
	}
	return b.dropped.Load()
}

// WriteErrors returns the count of snapshot jobs the worker accepted but
// failed to persist to disk (os.WriteFile error — disk full, permission,
// removed root, etc). Zero means every queued snapshot reached the
// filesystem. Used by tests and surfaced in the manifest's notes.
func (b *Bundle) WriteErrors() int64 {
	if b == nil {
		return 0
	}
	return b.writeErrors.Load()
}

// safeRequestID strips characters that are problematic in directory names on
// common filesystems (Windows path separators, control chars, etc.). Returns
// the input lower-cased with unsafe chars replaced by '_'.
func safeRequestID(id string) string {
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			b.WriteRune('_')
		case r < 0x20:
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// bundleKey is the unexported context key for storing the per-request bundle.
type bundleKey struct{}

// Inject returns a child context carrying the bundle. nil bundle is allowed
// (lets callers blindly Inject without checking — From will return nil-safe).
func Inject(ctx context.Context, b *Bundle) context.Context {
	return context.WithValue(ctx, bundleKey{}, b)
}

// From retrieves the bundle from ctx, or returns nil if none is present.
// Snapshot() and SnapshotRaw() are nil-safe so callers don't need to check.
func From(ctx context.Context) *Bundle {
	if ctx == nil {
		return nil
	}
	if v, ok := ctx.Value(bundleKey{}).(*Bundle); ok {
		return v
	}
	return nil
}
