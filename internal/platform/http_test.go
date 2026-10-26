package platform

import (
	"bytes"
	"context"
	"eczanet/internal/auth"
	"encoding/json"
	"errors"
	"golang.org/x/crypto/bcrypt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type memoryStore struct {
	sessions map[string]auth.Session
	hash     string
	role     string
	offline  bool
}

func (m *memoryStore) Ping(context.Context) error {
	if m.offline {
		return errors.New("secret DSN")
	}
	return nil
}
func (m *memoryStore) Credentials(_ context.Context, email string) (auth.User, string, error) {
	if email != "demo@example.invalid" {
		return auth.User{}, "", auth.ErrUnauthenticated
	}
	return auth.User{ID: "1", Email: email, Role: m.role}, m.hash, nil
}
func (m *memoryStore) CreateSession(_ context.Context, uid, token, csrf string, e time.Time) error {
	m.sessions[token] = auth.Session{User: auth.User{ID: uid, Role: m.role}, CSRFHash: csrf, Expires: e}
	return nil
}
func (m *memoryStore) Session(_ context.Context, token string) (auth.Session, error) {
	s, ok := m.sessions[token]
	if !ok {
		return s, auth.ErrUnauthenticated
	}
	s.User.Role = m.role
	return s, nil
}
func (m *memoryStore) Revoke(_ context.Context, token string) error {
	delete(m.sessions, token)
	return nil
}
func (m *memoryStore) Locations(context.Context, string) ([]auth.Location, error) {
	return []auth.Location{{ID: "1", Name: "A", Kind: "branch"}}, nil
}

type fixture struct {
	s     *Server
	store *memoryStore
	clock time.Time
}

func setup(t *testing.T) *fixture {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	m := &memoryStore{sessions: map[string]auth.Session{}, hash: string(hash), role: "pharmacist"}
	f := &fixture{store: m, clock: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	f.s = NewServer(Config{Origins: map[string]bool{"http://localhost:3000": true}, RequestTimeout: time.Second}, m, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	f.s.Now = func() time.Time { return f.clock }
	return f
}
func (f *fixture) call(method, path, body, csrf string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Origin", "http://localhost:3000")
	r.Header.Set("X-CSRF-Token", csrf)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(w, r)
	return w
}
func field(w *httptest.ResponseRecorder, key string) string {
	var b map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	s, _ := b[key].(string)
	return s
}
func findCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}
func (f *fixture) login(t *testing.T) (*http.Cookie, string) {
	t.Helper()
	p := f.call("GET", "/api/v1/auth/csrf", "", "", nil)
	w := f.call("POST", "/api/v1/auth/login", `{"email":"demo@example.invalid","password":"correct-password"}`, field(p, "csrf_token"), findCookie(p, preCookie))
	if w.Code != 200 {
		t.Fatalf("login %d %s", w.Code, w.Body.String())
	}
	return findCookie(w, sessionCookie), field(w, "csrf_token")
}
func status(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status %d want %d: %s", w.Code, want, w.Body.String())
	}
	if w.Header().Get("X-DDO-Stamp") != "a33906" {
		t.Fatal("missing stamp")
	}
}

func TestSessionExpiryAndLogout(t *testing.T) {
	f := setup(t)
	cookie, csrf := f.login(t)
	status(t, f.call("GET", "/api/v1/auth/me", "", "", cookie), 200)
	f.clock = f.clock.Add(8*time.Hour - time.Nanosecond)
	status(t, f.call("GET", "/api/v1/auth/me", "", "", cookie), 200)
	f.clock = f.clock.Add(time.Nanosecond)
	status(t, f.call("GET", "/api/v1/auth/me", "", "", cookie), 401)
	cookie, csrf = f.login(t)
	status(t, f.call("POST", "/api/v1/auth/logout", "", csrf, cookie), 204)
	status(t, f.call("GET", "/api/v1/auth/me", "", "", cookie), 401)
}
func TestCSRFOriginCookieAndScope(t *testing.T) {
	f := setup(t)
	f.s.Config.CookieSecure = true
	cookie, csrf := f.login(t)
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != 28800 {
		t.Fatal("cookie policy")
	}
	status(t, f.call("POST", "/api/v1/auth/logout", "", "", cookie), 403)
	status(t, f.call("POST", "/api/v1/auth/logout", "", "wrong", cookie), 403)
	r := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	r.AddCookie(cookie)
	r.Header.Set("Origin", "https://evil.invalid")
	r.Header.Set("X-CSRF-Token", csrf)
	w := httptest.NewRecorder()
	f.s.Handler().ServeHTTP(w, r)
	status(t, w, 403)
	status(t, f.call("GET", "/api/v1/locations/1", "", "", cookie), 200)
	status(t, f.call("GET", "/api/v1/locations/2", "", "", cookie), 403)
	f.store.role = "simulator"
	status(t, f.call("GET", "/api/v1/auth/me", "", "", cookie), 200)
}
func TestPreloginExpiryValidationRateLimit(t *testing.T) {
	f := setup(t)
	p := f.call("GET", "/api/v1/auth/csrf", "", "", nil)
	csrf := field(p, "csrf_token")
	cookie := findCookie(p, preCookie)
	status(t, f.call("POST", "/api/v1/auth/login", `{}`, csrf, nil), 403)
	status(t, f.call("POST", "/api/v1/auth/login", `{"email":"demo@example.invalid","password":"correct-password","role":"admin"}`, csrf, cookie), 400)
	status(t, f.call("POST", "/api/v1/auth/login", `{} {}`, csrf, cookie), 400)
	for i := 0; i < 10; i++ {
		status(t, f.call("POST", "/api/v1/auth/login", `{"email":"demo@example.invalid","password":"bad"}`, csrf, cookie), 401)
	}
	w := f.call("POST", "/api/v1/auth/login", `{"email":"demo@example.invalid","password":"bad"}`, csrf, cookie)
	status(t, w, 429)
	if w.Header().Get("Retry-After") == "" {
		t.Fatal("retry-after")
	}
	f.clock = f.clock.Add(10 * time.Minute)
	status(t, f.call("POST", "/api/v1/auth/login", `{}`, csrf, cookie), 403)
	f.clock = f.clock.Add(5 * time.Minute)
	f.login(t)
}
func TestPlatformErrorsAndHealth(t *testing.T) {
	f := setup(t)
	for _, p := range []struct {
		method, path string
		code         int
	}{{"GET", "/health", 200}, {"GET", "/_hz/a339", 200}, {"GET", "/metrics", 200}, {"GET", "/unknown", 404}, {"POST", "/health", 405}, {"GET", "/api/v1/auth/me", 401}} {
		status(t, f.call(p.method, p.path, "", "", nil), p.code)
	}
	f.store.offline = true
	w := f.call("GET", "/health", "", "", nil)
	status(t, w, 503)
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("secret leaked")
	}
	status(t, f.call("GET", "/_hz/a339", "", "", nil), 200)
	w = httptest.NewRecorder()
	f.s.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("secret panic") })).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	status(t, w, 500)
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("panic leaked")
	}
}
func TestRequestDeadline(t *testing.T) {
	f := setup(t)
	f.s.Config.RequestTimeout = time.Millisecond
	h := f.s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		fail(w, 503, "TIMEOUT", "Istek zaman asimi")
	}))
	start := time.Now()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", bytes.NewReader(nil)))
	status(t, w, 503)
	if time.Since(start) > time.Second {
		t.Fatal("unbounded request")
	}
}
