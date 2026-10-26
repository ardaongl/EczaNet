package stock

import (
	"context"
	"github.com/jackc/pgx/v5"
	"sort"
	"strconv"
	"strings"
	"time"
)

func authorizeProcurement(ctx context.Context, tx Tx, actor, role, kind, cmd, id string, in Input) error {
	allowed := role == "warehouse" && kind == "warehouse"
	if cmd == "need.create" {
		allowed = (role == "pharmacist" || role == "manager") && kind == "branch"
	}
	if cmd == "need.approve" {
		allowed = role == "manager" && kind == "branch"
	}
	if !allowed {
		return Reject(403, "FORBIDDEN", "Tedarik komutuna yetkiniz yok")
	}
	if cmd == "need.approve" || cmd == "order.approve" || cmd == "order.cancel" {
		if _, e := Positive(id); e != nil {
			return e
		}
		table := "pl-orders"
		if cmd == "need.approve" {
			table = "pl-needs"
		}
		var location string
		if e := tx.QueryRow(ctx, `SELECT location_id::text FROM "`+table+`" WHERE id=$1`, id).Scan(&location); e != nil {
			return e
		}
		if location != in.LocationID {
			return Reject(403, "FORBIDDEN", "Belgenin lokasyonuna yetkiniz yok")
		}
	}
	return nil
}

// Lock all existing parent/child documents before any stock guard. Every order
// writer takes the header first; row locks are then acquired in numeric ID order.
func lockOrder(ctx context.Context, tx Tx, id string) error {
	if _, e := Positive(id); e != nil {
		return e
	}
	var found string
	if e := tx.QueryRow(ctx, `SELECT id::text FROM "pl-orders" WHERE id=$1 FOR UPDATE`, id).Scan(&found); e != nil {
		return e
	}
	for _, q := range []string{`SELECT id FROM "pl-order-lines" WHERE order_id=$1 ORDER BY id FOR UPDATE`, `SELECT a.id FROM "pl-order-allocations" a JOIN "pl-order-lines" l ON l.id=a.line_id WHERE l.order_id=$1 ORDER BY a.id FOR UPDATE OF a`, `SELECT x.allocation_id FROM "pl-allocation-lots" x JOIN "pl-order-allocations" a ON a.id=x.allocation_id JOIN "pl-order-lines" l ON l.id=a.line_id WHERE l.order_id=$1 ORDER BY x.allocation_id,x.lot_id FOR UPDATE OF x`} {
		rows, e := tx.Query(ctx, q, id)
		if e != nil {
			return e
		}
		for rows.Next() {
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
	}
	return nil
}

// Transfer order parents precede the transfer header, including replay-independent
// different-key receipt/cancel operations. Links are immutable once a draft exists.
func lockTransferOrder(ctx context.Context, tx Tx, cmd, object string, in Input) error {
	var order string
	if cmd == "transfer.create" && in.AllocationID != "" {
		if _, e := Positive(in.AllocationID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `SELECT l.order_id::text FROM "pl-order-allocations" a JOIN "pl-order-lines" l ON l.id=a.line_id WHERE a.id=$1`, in.AllocationID).Scan(&order); e != nil {
			return e
		}
	} else if strings.HasPrefix(cmd, "transfer.") && cmd != "transfer.create" {
		e := tx.QueryRow(ctx, `SELECT l.order_id::text FROM "pl-transfer-order-links" t JOIN "pl-order-allocations" a ON a.id=t.allocation_id JOIN "pl-order-lines" l ON l.id=a.line_id WHERE t.transfer_id=$1`, object).Scan(&order)
		if e == pgx.ErrNoRows {
			return nil
		}
		if e != nil {
			return e
		}
	}
	if order != "" {
		return lockOrder(ctx, tx, order)
	}
	return nil
}

func procurementCommand(ctx context.Context, tx Tx, actor, cmd, id string, in Input) (any, error) {
	if cmd == "need.create" {
		n := in.Need
		if n == nil || in.Reason == "" || len(in.Reason) > 500 {
			return nil, Invalid()
		}
		for _, v := range []string{n.ProductID, n.Quantity} {
			if _, e := Positive(v); e != nil {
				return nil, e
			}
		}
		var active bool
		if e := tx.QueryRow(ctx, `SELECT active FROM "pl-products" WHERE id=$1`, n.ProductID).Scan(&active); e != nil {
			return nil, e
		}
		if !active {
			return nil, Invalid()
		}
		if e := tx.QueryRow(ctx, `INSERT INTO "pl-needs"(location_id,product_id,quantity,status,actor_id,reason) VALUES($1,$2,$3,'DRAFT',$4,$5) RETURNING id::text`, in.LocationID, n.ProductID, n.Quantity, actor, in.Reason).Scan(&id); e != nil {
			return nil, e
		}
		return map[string]string{"id": id, "status": "DRAFT"}, nil
	}
	if cmd == "need.approve" {
		var state string
		if e := tx.QueryRow(ctx, `SELECT status FROM "pl-needs" WHERE id=$1 FOR UPDATE`, id).Scan(&state); e != nil {
			return nil, e
		}
		if state != "DRAFT" {
			return nil, Reject(409, "INVALID_STATE", "Ihtiyac onaylanabilir durumda degil")
		}
		_, e := tx.Exec(ctx, `UPDATE "pl-needs" SET status='APPROVED' WHERE id=$1`, id)
		return map[string]string{"id": id, "status": "APPROVED"}, e
	}
	if cmd == "order.create" {
		return createOrder(ctx, tx, actor, in)
	}
	if e := lockOrder(ctx, tx, id); e != nil {
		return nil, e
	}
	var state string
	if e := tx.QueryRow(ctx, `SELECT status FROM "pl-orders" WHERE id=$1`, id).Scan(&state); e != nil {
		return nil, e
	}
	if cmd == "order.approve" {
		if state != "DRAFT" {
			return nil, Reject(409, "INVALID_STATE", "Siparis onaylanabilir durumda degil")
		}
		// Products may have been disabled since draft creation.
		var active bool
		if e := tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM "pl-order-lines" l JOIN "pl-products" p ON p.id=l.product_id WHERE l.order_id=$1 AND NOT p.active)`, id).Scan(&active); e != nil {
			return nil, e
		}
		if !active {
			return nil, Invalid()
		}
		_, e := tx.Exec(ctx, `UPDATE "pl-orders" SET status='APPROVED' WHERE id=$1`, id)
		return map[string]string{"id": id, "status": "APPROVED"}, e
	}
	if in.Reason == "" || len(in.Reason) > 500 {
		return nil, Invalid()
	}
	if state != "DRAFT" && state != "APPROVED" && state != "PARTIALLY_RECEIVED" {
		return nil, Reject(409, "INVALID_STATE", "Siparisin acik kalani yok")
	}
	// Cancellation only closes the unreceived remainder. Accepted warehouse holds
	// and in-transit shipments retain their branch ownership.
	if _, e := tx.Exec(ctx, `UPDATE "pl-order-allocations" a SET cancelled=quantity-received WHERE line_id IN (SELECT id FROM "pl-order-lines" WHERE order_id=$1)`, id); e != nil {
		return nil, e
	}
	if _, e := tx.Exec(ctx, `UPDATE "pl-order-lines" SET cancelled=quantity-received WHERE order_id=$1`, id); e != nil {
		return nil, e
	}
	next := "CANCELLED"
	if state == "PARTIALLY_RECEIVED" {
		next = "CLOSED"
	}
	_, e := tx.Exec(ctx, `UPDATE "pl-orders" SET status=$2 WHERE id=$1`, id, next)
	return map[string]string{"id": id, "status": next}, e
}

func createOrder(ctx context.Context, tx Tx, actor string, in Input) (any, error) {
	if in.Order == nil || len(in.Order.Lines) == 0 || len(in.Order.Lines) > 100 || in.Reason == "" || len(in.Reason) > 500 {
		return nil, Invalid()
	}
	if _, e := Positive(in.SupplierID); e != nil {
		return nil, e
	}
	lines := append([]OrderLineInput{}, in.Order.Lines...)
	sort.Slice(lines, func(i, j int) bool {
		a, _ := Positive(lines[i].ProductID)
		b, _ := Positive(lines[j].ProductID)
		return a < b
	})
	ids := []int64{}
	seen := map[string]bool{}
	products := map[string]bool{}
	for _, l := range lines {
		if _, e := Positive(l.ProductID); e != nil {
			return nil, e
		}
		if _, e := Positive(l.Quantity); e != nil {
			return nil, e
		}
		if products[l.ProductID] || len(l.Needs) > 100 {
			return nil, Invalid()
		}
		products[l.ProductID] = true
		for _, id := range l.Needs {
			n, e := Positive(id)
			if e != nil {
				return nil, e
			}
			if seen[id] {
				return nil, Invalid()
			}
			seen[id] = true
			ids = append(ids, n)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	type need struct {
		product, branch string
		quantity        int64
	}
	needs := map[string]need{}
	for _, id := range ids {
		var n need
		var state string
		var used bool
		if e := tx.QueryRow(ctx, `SELECT product_id::text,location_id::text,quantity,status FROM "pl-needs" WHERE id=$1 FOR UPDATE`, id).Scan(&n.product, &n.branch, &n.quantity, &state); e != nil {
			return nil, e
		}
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "pl-order-allocations" WHERE need_id=$1)`, id).Scan(&used); e != nil {
			return nil, e
		}
		if state != "APPROVED" || used {
			return nil, Reject(409, "NEED_UNAVAILABLE", "Ihtiyac onaysiz veya daha once siparise baglandi")
		}
		needs[strconv.FormatInt(id, 10)] = n
	}
	var id string
	if e := tx.QueryRow(ctx, `INSERT INTO "pl-orders"(location_id,supplier_id,actor_id,status,reason) VALUES($1,$2,$3,'DRAFT',$4) RETURNING id::text`, in.LocationID, in.SupplierID, actor, in.Reason).Scan(&id); e != nil {
		return nil, e
	}
	for _, l := range lines {
		var active bool
		if e := tx.QueryRow(ctx, `SELECT active FROM "pl-products" WHERE id=$1`, l.ProductID).Scan(&active); e != nil {
			return nil, e
		}
		if !active {
			return nil, Invalid()
		}
		available, _ := Positive(l.Quantity)
		var line string
		if e := tx.QueryRow(ctx, `INSERT INTO "pl-order-lines"(order_id,product_id,quantity) VALUES($1,$2,$3) RETURNING id::text`, id, l.ProductID, l.Quantity).Scan(&line); e != nil {
			return nil, e
		}
		sort.Slice(l.Needs, func(i, j int) bool { a, _ := Positive(l.Needs[i]); b, _ := Positive(l.Needs[j]); return a < b })
		for _, nid := range l.Needs {
			n := needs[nid]
			if n.product != l.ProductID || n.quantity > available {
				return nil, Reject(422, "ALLOCATION_EXCEEDED", "Tahsis siparis miktarini/urununu asiyor")
			}
			available -= n.quantity
			if _, e := tx.Exec(ctx, `INSERT INTO "pl-order-allocations"(line_id,need_id,location_id,quantity) VALUES($1,$2,$3,$4)`, line, nid, n.branch, n.quantity); e != nil {
				return nil, e
			}
		}
	}
	return map[string]string{"id": id, "status": "DRAFT"}, nil
}

func checkReceiptOrder(ctx context.Context, tx Tx, in Input) error {
	var location, supplier, state string
	if e := tx.QueryRow(ctx, `SELECT location_id::text,supplier_id::text,status FROM "pl-orders" WHERE id=$1`, in.OrderID).Scan(&location, &supplier, &state); e != nil {
		return e
	}
	if location != in.LocationID {
		return Reject(403, "FORBIDDEN", "Siparis deposuna yetkiniz yok")
	}
	if supplier != in.SupplierID {
		return Invalid()
	}
	if state != "APPROVED" && state != "PARTIALLY_RECEIVED" {
		return Reject(409, "INVALID_STATE", "Siparis kabule acik degil")
	}
	return nil
}

func applyOrderReceipt(ctx context.Context, tx Tx, receipt, lot, product string, q int64, in Input) error {
	var line string
	var remaining int64
	e := tx.QueryRow(ctx, `SELECT id::text,quantity-received-cancelled FROM "pl-order-lines" WHERE order_id=$1 AND product_id=$2`, in.OrderID, product).Scan(&line, &remaining)
	if e == pgx.ErrNoRows {
		return Invalid()
	}
	if e != nil {
		return e
	}
	accepted, unsuitable := q, int64(0)
	if in.ReceiptMode == "quarantine" {
		accepted, unsuitable = 0, q
	}
	if accepted > remaining {
		return Reject(409, "ORDER_QUANTITY_EXCEEDED", "Uygun kabul siparisin acik kalanini asiyor")
	}
	if _, e = tx.Exec(ctx, `INSERT INTO "pl-order-receipt-lines" VALUES($1,$2,$3,$4,$5)`, receipt, lot, line, accepted, unsuitable); e != nil {
		return e
	}
	if accepted == 0 {
		return nil
	}
	if _, e = tx.Exec(ctx, `UPDATE "pl-order-lines" SET received=received+$2 WHERE id=$1`, line, accepted); e != nil {
		return e
	}
	rows, e := tx.Query(ctx, `SELECT id::text,quantity-received-cancelled FROM "pl-order-allocations" WHERE line_id=$1 ORDER BY id`, line)
	if e != nil {
		return e
	}
	type share struct {
		id string
		q  int64
	}
	shares := []share{}
	for rows.Next() {
		var x share
		if e = rows.Scan(&x.id, &x.q); e != nil {
			rows.Close()
			return e
		}
		if x.q > accepted {
			x.q = accepted
		}
		if x.q > 0 {
			shares = append(shares, x)
			accepted -= x.q
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, x := range shares {
		if _, e = tx.Exec(ctx, `UPDATE "pl-order-allocations" SET received=received+$2 WHERE id=$1`, x.id, x.q); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO "pl-allocation-lots"(allocation_id,lot_id,quantity) VALUES($1,$2,$3) ON CONFLICT(allocation_id,lot_id) DO UPDATE SET quantity="pl-allocation-lots".quantity+excluded.quantity`, x.id, lot, x.q); e != nil {
			return e
		}
	}
	return nil
}

func reservedLot(ctx context.Context, tx Tx, location, lot string) (int64, error) {
	var q int64
	e := tx.QueryRow(ctx, `SELECT coalesce(sum(x.quantity-x.shipped),0)::bigint FROM "pl-allocation-lots" x JOIN "pl-order-allocations" a ON a.id=x.allocation_id JOIN "pl-order-lines" l ON l.id=a.line_id JOIN "pl-orders" o ON o.id=l.order_id WHERE o.location_id=$1 AND x.lot_id=$2`, location, lot).Scan(&q)
	return q, e
}

func allocateReserved(ctx context.Context, tx Tx, aid, source, target, product string, quantity int64, at time.Time) ([]allocation, error) {
	var from, to, p string
	if e := tx.QueryRow(ctx, `SELECT o.location_id::text,a.location_id::text,l.product_id::text FROM "pl-order-allocations" a JOIN "pl-order-lines" l ON l.id=a.line_id JOIN "pl-orders" o ON o.id=l.order_id WHERE a.id=$1`, aid).Scan(&from, &to, &p); e != nil {
		return nil, e
	}
	if from != source || to != target {
		return nil, Reject(403, "FORBIDDEN", "Tahsis kaynak/hedef ile uyusmuyor")
	}
	if p != product {
		return nil, Invalid()
	}
	rows, e := tx.Query(ctx, `SELECT x.lot_id::text,x.quantity-x.shipped FROM "pl-allocation-lots" x JOIN "pl-lots" l ON l.id=x.lot_id WHERE x.allocation_id=$1 AND x.quantity>x.shipped AND l.expires_on>=($2::timestamptz AT TIME ZONE 'Europe/Istanbul')::date ORDER BY l.expires_on,l.id`, aid, at)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []allocation{}
	for rows.Next() {
		var lot string
		var q int64
		if e = rows.Scan(&lot, &q); e != nil {
			return nil, e
		}
		if q > quantity {
			q = quantity
		}
		if q > 0 {
			out = append(out, allocation{lot, product, q, strconv.FormatInt(q, 10)})
			quantity -= q
		}
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if quantity > 0 {
		return nil, Reject(409, "INSUFFICIENT_ALLOCATION", "Uygun depoda tahsisli miktar yetersiz")
	}
	return out, nil
}

func reserveShipment(ctx context.Context, tx Tx, transfer, location, lot string, q int64) error {
	var aid string
	e := tx.QueryRow(ctx, `SELECT allocation_id::text FROM "pl-transfer-order-links" WHERE transfer_id=$1`, transfer).Scan(&aid)
	if e == pgx.ErrNoRows {
		reserved, e := reservedLot(ctx, tx, location, lot)
		if e != nil {
			return e
		}
		var stock int64
		if e = tx.QueryRow(ctx, `SELECT quantity FROM "pl-stock-balances" WHERE location_id=$1 AND lot_id=$2 AND account='physical'`, location, lot).Scan(&stock); e != nil {
			return e
		}
		if reserved > stock || q > stock-reserved {
			return Reject(409, "INSUFFICIENT_STOCK", "Tahsisli stok serbest sevke verilemez")
		}
		return nil
	}
	if e != nil {
		return e
	}
	tag, e := tx.Exec(ctx, `UPDATE "pl-allocation-lots" SET shipped=shipped+$3 WHERE allocation_id=$1 AND lot_id=$2 AND quantity-shipped>=$3`, aid, lot, q)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return Reject(409, "INSUFFICIENT_ALLOCATION", "Tahsis daha once sevk edilmis")
	}
	_, e = tx.Exec(ctx, `INSERT INTO "pl-transfer-allocations"(transfer_id,lot_id,allocation_id,quantity) VALUES($1,$2,$3,$4)`, transfer, lot, aid, q)
	return e
}
