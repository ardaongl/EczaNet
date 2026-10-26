package stock

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

// HistoricalSeedService is offline-only: database identity, rather than a request
// parameter or app role, restricts the clock adapter to the migration principal.
// It still runs the identical quote/sale transaction and all domain invariants.
func HistoricalSeedService(ctx context.Context, pool *pgxpool.Pool, at time.Time) (Service, error) {
	var role string
	var now time.Time
	if e := pool.QueryRow(ctx, `SELECT current_user,clock_timestamp()`).Scan(&role, &now); e != nil {
		return Service{}, e
	}
	if role != "eczanet_migrator" {
		return Service{}, Reject(403, "FORBIDDEN", "Gecmis seed yalniz offline migrator tarafindan kullanilir")
	}
	if at.IsZero() || at.After(now) {
		return Service{}, Invalid()
	}
	return Service{Pool: pool, clock: func(context.Context, Tx) (time.Time, error) { return at, nil }}, nil
}
