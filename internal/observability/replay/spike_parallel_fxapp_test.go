//go:build replay_spike
// +build replay_spike

// Spike test for Phase R3 Pre-Flight (§2 of
// docs/refactoring/observability-replay-tooling-r3-implementation-plan.md).
//
// Purpose: prove that 4 concurrent fx.App lifecycles built from
// replay.Module(bundleDir, opts) can start/stop in parallel without:
//   - panic in any goroutine
//   - deadlock (each completes within a 30s timeout)
//   - data races detected by `-race`
//   - cross-app metrics-registry pollution (per-app *metrics.Service registries
//     must be pairwise distinct)
//
// If this test passes, R3's Stage I parallel-replay design is sound. If it
// fails, BACKEND surfaces the fx-concurrency issue immediately rather than
// discovering races during Stage I implementation.
//
// Build-tag-gated under `replay_spike` (same tag R2's spike_test.go uses);
// the spike is excluded from default `go test ./...` runs and ships only when
// explicitly invoked. Per plan §2 "Disposition" the test is retained as a
// permanent regression guard — future replay refactors that touch
// replay.Module composition or fx app lifecycle MUST re-run the spike before
// merge.
//
// Run: go test -tags=replay_spike -race -count=10 -run TestSpike_ParallelFxAppLifecycle ./internal/observability/replay/

package replay

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.uber.org/fx"

	"github.com/midas/dcf-valuation-api/internal/services/metrics"
	"github.com/midas/dcf-valuation-api/internal/services/valuation"
)

// TestSpike_ParallelFxAppLifecycle is the load-bearing assertion of the
// R3 Pre-Flight spike. Per plan §2 step 4: 4 concurrent fx.App lifecycles
// against the same bundle directory complete with no panic, no deadlock,
// no race, and distinct per-app metrics registries.
func TestSpike_ParallelFxAppLifecycle(t *testing.T) {
	// Resolve the testdata/happy/ bundle absolute path. The same bundle is
	// used by every goroutine — sharing a read-only bundleDir across apps
	// is the realistic load shape for `--workers > 1` runs against a single
	// artifacts/<UTC-date>/<TICKER>/ tree.
	bundleDir, err := filepath.Abs(filepath.Join("testdata", "happy"))
	if err != nil {
		t.Fatalf("resolve bundleDir: %v", err)
	}

	const numWorkers = 4
	const startStopTimeout = 30 * time.Second

	type result struct {
		registry interface{} // captures *prometheus.Registry pointer; interface{} dodges import-cycle
		err      error
	}

	results := make([]result, numWorkers)
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for i := 0; i < numWorkers; i++ {
		i := i
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					results[i] = result{err: panicErr(r)}
				}
			}()

			// Construct an independent fx.App. Resolve *metrics.Service so
			// we can capture its registry pointer for pairwise-distinctness
			// assertion.
			var metricsSvc *metrics.Service
			var valSvc *valuation.Service
			app := fx.New(
				Module(bundleDir, Options{}),
				fx.Populate(&metricsSvc),
				fx.Populate(&valSvc),
				fx.NopLogger,
			)
			if appErr := app.Err(); appErr != nil {
				results[i] = result{err: appErr}
				return
			}

			startCtx, cancel := context.WithTimeout(context.Background(), startStopTimeout)
			defer cancel()
			if startErr := app.Start(startCtx); startErr != nil {
				results[i] = result{err: startErr}
				return
			}

			// Stop cleanly. Use a fresh context (Start's may have drained).
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer stopCancel()
			if stopErr := app.Stop(stopCtx); stopErr != nil {
				results[i] = result{err: stopErr}
				return
			}

			if metricsSvc == nil {
				results[i] = result{err: errMissing("*metrics.Service")}
				return
			}
			results[i] = result{registry: metricsSvc.GetRegistry()}
		}()
	}

	// Bound the WaitGroup with a deadline so deadlock surfaces as a t.Fatal,
	// not as a hung CI run.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(startStopTimeout + 10*time.Second):
		t.Fatalf("deadlock: waited > %v for %d goroutines", startStopTimeout+10*time.Second, numWorkers)
	}

	// Assert no panic / lifecycle error in any worker.
	for i, r := range results {
		if r.err != nil {
			t.Errorf("worker %d failed: %v", i, r.err)
		}
	}
	if t.Failed() {
		return
	}

	// Assert per-app metrics-registry pointers are pairwise distinct. A
	// shared pointer would mean Module() leaked process-global state into
	// per-app construction — the central concern RPL-2g half-fix targets.
	for i := 0; i < numWorkers; i++ {
		for j := i + 1; j < numWorkers; j++ {
			if results[i].registry == results[j].registry {
				t.Errorf("metrics registry pollution: worker %d and worker %d share the same *prometheus.Registry pointer (%p)", i, j, results[i].registry)
			}
		}
	}

	// Final value-of-spike assertion: at least one of the 4 produced a
	// non-nil registry. If all 4 were nil, the assertion above is vacuous
	// (pairwise-distinct holds trivially for nil pointers).
	allNil := true
	for _, r := range results {
		if r.registry != nil {
			allNil = false
			break
		}
	}
	if allNil {
		t.Fatalf("all %d workers returned nil registries; spike cannot prove distinctness", numWorkers)
	}
}

// panicErr converts a recovered panic value into an error for storage in
// the result slice without forcing a goroutine-local t.Fatalf (which would
// fail to surface from a child goroutine in the standard testing harness).
type panicError struct{ v interface{} }

func (p panicError) Error() string { return "panic in worker goroutine" }

func panicErr(v interface{}) error { return panicError{v: v} }

// errMissing returns a typed error for "an fx.Populate target was not
// resolved" — symptomatic of a Module() composition regression.
type missingError string

func (m missingError) Error() string { return "fx.Populate target was nil: " + string(m) }

func errMissing(name string) error { return missingError(name) }
