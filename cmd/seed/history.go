package main

import (
	"context"
	"database/sql"
	"eczanet/internal/stock"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

// A tiny deterministic historical sample prepares the A6 bulk path. Volume and
// 90-day simulation are deliberately not claimed here. Re-runs do not sell twice.
func seedHistory(ctx context.Context, db *sql.DB, dsn, date, seed string) error {
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(33908)`); e != nil {
		return e
	}
	var done bool
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM "pl-feature-seeds" WHERE version='a3-history-v1')`).Scan(&done)
	if e != nil {
		return e
	}
	if done {
		return tx.Commit()
	}
	pool, e := pgxpool.New(ctx, dsn)
	if e != nil {
		return e
	}
	defer pool.Close()
	day, _ := time.Parse("2006-01-02", date)
	historical, e := stock.HistoricalSeedService(ctx, pool, day.Add(12*time.Hour))
	if e != nil {
		return e
	}
	// If the sample sale committed but the metadata transaction lost its response,
	// the stored sale idempotency record is authoritative; do not create new intent.
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM "pl-idempotency" WHERE actor_id=1 AND command='sale.create:' AND location_id=1 AND key='seed-a3-history-v1-sale' AND status=201)`).Scan(&done)
	if e != nil {
		return e
	}
	if !done {
		var product string
		e = tx.QueryRowContext(ctx, `SELECT id::text FROM "pl-products" WHERE barcode='000339060302'`).Scan(&product)
		if e != nil {
			return e
		}
		rx := stock.PrescriptionInput{ExternalID: "A3-RX-HISTORY", PatientID: "A3-HISTORY", BirthDate: "1990-01-01", IssuedAt: day.Add(9 * time.Hour), Lines: []stock.PrescriptionLineInput{{ProductID: product, Quantity: "3"}}}
		result, e := historical.Execute(ctx, "1", "prescription.create", "", "seed-a3-history-v1-rx", "offline-seed", stock.Input{LocationID: "1", Prescription: &rx})
		if e != nil {
			return e
		}
		var imported struct {
			ID    string `json:"id"`
			Lines []struct {
				ID string `json:"id"`
			} `json:"lines"`
		}
		if e = json.Unmarshal(result.Body, &imported); e != nil {
			return e
		}
		in := stock.Input{LocationID: "1", PrescriptionID: imported.ID, Dispense: []stock.DispenseItem{{LineID: imported.Lines[0].ID, ProductID: product, Quantity: "2", Unit: "unit"}}}
		quote, e := historical.Execute(ctx, "1", "sale.quote", "", "", "offline-seed", in)
		if e != nil {
			return e
		}
		var q struct {
			Quote string `json:"quote"`
		}
		if e = json.Unmarshal(quote.Body, &q); e != nil {
			return e
		}
		in.Quote = q.Quote
		if _, e = historical.Execute(ctx, "1", "sale.create", "", "seed-a3-history-v1-sale", "offline-seed", in); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO "pl-feature-seeds" VALUES('a3-history-v1',$1,$2)`, date, seed); e != nil {
		return e
	}
	return tx.Commit()
}
