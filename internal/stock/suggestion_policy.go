package stock

import (
	"context"
	"encoding/json"
	"math/big"
	"strconv"
)

type SuggestionPolicyInput struct {
	ProductID string `json:"product_id"`
	Version   string `json:"version"`
	Lead      int    `json:"lead_days"`
	Review    int    `json:"review_days"`
	Safety    int    `json:"safety_days"`
	Season    string `json:"season"`
	Minimum   string `json:"minimum"`
}

func setSuggestionPolicy(ctx context.Context, tx Tx, in Input) (any, error) {
	p := in.SuggestionPolicy
	if p == nil {
		return nil, Invalid()
	}
	if _, e := Positive(p.ProductID); e != nil {
		return nil, e
	}
	if n, e := strconv.ParseInt(p.Minimum, 10, 64); e != nil || n < 0 {
		return nil, Invalid()
	}
	if p.Lead < 0 || p.Review < 0 || p.Safety < 0 || p.Lead > 90 || p.Review > 90 || p.Safety > 90 || p.Lead+p.Review+p.Safety < 1 || p.Lead+p.Review+p.Safety > 90 || len(p.Version) < 1 || len(p.Version) > 100 {
		return nil, Invalid()
	}
	season, ok := new(big.Rat).SetString(p.Season)
	if !ok || season.Sign() <= 0 || season.Cmp(big.NewRat(100, 1)) > 0 {
		return nil, Invalid()
	}
	// Reject excess precision rather than silently rounding a supplied coefficient.
	if !new(big.Rat).Mul(season, big.NewRat(10000, 1)).IsInt() {
		return nil, Invalid()
	}
	payload, _ := json.Marshal(p)
	history, e := tx.Exec(ctx, `INSERT INTO "pl-suggestion-policy-history"(product_id,location_id,version,payload) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, p.ProductID, in.LocationID, p.Version, payload)
	if e != nil {
		return nil, e
	}
	if history.RowsAffected() != 1 {
		return nil, Reject(409, "POLICY_VERSION_CONFLICT", "Politika surumu daha once kullanildi")
	}
	tag, e := tx.Exec(ctx, `INSERT INTO "pl-suggestion-policy"(product_id,location_id,version,lead_days,review_days,safety_days,season,minimum) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(product_id,location_id) DO UPDATE SET version=excluded.version,lead_days=excluded.lead_days,review_days=excluded.review_days,safety_days=excluded.safety_days,season=excluded.season,minimum=excluded.minimum WHERE "pl-suggestion-policy".version<>excluded.version`, p.ProductID, in.LocationID, p.Version, p.Lead, p.Review, p.Safety, p.Season, p.Minimum)
	if e != nil {
		return nil, e
	}
	if tag.RowsAffected() != 1 {
		return nil, Reject(409, "POLICY_VERSION_CONFLICT", "Yeni politika icin yeni surum gerekli")
	}
	return map[string]string{"id": p.ProductID, "version": p.Version}, nil
}
