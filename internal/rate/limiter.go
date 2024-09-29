package rate

import (
	"context"
	"sync"
	"time"

	"github.com/benbjohnson/clock"
)

// Limiter implements a sliding window rate limiter.
type Limiter struct {
	window  time.Duration // time window
	limit   int           // requests limit
	history []time.Time   // requests timestamp history
	mu      sync.Mutex
	clock   Clock
}

type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

func NewLimiter(window time.Duration, limit int) *Limiter {
	return &Limiter{
		window: window,
		limit:  limit,
		clock:  clock.New(),
	}
}

func NewLimiterWithClock(window time.Duration, limit int, clock Clock) *Limiter {
	return &Limiter{
		window: window,
		limit:  limit,
		clock:  clock,
	}
}

// Allow checks if a request is allowed to be processed.
func (l *Limiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock.Now()
	l.history = l.slide(now)

	if len(l.history) >= l.limit {
		return false
	}

	l.history = append(l.history, now)

	return true
}

func (l *Limiter) slide(now time.Time) []time.Time {
	window := now.Add(-l.window)
	i := 0
	for i < len(l.history) && l.history[i].Before(window) {
		i++
	}
	return append(l.history[:0:0], l.history[i:]...)
}

func (l *Limiter) Slots() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	return l.limit - len(l.slide(now))
}

func (l *Limiter) Wait(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// There is already a slot available, no need to wait.
	if len(l.history) < l.limit {
		return nil
	}

	now := l.clock.Now()
	history := l.slide(now)

	if slots := len(history); slots < l.limit || slots == 0 {
		return nil
	}

	// Compute the next time a slot will be available.
	next := history[0].Add(l.window)
	wait := next.Sub(now)

	for {
		select {
		case <-l.clock.After(wait):
			l.history = append(l.history, l.clock.Now())
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
