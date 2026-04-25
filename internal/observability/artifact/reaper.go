package artifact

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// reaperTickInterval is the cadence at which the reaper checks for expired
// bundles. Hard-coded — the spec calls for ~1-hour ticks; making this
// configurable would just expose footguns.
const reaperTickInterval = time.Hour

// Reaper periodically prunes the artifact bundle tree by age and total size.
// Idle when cfg.Enabled is false. Exposes Sweep() for direct invocation in
// unit tests.
type Reaper struct {
	cfg Config

	// onSweepDone signals one completed pass; tests can synchronise on this.
	onSweepDone chan struct{}

	stopOnce sync.Once
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewReaper constructs a reaper without starting it. Call Start to launch
// the background goroutine.
func NewReaper(cfg Config) *Reaper {
	return &Reaper{
		cfg:         cfg,
		onSweepDone: make(chan struct{}, 1),
		done:        make(chan struct{}),
	}
}

// Start launches the reaper goroutine. No-op when disabled. Returns
// immediately. Call Stop on shutdown.
func (r *Reaper) Start(parent context.Context) {
	if r == nil || !r.cfg.Enabled {
		// Mark done so a paranoid Stop call doesn't block.
		close(r.done)
		return
	}

	ctx, cancel := context.WithCancel(parent)
	r.cancel = cancel

	go r.loop(ctx)
}

// Stop signals the goroutine to exit and waits for it. Idempotent.
func (r *Reaper) Stop() {
	if r == nil {
		return
	}
	r.stopOnce.Do(func() {
		if r.cancel != nil {
			r.cancel()
		}
	})
	<-r.done
}

// Sweep runs one prune pass. Public so tests can drive it deterministically
// (avoiding the 1-hour tick).
func (r *Reaper) Sweep() error {
	if r == nil || !r.cfg.Enabled {
		return nil
	}
	if r.cfg.RootPath == "" {
		return errors.New("reaper: empty RootPath")
	}

	now := time.Now()
	if err := r.sweepByAge(now); err != nil {
		return err
	}
	return r.sweepBySize()
}

func (r *Reaper) loop(ctx context.Context) {
	defer close(r.done)

	t := time.NewTicker(reaperTickInterval)
	defer t.Stop()

	// Run an immediate sweep on startup, then on each tick.
	_ = r.Sweep()
	r.signalDone()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = r.Sweep()
			r.signalDone()
		}
	}
}

func (r *Reaper) signalDone() {
	select {
	case r.onSweepDone <- struct{}{}:
	default:
	}
}

// sweepByAge removes bundle directories older than retention_days.
//
// Bundle layout: <root>/<UTC-date>/<TICKER>/req_<id>/. We use the on-disk
// date directory name as the age signal — cheaper than stat'ing thousands
// of req directories. Date dirs older than the cutoff are deleted whole.
func (r *Reaper) sweepByAge(now time.Time) error {
	if r.cfg.RetentionDays <= 0 {
		return nil
	}
	cutoff := now.UTC().AddDate(0, 0, -r.cfg.RetentionDays)
	entries, err := os.ReadDir(r.cfg.RootPath)
	if err != nil {
		// Missing root is fine — nothing to reap.
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Date directories are named YYYY-MM-DD.
		t, err := time.Parse("2006-01-02", e.Name())
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			_ = os.RemoveAll(filepath.Join(r.cfg.RootPath, e.Name()))
		}
	}
	return nil
}

// sweepBySize evicts oldest req-directories until total bytes are under cap.
func (r *Reaper) sweepBySize() error {
	if r.cfg.MaxTotalBytes <= 0 {
		return nil
	}

	type bundleStat struct {
		path  string
		bytes int64
		mtime time.Time
	}

	var bundles []bundleStat
	var total int64

	// Walk: <root>/<date>/<ticker>/req_*/
	dateDirs, err := os.ReadDir(r.cfg.RootPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, dd := range dateDirs {
		if !dd.IsDir() {
			continue
		}
		datePath := filepath.Join(r.cfg.RootPath, dd.Name())
		tickerDirs, err := os.ReadDir(datePath)
		if err != nil {
			continue
		}
		for _, td := range tickerDirs {
			if !td.IsDir() {
				continue
			}
			tickerPath := filepath.Join(datePath, td.Name())
			reqDirs, err := os.ReadDir(tickerPath)
			if err != nil {
				continue
			}
			for _, rd := range reqDirs {
				if !rd.IsDir() {
					continue
				}
				reqPath := filepath.Join(tickerPath, rd.Name())
				size, mtime := dirSize(reqPath)
				bundles = append(bundles, bundleStat{path: reqPath, bytes: size, mtime: mtime})
				total += size
			}
		}
	}

	if total <= r.cfg.MaxTotalBytes {
		return nil
	}

	// Oldest first.
	sort.Slice(bundles, func(i, j int) bool { return bundles[i].mtime.Before(bundles[j].mtime) })

	for _, b := range bundles {
		if total <= r.cfg.MaxTotalBytes {
			break
		}
		if err := os.RemoveAll(b.path); err == nil {
			total -= b.bytes
		}
	}
	return nil
}

// dirSize sums the sizes of every regular file under path and returns the
// most-recent mtime in the subtree (for size-based eviction ordering).
func dirSize(path string) (int64, time.Time) {
	var total int64
	var newest time.Time
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		total += fi.Size()
		if fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
		return nil
	})
	if newest.IsZero() {
		newest = time.Now()
	}
	return total, newest
}
