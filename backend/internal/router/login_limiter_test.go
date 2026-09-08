package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoginLimiterBlocksAfterFiveFailures(t *testing.T) {
	limiter := newLoginLimiter()
	request := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	request.RemoteAddr = "192.0.2.10:54321"
	for range 5 {
		limiter.failure(request, "admin")
	}
	if limiter.allowed(request, "admin") {
		t.Fatal("expected login to be rate limited")
	}
	if !limiter.allowed(request, "another-user") {
		t.Fatal("limit must be scoped to account and address")
	}
}

func TestLoginLimiterSuccessClearsFailures(t *testing.T) {
	limiter := newLoginLimiter()
	request := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	request.RemoteAddr = "192.0.2.10:54321"
	for range 4 {
		limiter.failure(request, "admin")
	}
	limiter.success(request, "admin")
	if !limiter.allowed(request, "admin") {
		t.Fatal("successful login should clear prior failures")
	}
}
