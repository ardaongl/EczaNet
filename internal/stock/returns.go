package stock

import (
	"context"
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
)

func previewCommand(command string) bool { return command == "sale.quote" || command == "return.quote" }

// RefundDelta rounds each share cumulatively. It never reads a current price.
func RefundDelta(share, sold, previous, quantity int64) (int64, error) {
	if share < 0 || sold <= 0 || previous < 0 || quantity <= 0 || previous > sold || quantity > sold-previous {
		return 0, Invalid()
	}
	cumulative := func(n int64) (int64, error) {
		return halfUp(new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(share), big.NewInt(n)), big.NewInt(sold)))
	}
	before, e := cumulative(previous)
	if e != nil {
		return 0, e
	}
	after, e := cumulative(previous + quantity)
	return after - before, e
}

// Called before cached success as well as before a new command. Document locations
// cannot change, so this check does not require a lock above the idempotency class.
func authorizeReturn(ctx context.Context, tx Tx, command, object string, in Input) error {
	var location string
	if command == "quarantine.decide" {
		if _, e := Positive(object); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `SELECT location_id::text FROM "pl-quarantine-items" WHERE id=$1`, object).Scan(&location); e != nil {
			return e
		}
	} else {
		if in.Return == nil {
			return Invalid()
		}
		for _, id := range []string{in.Return.SaleID, in.Return.SaleLineID, in.Return.LotID, in.Return.Quantity} {
			if _, e := Positive(id); e != nil {
				return e
			}
		}
		if e := tx.QueryRow(ctx, `SELECT location_id::text FROM "pl-sales" WHERE id=$1`, in.Return.SaleID).Scan(&location); e != nil {
			return e
		}
	}
	if location != in.LocationID {
		return Reject(403, "FORBIDDEN", "Kaynak belgenin subesine yetkiniz yok")
	}
	return nil
}

func (s Service) returnSale(ctx context.Context, tx Tx, actor, command string, in Input) (any, error) {
	r := in.Return
	if r == nil || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 500 {
		return nil, Invalid()
	}
	// One sale header serializes every line/lot return, including different keys.
	var sale string
	if e := tx.QueryRow(ctx, `SELECT id::text FROM "pl-sales" WHERE id=$1 AND location_id=$2 FOR UPDATE`, r.SaleID, in.LocationID).Scan(&sale); e != nil {
		return nil, e
	}
	var product string
	var sold, allocated, total int64
	var snapshot []byte
	var rx bool
	var validPrice bool
	e := tx.QueryRow(ctx, `SELECT l.product_id::text,l.quantity,a.quantity,l.total,l.snapshot,s.prescription_id IS NOT NULL,(s.effective_at>=p.valid_from AND s.effective_at<p.valid_to)
 FROM "pl-sale-lines" l JOIN "pl-sales" s ON s.id=l.sale_id JOIN "pl-sale-allocations" a ON a.line_id=l.id JOIN "pl-price-periods" p ON p.id=l.price_id
 WHERE l.id=$1 AND l.sale_id=$2 AND a.lot_id=$3`, r.SaleLineID, r.SaleID, r.LotID).Scan(&product, &sold, &allocated, &total, &snapshot, &rx, &validPrice)
	if e != nil {
		return nil, e
	}
	patient, institution := total, int64(0)
	if !validPrice {
		return nil, Reject(422, "RETURN_SNAPSHOT_INVALID", "Satis snapshot incelemesi gerekiyor")
	}
	if rx {
		var snap struct {
			Total   string       `json:"total"`
			Pricing *Calculation `json:"pricing"`
		}
		if json.Unmarshal(snapshot, &snap) != nil || snap.Pricing == nil || snap.Total != strconv.FormatInt(total, 10) {
			return nil, Reject(422, "RETURN_SNAPSHOT_INVALID", "Satis snapshot incelemesi gerekiyor")
		}
		patient, e = strconv.ParseInt(snap.Pricing.Patient, 10, 64)
		if e != nil {
			return nil, Invalid()
		}
		institution, e = strconv.ParseInt(snap.Pricing.Institution, 10, 64)
		if e != nil || patient < 0 || institution < 0 || patient > total || institution != total-patient {
			return nil, Invalid()
		}
	}
	var previous, lotPrevious int64
	if e = tx.QueryRow(ctx, `SELECT coalesce(sum(quantity),0)::bigint,coalesce(sum(quantity) FILTER(WHERE lot_id=$2),0)::bigint FROM "pl-return-lines" WHERE sale_line_id=$1`, r.SaleLineID, r.LotID).Scan(&previous, &lotPrevious); e != nil {
		return nil, e
	}
	q, _ := Positive(r.Quantity)
	if previous > sold || q > sold-previous || lotPrevious > allocated || q > allocated-lotPrevious {
		return nil, Reject(409, "RETURN_QUANTITY_EXCEEDED", "Iade satilan satir/lot miktarini asiyor")
	}
	p, e := RefundDelta(patient, sold, previous, q)
	if e != nil {
		return nil, e
	}
	i, e := RefundDelta(institution, sold, previous, q)
	if e != nil {
		return nil, e
	}
	value := map[string]string{"sale_id": r.SaleID, "sale_line_id": r.SaleLineID, "lot_id": r.LotID, "quantity": r.Quantity, "cumulative_quantity": strconv.FormatInt(previous+q, 10), "patient_total": strconv.FormatInt(p, 10), "institution_total": strconv.FormatInt(i, 10), "total": strconv.FormatInt(p+i, 10)}
	token := digest(value)
	value["quote"] = token
	if previewCommand(command) {
		return value, nil
	}
	if r.Quote != "" && r.Quote != token {
		return nil, Reject(409, "RETURN_CHANGED", "Iade miktari/paylari degisti; yeniden onizleyin")
	}
	if e = guards(ctx, tx, in.LocationID, []string{product}); e != nil {
		return nil, e
	}
	at, e := s.now(ctx, tx)
	if e != nil {
		return nil, e
	}
	var id, line, event, item string
	if e = tx.QueryRow(ctx, `INSERT INTO "pl-returns"(sale_id,location_id,actor_id,reason,patient_total,institution_total,effective_at) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text`, sale, in.LocationID, actor, in.Reason, p, i, at).Scan(&id); e != nil {
		return nil, e
	}
	if e = tx.QueryRow(ctx, `INSERT INTO "pl-return-lines"(return_id,sale_id,sale_line_id,lot_id,quantity,cumulative_quantity,patient_total,institution_total) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`, id, sale, r.SaleLineID, r.LotID, q, previous+q, p, i).Scan(&line); e != nil {
		return nil, e
	}
	if e = movement(ctx, tx, "return", id, in.LocationID, in.LocationID, r.LotID, "sold", "quarantine", q); e != nil {
		return nil, e
	}
	if e = tx.QueryRow(ctx, `SELECT id::text FROM "pl-stock-events" WHERE kind='return' AND reference=$1`, id).Scan(&event); e != nil {
		return nil, e
	}
	if e = tx.QueryRow(ctx, `INSERT INTO "pl-quarantine-items"(event_id,return_line_id,location_id,lot_id,quantity) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, event, line, in.LocationID, r.LotID, q).Scan(&item); e != nil {
		return nil, e
	}
	// Append a reference to the original controlled consumption; never decrement it.
	if _, e = tx.Exec(ctx, `INSERT INTO "pl-controlled-returns"(return_line_id,ledger_id,quantity) SELECT $1,id,$4 FROM "pl-controlled-ledger" WHERE sale_line_id=$2 AND lot_id=$3`, line, r.SaleLineID, r.LotID, q); e != nil {
		return nil, e
	}
	value["id"], value["return_line_id"], value["quarantine_id"], value["status"] = id, line, item, "QUARANTINED"
	return value, nil
}

func (s Service) decideQuarantine(ctx context.Context, tx Tx, actor, object string, in Input) (any, error) {
	d := in.Decision
	if d == nil || in.Reason == "" || len(in.Reason) > 500 || (d.Action != "release" && d.Action != "destroy") {
		return nil, Invalid()
	}
	q, e := Positive(d.Quantity)
	if e != nil {
		return nil, e
	}
	var product, lot, expiry string
	var quantity, used int64
	if e = tx.QueryRow(ctx, `SELECT l.product_id::text,l.id::text,l.expires_on::text,i.quantity FROM "pl-quarantine-items" i JOIN "pl-lots" l ON l.id=i.lot_id WHERE i.id=$1 AND i.location_id=$2 FOR UPDATE OF i`, object, in.LocationID).Scan(&product, &lot, &expiry, &quantity); e != nil {
		return nil, e
	}
	if e = guards(ctx, tx, in.LocationID, []string{product}); e != nil {
		return nil, e
	}
	if e = tx.QueryRow(ctx, `SELECT ((SELECT coalesce(sum(quantity),0) FROM "pl-quarantine-decisions" WHERE item_id=$1)+(SELECT coalesce(sum(quantity),0) FROM "pl-quarantine-count-losses" WHERE item_id=$1))::bigint`, object).Scan(&used); e != nil {
		return nil, e
	}
	if q > quantity-used {
		return nil, Reject(409, "QUARANTINE_QUANTITY_EXCEEDED", "Degerlendirilecek karantina miktari yetersiz")
	}
	at, e := s.now(ctx, tx)
	if e != nil {
		return nil, e
	}
	account := "destroyed"
	if d.Action == "release" {
		if !d.Sealed {
			return nil, Reject(422, "SEAL_REQUIRED", "Muhurlu ambalaj dogrulamasi gerekli")
		}
		if expiry < businessDay(at) {
			return nil, Reject(422, "EXPIRED_LOT", "Gecmis SKT serbest birakilamaz")
		}
		var active bool
		if e = tx.QueryRow(ctx, `SELECT active FROM "pl-products" WHERE id=$1`, product).Scan(&active); e != nil {
			return nil, e
		}
		if !active {
			return nil, Invalid()
		}
		account = "physical"
	}
	var id string
	if e = tx.QueryRow(ctx, `INSERT INTO "pl-quarantine-decisions"(item_id,actor_id,action,quantity,sealed,reason,effective_at) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text`, object, actor, d.Action, q, d.Sealed, in.Reason, at).Scan(&id); e != nil {
		return nil, e
	}
	if e = movement(ctx, tx, "quarantine."+d.Action, id, in.LocationID, in.LocationID, lot, "quarantine", account, q); e != nil {
		return nil, e
	}
	return map[string]string{"id": id, "quarantine_id": object, "action": d.Action, "quantity": d.Quantity}, nil
}
