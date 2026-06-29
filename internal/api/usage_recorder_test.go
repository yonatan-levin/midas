package api

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

func testJob(endpoint string) usageRecordJob {
	return usageRecordJob{
		keyID:  "k",
		record: entities.UsageRecord{Endpoint: endpoint},
		logger: zap.NewNop(),
	}
}

// SR-1 B11: every enqueued job is eventually persisted by the worker pool.
func TestUsageRecorder_DrainsEnqueuedJobs(t *testing.T) {
	const n = 50
	var count int64
	var wg sync.WaitGroup
	wg.Add(n)
	rec := func(_ context.Context, _ string, _ entities.UsageRecord) error {
		atomic.AddInt64(&count, 1)
		wg.Done()
		return nil
	}

	r := newUsageRecorder(rec, zap.NewNop())
	for i := 0; i < n; i++ {
		r.Enqueue(testJob("/x"))
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("recorder did not drain all jobs; got %d/%d", atomic.LoadInt64(&count), n)
	}
	assert.Equal(t, int64(n), atomic.LoadInt64(&count))
}

// SR-1 B11: a saturated queue must DROP (load-shed) without blocking the
// caller — the whole point of replacing the unbounded per-request goroutine.
func TestUsageRecorder_FullQueueDropsWithoutBlocking(t *testing.T) {
	release := make(chan struct{})
	var started sync.WaitGroup
	started.Add(usageRecorderWorkers)
	var startedCount int64
	rec := func(_ context.Context, _ string, _ entities.UsageRecord) error {
		if atomic.AddInt64(&startedCount, 1) <= int64(usageRecorderWorkers) {
			started.Done()
		}
		<-release
		return nil
	}

	r := newUsageRecorder(rec, zap.NewNop())

	// Pin every worker inside rec (each pulls one job, then blocks on release).
	for i := 0; i < usageRecorderWorkers; i++ {
		r.Enqueue(testJob("/pin"))
	}
	started.Wait() // all workers now blocked; the channel is drained

	// Fill the buffer to capacity — no worker is free to drain it.
	for i := 0; i < usageRecorderBuffer; i++ {
		r.Enqueue(testJob("/fill"))
	}

	// The queue is now full (workers blocked + buffer full). One more Enqueue
	// must return promptly via the drop path, not block.
	done := make(chan struct{})
	go func() {
		r.Enqueue(testJob("/drop"))
		close(done)
	}()
	select {
	case <-done:
		// non-blocking drop — correct
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("Enqueue blocked on a full queue; expected a non-blocking drop")
	}

	close(release)
}
