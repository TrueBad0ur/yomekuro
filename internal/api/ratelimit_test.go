package api

import "testing"

func TestLoginLimiter(t *testing.T) {
	l := newLoginLimiter()
	key := "1.2.3.4:admin"

	if l.blocked(key) {
		t.Fatal("fresh key should not be blocked")
	}

	l.recordFailure(key)
	if !l.blocked(key) {
		t.Error("expected key to be blocked after a failure")
	}

	l.reset(key)
	if l.blocked(key) {
		t.Error("expected key to be unblocked after reset")
	}
}

func TestLoginLimiter_BackoffGrows(t *testing.T) {
	l := newLoginLimiter()
	key := "1.2.3.4:admin"

	l.recordFailure(key)
	first := l.state[key].blockedUntil

	l.recordFailure(key)
	second := l.state[key].blockedUntil
	if !second.After(first) {
		t.Errorf("expected backoff to grow with repeated failures: first=%v second=%v", first, second)
	}
}
