package gtm

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"google.golang.org/api/googleapi"
)

func TestSchedulerRetriesTransientErrors(t *testing.T) {
	s := NewScheduler(SchedulerOptions{
		QPS:         100,
		Burst:       10,
		MaxAttempts: 3,
		BaseBackoff: 5 * time.Millisecond,
		MaxBackoff:  20 * time.Millisecond,
	})

	attempts := 0
	err := s.Do(context.Background(), func(context.Context) error {
		attempts++
		if attempts < 3 {
			return &googleapi.Error{Code: http.StatusTooManyRequests, Message: "quota exceeded"}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected eventual success, got: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestSchedulerStopsOnNonRetryableError(t *testing.T) {
	s := NewScheduler(SchedulerOptions{
		QPS:         100,
		Burst:       10,
		MaxAttempts: 4,
		BaseBackoff: 5 * time.Millisecond,
	})

	attempts := 0
	err := s.Do(context.Background(), func(context.Context) error {
		attempts++
		return &googleapi.Error{Code: http.StatusNotFound, Message: "tag missing"}
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt on 404, got %d", attempts)
	}
}

func TestSchedulerExhaustedRateLimit(t *testing.T) {
	s := NewScheduler(SchedulerOptions{
		QPS:         100,
		Burst:       10,
		MaxAttempts: 2,
		BaseBackoff: 2 * time.Millisecond,
	})

	attempts := 0
	err := s.Do(context.Background(), func(context.Context) error {
		attempts++
		return &googleapi.Error{Code: http.StatusTooManyRequests, Message: "still limited"}
	})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestSchedulerHonorsContextCancel(t *testing.T) {
	s := NewScheduler(SchedulerOptions{
		QPS:         100,
		Burst:       10,
		MaxAttempts: 4,
		BaseBackoff: 500 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	err := s.Do(ctx, func(context.Context) error {
		return &googleapi.Error{Code: http.StatusServiceUnavailable, Message: "busy"}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
