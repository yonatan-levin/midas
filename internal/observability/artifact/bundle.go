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
	root      string
	manifest  *ManifestBuilder
	queue     chan snapshotJob
	worker    sync.WaitGroup
	closed    atomic.Bool
	dropped   atomic.Int64 // count of snapshots dropped due to a full queue
	requestID string
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

// runWorker is the bundle's background writer. Drains the queue serially so
// disk I/O ordering is deterministic.
func (b *Bundle) runWorker() {
	defer b.worker.Done()
	for job := range b.queue {
		path := filepath.Join(b.root, job.filename)
		err := os.WriteFile(path, job.data, 0o644)
		if err != nil {
			// We can't reliably log here — the bundle has no zap.Logger and
			// the request context may already be done. Silent drop is the
			// least-bad option; the bundle's manifest will reflect the
			// missing phase row.
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

	// If snapshots were dropped, downgrade the bundle outcome to partial so
	// consumers know the on-disk record is incomplete.
	if b.dropped.Load() > 0 {
		b.manifest.SetOutcome("partial")
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
