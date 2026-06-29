package api

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/midas/dcf-valuation-api/internal/core/entities"
)

// usageRecordFunc persists one usage record. It matches auth.Service.RecordUsage.
type usageRecordFunc func(ctx context.Context, keyID string, record entities.UsageRecord) error

// usageRecordJob is one enqueued usage write. logger is the request-scoped
// logger captured at enqueue time so a failed write still logs with the
// originating request_id / user_id / key_id.
type usageRecordJob struct {
	keyID  string
	record entities.UsageRecord
	logger *zap.Logger
}

const (
	usageRecorderBuffer  = 256
	usageRecorderWorkers = 4
	usageRecordTimeout   = 5 * time.Second
)

// usageRecorder persists API-key usage on a bounded background worker pool.
//
// SR-1 B11: the auth middleware previously spawned one `go func()` per
// authenticated request, each holding a DB write — unbounded goroutine + DB
// connection growth under load. This replaces that with a fixed worker pool
// draining a buffered queue. Enqueue NEVER blocks the request path: when the
// queue is full it sheds the record with a WARN rather than stalling the caller
// (usage bookkeeping must never add latency to a valuation request).
type usageRecorder struct {
	queue  chan usageRecordJob
	record usageRecordFunc
	logger *zap.Logger
}

// newUsageRecorder builds the recorder and starts its worker pool. The workers
// run for the process lifetime (the queue is not closed — matching the prior
// fire-and-forget lifecycle, only now bounded).
func newUsageRecorder(record usageRecordFunc, logger *zap.Logger) *usageRecorder {
	r := &usageRecorder{
		queue:  make(chan usageRecordJob, usageRecorderBuffer),
		record: record,
		logger: logger,
	}
	for i := 0; i < usageRecorderWorkers; i++ {
		go r.worker()
	}
	return r
}

func (r *usageRecorder) worker() {
	for job := range r.queue {
		ctx, cancel := context.WithTimeout(context.Background(), usageRecordTimeout)
		if err := r.record(ctx, job.keyID, job.record); err != nil {
			job.logger.Error("Failed to record API usage", zap.Error(err))
		}
		cancel()
	}
}

// Enqueue submits a usage record for async persistence. Non-blocking: a full
// queue drops the record with a WARN (load-shedding) so the request path is
// never stalled by usage bookkeeping.
func (r *usageRecorder) Enqueue(job usageRecordJob) {
	select {
	case r.queue <- job:
	default:
		r.logger.Warn("usage-recording queue full; dropping usage record",
			zap.String("endpoint", job.record.Endpoint))
	}
}
