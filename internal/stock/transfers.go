package stock

import (
	"context"
	"sort"
	"strconv"
)

func cancelTransfer(ctx context.Context, tx Tx, id string, in Input) (any, error) {
	if in.Reason == "" || len(in.Reason) > 500 {
		return nil, Invalid()
	}
	tag, e := tx.Exec(ctx, `UPDATE "pl-transfers" SET status='CANCELLED' WHERE id=$1 AND status='DRAFT'`, id)
	if e != nil {
		return nil, e
	}
	if tag.RowsAffected() != 1 {
		return nil, Reject(409, "INVALID_STATE", "Yalniz taslak iptal edilebilir")
	}
	return map[string]string{"id": id, "status": "CANCELLED"}, nil
}

// Multi-location guards are acquired in numeric (location,product) order, before
// reading any balance. Receipt also changes the source's transit account.
func transferGuards(ctx context.Context, tx Tx, source, target string, products []string) error {
	locations := []string{source, target}
	sort.Slice(locations, func(i, j int) bool { a, _ := Positive(locations[i]); b, _ := Positive(locations[j]); return a < b })
	for _, location := range locations {
		if e := guards(ctx, tx, location, products); e != nil {
			return e
		}
	}
	return nil
}

func (s Service) receiveTransfer(ctx context.Context, tx Tx, actor, id string, in Input) (any, error) {
	var source, target, state string
	if e := tx.QueryRow(ctx, `SELECT source_id::text,target_id::text,status FROM "pl-transfers" WHERE id=$1 FOR UPDATE`, id).Scan(&source, &target, &state); e != nil {
		return nil, e
	}
	if state != "SENT" && state != "PARTIALLY_RECEIVED" {
		return nil, Reject(409, "INVALID_STATE", "Transfer teslim alinabilir durumda degil")
	}
	type sentLine struct {
		lot, product, expiry string
		remaining            int64
	}
	rows, e := tx.Query(ctx, `SELECT l.lot_id::text,p.product_id::text,p.expires_on::text,(l.quantity-coalesce((SELECT sum(r.good::numeric+r.damaged+r.missing) FROM "pl-transfer-receipt-lines" r WHERE r.transfer_id=l.transfer_id AND r.lot_id=l.lot_id),0))::bigint FROM "pl-transfer-lines" l JOIN "pl-lots" p ON p.id=l.lot_id WHERE l.transfer_id=$1 ORDER BY p.product_id,l.lot_id`, id)
	if e != nil {
		return nil, e
	}
	sent := []sentLine{}
	products := []string{}
	for rows.Next() {
		var v sentLine
		if e = rows.Scan(&v.lot, &v.product, &v.expiry, &v.remaining); e != nil {
			rows.Close()
			return nil, e
		}
		sent = append(sent, v)
		products = append(products, v.product)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if len(sent) == 0 {
		return nil, Invalid()
	}
	if e = transferGuards(ctx, tx, source, target, products); e != nil {
		return nil, e
	}
	at, e := s.now(ctx, tx)
	if e != nil {
		return nil, e
	}
	d := in.Delivery
	if d == nil {
		// A2 compatibility: receive all remaining; still recheck expiry at delivery.
		d = &DeliveryInput{Final: true}
		for _, v := range sent {
			if v.remaining > 0 {
				d.Lines = append(d.Lines, DeliveryLine{LotID: v.lot, Good: strconv.FormatInt(v.remaining, 10), Damaged: "0", Missing: "0"})
			}
		}
	}
	if len(d.Lines) > 100 || len(in.Reason) > 500 || (!d.Final && len(d.Lines) == 0) {
		return nil, Invalid()
	}
	input := map[string][3]int64{}
	for _, l := range d.Lines {
		if _, e := Positive(l.LotID); e != nil {
			return nil, e
		}
		if _, exists := input[l.LotID]; exists {
			return nil, Invalid()
		}
		var v [3]int64
		for n, x := range []string{l.Good, l.Damaged, l.Missing} {
			q, e := strconv.ParseInt(x, 10, 64)
			if e != nil || q < 0 || strconv.FormatInt(q, 10) != x {
				return nil, Invalid()
			}
			v[n] = q
		}
		input[l.LotID] = v
	}
	type receiptLine struct {
		lot                    string
		good, damaged, missing int64
	}
	effects := []receiptLine{}
	remaining := int64(0)
	for _, v := range sent {
		x := input[v.lot]
		delete(input, v.lot)
		// Subtraction bounds avoid overflow in untrusted quantities.
		left := v.remaining
		for _, q := range x {
			if q > left {
				return nil, Reject(409, "TRANSFER_QUANTITY_EXCEEDED", "Teslim yolda kalani asiyor")
			}
			left -= q
		}
		if !d.Final && x[2] != 0 {
			return nil, Reject(422, "FINAL_REQUIRED", "Eksik miktar yalniz nihai teslimde kapanir")
		}
		if d.Final && left != 0 {
			return nil, Reject(422, "FINAL_REMAINDER_REQUIRED", "Nihai teslimde kalan miktarin tamami beyan edilmeli")
		}
		if (x[1] > 0 || x[2] > 0) && in.Reason == "" {
			return nil, Reject(422, "REASON_REQUIRED", "Hasar/eksik icin gerekce gerekli")
		}
		if v.expiry < businessDay(at) {
			if x[0] > 0 && in.Reason == "" {
				in.Reason = "Teslim aninda SKT gecmis; otomatik karantina"
			}
			x[1] += x[0]
			x[0] = 0
		}
		if x[0]+x[1]+x[2] > 0 {
			effects = append(effects, receiptLine{v.lot, x[0], x[1], x[2]})
		}
		// Only the presence of a remainder is needed; no cross-product quantity sum.
		if left > 0 {
			remaining = 1
		}
	}
	if len(input) != 0 || len(effects) == 0 {
		return nil, Invalid()
	}
	var receipt string
	if e = tx.QueryRow(ctx, `INSERT INTO "pl-transfer-receipts"(transfer_id,actor_id,final,reason,effective_at) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, id, actor, d.Final, in.Reason, at).Scan(&receipt); e != nil {
		return nil, e
	}
	for _, v := range effects {
		if _, e = tx.Exec(ctx, `UPDATE "pl-transfer-allocations" SET good=good+$3,damaged=damaged+$4,missing=missing+$5 WHERE transfer_id=$1 AND lot_id=$2`, id, v.lot, v.good, v.damaged, v.missing); e != nil {
			return nil, e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO "pl-transfer-receipt-lines" VALUES($1,$2,$3,$4,$5,$6)`, receipt, id, v.lot, v.good, v.damaged, v.missing); e != nil {
			return nil, e
		}
		for _, effect := range []struct {
			kind, account, location string
			quantity                int64
		}{{"good", "physical", target, v.good}, {"damaged", "quarantine", target, v.damaged}, {"missing", "loss", source, v.missing}} {
			if effect.quantity == 0 {
				continue
			}
			kind := "transfer.receive." + effect.kind
			if e = movement(ctx, tx, kind, receipt, source, effect.location, v.lot, "transit:"+id, effect.account, effect.quantity); e != nil {
				return nil, e
			}
			if effect.kind == "damaged" {
				if _, e = tx.Exec(ctx, `INSERT INTO "pl-quarantine-items"(event_id,location_id,lot_id,quantity) SELECT id,$2,$3,$4 FROM "pl-stock-events" WHERE kind=$5 AND reference=$1 AND id=(SELECT max(id) FROM "pl-stock-events" WHERE kind=$5 AND reference=$1)`, receipt, target, v.lot, effect.quantity, kind); e != nil {
					return nil, e
				}
			}
		}
	}
	state = "PARTIALLY_RECEIVED"
	if d.Final {
		state = "CLOSED"
	} else if remaining == 0 {
		return nil, Reject(422, "FINAL_REQUIRED", "Tum miktar icin nihai teslim secin")
	}
	if _, e = tx.Exec(ctx, `UPDATE "pl-transfers" SET status=$2 WHERE id=$1`, id, state); e != nil {
		return nil, e
	}
	return map[string]string{"id": id, "receipt_id": receipt, "status": state}, nil
}
