package api

import (
	"sync"
	"time"
)

// loginLimiter throttles repeated failed logins per IP+username, since login
// previously had no rate limit at all — combined with public self-
// registration being intentionally open, that made username/password
// brute-forcing free. In-memory only: a restart resets it, which is fine for
// a single-instance deployment.
type loginLimiter struct {
	mu    sync.Mutex
	state map[string]*loginAttempt
}

type loginAttempt struct {
	failures     int
	blockedUntil time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{state: make(map[string]*loginAttempt)}
}

// blocked reports whether key is currently locked out.
func (l *loginLimiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.state[key]
	return ok && time.Now().Before(a.blockedUntil)
}

// recordFailure lengthens the lockout: 1s, 2s, 4s, ... capped at 5 minutes.
func (l *loginLimiter) recordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.state[key]
	if !ok {
		a = &loginAttempt{}
		l.state[key] = a
	}
	a.failures++
	delay := time.Duration(1<<uint(min(a.failures, 9))) * time.Second // caps at 512s
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	a.blockedUntil = time.Now().Add(delay)
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.state, key)
}
