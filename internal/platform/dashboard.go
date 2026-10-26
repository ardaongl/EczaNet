package platform

import (
	"encoding/json"
	"net/http"
	"os"
	"time"
)

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	if session.User.Role == "simulator" {
		fail(w, 403, "FORBIDDEN", "Servis hesabi siniri")
		return
	}
	if s.Stock == nil {
		fail(w, 503, "UNAVAILABLE", "Veri servisi hazir degil")
		return
	}
	var sales, physical string
	// Aggregate only the caller's locations; no patient/RX identifiers in status.
	e := s.Stock.Pool.QueryRow(r.Context(), `SELECT
 (SELECT count(*)::text FROM "pl-sales" x WHERE $2 OR EXISTS(SELECT 1 FROM "pl-user-locations" u WHERE u.user_id=$1 AND u.location_id=x.location_id)),
 (SELECT coalesce(sum(quantity),0)::text FROM "pl-stock-balances" b WHERE account='physical' AND ($2 OR EXISTS(SELECT 1 FROM "pl-user-locations" u WHERE u.user_id=$1 AND u.location_id=b.location_id)))`, session.User.ID, session.User.Role == "accountant").Scan(&sales, &physical)
	if e != nil {
		fail(w, 503, "UNAVAILABLE", "Ozet okunamadi")
		return
	}
	simulator := map[string]any{"status": "offline"}
	url := os.Getenv("SIMULATOR_STATUS_URL")
	if url == "" {
		url = "http://simulator:8090/health"
	}
	client := http.Client{Timeout: time.Second}
	req, _ := http.NewRequestWithContext(r.Context(), "GET", url, nil)
	if response, err := client.Do(req); err == nil {
		defer response.Body.Close()
		if response.StatusCode == 200 {
			_ = json.NewDecoder(response.Body).Decode(&simulator)
		}
	}
	// Branch-specific event details are visible only to branch1 participants.
	locations, err := s.Store.Locations(r.Context(), session.User.ID)
	show := false
	if err != nil {
		fail(w, 503, "UNAVAILABLE", "Lokasyonlar okunamadi")
		return
	}
	for _, l := range locations {
		if l.ID == "1" {
			show = true
		}
	}
	if !show {
		delete(simulator, "progress")
	}
	jsonOut(w, 200, map[string]any{"sale_count": sales, "physical": physical, "simulator": simulator, "updated_at": time.Now().UTC()})
}
