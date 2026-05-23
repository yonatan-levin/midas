package scheduler

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Config governs the scheduler behavior and is meant to be provided via app config
type Config struct {
	Enabled        bool
	Interval       time.Duration
	MaxConcurrency int
}

// Job defines a unit of scheduled work
type Job interface {
	Name() string
	Run(ctx context.Context) error
}

// Service provides a very small, DI-friendly scheduler. It starts a ticker and runs
// registered jobs at the configured interval with a bounded concurrency semaphore.
type Service struct {
	cfg       Config
	logger    *zap.Logger
	jobs      []Job
	semaphore chan struct{}

	// jobsWG tracks in-flight per-job goroutines launched by runOnce. Each launched
	// job goroutine increments the WaitGroup before starting and decrements via defer.
	// The Start() supervisor goroutine waits on this group after the main loop exits,
	// so all child goroutines drain before done is closed.
	jobsWG sync.WaitGroup

	// done is closed when the Start() supervisor goroutine AND all in-flight job
	// goroutines have fully exited. It is nil until Start() is called for the
	// first time on an enabled scheduler; Stop() handles the nil case by returning
	// immediately. Allocated under startOnce to keep Start() safe to call multiple
	// times during tests / fx lifecycle hooks.
	done chan struct{}

	// startOnce guards allocation of `done` and the goroutine launch in Start() so
	// that repeated Start() calls (e.g. mistaken double-wiring in tests) don't leak
	// supervisor goroutines or replace the done channel mid-flight.
	startOnce sync.Once
}

// New creates a new scheduler service
func New(cfg Config, logger *zap.Logger, jobs ...Job) *Service {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Hour
	}
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 2
	}
	return &Service{cfg: cfg, logger: logger, jobs: jobs, semaphore: make(chan struct{}, cfg.MaxConcurrency)}
}

// Start begins the periodic execution loop. It is safe to call in fx lifecycle OnStart.
// The launched goroutine exits when ctx is canceled. Callers that need to wait for
// the supervisor and all in-flight job goroutines to drain (notably tests) must call
// Stop() after canceling ctx.
func (s *Service) Start(ctx context.Context) {
	if !s.cfg.Enabled {
		s.logger.Info("scheduler disabled; not starting")
		return
	}
	s.startOnce.Do(func() {
		s.done = make(chan struct{})
		ticker := time.NewTicker(s.cfg.Interval)
		go func() {
			defer close(s.done)
			defer s.jobsWG.Wait() // drain in-flight job goroutines before signaling done
			defer ticker.Stop()
			s.logger.Info("scheduler started", zap.Duration("interval", s.cfg.Interval))
			for {
				select {
				case <-ctx.Done():
					s.logger.Info("scheduler stopping")
					return
				case <-ticker.C:
					s.runOnce(ctx)
				}
			}
		}()
	})
}

// Stop blocks until the supervisor goroutine launched by Start() and any in-flight
// per-job goroutines have fully exited. It does NOT cancel the context; the caller
// is responsible for canceling the context passed to Start() (typically via the
// same context.WithCancel they own, or via fx OnStop in production).
//
// Stop() is safe to call multiple times and safe to call without a prior Start()
// (returns immediately when the scheduler was disabled or never started).
//
// Tests should register `t.Cleanup(func() { cancel(); svc.Stop() })` to ensure
// goroutines launched by Start() do not outlive the *testing.T scope — otherwise
// late `zaptest.NewLogger(t)` log calls panic with
// "Log in goroutine after <Test> has completed". See docs/reviewer/scheduler-test-cleanup-race.md.
func (s *Service) Stop() {
	if s.done == nil {
		return
	}
	<-s.done
}

// runOnce triggers all jobs once with bounded concurrency
func (s *Service) runOnce(ctx context.Context) {
	for _, job := range s.jobs {
		select {
		case s.semaphore <- struct{}{}:
			j := job
			// Track the launched job goroutine on jobsWG so Stop()/the supervisor's
			// final defer can drain in-flight jobs before signaling done. Increment
			// MUST happen on the parent goroutine (before `go func()`) to avoid
			// racing with the supervisor's s.jobsWG.Wait().
			s.jobsWG.Add(1)
			go func() {
				defer s.jobsWG.Done()
				defer func() { <-s.semaphore }()
				if err := j.Run(ctx); err != nil {
					s.logger.Warn("scheduled job failed", zap.String("job", j.Name()), zap.Error(err))
				} else {
					s.logger.Debug("scheduled job completed", zap.String("job", j.Name()))
				}
			}()
		case <-ctx.Done():
			return
		}
	}
}
