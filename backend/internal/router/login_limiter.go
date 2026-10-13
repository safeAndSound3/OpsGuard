package router

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

type loginAttempt struct {
	failures     int
	blockedUntil time.Time
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{attempts: make(map[string]loginAttempt)} }

func (l *loginLimiter) key(r *http.Request, username string) string {
	return requestClientIP(r) + "|" + strings.ToLower(strings.TrimSpace(username))
}

func (l *loginLimiter) allowed(r *http.Request, username string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !time.Now().Before(l.attempts[l.key(r, username)].blockedUntil)
}

func (l *loginLimiter) failure(r *http.Request, username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := l.key(r, username)
	item := l.attempts[key]
	item.failures++
	if item.failures >= 5 {
		item.blockedUntil = time.Now().Add(15 * time.Minute)
		item.failures = 0
	}
	l.attempts[key] = item
}

func (l *loginLimiter) success(r *http.Request, username string) {
	l.mu.Lock()
	delete(l.attempts, l.key(r, username))
	l.mu.Unlock()
}
