package stock

import (
	"context"
	"sort"
	"strconv"
	"time"
)

func (s Service) countCommand(ctx context.Context, tx Tx, actor, command, id string, in Input) (any, error) {
	var product, state string
	var expires time.Time
	if command == "count.start" {
		if in.Count == nil || len(in.Count.Lines) != 0 {
			return nil, Invalid()
		}
		product = in.Count.ProductID
		if _, e := Positive(product); e != nil {
			return nil, e
		}
	} else {
		if e := tx.QueryRow(ctx, `SELECT product_id::text FROM "pl-counts" WHERE id=$1 AND location_id=$2`, id, in.LocationID).Scan(&product); e != nil {
			return nil, e
		}
		if in.Reason == "" || len(in.Reason) > 500 {
			return nil, Invalid()
		}
	}
	// Count headers have no independent lock class; every lifecycle operation owns
	// this exact guard before reading/updating state, expiry, snapshots or results.
	if e := lockGuards(ctx, tx, in.LocationID, []string{product}); e != nil {
		return nil, e
	}
	at, e := s.now(ctx, tx)
	if e != nil {
		return nil, e
	}
	if _, e = tx.Exec(ctx, `UPDATE "pl-counts" SET status='EXPIRED' WHERE location_id=$1 AND product_id=$2 AND status='OPEN' AND expires_at<=$3`, in.LocationID, product, at); e != nil {
		return nil, e
	}
	if command == "count.start" {
		var open bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "pl-counts" WHERE location_id=$1 AND product_id=$2 AND status='OPEN')`, in.LocationID, product).Scan(&open); e != nil {
			return nil, e
		}
		if open {
			return nil, Reject(409, "COUNT_IN_PROGRESS", "Urun icin aktif sayim var")
		}
		if e = tx.QueryRow(ctx, `INSERT INTO "pl-counts"(location_id,product_id,actor_id,status,expires_at) VALUES($1,$2,$3,'OPEN',$4) RETURNING id::text`, in.LocationID, product, actor, at.Add(30*time.Minute)).Scan(&id); e != nil {
			return nil, e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO "pl-count-lines"(count_id,lot_id,account,expected) SELECT $1,b.lot_id,b.account,b.quantity FROM "pl-stock-balances" b JOIN "pl-lots" l ON l.id=b.lot_id WHERE b.location_id=$2 AND l.product_id=$3 AND b.account IN ('physical','quarantine')`, id, in.LocationID, product); e != nil {
			return nil, e
		}
		return map[string]string{"id": id, "status": "OPEN", "expires_at": at.Add(30 * time.Minute).Format(time.RFC3339Nano)}, nil
	}
	if e = tx.QueryRow(ctx, `SELECT status,expires_at FROM "pl-counts" WHERE id=$1`, id).Scan(&state, &expires); e != nil {
		return nil, e
	}
	if state != "OPEN" || !at.Before(expires) {
		return nil, Reject(409, "INVALID_STATE", "Sayim kapali veya suresi dolmus")
	}
	switch command {
	case "count.extend":
		expires = at.Add(30 * time.Minute)
		_, e = tx.Exec(ctx, `UPDATE "pl-counts" SET expires_at=$2 WHERE id=$1`, id, expires)
		return map[string]string{"id": id, "status": "OPEN", "expires_at": expires.Format(time.RFC3339Nano)}, e
	case "count.cancel":
		_, e = tx.Exec(ctx, `UPDATE "pl-counts" SET status='CANCELLED' WHERE id=$1`, id)
		return map[string]string{"id": id, "status": "CANCELLED"}, e
	}
	if in.Count == nil || in.Count.ProductID != product || len(in.Count.Lines) > 200 {
		return nil, Invalid()
	}
	type key struct{ lot, account string }
	expected := map[key]int64{}
	rows, e := tx.Query(ctx, `SELECT lot_id::text,account,expected FROM "pl-count-lines" WHERE count_id=$1`, id)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var k key
		var q int64
		if e = rows.Scan(&k.lot, &k.account, &q); e != nil {
			rows.Close()
			return nil, e
		}
		expected[k] = q
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	actual := map[key]int64{}
	for _, line := range in.Count.Lines {
		if _, e = Positive(line.LotID); e != nil {
			return nil, e
		}
		if line.Account != "physical" && line.Account != "quarantine" {
			return nil, Invalid()
		}
		q, err := strconv.ParseInt(line.Actual, 10, 64)
		if err != nil || q < 0 || strconv.FormatInt(q, 10) != line.Actual {
			return nil, Invalid()
		}
		k := key{line.LotID, line.Account}
		if _, ok := actual[k]; ok {
			return nil, Invalid()
		}
		actual[k] = q
		if _, ok := expected[k]; !ok {
			var valid bool
			if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "pl-lots" WHERE id=$1 AND product_id=$2)`, line.LotID, product).Scan(&valid); e != nil {
				return nil, e
			}
			if !valid {
				return nil, Invalid()
			}
			expected[k] = 0
		}
	}
	for k := range expected {
		if _, ok := actual[k]; !ok {
			return nil, Reject(422, "COUNT_LINES_REQUIRED", "Beklenen her lot/durum icin fiziksel miktar gerekli")
		}
	}
	keys := make([]key, 0, len(expected))
	for k := range expected {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, _ := Positive(keys[i].lot)
		b, _ := Positive(keys[j].lot)
		if a == b {
			return keys[i].account < keys[j].account
		}
		return a < b
	})
	for _, k := range keys {
		before, after := expected[k], actual[k]
		if k.account == "physical" {
			reserved, err := reservedLot(ctx, tx, in.LocationID, k.lot)
			if err != nil {
				return nil, err
			}
			if after < reserved {
				return nil, Reject(409, "ALLOCATION_PROTECTED", "Sayim fiziksel miktari tahsis altina indiremez; once belgeli tahsis uzlastirmasi gerekir")
			}
		}
		if _, e = tx.Exec(ctx, `INSERT INTO "pl-count-results" VALUES($1,$2,$3,$4,$5)`, id, k.lot, k.account, before, after); e != nil {
			return nil, e
		}
		delta := after - before
		if delta == 0 {
			continue
		}
		from, to, q := "count", k.account, delta
		if delta < 0 {
			from, to, q = k.account, "count", -delta
		}
		if e = movement(ctx, tx, "count.finalize", id, in.LocationID, in.LocationID, k.lot, from, to, q); e != nil {
			return nil, e
		}
		if k.account == "quarantine" {
			if delta > 0 {
				if _, e = tx.Exec(ctx, `INSERT INTO "pl-quarantine-items"(event_id,location_id,lot_id,quantity) SELECT max(id),$2,$3,$4 FROM "pl-stock-events" WHERE kind='count.finalize' AND reference=$1`, id, in.LocationID, k.lot, delta); e != nil {
					return nil, e
				}
			} else if e = quarantineCountLoss(ctx, tx, id, in.LocationID, k.lot, -delta); e != nil {
				return nil, e
			}
		}
	}
	_, e = tx.Exec(ctx, `UPDATE "pl-counts" SET status='COMPLETED' WHERE id=$1`, id)
	return map[string]string{"id": id, "status": "COMPLETED"}, e
}

// No source-item row locks here: its guard owns quantity classification. Decisions
// lock their source first, then recompute availability after taking the same guard.
func quarantineCountLoss(ctx context.Context, tx Tx, count, location, lot string, quantity int64) error {
	rows, e := tx.Query(ctx, `SELECT i.id::text,(i.quantity-coalesce((SELECT sum(quantity) FROM "pl-quarantine-decisions" WHERE item_id=i.id),0)-coalesce((SELECT sum(quantity) FROM "pl-quarantine-count-losses" WHERE item_id=i.id),0))::bigint FROM "pl-quarantine-items" i WHERE location_id=$1 AND lot_id=$2 ORDER BY i.id`, location, lot)
	if e != nil {
		return e
	}
	type loss struct {
		id string
		q  int64
	}
	losses := []loss{}
	for rows.Next() {
		var v loss
		if e = rows.Scan(&v.id, &v.q); e != nil {
			rows.Close()
			return e
		}
		if v.q > quantity {
			v.q = quantity
		}
		if v.q > 0 {
			losses = append(losses, v)
			quantity -= v.q
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	// Historical unattributed quarantine can exist; only tracked sources receive a loss reference.
	for _, v := range losses {
		if _, e = tx.Exec(ctx, `INSERT INTO "pl-quarantine-count-losses" VALUES($1,$2,$3)`, count, v.id, v.q); e != nil {
			return e
		}
	}
	return nil
}

// ExpireCounts is a bounded periodic sweep. Every scope is serialized by its guard;
// cancellation or concurrent finalize cannot create a second adjustment.
func (s Service) ExpireCounts(ctx context.Context) error {
	rows, e := s.Pool.Query(ctx, `SELECT id::text,location_id::text,product_id::text FROM "pl-counts" WHERE status='OPEN' AND expires_at<=clock_timestamp() ORDER BY location_id,product_id LIMIT 100`)
	if e != nil {
		return e
	}
	type expired struct{ id, loc, product string }
	items := []expired{}
	for rows.Next() {
		var v expired
		if e = rows.Scan(&v.id, &v.loc, &v.product); e != nil {
			rows.Close()
			return e
		}
		items = append(items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, v := range items {
		tx, e := s.Pool.Begin(ctx)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `SELECT set_config('lock_timeout','2s',true),set_config('statement_timeout','5s',true)`)
		if e == nil {
			e = lockGuards(ctx, tx, v.loc, []string{v.product})
		}
		if e == nil {
			_, e = tx.Exec(ctx, `UPDATE "pl-counts" SET status='EXPIRED' WHERE id=$1 AND status='OPEN' AND expires_at<=clock_timestamp()`, v.id)
		}
		if e == nil {
			e = tx.Commit(ctx)
		} else {
			_ = tx.Rollback(context.Background())
		}
		if e != nil {
			return e
		}
	}
	return nil
}
