package stock

import (
	"context"
	_ "embed"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"strconv"
	"time"
)

//go:embed suggestion_calculation.sql
var suggestionCalculationSQL string

func setProcurementETA(ctx context.Context, tx Tx, id string, in Input) (any, error) {
	if _, e := Positive(id); e != nil {
		return nil, e
	}
	if _, e := time.Parse("2006-01-02", in.ExpectedOn); e != nil {
		return nil, Invalid()
	}
	var order, warehouse string
	if e := tx.QueryRow(ctx, `SELECT l.order_id::text,o.location_id::text FROM "pl-order-allocations" a JOIN "pl-order-lines" l ON l.id=a.line_id JOIN "pl-orders" o ON o.id=l.order_id WHERE a.id=$1`, id).Scan(&order, &warehouse); e != nil {
		return nil, e
	}
	if warehouse != in.LocationID {
		return nil, Reject(403, "FORBIDDEN", "Tahsis deposuna yetkiniz yok")
	}
	if e := lockOrder(ctx, tx, order); e != nil {
		return nil, e
	}
	_, e := tx.Exec(ctx, `INSERT INTO "pl-procurement-eta" VALUES($1,$2) ON CONFLICT(allocation_id) DO UPDATE SET expected_on=excluded.expected_on`, id, in.ExpectedOn)
	return map[string]string{"id": id, "expected_on": in.ExpectedOn}, e
}

func calculateSuggestions(ctx context.Context, tx Tx, asOf string) (any, error) {
	if _, e := time.Parse("2006-01-02", asOf); e != nil {
		return nil, Invalid()
	}
	if _, e := tx.Exec(ctx, `SET LOCAL jit=off; SET LOCAL work_mem='64MB'`); e != nil {
		return nil, e
	}
	// REPEATABLE READ is chosen by once(). A single snapshot covers summary + inputs + publication.
	var started time.Time
	if e := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&started); e != nil {
		return nil, e
	}
	preparationStarted := started
	var summaryFresh bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "pl-summary-watermark" WHERE source="pl-sales-source"())`).Scan(&summaryFresh); e != nil {
		return nil, e
	}
	if !summaryFresh {
		if _, e := tx.Exec(ctx, `SELECT "pl-refresh-daily-sales"()`); e != nil {
			return nil, e
		}
	}

	var preparation float64
	if e := tx.QueryRow(ctx, `SELECT extract(epoch FROM clock_timestamp()-$1::timestamptz)*1000`, started).Scan(&preparation); e != nil {
		return nil, e
	}
	var id string
	if e := tx.QueryRow(ctx, `INSERT INTO "pl-suggestion-runs"(as_of,source,summary_source,summary_ms) VALUES($1,"pl-suggestion-source"(),"pl-sales-source"(),$2) RETURNING id::text`, asOf, preparation).Scan(&id); e != nil {
		return nil, e
	}
	if e := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&started); e != nil {
		return nil, e
	}
	preparationMS := float64(started.Sub(preparationStarted).Microseconds()) / 1000
	// These immutable parent mappings replace redundant per-result parent checks.
	// They are written inside the same snapshot and calculation timing, never prewarmed.
	if _, e := tx.Exec(ctx, `INSERT INTO "pl-suggestion-run-products" SELECT $1,id FROM "pl-products" WHERE active`, id); e != nil {
		return nil, e
	}
	if _, e := tx.Exec(ctx, `INSERT INTO "pl-suggestion-run-locations" SELECT $1,id FROM "pl-locations" WHERE kind='branch'`, id); e != nil {
		return nil, e
	}
	var scopeFinished time.Time
	if e := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&scopeFinished); e != nil {
		return nil, e
	}
	tag, e := tx.Exec(ctx, `INSERT INTO "pl-order-suggestions"(run_id,location_id,product_id,observed_days,gross,returned,rate,horizon,season,minimum,target,physical,risk,effective,pending,excluded_pending,unknown_lot,suggested,boxes,return_quantity,defaults,history_unknown,policy_version) SELECT $1,x.* FROM (`+suggestionCalculationSQL+`) x ORDER BY x.product_id,x.location_id`, pgx.QueryExecModeSimpleProtocol, id, asOf)
	if e != nil {
		return nil, e
	}
	var elapsed float64
	if e = tx.QueryRow(ctx, `UPDATE "pl-suggestion-runs" SET published=true,row_count=$2,calculation_ms=extract(epoch FROM clock_timestamp()-$3::timestamptz)*1000 WHERE id=$1 RETURNING calculation_ms`, id, tag.RowsAffected(), started).Scan(&elapsed); e != nil {
		return nil, e
	}
	return map[string]any{"id": id, "rows": strconv.FormatInt(tag.RowsAffected(), 10), "as_of": asOf, "version": "onr-v1", "summary_ms": preparation, "preparation_ms": preparationMS, "scope_ms": float64(scopeFinished.Sub(started).Microseconds()) / 1000, "summary_rebuilt": !summaryFresh, "calculation_ms": elapsed, "published": true}, nil
}

func consumeSuggestion(ctx context.Context, tx Tx, actor, object, cmd string, in Input) (any, error) {
	if len(in.Reason) == 0 || len(in.Reason) > 500 {
		return nil, Invalid()
	}
	if _, e := Positive(object); e != nil {
		return nil, e
	}
	var product, location, quantity, returns, source, asOf string
	var published bool
	if e := tx.QueryRow(ctx, `SELECT s.product_id::text,s.location_id::text,s.suggested::text,s.return_quantity::text,r.source,r.as_of::text,r.published FROM "pl-order-suggestions" s JOIN "pl-suggestion-runs" r ON r.id=s.run_id WHERE s.id=$1 FOR UPDATE OF s`, object).Scan(&product, &location, &quantity, &returns, &source, &asOf, &published); e != nil {
		return nil, e
	}
	if location != in.LocationID {
		return nil, Reject(403, "FORBIDDEN", "Onerinin subesine yetkiniz yok")
	}
	var fresh bool
	if e := tx.QueryRow(ctx, `SELECT $1="pl-suggestion-source"() AND $2::date=(clock_timestamp() AT TIME ZONE 'Europe/Istanbul')::date`, source, asOf).Scan(&fresh); e != nil {
		return nil, e
	}
	if !fresh || !published {
		return nil, Reject(409, "STALE_SUGGESTION", "Veriler degisti; hesabi yenileyip yeniden onaylayin")
	}
	kind := "need"
	if cmd == "suggestion.return" {
		kind = "return"
		quantity = returns
	}
	var exists bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "pl-suggestion-consumptions" WHERE suggestion_id=$1 AND kind=$2)`, object, kind).Scan(&exists); e != nil {
		return nil, e
	}
	if exists {
		return nil, Reject(409, "SUGGESTION_CONSUMED", "Oneri bu belge icin kullanildi")
	}
	if _, e := Positive(quantity); e != nil {
		return nil, Reject(422, "ZERO_SUGGESTION", "Bu oneride uretilecek miktar yok")
	}
	var result any
	var e error
	if kind == "need" {
		in.Need = &NeedInput{ProductID: product, Quantity: quantity}
		result, e = procurementCommand(ctx, tx, actor, "need.create", "", in)
	} else {
		var warehouse bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "pl-locations" WHERE id=$1 AND kind='warehouse')`, in.TargetID).Scan(&warehouse); e != nil {
			return nil, e
		}
		if !warehouse {
			return nil, Invalid()
		}
		in.Items = []Item{{ProductID: product, Quantity: quantity, Unit: "unit"}}
		result, e = createTransfer(ctx, tx, in)
	}
	if e != nil {
		return nil, e
	}
	b, _ := json.Marshal(result)
	var doc struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &doc)
	_, e = tx.Exec(ctx, `INSERT INTO "pl-suggestion-consumptions" VALUES($1,$2,$3)`, object, kind, doc.ID)
	return result, e
}
