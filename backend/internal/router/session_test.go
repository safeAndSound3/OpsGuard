package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"monitor-platform/internal/config"
)

func TestSessionManagerRequiresServerIssuedCookie(t *testing.T) {
	manager := newSessionManager(config.AppConfig{SessionTTL: "1h"})
	protected := manager.require(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	request.Header.Set("Authorization", "Bearer opsguard-admin")
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("fixed client token must not authenticate: got %d", response.Code)
	}

	loginResponse := httptest.NewRecorder()
	if _, err := manager.create(loginResponse, "admin"); err != nil {
		t.Fatal(err)
	}
	cookies := loginResponse.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("expected one protected session cookie, got %#v", cookies)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("server-issued session should authenticate: got %d", response.Code)
	}

	manager.destroy(httptest.NewRecorder(), request)
	response = httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("destroyed session must be rejected: got %d", response.Code)
	}
}

func TestSessionExpiry(t *testing.T) {
	manager := newSessionManager(config.AppConfig{SessionTTL: "1ns"})
	response := httptest.NewRecorder()
	if _, err := manager.create(response, "admin"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	request := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	request.AddCookie(response.Result().Cookies()[0])
	if _, ok := manager.username(request); ok {
		t.Fatal("expired session was accepted")
	}
}

func TestCORSMiddleware(t *testing.T) {
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), []string{"https://opsguard.example.com"})

	allowed := httptest.NewRequest(http.MethodGet, "http://api.internal/api/session", nil)
	allowed.Header.Set("Origin", "https://opsguard.example.com")
	allowedResponse := httptest.NewRecorder()
	handler.ServeHTTP(allowedResponse, allowed)
	if allowedResponse.Code != http.StatusNoContent || allowedResponse.Header().Get("Access-Control-Allow-Origin") != "https://opsguard.example.com" {
		t.Fatalf("configured origin was not allowed: code=%d origin=%q", allowedResponse.Code, allowedResponse.Header().Get("Access-Control-Allow-Origin"))
	}

	denied := httptest.NewRequest(http.MethodPost, "http://api.internal/api/login", nil)
	denied.Header.Set("Origin", "https://attacker.example")
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("unexpected origin should be rejected: got %d", deniedResponse.Code)
	}
}

func TestSameOriginOnlyRelaxesPortsForLoopbackDevelopment(t *testing.T) {
	loopback := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8030/api/login", nil)
	if !sameOrigin(loopback, "http://127.0.0.1:3000") {
		t.Fatal("loopback development proxy origin should be accepted")
	}
	production := httptest.NewRequest(http.MethodPost, "https://opsguard.example.com:8443/api/login", nil)
	if sameOrigin(production, "https://opsguard.example.com:9443") {
		t.Fatal("different production ports must not be treated as same-origin")
	}
}
