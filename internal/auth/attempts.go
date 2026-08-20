package auth

import (
	"sync"
	"time"
)

// AttemptLimiter tracks failed attempts per key and applies an
// exponential-ish backoff lockout once a key accumulates enough failures —
// used here for login (keyed by username) and, in internal/hub, the
// reverse-tunnel token check (keyed by host id). A guessable secret (a
// password) needs this to slow brute-forcing down; a high-entropy one (the
// tunnel's 192-bit token) mainly needs it to stop an attacker or
// misbehaving client hammering the endpoint at all rather than to make
// guessing infeasible — the same mechanism serves both.
//
// In-memory only, with no expiry sweep: a process restart clears it, and a
// key that stops failing simply stops growing its own entry further — the
// same trade-off this project already makes for other process-lifetime
// caches (SSH connection pools, the hub's overview cache). Callers should
// key by something with naturally bounded cardinality (real usernames,
// host ids that already passed a lookup) rather than raw unvalidated
// input, so an attacker can't cheaply grow the map with an unlimited
// stream of distinct garbage keys.
type AttemptLimiter struct {
	mu       sync.Mutex
	attempts map[string]*attemptState
}

type attemptState struct {
	count   int
	blocked time.Time
}

// NewAttemptLimiter returns an empty limiter.
func NewAttemptLimiter() *AttemptLimiter {
	return &AttemptLimiter{attempts: map[string]*attemptState{}}
}

// Allow reports whether key may attempt right now — false means it's
// currently locked out from recent failures and the caller should refuse
// the request without spending the cost of whatever real verification
// (password hashing, a DB round trip) would otherwise run on every
// hammering attempt.
func (l *AttemptLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.attempts[key]
	return st == nil || time.Now().After(st.blocked)
}

// Fail records a failed attempt for key. After 5, an exponential-ish
// backoff kicks in, capped at 5 minutes.
func (l *AttemptLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.attempts[key]
	if st == nil {
		st = &attemptState{}
		l.attempts[key] = st
	}
	st.count++
	if st.count >= 5 {
		delay := time.Duration(1<<min(st.count-5, 5)) * 2 * time.Second
		if delay > 5*time.Minute {
			delay = 5 * time.Minute
		}
		st.blocked = time.Now().Add(delay)
	}
}

// Clear forgets key's recorded failures — a successful attempt shouldn't
// leave a key one step from a lockout caused by earlier, unrelated failures.
func (l *AttemptLimiter) Clear(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}
