package router

import (
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"monitor-platform/internal/config"
)

const sessionCookieName = "opsguard_session"

type sessionEntry struct {
	username  string
	expiresAt time.Time
}

type sessionManager struct {
	mu           sync.Mutex
	sessions     map[string]sessionEntry
	ttl          time.Duration
	cookieSecure bool
}

func newSessionManager(cfg config.AppConfig) *sessionManager {
	ttl, err := time.ParseDuration(cfg.SessionTTL)
	if err != nil || ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return &sessionManager{sessions: make(map[string]sessionEntry), ttl: ttl, cookieSecure: cfg.SessionCookieSecure}
}

func (m *sessionManager) create(w http.ResponseWriter, username string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expiresAt := time.Now().Add(m.ttl)
	m.mu.Lock()
	m.sessions[token] = sessionEntry{username: username, expiresAt: expiresAt}
	m.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", HttpOnly: true, Secure: m.cookieSecure, SameSite: http.SameSiteLaxMode, Expires: expiresAt, MaxAge: int(m.ttl.Seconds())})
	return token, nil
}

func (m *sessionManager) username(r *http.Request) (string, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.sessions[cookie.Value]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(m.sessions, cookie.Value)
		return "", false
	}
	return entry.username, true
}

func (m *sessionManager) destroy(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		m.mu.Lock()
		delete(m.sessions, cookie.Value)
		m.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Path: "/", HttpOnly: true, Secure: m.cookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
}

func (m *sessionManager) require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := m.username(r); !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "登录已失效，请重新登录"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func corsMiddleware(next http.Handler, allowedOrigins []string) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[strings.TrimRight(origin, "/")] = struct{}{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		origin := strings.TrimRight(strings.TrimSpace(r.Header.Get("Origin")), "/")
		_, explicitlyAllowed := allowed[origin]
		if origin != "" && !explicitlyAllowed && !sameOrigin(r, origin) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "origin not allowed"})
			return
		}
		if explicitlyAllowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
			r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		}
		next.ServeHTTP(w, r)
	})
}

func sameOrigin(r *http.Request, origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if strings.EqualFold(parsed.Host, r.Host) {
		return true
	}
	requestHost := r.Host
	if host, _, splitErr := net.SplitHostPort(r.Host); splitErr == nil {
		requestHost = host
	}
	if !strings.EqualFold(parsed.Hostname(), requestHost) {
		return false
	}
	return parsed.Hostname() == "localhost" || net.ParseIP(parsed.Hostname()).IsLoopback()
}
