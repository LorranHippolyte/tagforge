package gtm

import (
	"context"
	"math/rand/v2"
	"time"

	"golang.org/x/time/rate"
)

const (
	DefaultRateLimitQPS = 0.5
	DefaultRateBurst    = 4
	DefaultMaxAttempts  = 4
	DefaultBaseBackoff  = 500 * time.Millisecond
	DefaultMaxBackoff   = 10 * time.Second
)

// SchedulerOptions configures GTM API rate limiting and retry behavior.
type SchedulerOptions struct {
	QPS         float64
	Burst       int
	MaxAttempts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

// Scheduler serializes and rate-limits calls against the GTM API with bounded
// exponential backoff + jitter for transient 429/5xx responses.
type Scheduler struct {
	limiter     *rate.Limiter
	maxAttempts int
	baseBackoff time.Duration
	maxBackoff  time.Duration
}

// NewScheduler constructs a Scheduler using defaults for any zero-valued option.
func NewScheduler(opts SchedulerOptions) *Scheduler {
	qps := opts.QPS
	if qps <= 0 {
		qps = DefaultRateLimitQPS
	}
	burst := opts.Burst
	if burst <= 0 {
		burst = DefaultRateBurst
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	baseBackoff := opts.BaseBackoff
	if baseBackoff <= 0 {
		baseBackoff = DefaultBaseBackoff
	}
	maxBackoff := opts.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = DefaultMaxBackoff
	}

	return &Scheduler{
		limiter:     rate.NewLimiter(rate.Limit(qps), burst),
		maxAttempts: maxAttempts,
		baseBackoff: baseBackoff,
		maxBackoff:  maxBackoff,
	}
}

// Do runs fn respecting rate limits and retrying transient errors with jittered backoff.
func (s *Scheduler) Do(ctx context.Context, fn func(context.Context) error) error {
	var lastErr error
	for attempt := 0; attempt < s.maxAttempts; attempt++ {
		if err := s.limiter.Wait(ctx); err != nil {
			return err
		}

		err := fn(ctx)
		if err == nil {
			return nil
		}
		lastErr = err

		if !isRetryableError(err) || attempt == s.maxAttempts-1 {
			return TranslateGoogleError(err)
		}

		backoff := s.computeBackoff(attempt)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return TranslateGoogleError(lastErr)
}

func (s *Scheduler) computeBackoff(attempt int) time.Duration {
	delay := s.baseBackoff << attempt
	if delay > s.maxBackoff || delay <= 0 {
		delay = s.maxBackoff
	}
	// Apply 50%..100% jitter to avoid thundering herds.
	jitterFactor := 0.5 + rand.Float64()*0.5
	return time.Duration(float64(delay) * jitterFactor)
}
