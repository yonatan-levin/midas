package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// mockJob implements the Job interface for testing
type mockJob struct {
	name        string
	runCount    int
	runDuration time.Duration
	shouldError bool
	mutex       sync.Mutex
}

func newMockJob(name string) *mockJob {
	return &mockJob{
		name:        name,
		runDuration: 10 * time.Millisecond,
	}
}

func (m *mockJob) Name() string {
	return m.name
}

func (m *mockJob) Run(ctx context.Context) error {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.runCount++

	// Simulate job execution time
	time.Sleep(m.runDuration)

	if m.shouldError {
		return assert.AnError
	}
	return nil
}

func (m *mockJob) getRunCount() int {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.runCount
}

func (m *mockJob) setError(shouldError bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	m.shouldError = shouldError
}

func TestSchedulerService_StartStop(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
	}{
		{"enabled scheduler should run", true},
		{"disabled scheduler should not run", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zaptest.NewLogger(t)
			job := newMockJob("test-job")

			cfg := Config{
				Enabled:        tt.enabled,
				Interval:       50 * time.Millisecond, // Fast interval for testing
				MaxConcurrency: 1,
			}

			scheduler := New(cfg, logger, job)
			require.NotNil(t, scheduler, "scheduler should be created")

			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			// Start scheduler
			scheduler.Start(ctx)

			// Wait for context timeout
			<-ctx.Done()

			// Check if job ran based on enabled status
			runCount := job.getRunCount()
			if tt.enabled {
				assert.Greater(t, runCount, 0, "job should have run at least once when scheduler enabled")
			} else {
				assert.Equal(t, 0, runCount, "job should not run when scheduler disabled")
			}
		})
	}
}

func TestSchedulerService_ConcurrencyLimit(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Create multiple jobs with longer execution time
	job1 := newMockJob("job1")
	job2 := newMockJob("job2")
	job3 := newMockJob("job3")

	// Make jobs take longer to test concurrency
	job1.runDuration = 100 * time.Millisecond
	job2.runDuration = 100 * time.Millisecond
	job3.runDuration = 100 * time.Millisecond

	cfg := Config{
		Enabled:        true,
		Interval:       50 * time.Millisecond, // Fast interval for testing
		MaxConcurrency: 2,                     // Limit to 2 concurrent jobs
	}

	scheduler := New(cfg, logger, job1, job2, job3)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	scheduler.Start(ctx)
	<-ctx.Done()

	// All jobs should have run at least once
	assert.Greater(t, job1.getRunCount(), 0, "job1 should have run")
	assert.Greater(t, job2.getRunCount(), 0, "job2 should have run")
	assert.Greater(t, job3.getRunCount(), 0, "job3 should have run")
}

func TestSchedulerService_ErrorHandling(t *testing.T) {
	logger := zaptest.NewLogger(t)

	successJob := newMockJob("success-job")
	errorJob := newMockJob("error-job")
	errorJob.setError(true)

	cfg := Config{
		Enabled:        true,
		Interval:       50 * time.Millisecond,
		MaxConcurrency: 2,
	}

	scheduler := New(cfg, logger, successJob, errorJob)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	scheduler.Start(ctx)
	<-ctx.Done()

	// Both jobs should have attempted to run
	assert.Greater(t, successJob.getRunCount(), 0, "success job should have run")
	assert.Greater(t, errorJob.getRunCount(), 0, "error job should have attempted to run")
}

func TestSchedulerService_DefaultConfiguration(t *testing.T) {
	logger := zaptest.NewLogger(t)
	job := newMockJob("test-job")

	// Test with zero/invalid configuration values
	cfg := Config{
		Enabled:        true,
		Interval:       0, // Should default to 1 hour
		MaxConcurrency: 0, // Should default to 2
	}

	scheduler := New(cfg, logger, job)

	assert.Equal(t, time.Hour, scheduler.cfg.Interval, "should default to 1 hour interval")
	assert.Equal(t, 2, scheduler.cfg.MaxConcurrency, "should default to 2 max concurrency")
	assert.Equal(t, 2, cap(scheduler.semaphore), "semaphore should have capacity of 2")
}
