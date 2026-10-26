package platform

import (
	"eczanet/internal/auth"
	"eczanet/internal/stock"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

// business is the common entry point: fresh session, Origin/CSRF for every POST,
// then live command/record/location authorization inside the transaction service.
func (s *Server) business(w http.ResponseWriter, r *http.Request) {
	session, _, ok := s.authenticated(w, r)
	if !ok {
		return
	}
	if r.Method == "POST" {
		if !s.origin(w, r) {
			return
		}
		if !equal(session.CSRFHash, auth.Hash(r.Header.Get("X-CSRF-Token"))) {
			fail(w, 403, "CSRF_REJECTED", "Gecersiz guvenlik kodu")
			return
		}
	}
	if s.Stock == nil {
		fail(w, 503, "UNAVAILABLE", "Stok servisi hazir degil")
		return
	}
	var err error
	if r.Method == "GET" {
		if r.Pattern == "GET /api/v1/sales/{id}" {
			var body json.RawMessage
			body, err = s.Stock.SaleDetail(r.Context(), session.User.ID, r.PathValue("id"))
			if err == nil {
				jsonOut(w, 200, body)
				return
			}
		} else {
			limit := 50
			if v := r.URL.Query().Get("limit"); v != "" {
				limit, err = strconv.Atoi(v)
				if err != nil {
					fail(w, 400, "INVALID_PAGE", "Sayfa limiti gecersiz")
					return
				}
			}
			var result stock.Page
			search := r.URL.Query().Get("q")
			if businessRoutes[r.Pattern] == "equivalents" {
				search = r.PathValue("id")
			}
			result, err = s.Stock.Read(r.Context(), session.User.ID, businessRoutes[r.Pattern], r.URL.Query().Get("location_id"), search, r.URL.Query().Get("cursor"), limit)
			if err == nil {
				jsonOut(w, 200, result)
				return
			}
		}
	} else {
		var input stock.Input
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if e := d.Decode(&input); e != nil {
			fail(w, 400, "INVALID_INPUT", "Gecersiz JSON")
			return
		}
		var extra any
		if e := d.Decode(&extra); !errors.Is(e, io.EOF) {
			fail(w, 400, "INVALID_INPUT", "Tek JSON nesnesi gerekli")
			return
		}
		var result stock.Result
		result, err = s.Stock.Execute(r.Context(), session.User.ID, businessRoutes[r.Pattern], r.PathValue("id"), r.Header.Get("Idempotency-Key"), w.Header().Get("X-Request-ID"), input)
		if err == nil {
			// Transport timing stays outside the cached, idempotent JSON result.
			w.Header().Set("Server-Timing", "commit;dur="+strconv.FormatFloat(result.CommitMS, 'f', 3, 64))
			jsonOut(w, result.Status, result.Body)
			return
		}
	}
	var business *stock.Error
	if errors.As(err, &business) {
		if business.Status < 500 {
			s.rejections.Add(1)
		}
		s.Log.Info("business_result", "request_id", w.Header().Get("X-Request-ID"), "command", businessRoutes[r.Pattern], "actor_id", session.User.ID, "class", business.Code, "status", business.Status)
		if business.NewQuote != nil {
			jsonOut(w, business.Status, map[string]any{"error": map[string]string{"code": business.Code, "message": business.Message}, "new_quote": business.NewQuote})
			return
		}
		fail(w, business.Status, business.Code, business.Message)
		return
	}
	fail(w, 503, "UNAVAILABLE", "Islem tamamlanamadi")
}

var businessRoutes = map[string]string{
	"GET /api/v1/reports/reconciliation": "report-reconciliation",
	"POST /api/v1/suggestion-policies":   "suggestion.policy",
	"POST /api/v1/allocations/{id}/eta":  "allocation.eta",
	"POST /api/v1/suggestion-runs":       "suggestion.calculate", "GET /api/v1/suggestions": "suggestions", "POST /api/v1/suggestions/{id}/need": "suggestion.need", "POST /api/v1/suggestions/{id}/return": "suggestion.return",
	"GET /api/v1/reports/expiry": "report-expiry", "GET /api/v1/reports/critical": "report-critical", "GET /api/v1/reports/finance": "report-finance", "GET /api/v1/reports/transfer-differences": "report-transfers",
	"POST /api/v1/needs": "need.create", "POST /api/v1/needs/{id}/approve": "need.approve", "GET /api/v1/needs": "needs",
	"POST /api/v1/orders": "order.create", "POST /api/v1/orders/{id}/approve": "order.approve", "POST /api/v1/orders/{id}/cancel": "order.cancel", "GET /api/v1/orders": "orders", "GET /api/v1/allocations": "allocations",
	"GET /api/v1/counts": "counts", "POST /api/v1/counts": "count.start", "POST /api/v1/counts/{id}/finalize": "count.finalize", "POST /api/v1/counts/{id}/cancel": "count.cancel", "POST /api/v1/counts/{id}/extend": "count.extend",
	"POST /api/v1/transfers/{id}/cancel": "transfer.cancel",
	"POST /api/v1/returns/quote":         "return.quote", "POST /api/v1/returns": "return.create", "GET /api/v1/returns": "returns", "GET /api/v1/quarantine": "quarantine", "POST /api/v1/quarantine/{id}/decide": "quarantine.decide",
	"POST /api/v1/prescriptions": "prescription.create", "GET /api/v1/prescriptions": "prescriptions", "GET /api/v1/products/{id}/equivalents": "equivalents", "GET /api/v1/prices": "prices", "POST /api/v1/prices": "price.create", "GET /api/v1/controlled-ledger": "controlled-ledger",
	"GET /api/v1/products": "products", "POST /api/v1/products": "product.create", "POST /api/v1/products/{id}/disable": "product.disable",
	"GET /api/v1/inventory": "inventory", "GET /api/v1/inventory/movements": "movements", "GET /api/v1/suppliers": "suppliers", "GET /api/v1/transfer-targets": "targets",
	"POST /api/v1/goods-receipts": "receipt.create", "GET /api/v1/transfers": "transfers", "POST /api/v1/transfers": "transfer.create", "POST /api/v1/transfers/{id}/send": "transfer.send", "POST /api/v1/transfers/{id}/receive": "transfer.receive",
	"POST /api/v1/sales/quote": "sale.quote", "POST /api/v1/sales": "sale.create", "GET /api/v1/sales": "sales", "GET /api/v1/sales/{id}": "sale.detail",
}
