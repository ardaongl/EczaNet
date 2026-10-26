package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strconv"
	"strings"
	"time"
)

type runner struct {
	base, origin, password string
	client                 *http.Client
	key                    string
	failed                 bool
	commitMS               float64
	hasCommit              bool
	full                   bool
	results                map[string]bool
	controlledLine         string
}

func (v *runner) call(method, path string, body any, csrf string, want int) map[string]any {
	var b io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		b = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, v.base+path, b)
	if err != nil {
		v.failed = true
		return nil
	}
	req.Header.Set("Origin", v.origin)
	req.Header.Set("Idempotency-Key", v.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := v.client.Do(req)
	if err != nil {
		fmt.Printf("FAIL %s %s transport\n", method, path)
		v.failed = true
		return nil
	}
	defer resp.Body.Close()
	v.hasCommit = false
	for _, part := range strings.Split(resp.Header.Get("Server-Timing"), ",") {
		if strings.HasPrefix(strings.TrimSpace(part), "commit;dur=") {
			v.commitMS, err = strconv.ParseFloat(strings.TrimPrefix(strings.TrimSpace(part), "commit;dur="), 64)
			v.hasCommit = err == nil
		}
	}

	if resp.StatusCode != want || resp.Header.Get("X-DDO-Stamp") != "a33906" {
		fmt.Printf("FAIL %s %s status=%d expected=%d stamp=%q\n", method, path, resp.StatusCode, want, resp.Header.Get("X-DDO-Stamp"))
		v.failed = true
	}
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	return result
}
func token(m map[string]any) string {
	if s, ok := m["csrf_token"].(string); ok {
		return s
	}
	return ""
}
func main() {
	started := time.Now()
	base := os.Getenv("API_URL")
	if base == "" {
		base = "http://localhost:8080"
	}
	origin := os.Getenv("REQUEST_ORIGIN")
	if origin == "" {
		origin = "http://localhost:8080"
	}
	jar, _ := cookiejar.New(nil)
	v := runner{base: base, origin: origin, password: os.Getenv("DEMO_PASSWORD"), client: &http.Client{Jar: jar, Timeout: 20 * time.Second}}
	for _, arg := range os.Args[1:] {
		if arg == "--invariants" {
			v.runInvariants()
			if v.failed {
				os.Exit(1)
			}
			return
		}
	}
	if v.password == "" {
		fmt.Println("FAIL DEMO_PASSWORD required")
		os.Exit(1)
	}
	v.call("GET", "/health", nil, "", 200)
	v.call("GET", "/_hz/a339", nil, "", 200)
	v.results = map[string]bool{"HEALTH": !v.failed}
	if !v.failed {
		fmt.Println("PASS HEALTH")
	}
	v.call("GET", "/api/v1/auth/me", nil, "", 401)
	prep := token(v.call("GET", "/api/v1/auth/csrf", nil, "", 200))
	v.call("POST", "/api/v1/auth/login", map[string]string{"email": "seed.yildiz@ddo-case.invalid", "password": "wrong"}, prep, 401)
	accounts := []string{"seed.yildiz", "teknisyen", "depo", "mudur", "muhasebe", "eczaci.b"}
	for _, account := range accounts {
		jar, _ := cookiejar.New(nil)
		v.client.Jar = jar
		prep = token(v.call("GET", "/api/v1/auth/csrf", nil, "", 200))
		result := v.call("POST", "/api/v1/auth/login", map[string]string{"email": account + "@ddo-case.invalid", "password": v.password}, prep, 200)
		csrf := token(result)
		if account == "seed.yildiz" {
			allowed := v.origin
			v.origin = "https://blocked.invalid"
			v.call("POST", "/api/v1/auth/logout", nil, csrf, 403)
			v.origin = allowed
			v.call("POST", "/api/v1/auth/logout", nil, "wrong-csrf", 403)
		}
		v.call("GET", "/api/v1/auth/me", nil, "", 200)
		v.call("GET", "/api/v1/auth/locations", nil, "", 200)
		if account == "seed.yildiz" {
			v.call("GET", "/api/v1/locations/1", nil, "", 200)
			v.call("GET", "/api/v1/locations/2", nil, "", 403)
		}
		v.call("POST", "/api/v1/auth/logout", nil, "", 403)
		v.call("POST", "/api/v1/auth/logout", nil, csrf, 204)
		v.call("GET", "/api/v1/auth/me", nil, "", 401)
	}
	v.call("GET", "/does-not-exist", nil, "", 404)
	v.call("POST", "/health", nil, "", 405)
	v.call("GET", "/metrics", nil, "", 200)
	// Unique nonexistent account keeps this fixture independent of demo user limits.
	prep = token(v.call("GET", "/api/v1/auth/csrf", nil, "", 200))
	email := fmt.Sprintf("verify-%d@ddo-case.invalid", time.Now().UnixNano())
	for i := 0; i < 10; i++ {
		v.call("POST", "/api/v1/auth/login", map[string]string{"email": email, "password": "incorrect-password"}, prep, 401)
	}
	v.call("POST", "/api/v1/auth/login", map[string]string{"email": email, "password": "incorrect-password"}, prep, 429)
	v.results["AUTH"] = !v.failed
	if !v.failed {
		fmt.Println("PASS AUTH (six accounts, logout, CSRF, location scope, stamp)")
	}
	full := true
	a2 := true
	for _, arg := range os.Args[1:] {
		if arg == "--a1" {
			full = false
			a2 = false
		}
	}
	for _, arg := range os.Args[1:] {
		if arg == "--a2" || arg == "--a3" || arg == "--a4" || arg == "--a5" {
			full = false
			a2 = true
		}
	}
	v.full = full
	if full {
		v.stage("T-5", v.runFullT5)
	}
	if a2 {
		v.stage("F-1 F-2 F-4 T-1 IDEMPOTENCY", v.runA2)
	}
	a3 := full
	for _, arg := range os.Args[1:] {
		if arg == "--a3" || arg == "--a4" || arg == "--a5" {
			a3 = true
		}
	}
	if a3 {
		v.stage("F-3 F-4 T-2 T-3", v.runA3)
	}
	a4 := full
	for _, arg := range os.Args[1:] {
		if arg == "--a4" || arg == "--a5" {
			a4 = true
		}
	}
	if a4 {
		v.stage("F-2 F-4 F-5 T-3 T-4", v.runA4)
	}
	a5 := full
	for _, arg := range os.Args[1:] {
		if arg == "--a5" {
			a5 = true
		}
	}
	if a5 {
		v.stage("F-6 F-7", v.runA5)
	}
	if full {
		v.stage("INVARIANTS", v.runInvariants)
		if time.Since(started) >= 15*time.Minute {
			v.failed = true
			fmt.Println("FAIL VERIFY duration>=15min")
		}
		fmt.Printf("VERIFY duration=%s\n", time.Since(started))
	}
	if full {
		for _, id := range strings.Fields("HEALTH AUTH F-1 F-2 F-3 F-4 F-5 F-6 F-7 T-1 T-2 T-3 T-4 T-5 IDEMPOTENCY INVARIANTS") {
			label := "FAIL"
			if v.results[id] {
				label = "PASS"
			}
			fmt.Println(label, id, "FINAL")
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"results": v.results, "duration_seconds": time.Since(started).Seconds(), "passed": !v.failed})
	}
	if v.failed {
		os.Exit(1)
	}
	if full {
		fmt.Println("PASS FULL F-1..F-7 T-1..T-5 AUTH HEALTH IDEMPOTENCY INVARIANTS")
	} else if a5 {
		fmt.Println("PASS A5 targeted verification; general historical invariants remain separate")
	} else if a4 {
		fmt.Println("PASS A4 targeted verification; historical general invariants require separate SQL")
	} else if a3 {
		fmt.Println("PASS A3 targeted verification")
	} else if a2 {
		fmt.Println("PASS A2 targeted verification")
	} else {
		fmt.Println("PASS A1 targeted verification")
	}
}

func (v *runner) stage(ids string, work func()) {
	previous := v.failed
	v.failed = false
	work()
	passed := !v.failed
	for _, id := range strings.Fields(ids) {
		old, exists := v.results[id]
		v.results[id] = passed && (!exists || old)
	}
	v.failed = previous || v.failed
}
