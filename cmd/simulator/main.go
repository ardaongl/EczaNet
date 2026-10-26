package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type progress struct {
	Config     string         `json:"config"`
	Next       int64          `json:"next"`
	At         string         `json:"pending_at,omitempty"`
	Sale       map[string]any `json:"pending_sale,omitempty"`
	Last       map[string]any `json:"last_event,omitempty"`
	Expected   int64          `json:"expected_rejections"`
	Unexpected int64          `json:"unexpected_errors"`
}

var mu sync.Mutex
var state progress
var statePath string

func save() error {
	b, e := json.Marshal(state)
	if e != nil {
		return e
	}
	if e = os.WriteFile(statePath+".tmp", b, 0600); e != nil {
		return e
	}
	return os.Rename(statePath+".tmp", statePath)
}

type api struct {
	client                  *http.Client
	csrf, base, origin, key string
}

func (a *api) call(method, path string, body any, want int) (map[string]any, error) {
	b, _ := json.Marshal(body)
	req, e := http.NewRequest(method, a.base+path, bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Origin", a.origin)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", a.csrf)
	req.Header.Set("Idempotency-Key", a.key)
	r, e := a.client.Do(req)
	if e != nil {
		return nil, fmt.Errorf("transport")
	}
	defer r.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(io.LimitReader(r.Body, 2<<20)).Decode(&out)
	if r.StatusCode != want {
		return nil, fmt.Errorf("status_%d_expected_%d", r.StatusCode, want)
	}
	return out, nil
}
func (a *api) product(barcode string) (string, error) {
	m, e := a.call("GET", "/api/v1/products?q="+barcode, nil, 200)
	if e != nil {
		return "", e
	}
	rows, _ := m["items"].([]any)
	for _, r := range rows {
		p := r.(map[string]any)
		if p["barcode"] == barcode {
			return p["id"].(string), nil
		}
	}
	return "", fmt.Errorf("barcode_not_found")
}
func event() error {
	jar, _ := cookiejar.New(nil)
	a := api{client: &http.Client{Jar: jar, Timeout: 20 * time.Second}, base: os.Getenv("API_URL"), origin: os.Getenv("REQUEST_ORIGIN")}
	prep, e := a.call("GET", "/api/v1/auth/csrf", nil, 200)
	if e != nil {
		return e
	}
	a.csrf, _ = prep["csrf_token"].(string)
	login, e := a.call("POST", "/api/v1/auth/login", map[string]string{"email": "simulator@ddo-case.invalid", "password": os.Getenv("DEMO_PASSWORD")}, 200)
	if e != nil {
		return e
	}
	a.csrf, _ = login["csrf_token"].(string)
	defer a.call("POST", "/api/v1/auth/logout", nil, 204)
	mu.Lock()
	s := state
	mu.Unlock()
	key := fmt.Sprintf("sim-%s-%d", s.Config, s.Next)
	a.key = key + "-aux"
	normal, e := a.product(normalBarcode(os.Getenv("DEMO_SEED"), s.Next))
	if e != nil {
		return e
	}
	body := map[string]any{"location_id": "1", "items": []any{map[string]string{"product_id": normal, "quantity": "1", "unit": "unit"}}}
	kind := "sale"
	expected := false
	if s.Sale == nil {
		switch s.Next % 6 {
		case 1:
			kind = "receipt+sale"
			_, e = a.call("POST", "/api/v1/goods-receipts", map[string]any{"location_id": "21", "supplier_id": "1", "document_no": key, "reason": "Deterministik simulator irsaliyesi", "lines": []any{map[string]string{"product_id": normal, "lot": key, "expires_on": "2099-12-31", "quantity": "10"}}}, 201)
		case 2:
			kind = "invalid-barcode+sale"
			// Unknown scanned barcode never becomes a product or a sale line.
			m, err := a.call("GET", "/api/v1/products?q=BOZUK-BARKOD", nil, 200)
			e = err
			if rows, _ := m["items"].([]any); e == nil && len(rows) != 0 {
				e = fmt.Errorf("invalid_barcode_matched")
			}
			if e == nil {
				_, e = a.call("POST", "/api/v1/sales/quote", map[string]any{"location_id": "1", "items": []any{map[string]string{"product_id": "BOZUK-BARKOD", "quantity": "1", "unit": "unit"}}}, 422)
			}
			expected = e == nil
		case 3:
			kind = "expired-receipt+sale"
			_, e = a.call("POST", "/api/v1/goods-receipts", map[string]any{"location_id": "21", "supplier_id": "1", "document_no": key, "reason": "Beklenen gecmis SKT reddi", "lines": []any{map[string]string{"product_id": normal, "lot": key, "expires_on": "2000-01-01", "quantity": "1"}}}, 422)
			expected = e == nil
		case 4, 5:
			kind = "prescription+sale"
			p, err := a.product("000339060303")
			if err != nil {
				return err
			}
			qty := "1"
			// Five controlled dispenses per approximate3000-event day; the remaining
			// prescription sales use existing normal catalog products. Stock survives the
			// configured seven-day prescription demo window without privileged refills.
			if s.Next%6 == 4 && (s.Next/6)%100 != 0 {
				p = normal
			} else if s.Next%6 == 4 {
				kind = "controlled-prescription+sale"
			}
			if s.Next%6 == 5 {
				qty = "11"
				kind = "daily-limit+sale"
			}
			rxbody := map[string]any{"location_id": "1", "prescription": map[string]any{"external_id": key, "patient_external_id": key, "birth_date": "1990-01-01", "issued_at": s.At, "lines": []any{map[string]string{"prescribed_product_id": p, "prescribed_base": qty}}}}
			rx, err := a.call("POST", "/api/v1/prescriptions", rxbody, 201)
			if err != nil {
				return err
			}
			a.key = key + "-duplicate"
			duplicate, err := a.call("POST", "/api/v1/prescriptions", rxbody, 201)
			if err != nil {
				return err
			}
			if rx["id"] != duplicate["id"] {
				return fmt.Errorf("duplicate_rx")
			}
			ls, _ := rx["lines"].([]any)
			if len(ls) != 1 {
				return fmt.Errorf("rx_line")
			}
			rxSale := map[string]any{"location_id": "1", "prescription_id": rx["id"], "dispense": []any{map[string]any{"prescription_line_id": ls[0].(map[string]any)["id"], "dispensed_product_id": p, "quantity": qty, "unit": "unit"}}}
			if s.Next%6 == 4 {
				body = rxSale
			} else {
				_, e = a.call("POST", "/api/v1/sales/quote", rxSale, 422)
				expected = e == nil
			}
		}
		if e != nil {
			return e
		}
		quote, err := a.call("POST", "/api/v1/sales/quote", body, 200)
		if err != nil {
			return err
		}
		body["quote"] = quote["quote"]
		mu.Lock()
		state.Sale = body
		if expected {
			state.Expected++
		}
		e = save()
		mu.Unlock()
		if e != nil {
			return e
		}
	} else {
		body = s.Sale
		kind = "replayed-sale"
	}
	a.key = key + "-sale"
	sale, e := a.call("POST", "/api/v1/sales", body, 201)
	if e != nil {
		return e
	}
	mu.Lock()
	defer mu.Unlock()
	state.Last = map[string]any{"event_id": key, "kind": kind, "sale_id": sale["id"], "total": sale["total"], "completed_at": time.Now().UTC().Format(time.RFC3339Nano)}
	state.Next++
	state.At = ""
	state.Sale = nil
	return save()
}
func main() {
	if len(os.Args) > 1 && os.Args[1] == "probe" {
		c := http.Client{Timeout: time.Second}
		r, e := c.Get("http://127.0.0.1:8090/health")
		if e != nil {
			os.Exit(1)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	enabled := os.Getenv("SIMULATOR_ENABLED") == "true"
	speed := 1.0
	if s := os.Getenv("SIMULATOR_SPEED"); s != "" {
		var e error
		speed, e = strconv.ParseFloat(s, 64)
		if e != nil || speed <= 0 || speed > 1000 {
			slog.Error("invalid speed")
			os.Exit(1)
		}
	}
	statePath = os.Getenv("SIMULATOR_STATE")
	if statePath == "" {
		statePath = "/state/progress.json"
	}
	config := os.Getenv("DEMO_SEED") + "-" + os.Getenv("DEMO_DATE") + "-v2"
	if enabled {
		if b, e := os.ReadFile(statePath); e == nil {
			if json.Unmarshal(b, &state) != nil || state.Config != config {
				slog.Error("state/config mismatch; explicit isolated reset required")
				os.Exit(1)
			}
		} else if !os.IsNotExist(e) {
			os.Exit(1)
		}
		state.Config = config
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		public := state
		public.Sale = nil
		public.At = ""
		status := "stopped"
		if enabled {
			status = "running"
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": status, "speed": speed, "interval_seconds": 28.8 / speed, "progress": public})
	})
	server := &http.Server{Addr: ":8090", Handler: mux, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		if e := server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			stop()
		}
	}()
	workerDone := make(chan struct{})
	if enabled {
		go func() {
			defer close(workerDone)
			timer := time.NewTimer(0)
			defer timer.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-timer.C:
					mu.Lock()
					if state.At == "" {
						state.At = os.Getenv("DEMO_DATE") + "T09:00:00Z"
					}
					e := save()
					mu.Unlock()
					if e == nil {
						e = event()
					}
					if e != nil {
						mu.Lock()
						state.Unexpected++
						_ = save()
						mu.Unlock()
						slog.Error("simulator_event_retry", "class", e.Error())
					}
					timer.Reset(time.Duration(float64(time.Second) * 28.8 / speed))
				}
			}
		}()
	}
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	if enabled {
		select {
		case <-workerDone:
		case <-shutdown.Done():
		}
	}
}

// A full catalog cycle distributes live demand; no new products are created.
func normalBarcode(seed string, event int64) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	n := (h.Sum64()%4995+uint64(event)%4995)%4995 + 1
	return fmt.Sprintf("0099%09d", n)
}
