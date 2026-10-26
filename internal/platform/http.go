package platform

import (
	"context"
	"crypto/subtle"
	"eczanet/internal/auth"
	"eczanet/internal/stock"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const sessionCookie = "eczanet_session"
const preCookie = "eczanet_prelogin"

type attempt struct {
	Count int
	Until time.Time
}
type Server struct {
	Stock      *stock.Service
	Config     Config
	Store      auth.Store
	Now        func() time.Time
	Log        *slog.Logger
	mu         sync.Mutex
	pre        map[string]time.Time
	attempts   map[string]attempt
	requests   atomic.Uint64
	rejections atomic.Uint64
	failures   atomic.Uint64
	duration   atomic.Uint64
	dummy      []byte
}

func NewServer(c Config, store auth.Store, logger *slog.Logger) *Server {
	dummy, _ := bcrypt.GenerateFromPassword([]byte(auth.Token()), bcrypt.DefaultCost)
	return &Server{Config: c, Store: store, Now: time.Now, Log: logger, pre: map[string]time.Time{}, attempts: map[string]attempt{}, dummy: dummy}
}
func jsonOut(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, code, message string) {
	jsonOut(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func (s *Server) cookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: s.Config.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: age})
}
func (s *Server) authenticated(w http.ResponseWriter, r *http.Request) (auth.Session, string, bool) {
	c, e := r.Cookie(sessionCookie)
	if e != nil {
		fail(w, 401, "UNAUTHENTICATED", "Oturum gerekli")
		return auth.Session{}, "", false
	}
	session, e := s.Store.Session(r.Context(), auth.Hash(c.Value))
	if errors.Is(e, auth.ErrUnauthenticated) || (e == nil && !auth.Valid(session, s.Now())) {
		fail(w, 401, "UNAUTHENTICATED", "Oturum gerekli")
		return session, "", false
	}
	if e != nil {
		fail(w, 503, "UNAVAILABLE", "Veritabanina ulasilamiyor")
		return session, "", false
	}
	if !auth.Interactive(session.User.Role) && session.User.Role != "simulator" {
		fail(w, 403, "FORBIDDEN", "Bu isleme yetkiniz yok")
		return session, "", false
	}
	return session, c.Value, true
}
func equal(a, b string) bool { return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func (s *Server) origin(w http.ResponseWriter, r *http.Request) bool {
	if !s.Config.Origins[r.Header.Get("Origin")] {
		fail(w, 403, "ORIGIN_REJECTED", "Istek kaynagi izinli degil")
		return false
	}
	return true
}
func (s *Server) csrf(w http.ResponseWriter, r *http.Request) {
	if c, e := r.Cookie(sessionCookie); e == nil {
		session, err := s.Store.Session(r.Context(), auth.Hash(c.Value))
		if err != nil && !errors.Is(err, auth.ErrUnauthenticated) {
			fail(w, 503, "UNAVAILABLE", "Veritabanina ulasilamiyor")
			return
		}
		if err == nil && auth.Valid(session, s.Now()) {
			jsonOut(w, 200, map[string]string{"csrf_token": auth.Hash(c.Value + ":csrf")})
			return
		}
	}
	token := auth.Token()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, t := range s.pre {
		if !s.Now().Before(t) {
			delete(s.pre, k)
		}
	}
	if len(s.pre) >= 2048 {
		fail(w, 429, "RATE_LIMITED", "Daha sonra tekrar deneyin")
		return
	}
	s.pre[auth.Hash(token)] = s.Now().Add(10 * time.Minute)
	s.cookie(w, preCookie, token, 600)
	jsonOut(w, 200, map[string]string{"csrf_token": token})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.origin(w, r) {
		return
	}
	c, e := r.Cookie(preCookie)
	if e != nil {
		fail(w, 403, "CSRF_REJECTED", "Giris hazirligini yenileyin")
		return
	}
	s.mu.Lock()
	expiry, exists := s.pre[auth.Hash(c.Value)]
	s.mu.Unlock()
	if !exists || !s.Now().Before(expiry) || !equal(c.Value, r.Header.Get("X-CSRF-Token")) {
		fail(w, 403, "CSRF_REJECTED", "Giris hazirligini yenileyin")
		return
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		fail(w, 400, "INVALID_INPUT", "Gecersiz JSON")
		return
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		fail(w, 400, "INVALID_INPUT", "Tek JSON nesnesi gerekli")
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if len(input.Email) > 254 || input.Email == "" || len(input.Password) > 72 || input.Password == "" {
		fail(w, 400, "INVALID_INPUT", "E-posta ve parola gerekli")
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	key := auth.Hash(input.Email + "|" + ip)
	s.mu.Lock()
	for k, a := range s.attempts {
		if !s.Now().Before(a.Until) {
			delete(s.attempts, k)
		}
	}
	a := s.attempts[key]
	if a.Count >= 10 || len(s.attempts) >= 4096 {
		s.mu.Unlock()
		w.Header().Set("Retry-After", "900")
		fail(w, 429, "RATE_LIMITED", "Daha sonra tekrar deneyin")
		return
	}
	// Reserve the attempt before bcrypt so concurrent failures cannot bypass the limit.
	if a.Count == 0 {
		a.Until = s.Now().Add(15 * time.Minute)
	}
	a.Count++
	s.attempts[key] = a
	s.mu.Unlock()
	user, hash, err := s.Store.Credentials(r.Context(), input.Email)
	if err != nil && !errors.Is(err, auth.ErrUnauthenticated) {
		fail(w, 503, "UNAVAILABLE", "Veritabanina ulasilamiyor")
		return
	}
	if err != nil {
		hash = string(s.dummy)
	}
	passwordErr := bcrypt.CompareHashAndPassword([]byte(hash), []byte(input.Password))
	if err != nil || passwordErr != nil || (!auth.Interactive(user.Role) && user.Role != "simulator") {
		fail(w, 401, "INVALID_CREDENTIALS", "E-posta veya parola hatali")
		return
	}
	token := auth.Token()
	csrf := auth.Hash(token + ":csrf")
	if err := s.Store.CreateSession(r.Context(), user.ID, auth.Hash(token), auth.Hash(csrf), s.Now().Add(8*time.Hour)); err != nil {
		fail(w, 503, "UNAVAILABLE", "Oturum olusturulamadi")
		return
	}
	// Rotate an existing browser session after successful reauthentication.
	if old, err := r.Cookie(sessionCookie); err == nil {
		if err := s.Store.Revoke(r.Context(), auth.Hash(old.Value)); err != nil {
			_ = s.Store.Revoke(r.Context(), auth.Hash(token))
			fail(w, 503, "UNAVAILABLE", "Oturum yenilenemedi")
			return
		}
	}
	s.mu.Lock()
	delete(s.attempts, key)
	delete(s.pre, auth.Hash(c.Value))
	s.mu.Unlock()
	s.cookie(w, preCookie, "", -1)
	s.cookie(w, sessionCookie, token, 8*3600)
	jsonOut(w, 200, map[string]any{"user": user, "csrf_token": csrf})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if !s.origin(w, r) {
		return
	}
	session, token, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	if !equal(auth.Hash(r.Header.Get("X-CSRF-Token")), session.CSRFHash) {
		fail(w, 403, "CSRF_REJECTED", "Gecersiz guvenlik kodu")
		return
	}
	if err := s.Store.Revoke(r.Context(), auth.Hash(token)); err != nil {
		fail(w, 503, "UNAVAILABLE", "Cikis tamamlanamadi")
		return
	}
	s.cookie(w, sessionCookie, "", -1)
	w.WriteHeader(204)
}
func (s *Server) identity(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	locations, err := s.Store.Locations(r.Context(), session.User.ID)
	if err != nil {
		fail(w, 503, "UNAVAILABLE", "Lokasyonlar okunamadi")
		return
	}
	if r.URL.Path == "/api/v1/auth/me" {
		jsonOut(w, 200, map[string]any{"user": session.User})
		return
	}
	if id := r.PathValue("id"); id != "" {
		for _, l := range locations {
			if l.ID == id {
				jsonOut(w, 200, l)
				return
			}
		}
		fail(w, 403, "FORBIDDEN", "Lokasyona erisim yetkiniz yok")
		return
	}
	jsonOut(w, 200, map[string]any{"locations": locations})
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if err := s.Store.Ping(ctx); err != nil {
		fail(w, 503, "UNAVAILABLE", "Veritabani hazir degil")
		return
	}
	jsonOut(w, 200, map[string]string{"status": "ok"})
}

// Handler wraps routing, including unknown methods and panic responses, with the stamp.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for route := range businessRoutes {
		mux.HandleFunc(route, s.business)
	}
	mux.HandleFunc("GET /api/v1/dashboard", s.dashboard)
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /_hz/a339", func(w http.ResponseWriter, r *http.Request) { jsonOut(w, 200, map[string]string{"status": "alive"}) })
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w, "# TYPE eczanet_http_requests_total counter\neczanet_http_requests_total %d\n# TYPE eczanet_http_failures_total counter\neczanet_http_failures_total %d\n# TYPE eczanet_http_duration_seconds_sum counter\neczanet_http_duration_seconds_sum %f\n", s.requests.Load(), s.failures.Load(), float64(s.duration.Load())/1e9)
		fmt.Fprintf(w, "# TYPE eczanet_business_rejections_total counter\neczanet_business_rejections_total %d\n", s.rejections.Load())
		if p, ok := s.Store.(interface{ PoolStats() (int32, int32) }); ok {
			total, acquired := p.PoolStats()
			fmt.Fprintf(w, "# TYPE eczanet_db_pool_connections gauge\neczanet_db_pool_connections %d\n# TYPE eczanet_db_pool_acquired gauge\neczanet_db_pool_acquired %d\n", total, acquired)
		}
	})
	mux.HandleFunc("GET /api/v1/auth/csrf", s.csrf)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	mux.HandleFunc("GET /api/v1/auth/me", s.identity)
	mux.HandleFunc("GET /api/v1/auth/locations", s.identity)
	mux.HandleFunc("GET /api/v1/locations/{id}", s.identity)
	// Avoid net/http's text error responses for 404/405.
	paths := map[string]string{"/health": "GET", "/_hz/a339": "GET", "/metrics": "GET", "/api/v1/auth/csrf": "GET", "/api/v1/auth/login": "POST", "/api/v1/auth/logout": "POST", "/api/v1/auth/me": "GET", "/api/v1/auth/locations": "GET"}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		method, exists := paths[r.URL.Path]
		if !exists && strings.HasPrefix(r.URL.Path, "/api/v1/locations/") {
			method = "GET"
			exists = true
		}
		if !exists {
			for route := range businessRoutes {
				parts := strings.SplitN(route, " ", 2)
				a, b := strings.Split(parts[1], "/"), strings.Split(r.URL.Path, "/")
				if len(a) != len(b) {
					continue
				}
				match := true
				for i := range a {
					if a[i] != "{id}" && a[i] != b[i] {
						match = false
						break
					}
				}
				if match {
					method = parts[0]
					exists = true
					break
				}
			}
		}
		if exists && r.Method != method {
			w.Header().Set("Allow", method)
			fail(w, 405, "METHOD_NOT_ALLOWED", "HTTP yontemi desteklenmiyor")
			return
		}
		fail(w, 404, "NOT_FOUND", "Uc bulunamadi")
	})
	return s.Middleware(mux)
}

type response struct {
	http.ResponseWriter
	status int
}

func (w *response) WriteHeader(n int) {
	if w.status == 0 {
		w.status = n
		w.ResponseWriter.WriteHeader(n)
	}
}
func (w *response) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func (s *Server) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := auth.Token()[:24]
		w.Header().Set("X-DDO-Stamp", "a33906")
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		rw := &response{ResponseWriter: w}
		s.requests.Add(1)
		defer func() {
			if recover() != nil {
				fail(rw, 500, "INTERNAL", "Beklenmeyen hata")
			}
			if rw.status >= 500 {
				s.failures.Add(1)
			}
			s.duration.Add(uint64(time.Since(start)))
			s.Log.Info("http", "request_id", requestID, "method", r.Method, "status", rw.status, "duration_ms", time.Since(start).Milliseconds())
		}()
		r.Body = http.MaxBytesReader(rw, r.Body, 16*1024)
		ctx, cancel := context.WithTimeout(r.Context(), s.Config.RequestTimeout)
		defer cancel()
		next.ServeHTTP(rw, r.WithContext(ctx))
	})
}
