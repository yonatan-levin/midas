package artifact

import (
	"bytes"
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

// defaultPendingBytesCap is the per-bundle in-memory buffer ceiling for
// deferred bundles when Config.PendingBytesCap is unset. 10 MiB matches the
// brief and is roughly 2x the worst-case streamed payload for a single
// happy-path valuation request (snapshots + JSONL streams).
const defaultPendingBytesCap = int64(10) << 20

// MaxStreamLineBytes is the hard upper bound on a single stream-line write
// (one zap entry teed via BundleSink, or one direct AppendStream caller).
// Lines that exceed this cap are dropped at AppendStream / bufferStream entry
// and counted via Bundle.oversizeLines so the manifest's notes can surface
// them at Close()-time. 256 KiB is large enough for any reasonable structured
// log line (including the biggest narrate entry we produce in production —
// ~8 KiB) but small enough that a single rogue Debug payload (e.g. the full
// 5 MiB SEC company-facts response logged with zap.Any) cannot evict the
// entire deferred buffer in one shot. See REVIEWER finding HIGH-3 for the
// regression scenario this bound prevents.
//
// Exported so BundleSink (zap_core.go) can short-circuit oversized entries
// before serialization if a future optimization wants to skip the
// EncodeEntry cost on giant payloads. Today the check lives only in
// AppendStream / bufferStream — the simpler placement that catches both
// the BundleSink path and direct AppendStream callers in one place.
const MaxStreamLineBytes = 256 * 1024

// Trigger names how a bundle was opened. Used in the manifest to let
// consumers tell apart manual debugging from future auto-triggered captures.
type Trigger string

const (
	// TriggerHeader — request had X-Midas-Trace: 1.
	TriggerHeader Trigger = "header"
	// TriggerQuery — request had ?trace=1.
	TriggerQuery Trigger = "query"
	// TriggerOnError — request returned HTTP status >=500 with the
	// auto-on-error trigger enabled. Set on the manifest at Promote()-time
	// for bundles opened in deferred mode (Phase 2.A; see spec §13).
	// Manual triggers (header/query) take precedence: when both an opt-in
	// flag AND an error happen for the same request, the manifest's trigger
	// stays "header"/"query" so debug sessions remain attributable.
	TriggerOnError Trigger = "on_error"
	// TriggerOnQualityFlag — request emitted at least one data-cleaner flag
	// at or above the configured severity threshold (Phase 2.B; see spec
	// §13.B). Set on the manifest at Promote()-time for bundles opened in
	// deferred mode. Precedence (lowest to highest):
	//   on_error  <  on_quality_flag  <  manual (header/query)
	// Quality flags outrank on_error because a 500 alone tells you the
	// request failed; a quality flag tells you WHY the upstream data was
	// suspicious — which is more actionable for postmortem reading.
	TriggerOnQualityFlag Trigger = "on_quality_flag"
)

// TriggerConfig groups the per-request conditions that may open a bundle.
// Phase 1 honoured only the manual flag; Phase 2.A adds OnError; Phase 2.B
// adds QualityFlagThreshold. The shape mirrors config.ArtifactTriggers so
// callers can copy field-by-field without pulling the config package into
// artifact's import graph.
type TriggerConfig struct {
	// OnError: when true, requests that return HTTP status >=500 promote
	// their in-memory deferred bundle to disk even without an opt-in flag.
	OnError bool

	// QualityFlagThreshold: when non-empty, requests whose data-cleaner
	// raises one or more flags at or above this severity promote their
	// in-memory deferred bundle to disk (Phase 2.B). Valid values mirror
	// the FlagSeverity vocabulary (info / low / warning / medium / high /
	// critical); empty string disables the trigger. The artifact package
	// itself stays free of severity-ranking semantics — the cleaner reads
	// the threshold via Bundle.QualityFlagThreshold and reports the
	// qualifying-flag count via Bundle.RecordQualityFlagCount.
	QualityFlagThreshold string
}

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

	// PendingBytesCap is the per-bundle in-memory buffer ceiling for
	// deferred (auto-on-error) bundles (Phase 2.A). When the buffered
	// snapshot + stream payload would exceed this cap, oldest snapshots are
	// dropped first; counts surface in the manifest's notes at promote-time.
	// Only consulted by deferred bundles — eager bundles write straight to
	// disk and are unaffected. Default 10 MiB.
	PendingBytesCap int64

	// Triggers selects which per-request conditions open a bundle.
	// Manual (header/query) is always honoured by the trace middleware
	// regardless of this struct. OnError enables Phase 2.A's auto-trigger.
	Triggers TriggerConfig

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
	// oversizeLines counts stream-line writes that were rejected at
	// AppendStream / bufferStream entry because their byte length exceeded
	// MaxStreamLineBytes. Surfaces in the manifest's notes at Close-time
	// alongside dropped/writeErrors so postmortem readers can distinguish
	// "buffer full so we evicted oldest" (queue_drops) from "one rogue line
	// was too big to ever accept" (oversize_lines). REVIEWER HIGH-3 fix.
	oversizeLines atomic.Int64
	requestID     string

	// mu protects the streams cache. AppendStream uses cached file handles
	// so we don't pay open() per line for the ~17 narrate lines + potentially
	// hundreds of debug lines per request.
	mu      sync.Mutex
	streams map[string]*os.File

	// deferred-mode state (Phase 2.A — auto-on-error).
	//
	// When deferred is true, Snapshot/SnapshotRaw/AppendStream buffer their
	// payloads into pendingJobs / pendingStreams instead of writing to disk.
	// At request-end the trace middleware calls Promote() (HTTP status >=500)
	// to flush the buffers, or Close() (status <500) to drop them.
	//
	// pendingMu serialises access to the buffer fields; it is independent of
	// `mu` so a deferred AppendStream doesn't contend with the eager-mode
	// streams cache lock. Promote acquires pendingMu, drains the buffers,
	// flips deferred=false, and releases — late-arriving Snapshots that hit
	// pendingMu after the flip are forwarded to the eager queue.
	deferred atomic.Bool
	// promoted is set to true under pendingMu by Promote() AFTER the worker
	// goroutine has been spawned and BEFORE deferred is flipped to false.
	// Close() reads promoted under pendingMu to decide between the deferred-
	// dissolve path (no worker exists, just GC the buffers) and the eager-
	// finalize path (close(queue) + worker.Wait()). Required because
	// Close()/Promote() can race and reading `deferred` alone is not enough:
	// if Close arrives between Promote spawning the worker and Promote
	// flipping deferred=false, Close would take the dissolve path, return
	// without closing the queue, and leak the worker goroutine forever.
	// REVIEWER HIGH-2 fix.
	promoted       atomic.Bool
	pendingMu      sync.Mutex
	pendingJobs    []snapshotJob
	pendingStreams map[string]*bytes.Buffer
	// pendingBytes tracks the cumulative size (snapshot bodies + stream
	// bytes) currently held in memory for the deferred buffer. Bounded by
	// pendingCap; overflow drops oldest snapshot jobs first, then refuses
	// new stream lines.
	pendingBytes int64
	pendingCap   int64
	queueCap     int

	// Phase 2.B — auto-on-quality-flag trigger.
	//
	// qualityFlagThreshold is the severity floor configured at construction
	// time (copied from Config.Triggers.QualityFlagThreshold). The cleaner
	// reads this via QualityFlagThreshold() to decide which flags qualify;
	// stored as a plain string so the artifact package stays free of the
	// FlagSeverity vocabulary (which lives in core/entities).
	//
	// qualityFlagCount accumulates the count of qualifying flags reported
	// by the cleaner via RecordQualityFlagCount. The trace middleware reads
	// it post-c.Next() via QualityFlagCount() and decides whether to call
	// Promote(TriggerOnQualityFlag). atomic.Int64 because the cleaner may
	// run from different goroutines than the middleware in future fan-out
	// designs (today they share a goroutine, but pinning it now keeps the
	// contract honest under -race).
	qualityFlagThreshold string
	qualityFlagCount     atomic.Int64
}

// snapshotJob is the unit of work passed from Snapshot() (request-thread,
// non-blocking) to the bundle's background worker (writes to disk).
type snapshotJob struct {
	phase    string // narrate phase name, used for the manifest row
	filename string // file basename, e.g. "10-clean-input.json"
	data     []byte // ready-to-write bytes (already redacted/marshalled)
	pathsRed []string
}

// sanitizeTickerDir scrubs a ticker so it's safe to use as a single
// directory-name segment: replaces path separators and parent-traversal
// sequences with underscores so a malicious ticker can't escape the bundle
// root. Empty ticker → empty string (callers fall back to "_no-ticker").
// Used by both OpenBundle (initial creation) and SetTicker (late-binding rename).
func sanitizeTickerDir(ticker string) string {
	s := ticker
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, "..", "_")
	return s
}

// OpenBundle creates the on-disk directory for a request and returns the
// Bundle handle. Returns nil + nil error when cfg.Enabled is false (so callers
// can blindly defer Close on a nil bundle).
//
// Path layout: <root>/<UTC-date>/<TICKER>/req_<request_id>/
// When ticker is empty (request.received fires before parsing) it falls back
// to "_no-ticker"; the handler renames the directory to <TICKER>/ once the
// URL path param is parsed via SetTicker.
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

	tickerDir := sanitizeTickerDir(ticker)
	if tickerDir == "" {
		tickerDir = "_no-ticker"
	}

	// safeID drops any characters that aren't safe on common filesystems.
	safeID := safeRequestID(requestID)
	dateDir := time.Now().UTC().Format("2006-01-02")
	root := filepath.Join(cfg.RootPath, dateDir, tickerDir, "req_"+safeID)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("artifact: mkdir %s: %w", root, err)
	}

	pendingCap := cfg.PendingBytesCap
	if pendingCap <= 0 {
		pendingCap = defaultPendingBytesCap
	}

	b := &Bundle{
		root:                 root,
		manifest:             NewManifestBuilder(requestID, ticker, string(trigger), cfg.GitSHA, cfg.BuildVersion),
		queue:                make(chan snapshotJob, queueSize),
		requestID:            requestID,
		pendingCap:           pendingCap,
		queueCap:             queueSize,
		qualityFlagThreshold: cfg.Triggers.QualityFlagThreshold,
	}

	// Single background worker keeps the file-write order deterministic and
	// bounds the goroutine count regardless of bundle count.
	b.worker.Add(1)
	go b.runWorker()
	return b, nil
}

// OpenDeferredBundle constructs a Bundle in "deferred" mode for Phase 2.A's
// auto-on-error trigger (spec §13). Unlike OpenBundle:
//   - No directory is created on disk at construction time.
//   - No background worker is spawned. Snapshots and AppendStream calls
//     buffer into bounded in-memory queues instead of writing through.
//   - The bundle becomes visible on disk only when Promote() is called
//     (typically by the trace middleware at request-end when status >=500).
//   - If Close() runs before Promote(), the buffers are GC'd and nothing
//     ever lands on disk — the request looked clean and the bundle dissolves.
//
// Returns nil + nil error when cfg.Enabled is false (matches OpenBundle).
//
// The trigger argument is the trigger that WILL be stamped onto the manifest
// at promote-time. Today only TriggerOnError is meaningful here — the manual
// triggers (header/query) always go through OpenBundle (eager) because they
// know up-front the bundle should land on disk. Storing the intended trigger
// at construction lets the manifest's Trigger field be correct even if Snapshot
// runs before Promote (the manifest is built once at construction).
func OpenDeferredBundle(cfg Config, requestID, ticker string, trigger Trigger) (*Bundle, error) {
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
	pendingCap := cfg.PendingBytesCap
	if pendingCap <= 0 {
		pendingCap = defaultPendingBytesCap
	}

	tickerDir := sanitizeTickerDir(ticker)
	if tickerDir == "" {
		tickerDir = "_no-ticker"
	}

	safeID := safeRequestID(requestID)
	dateDir := time.Now().UTC().Format("2006-01-02")
	root := filepath.Join(cfg.RootPath, dateDir, tickerDir, "req_"+safeID)
	// NB: no os.MkdirAll here — Promote() does that. Pre-creating would
	// leave empty directories on disk for every request that does NOT 5xx,
	// defeating the point of deferred mode.

	b := &Bundle{
		root:                 root,
		manifest:             NewManifestBuilder(requestID, ticker, string(trigger), cfg.GitSHA, cfg.BuildVersion),
		queue:                make(chan snapshotJob, queueSize),
		requestID:            requestID,
		pendingCap:           pendingCap,
		queueCap:             queueSize,
		qualityFlagThreshold: cfg.Triggers.QualityFlagThreshold,
		// pendingStreams is allocated lazily on first AppendStream so the
		// common case (no stream activity) carries zero map overhead.
	}
	b.deferred.Store(true)

	// NB: worker is NOT started here. It is started by Promote() if/when
	// the bundle is promoted to disk. An unpromoted deferred bundle never
	// spawns a goroutine, keeping the runtime cost ~zero per non-erroring
	// request when the on_error trigger is enabled.
	return b, nil
}

// Root returns the on-disk directory of the bundle. The path may change
// during the bundle's lifetime if SetTicker renames the directory after URL
// parsing, so callers should re-read this rather than caching.
func (b *Bundle) Root() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.root
}

// SetTicker is called by the handler once the URL path param has been parsed.
// It updates BOTH the manifest's ticker field AND the on-disk directory name:
// the bundle moves from <root>/<date>/_no-ticker/req_<id>/ to
// <root>/<date>/<TICKER>/req_<id>/ so per-ticker forensics like
// `ls artifacts/<date>/TSM/` find the bundle.
//
// No-op when the bundle is nil, the ticker sanitizes to empty, the bundle is
// already at the target ticker, or after Close. On rename failure (rare —
// disk-full, permission), increments writeErrors so the manifest outcome
// degrades to "partial", and still updates the manifest ticker so the
// in-memory state is correct even if the directory is stuck at _no-ticker.
//
// File handles cached by AppendStream are closed before the rename (Windows
// won't rename a directory containing open files; on Unix it would work but
// the open handles would point to the old inode location). The streams map
// is cleared; the next AppendStream call reopens at the new path.
func (b *Bundle) SetTicker(ticker string) {
	if b == nil {
		return
	}
	sanitized := sanitizeTickerDir(ticker)
	if sanitized == "" {
		// Empty (or empty-after-sanitize) ticker — leave directory as-is and
		// don't overwrite the manifest with an empty value.
		return
	}

	b.mu.Lock()

	// No-op if the bundle is already in the target directory.
	currentParent := filepath.Base(filepath.Dir(b.root))
	if currentParent == sanitized {
		b.mu.Unlock()
		b.setManifestTicker(ticker)
		return
	}

	// Don't try to rename a closed bundle.
	if b.closed.Load() {
		b.mu.Unlock()
		return
	}

	// Compute target path: same date dir, swap ticker segment, same req_<id>.
	reqDirName := filepath.Base(b.root)
	dateDir := filepath.Dir(filepath.Dir(b.root))
	newRoot := filepath.Join(dateDir, sanitized, reqDirName)

	// Close cached stream file handles. Required on Windows (open files block
	// directory rename); harmless on Unix. The streams map is cleared so the
	// next AppendStream call reopens at the new path via b.root.
	for filename, f := range b.streams {
		if cerr := f.Close(); cerr != nil {
			b.writeErrors.Add(1)
		}
		delete(b.streams, filename)
	}

	// Ensure the new ticker directory exists.
	if err := os.MkdirAll(filepath.Dir(newRoot), 0o755); err != nil {
		b.writeErrors.Add(1)
		b.mu.Unlock()
		// Manifest still gets the ticker so the in-memory record is honest
		// about what the request was for, even if the dir is stuck.
		b.setManifestTicker(ticker)
		return
	}

	// Atomic rename. On Unix this is a single inode-level operation. On
	// Windows it's an atomic NTFS rename when source and destination are on
	// the same volume (which they are here — both under the same root).
	if err := os.Rename(b.root, newRoot); err != nil {
		b.writeErrors.Add(1)
		b.mu.Unlock()
		b.setManifestTicker(ticker)
		return
	}

	b.root = newRoot
	b.mu.Unlock()

	b.setManifestTicker(ticker)
}

// setManifestTicker updates the manifest's ticker field. Pulled into its own
// helper because the manifest mutex must not be held while b.mu is held — they
// are independent locks and nesting them would risk deadlock if any future
// call goes the other direction.
func (b *Bundle) setManifestTicker(ticker string) {
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
	b.dispatchSnapshot(job)
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
	b.dispatchSnapshot(job)
}

// dispatchSnapshot routes a snapshot job into either the deferred in-memory
// buffer (Phase 2.A) or the eager disk-bound queue, depending on the bundle's
// mode. Pulled into a single helper so Snapshot and SnapshotRaw share the
// dispatch logic and the deferred-vs-eager check is consistent.
func (b *Bundle) dispatchSnapshot(job snapshotJob) {
	if b.deferred.Load() {
		b.bufferSnapshot(job)
		return
	}
	select {
	case b.queue <- job:
		// queued for the worker
	default:
		// Drop on overflow rather than block. The dropped counter feeds into
		// the bundle outcome at Close().
		b.dropped.Add(1)
	}
}

// bufferSnapshot stores a snapshot job in the deferred buffer, enforcing both
// the per-bundle byte cap and the queue-count cap. On overflow the OLDEST
// snapshot is evicted (FIFO), which preserves the most recently emitted
// context — the entries closest in time to whatever triggered the bundle
// (typically a 5xx) are the most useful for postmortem reading.
//
// Re-checks deferred under pendingMu to handle a concurrent Promote() that
// flipped the bundle to eager mode between the caller's deferred.Load() and
// our lock acquisition. In that case we forward to the eager queue so the
// snapshot isn't silently dropped on mode-flip.
func (b *Bundle) bufferSnapshot(job snapshotJob) {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()

	// Race guard: Promote may have flipped deferred=false while we waited
	// for the lock. If so, redirect to the eager queue.
	if !b.deferred.Load() {
		select {
		case b.queue <- job:
		default:
			b.dropped.Add(1)
		}
		return
	}

	size := int64(len(job.data))

	// Bound by count: drop oldest snapshot if at queue cap.
	for len(b.pendingJobs) >= b.queueCap {
		oldest := b.pendingJobs[0]
		b.pendingBytes -= int64(len(oldest.data))
		// Slide the slice; aliases the backing array but the dropped element
		// becomes unreachable on the next append/realloc.
		b.pendingJobs = b.pendingJobs[1:]
		b.dropped.Add(1)
	}

	// Bound by bytes: drop oldest snapshots until the new job fits, or until
	// no snapshots remain to drop.
	for b.pendingBytes+size > b.pendingCap && len(b.pendingJobs) > 0 {
		oldest := b.pendingJobs[0]
		b.pendingBytes -= int64(len(oldest.data))
		b.pendingJobs = b.pendingJobs[1:]
		b.dropped.Add(1)
	}

	// If the new job alone is too big (no snapshots to evict made room),
	// drop it and account.
	if size > b.pendingCap {
		b.dropped.Add(1)
		return
	}

	b.pendingJobs = append(b.pendingJobs, job)
	b.pendingBytes += size
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

	// Hard upper bound on a single line. Catches both BundleSink-teed
	// entries (a runaway zap.Any payload) and direct callers. Done BEFORE
	// the deferred branch so the cap is enforced uniformly across modes.
	// REVIEWER HIGH-3: pre-fix, a 5 MiB log payload would blow through
	// the deferred buffer's pendingCap (10 MiB default) in two writes,
	// evicting all buffered snapshots to make room and silently destroying
	// the bundle's value as a debugging artifact.
	if int64(len(line)) > MaxStreamLineBytes {
		b.oversizeLines.Add(1)
		return nil
	}

	if b.deferred.Load() {
		return b.bufferStream(filename, line)
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

// bufferStream is the deferred-mode counterpart of the on-disk stream write
// path. Appends `line` (plus a trailing newline if missing) to an in-memory
// buffer keyed by filename. Bounded by Bundle.pendingCap; when adding the
// new line would overflow, oldest snapshots are evicted first to make room.
// If even with all snapshots evicted the line still won't fit, the line is
// dropped and the dropped counter increments.
//
// Stream lines themselves are never partially truncated mid-line (truncating
// JSONL would produce malformed JSON on disk after promote); we drop or
// keep at line granularity.
//
// As with bufferSnapshot, re-checks deferred under pendingMu so a concurrent
// Promote() that flipped to eager mode forwards the line to the on-disk
// AppendStream path instead of silently dropping it.
//
// AppendStream enforces MaxStreamLineBytes BEFORE dispatching here, so in
// practice the redundant cap check below is dead-code in normal flow. Kept
// as defense-in-depth so a future refactor that adds a direct caller to
// bufferStream cannot accidentally re-introduce the HIGH-3 regression.
func (b *Bundle) bufferStream(filename string, line []byte) error {
	// Defense-in-depth: AppendStream already enforced this, but keep the
	// guard local so bufferStream is safe even if a future call path skips
	// AppendStream.
	if int64(len(line)) > MaxStreamLineBytes {
		b.oversizeLines.Add(1)
		return nil
	}

	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()

	// Race guard: Promote may have flipped deferred=false while we waited.
	if !b.deferred.Load() {
		// Forward to the eager on-disk path. Release pendingMu first so we
		// don't hold both locks (mu + pendingMu) — they have no defined
		// ordering and nesting risks deadlock.
		b.pendingMu.Unlock()
		err := b.AppendStream(filename, line)
		b.pendingMu.Lock() // re-acquire so deferred return path is consistent
		return err
	}

	// Compute the byte cost: the line itself plus a trailing newline if
	// the caller didn't include one (matches eager AppendStream's contract).
	addNewline := len(line) == 0 || line[len(line)-1] != '\n'
	size := int64(len(line))
	if addNewline {
		size++
	}

	// If the single line is bigger than the entire buffer cap, drop it.
	// Truncating mid-line would produce malformed JSONL on disk, which is
	// strictly worse than dropping with a counter increment.
	if size > b.pendingCap {
		b.dropped.Add(1)
		return nil
	}

	// Bound by bytes: drop oldest snapshots first to make room. We don't
	// truncate stream buffers themselves — they're append-only line streams
	// where line ordering matters for postmortem reading.
	for b.pendingBytes+size > b.pendingCap && len(b.pendingJobs) > 0 {
		oldest := b.pendingJobs[0]
		b.pendingBytes -= int64(len(oldest.data))
		b.pendingJobs = b.pendingJobs[1:]
		b.dropped.Add(1)
	}

	// Still over cap and no snapshots left to evict — drop this line.
	if b.pendingBytes+size > b.pendingCap {
		b.dropped.Add(1)
		return nil
	}

	if b.pendingStreams == nil {
		b.pendingStreams = make(map[string]*bytes.Buffer)
	}
	buf, ok := b.pendingStreams[filename]
	if !ok {
		buf = &bytes.Buffer{}
		b.pendingStreams[filename] = buf
	}
	buf.Write(line)
	if addNewline {
		buf.WriteByte('\n')
	}
	b.pendingBytes += size
	return nil
}

// Promote materialises a deferred bundle to disk. Called by the trace
// middleware at request-end when the on-error trigger fires (Phase 2.A —
// HTTP status >=500 with logging.artifact_store.triggers.on_error=true).
//
// Behaviour:
//   - No-op when bundle is nil, already promoted, or already closed.
//   - mkdirs the bundle root (deferred mode skipped this at construction).
//   - Spawns the background snapshot worker (deferred mode skipped this).
//   - Sets the `promoted` flag under pendingMu so Close() can route to the
//     eager-finalize path even if it observes a stale `deferred` value.
//   - Drains buffered snapshots into the worker queue (blocking sends; the
//     queue is sized to match the cap, and the worker drains in parallel).
//   - Flushes buffered stream buffers via O_APPEND opens (one shot per
//     stream). Subsequent AppendStream calls in the same request fall
//     through to the eager path and append to the now-on-disk file.
//   - Updates the manifest's Trigger field. Useful when the deferred bundle
//     was opened with one trigger value but promoted with another (today
//     they always match — TriggerOnError — but defensive for future
//     trigger sources).
//
// Returns an error only when the mkdir fails. Drain/flush errors are
// absorbed and accounted via writeErrors so Close()'s outcome downgrade
// catches them. Callers can treat any error from Promote as "the bundle
// will not appear on disk" — they should still call Close() to release the
// in-memory state.
func (b *Bundle) Promote(trigger Trigger) error {
	if b == nil {
		return nil
	}

	b.pendingMu.Lock()
	if !b.deferred.Load() {
		b.pendingMu.Unlock()
		return nil // already promoted, idempotent
	}
	if b.closed.Load() {
		b.pendingMu.Unlock()
		return errors.New("artifact: promote on closed bundle")
	}

	// mkdir for the bundle root. We compute the parent up-front because the
	// MkdirAll target is the bundle's req_<id> directory, not just the
	// ticker dir, so a single MkdirAll suffices.
	if err := os.MkdirAll(b.root, 0o755); err != nil {
		b.pendingMu.Unlock()
		return fmt.Errorf("artifact: promote mkdir %s: %w", b.root, err)
	}

	// Snapshot the buffers and clear them under the lock.
	pendingJobs := b.pendingJobs
	pendingStreams := b.pendingStreams
	b.pendingJobs = nil
	b.pendingStreams = nil
	b.pendingBytes = 0

	// Update the manifest trigger BEFORE flipping deferred=false so any
	// concurrent Snapshot that races us sees a consistent state.
	b.manifest.mu.Lock()
	b.manifest.manifest.Trigger = string(trigger)
	b.manifest.mu.Unlock()

	// Spawn the worker now that we have a directory to write to.
	b.worker.Add(1)
	go b.runWorker()

	// Mark the bundle as promoted BEFORE flipping deferred=false. Close()
	// reads `promoted` under pendingMu to decide between the dissolve path
	// and the eager-finalize path; setting it here (still inside pendingMu)
	// ensures Close cannot observe (deferred=true && promoted=false) AFTER
	// the worker has been spawned. If we set it AFTER the deferred flip,
	// a Close racing in the gap would take the dissolve path, fail to
	// close(b.queue), and leak the worker forever (REVIEWER HIGH-2).
	b.promoted.Store(true)

	// Flip mode under the lock so concurrent Snapshot/AppendStream callers
	// that arrived BEFORE us see the new mode after they get the lock.
	b.deferred.Store(false)

	// Drain buffered snapshots into the worker queue WHILE STILL HOLDING
	// pendingMu. This serialises the channel-send against Close's
	// close(b.queue), which itself waits on pendingMu (via the
	// promoted-flag check) before progressing to the close. Without this
	// serialisation, Close could close(queue) while Promote is still
	// draining, causing a "send on closed channel" panic — the actual
	// race the REVIEWER HIGH-2 fix had to chase down.
	//
	// Holding pendingMu across the drain is bounded: the queue capacity
	// equals queueCap, len(pendingJobs) <= queueCap (enforced by
	// bufferSnapshot), and the worker is concurrently consuming. Worst
	// case the drain blocks briefly while the worker writes to disk;
	// pendingMu hold time stays in milliseconds, not seconds.
	for _, job := range pendingJobs {
		b.queue <- job
	}
	b.pendingMu.Unlock()

	// Flush each buffered stream by APPENDING the buffered bytes to the on-
	// disk file. We deliberately use O_APPEND|O_CREATE|O_WRONLY (NOT
	// os.WriteFile, which uses O_TRUNC) so that any concurrent eager
	// AppendStream call that beat us to the file — which is possible because
	// the eager path's AppendStream takes b.mu, not pendingMu, and may run
	// after we released pendingMu above — has its bytes preserved instead
	// of silently truncated. Pre-fix (REVIEWER HIGH-1):
	//   1. Promote unlocks pendingMu, eager AppendStream wakes, opens the
	//      file with O_APPEND|O_CREATE, writes "lineA\n", caches the handle.
	//   2. Promote's os.WriteFile here truncates the file to zero, writes the
	//      buffered content. lineA is gone.
	// The cached handle in b.streams still works (O_TRUNC reuses the inode
	// in place on Linux), so the post-promote AppendStream calls happily
	// append into the truncated file — making the data loss completely
	// invisible to the caller. The fix is to switch to append-mode here so
	// the worst case is a (lineA, buffered) ordering inversion rather than
	// data loss. We do NOT cache the file handle here — subsequent
	// AppendStream calls open their own handle via the eager path's handle
	// cache.
	for filename, buf := range pendingStreams {
		path := filepath.Join(b.root, filename)
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			b.writeErrors.Add(1)
			continue
		}
		if _, werr := f.Write(buf.Bytes()); werr != nil {
			b.writeErrors.Add(1)
		}
		if cerr := f.Close(); cerr != nil {
			b.writeErrors.Add(1)
		}
	}

	return nil
}

// runWorker is the bundle's background writer. Drains the queue serially so
// disk I/O ordering is deterministic.
func (b *Bundle) runWorker() {
	defer b.worker.Done()
	for job := range b.queue {
		// b.root may be mutated by SetTicker; snapshot it under mu to avoid
		// a data race. The window between read and WriteFile is tiny — if a
		// rename slips in, WriteFile will fail (old path gone) and writeErrors
		// will record the failure (downgrading manifest outcome to "partial").
		b.mu.Lock()
		path := filepath.Join(b.root, job.filename)
		b.mu.Unlock()
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
//
// For deferred bundles that were never promoted (Phase 2.A — no 5xx fired
// and no manual flag was present), Close drops the in-memory buffers and
// returns immediately without touching disk. Nothing was ever written, so
// there's nothing to finalise; the bundle dissolves cleanly.
//
// REVIEWER HIGH-2: the dissolve-vs-finalize decision must be observed under
// pendingMu (not via a bare deferred.Load()) so a Close that races a
// concurrent Promote sees the `promoted` flag atomically with respect to
// Promote's worker spawn. Reading deferred alone is racy: Promote spawns the
// worker, sets promoted=true, and flips deferred=false — all under pendingMu.
// Without the flag check, Close arriving between worker-spawn and
// deferred-flip would see deferred=true, take the dissolve path, return
// without close(b.queue), and leak the worker forever.
func (b *Bundle) Close() error {
	if b == nil {
		return nil
	}
	if !b.closed.CompareAndSwap(false, true) {
		return nil
	}

	// Decide deferred-dissolve vs eager-finalize under pendingMu so we
	// observe Promote() atomically. Promote sets `promoted=true` while
	// holding pendingMu and BEFORE releasing it; therefore by the time we
	// hold pendingMu here, either Promote ran fully (promoted=true,
	// deferred=false → eager finalize) or Promote has not yet started
	// (promoted=false, deferred=true → dissolve). The (promoted=true,
	// deferred=true) intermediate state inside Promote is invisible to us
	// because Promote holds pendingMu through that whole transition.
	b.pendingMu.Lock()
	dissolve := !b.promoted.Load() && b.deferred.Load()
	if dissolve {
		// Unpromoted deferred bundle — clean GC of the in-memory buffers.
		// We do NOT close(b.queue) because no worker was ever started;
		// closing would leave the channel un-drained but that's fine, GC
		// reclaims it. Skipping all disk I/O is the whole point of
		// Close-without-Promote: the request was uneventful and the bundle
		// should leave no trace.
		b.pendingJobs = nil
		b.pendingStreams = nil
		b.pendingBytes = 0
		b.pendingMu.Unlock()
		return nil
	}
	b.pendingMu.Unlock()

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
	// failures, stream flush errors, and oversize-line rejects all indicate
	// an incomplete capture; any degrades the outcome.
	dropped := b.dropped.Load()
	writeErrors := b.writeErrors.Load()
	oversize := b.oversizeLines.Load()
	if dropped > 0 || writeErrors > 0 || oversize > 0 {
		b.manifest.SetOutcome("partial")
		// Annotate the manifest so a reader of the bundle directory
		// immediately understands why outcome is "partial". The format is
		// stable so log-greppers and tooling can parse it.
		// REVIEWER HIGH-3: oversize_lines surfaces lines rejected by the
		// MaxStreamLineBytes guard so postmortem readers can distinguish
		// "buffer pressure evicted oldest" from "rogue line was too big".
		b.manifest.SetNotes(fmt.Sprintf("write_failures=%d queue_drops=%d oversize_lines=%d",
			writeErrors, dropped, oversize))
	}

	// Best effort — failure to write the manifest is logged only via the
	// returned-error swallow point. Caller has nowhere good to report it.
	// Snapshot b.root under mu in case a SetTicker is racing with Close.
	b.mu.Lock()
	root := b.root
	b.mu.Unlock()
	_ = b.manifest.Finalize(root)
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

// OversizeLines returns the count of stream-line writes (eager AppendStream
// or deferred bufferStream) rejected because their byte length exceeded
// MaxStreamLineBytes. Surfaced in the manifest's notes as oversize_lines=N
// at Close-time. REVIEWER HIGH-3 fix.
func (b *Bundle) OversizeLines() int64 {
	if b == nil {
		return 0
	}
	return b.oversizeLines.Load()
}

// QualityFlagThreshold returns the configured severity floor (Phase 2.B).
// The data cleaner reads this from the bundle attached to ctx so it can
// count flags that match or exceed the operator's chosen severity without
// pulling the artifact-package config into the cleaner. Empty string means
// the trigger is disabled — callers should skip their hook entirely.
//
// Nil-safe: nil bundle returns "".
func (b *Bundle) QualityFlagThreshold() string {
	if b == nil {
		return ""
	}
	return b.qualityFlagThreshold
}

// RecordQualityFlagCount adds n to the bundle's running count of qualifying
// flags (flags at or above QualityFlagThreshold). Negative values are
// ignored — they would silently disable the trigger by dragging the
// running total below zero, which is strictly worse than a no-op.
//
// Idempotent and concurrency-safe (atomic.Int64). Nil-safe so the cleaner
// can call this on `artifact.From(ctx)` without a nil check.
func (b *Bundle) RecordQualityFlagCount(n int) {
	if b == nil || n <= 0 {
		return
	}
	b.qualityFlagCount.Add(int64(n))
}

// QualityFlagCount returns the cumulative count of qualifying flags
// recorded via RecordQualityFlagCount. The trace middleware reads this
// post-c.Next() to decide whether to Promote with TriggerOnQualityFlag.
//
// Nil-safe: nil bundle returns 0.
func (b *Bundle) QualityFlagCount() int64 {
	if b == nil {
		return 0
	}
	return b.qualityFlagCount.Load()
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
