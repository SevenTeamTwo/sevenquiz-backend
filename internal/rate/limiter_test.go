package rate_test

import (
	"context"
	"runtime"
	"sevenquiz-backend/internal/rate"
	"sync/atomic"
	"testing"
	"time"

	"github.com/benbjohnson/clock"
)

func TestLimiter_Allow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		window   time.Duration
		limit    int
		requests int
		expect   bool
		interval time.Duration
		sleep    time.Duration
	}{
		{
			name:     "Within limit",
			window:   time.Minute,
			limit:    10,
			requests: 10,
			expect:   true,
		},
		{
			name:     "At limit",
			window:   time.Minute,
			limit:    10,
			requests: 11,
			expect:   false,
		},
		{
			name:     "Within limit after slide",
			window:   10 * time.Millisecond,
			interval: time.Millisecond,
			limit:    10,
			requests: 11,
			sleep:    time.Millisecond,
			expect:   true,
		},
		{
			name:     "At limit after slide",
			window:   10 * time.Millisecond,
			limit:    10,
			requests: 11,
			sleep:    9 * time.Millisecond,
			expect:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clock := clock.NewMock()
			limiter := rate.NewLimiterWithClock(tt.window, tt.limit, clock)

			clock.Set(time.Now())

			for i := 1; i < tt.requests; i++ {
				limiter.Allow()
				clock.Add(tt.interval)
			}

			clock.Add(tt.sleep)

			if got, want := limiter.Allow(), tt.expect; got != want {
				t.Fatalf("Invalid request allow, got %v, want %v", got, want)
			}
		})
	}
}

func TestLimiter_Wait(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		window   time.Duration
		limit    int
		requests int
		expect   bool
		interval time.Duration
		wait     time.Duration
	}{
		{
			name:     "No interval wait no slot",
			window:   time.Minute,
			limit:    10,
			requests: 10,
			wait:     59 * time.Second,
			expect:   false,
		},
		{
			name:     "No interval wait slot available",
			window:   time.Minute,
			limit:    10,
			requests: 10,
			wait:     time.Minute,
			expect:   true,
		},
		{
			name:     "Interval wait no slot",
			window:   time.Minute,
			limit:    10,
			requests: 10,
			wait:     5 * time.Second,
			interval: 6 * time.Second,
			expect:   false,
		},
		{
			name:     "Interval wait slot available",
			window:   time.Minute,
			limit:    10,
			requests: 10,
			wait:     6 * time.Second,
			interval: 6 * time.Second,
			expect:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clock := clock.NewMock()
			limiter := rate.NewLimiterWithClock(tt.window, tt.limit, clock)

			clock.Set(time.Now())

			for i := range tt.requests {
				limiter.Allow()
				if i != tt.requests-1 {
					clock.Add(tt.interval)
				}
			}

			var done atomic.Bool
			go func(done *atomic.Bool) {
				_ = limiter.Wait(context.Background())
				done.Store(true)
			}(&done)

			runtime.Gosched()
			clock.Add(tt.wait)

			if got, want := done.Load(), tt.expect; got != want {
				t.Fatalf("Invalid request wait, got %v, want %v", got, want)
			}
		})
	}
}
