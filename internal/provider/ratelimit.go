package provider

import (
	"sync"
	"time"
)

// RateLimit tracks API consumption using a sliding window.
type RateLimit struct {
	mu       sync.Mutex
	window   time.Duration
	maxReqs  int
	requests []time.Time // timestamps of recent requests
}

// NewRateLimit creates a rate limiter with the given window and max requests.
// Example: NewRateLimit(10, time.Minute) allows 10 requests per minute.
func NewRateLimit(maxReqs int, window time.Duration) *RateLimit {
	return &RateLimit{
		window:  window,
		maxReqs: maxReqs,
	}
}

// prune removes expired timestamps (older than window).
// Must be called with mu held.
func (r *RateLimit) prune() {
	cutoff := time.Now().Add(-r.window)
	i := 0
	for i < len(r.requests) && r.requests[i].Before(cutoff) {
		i++
	}
	if i > 0 {
		r.requests = r.requests[i:]
	}
}

// Allow returns true if a request can be made without exceeding the limit.
func (r *RateLimit) Allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune()
	return len(r.requests) < r.maxReqs
}

// Record records that a request was just made.
func (r *RateLimit) Record() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, time.Now())
}

// Remaining returns how many requests can still be made in the current window.
func (r *RateLimit) Remaining() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune()
	rem := r.maxReqs - len(r.requests)
	if rem < 0 {
		return 0
	}
	return rem
}

// Used returns how many requests have been made in the current window.
func (r *RateLimit) Used() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune()
	return len(r.requests)
}

// CapacityPct returns remaining capacity as a percentage (0.0 to 1.0).
// A provider at 100% capacity has made 0 requests. At 0% it's exhausted.
func (r *RateLimit) CapacityPct() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune()
	if r.maxReqs == 0 {
		return 0
	}
	return float64(r.maxReqs-len(r.requests)) / float64(r.maxReqs)
}

// WaitDuration returns how long to wait before another request can be made.
// Returns 0 if a request can be made immediately.
func (r *RateLimit) WaitDuration() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prune()
	if len(r.requests) < r.maxReqs {
		return 0
	}
	// The oldest request in the window determines when next slot opens.
	oldest := r.requests[0]
	wait := time.Until(oldest.Add(r.window))
	if wait < 0 {
		return 0
	}
	return wait
}
