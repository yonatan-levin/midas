package scheduler

import (
	"context"
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
func (s *Service) Start(ctx context.Context) {
	if !s.cfg.Enabled {
		s.logger.Info("scheduler disabled; not starting")
		return
	}
	ticker := time.NewTicker(s.cfg.Interval)
	go func() {
		s.logger.Info("scheduler started", zap.Duration("interval", s.cfg.Interval))
		defer ticker.Stop()
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
}

// runOnce triggers all jobs once with bounded concurrency
func (s *Service) runOnce(ctx context.Context) {
	for _, job := range s.jobs {
		select {
		case s.semaphore <- struct{}{}:
			j := job
			go func() {
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
