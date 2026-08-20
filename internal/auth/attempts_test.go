package auth

import (
	"testing"
	"time"
)

func TestAttemptLimiterAllowsUntilThreshold(t *testing.T) {
	l := NewAttemptLimiter()
	for i := 0; i < 4; i++ {
		if !l.Allow("k") {
			t.Fatalf("Allow(\"k\") = false after %d failures, want true (lockout starts at 5)", i)
		}
		l.Fail("k")
	}
	// 4 failures recorded — still under the threshold.
	if !l.Allow("k") {
		t.Error("Allow(\"k\") = false after 4 failures, want true")
	}
}

func TestAttemptLimiterLocksOutAfterFiveFailures(t *testing.T) {
	l := NewAttemptLimiter()
	for i := 0; i < 5; i++ {
		l.Fail("k")
	}
	if l.Allow("k") {
		t.Error("Allow(\"k\") = true after 5 failures, want false (locked out)")
	}
}

func TestAttemptLimiterClearResetsLockout(t *testing.T) {
	l := NewAttemptLimiter()
	for i := 0; i < 5; i++ {
		l.Fail("k")
	}
	if l.Allow("k") {
		t.Fatal("test setup bug: expected a lockout after 5 failures")
	}
	l.Clear("k")
	if !l.Allow("k") {
		t.Error("Allow(\"k\") = false after Clear, want true")
	}
}

// TestAttemptLimiterKeysAreIndependent confirms failures against one key
// never lock out an unrelated one — a real requirement, not just tidiness:
// otherwise an attacker hammering one username/host id would incidentally
// deny service to every other one sharing the same limiter instance.
func TestAttemptLimiterKeysAreIndependent(t *testing.T) {
	l := NewAttemptLimiter()
	for i := 0; i < 5; i++ {
		l.Fail("attacker-target")
	}
	if l.Allow("attacker-target") {
		t.Fatal("test setup bug: expected a lockout after 5 failures")
	}
	if !l.Allow("unrelated-key") {
		t.Error("Allow(\"unrelated-key\") = false — failures against a different key locked it out too")
	}
}

// TestAttemptLimiterBackoffGrowsWithRepeatedFailures confirms the lockout
// after the 6th failure lasts noticeably longer than after the 5th — the
// whole point of the exponential-ish backoff, not just a fixed cooldown.
func TestAttemptLimiterBackoffGrowsWithRepeatedFailures(t *testing.T) {
	l := NewAttemptLimiter()
	for i := 0; i < 5; i++ {
		l.Fail("k")
	}
	l.mu.Lock()
	firstBlock := l.attempts["k"].blocked
	l.mu.Unlock()

	l.Fail("k")
	l.mu.Lock()
	secondBlock := l.attempts["k"].blocked
	l.mu.Unlock()

	if !secondBlock.After(firstBlock) {
		t.Errorf("blocked-until after 6 failures (%v) is not later than after 5 (%v)", secondBlock, firstBlock)
	}
}

// TestAttemptLimiterCapsBackoffAtFiveMinutes confirms the backoff doesn't
// grow unbounded — a key that keeps failing forever must still eventually
// get a chance again within a bounded window, not be locked out for hours.
func TestAttemptLimiterCapsBackoffAtFiveMinutes(t *testing.T) {
	l := NewAttemptLimiter()
	for i := 0; i < 30; i++ {
		l.Fail("k")
	}
	l.mu.Lock()
	blocked := l.attempts["k"].blocked
	l.mu.Unlock()

	if max := time.Now().Add(5*time.Minute + time.Second); blocked.After(max) {
		t.Errorf("blocked-until = %v, more than 5 minutes out despite the documented cap", blocked)
	}
}
