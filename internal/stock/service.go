package stock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Service struct {
	Pool                          *pgxpool.Pool
	LockTimeout, StatementTimeout time.Duration
	clock                         func(context.Context, Tx) (time.Time, error)
}
type Result struct {
	Status   int
	Body     json.RawMessage
	CommitMS float64
}
type Tx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func classified(err error) error {
	if err == nil || IsBusiness(err) {
		return err
	}
	var p *pgconn.PgError
	if errors.As(err, &p) {
		switch p.Code {
		case "23505":
			return Reject(409, "CONFLICT", "Kayit zaten mevcut")
		case "23514", "23503", "22003", "22007", "22008":
			return Invalid()
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return Reject(404, "NOT_FOUND", "Kayit bulunamadi")
	}
	return Reject(503, "UNAVAILABLE", "Islem tamamlanamadi; ayni anahtarla sonucu kontrol edin")
}
func retryable(e error) bool {
	var p *pgconn.PgError
	if errors.Is(e, errDayChanged) {
		return true
	}
	return errors.As(e, &p) && (p.Code == "40P01" || p.Code == "40001")
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func normalize(in *Input) error {
	if in.Order != nil {
		copyOrder := &OrderInput{Lines: append([]OrderLineInput{}, in.Order.Lines...)}
		for i := range copyOrder.Lines {
			copyOrder.Lines[i].Needs = append([]string{}, copyOrder.Lines[i].Needs...)
			sort.Slice(copyOrder.Lines[i].Needs, func(a, b int) bool {
				x, _ := Positive(copyOrder.Lines[i].Needs[a])
				y, _ := Positive(copyOrder.Lines[i].Needs[b])
				return x < y
			})
		}
		sort.Slice(copyOrder.Lines, func(i, j int) bool {
			x, _ := Positive(copyOrder.Lines[i].ProductID)
			y, _ := Positive(copyOrder.Lines[j].ProductID)
			return x < y
		})
		in.Order = copyOrder
	}

	if _, e := Positive(in.LocationID); e != nil {
		return e
	}
	in.DocumentNo = strings.TrimSpace(in.DocumentNo)
	in.Reason = strings.TrimSpace(in.Reason)
	if len(in.Items) > 100 || len(in.Lines) > 100 {
		return Invalid()
	}
	for i := range in.Items {
		if _, e := Positive(in.Items[i].ProductID); e != nil {
			return e
		}
		if _, e := Positive(in.Items[i].Quantity); e != nil {
			return e
		}
	}
	sort.Slice(in.Items, func(i, j int) bool {
		a, b := in.Items[i], in.Items[j]
		if a.ProductID == b.ProductID {
			return a.Unit < b.Unit
		}
		x, _ := Positive(a.ProductID)
		y, _ := Positive(b.ProductID)
		return x < y
	})
	// Merge repeated product/unit inputs before hashing, then merge converted units under guards.
	merged := []Item{}
	for _, v := range in.Items {
		if len(merged) > 0 {
			last := &merged[len(merged)-1]
			if last.ProductID == v.ProductID && last.Unit == v.Unit {
				a, _ := Positive(last.Quantity)
				b, _ := Positive(v.Quantity)
				if a > math.MaxInt64-b {
					return Invalid()
				}
				last.Quantity = strconv.FormatInt(a+b, 10)
				continue
			}
		}
		merged = append(merged, v)
	}
	in.Items = merged
	for i := range in.Lines {
		v := &in.Lines[i]
		v.Lot = strings.TrimSpace(v.Lot)
		if _, e := Positive(v.ProductID); e != nil {
			return e
		}
		if _, e := Positive(v.Quantity); e != nil {
			return e
		}
		if v.Lot == "" || len(v.Lot) > 100 {
			return Invalid()
		}
		if _, e := time.Parse("2006-01-02", v.ExpiresOn); e != nil {
			return Invalid()
		}
	}
	sort.Slice(in.Lines, func(i, j int) bool {
		a, b := in.Lines[i], in.Lines[j]
		if a.ProductID != b.ProductID {
			x, _ := Positive(a.ProductID)
			y, _ := Positive(b.ProductID)
			return x < y
		}
		return a.Lot < b.Lot
	})
	for i := 1; i < len(in.Lines); i++ {
		if in.Lines[i].ProductID == in.Lines[i-1].ProductID && in.Lines[i].Lot == in.Lines[i-1].Lot {
			return Invalid()
		}
	}
	if in.Product != nil {
		if e := in.Product.Normalize(); e != nil {
			return e
		}
	}
	return nil
}

// Execute owns the only stock write transaction. Future prescription/limit checks extend sale() before effects.
func (s Service) Execute(ctx context.Context, actor, command, object, key, requestID string, in Input) (Result, error) {
	allowed := map[string]string{"suggestion.policy": "suggestion_policy", "allocation.eta": "expected_on", "suggestion.calculate": "as_of", "suggestion.need": "reason", "suggestion.return": "reason target_id", "product.create": "product", "product.disable": "", "receipt.create": "supplier_id document_no reason lines order_id receipt_mode", "transfer.create": "target_id items allocation_id", "transfer.send": "", "transfer.receive": "delivery reason", "transfer.cancel": "reason", "sale.quote": "items prescription_id dispense", "sale.create": "items quote prescription_id dispense payment_method", "prescription.create": "prescription", "price.create": "price", "return.quote": "return reason", "return.create": "return reason", "quarantine.decide": "decision reason", "count.start": "count", "count.finalize": "count reason", "count.cancel": "reason", "count.extend": "reason", "need.create": "need reason", "need.approve": "", "order.create": "order supplier_id reason", "order.approve": "", "order.cancel": "reason"}
	raw, _ := json.Marshal(in)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for field := range fields {
		if field != "location_id" && !strings.Contains(" "+allowed[command]+" ", " "+field+" ") {
			return Result{}, Reject(400, "INVALID_INPUT", "Komut icin beklenmeyen alan")
		}
	}
	if e := normalize(&in); e != nil {
		return Result{}, e
	}
	if !previewCommand(command) && (len(key) < 8 || len(key) > 128) {
		return Result{}, Reject(400, "IDEMPOTENCY_REQUIRED", "8-128 karakter Idempotency-Key gerekli")
	}
	var out Result
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		out, err = s.once(ctx, actor, command, object, key, requestID, in, nil)
		if !retryable(err) {
			break
		}
	}
	return out, classified(err)
}
func (s Service) once(ctx context.Context, actor, command, object, key, requestID string, in Input, beforeCommit func() error) (Result, error) {
	isolation := pgx.ReadCommitted
	if strings.HasPrefix(command, "suggestion.") {
		isolation = pgx.Serializable
	}
	if command == "suggestion.calculate" {
		isolation = pgx.RepeatableRead
	}
	tx, e := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: isolation})
	if e != nil {
		return Result{}, e
	}
	defer tx.Rollback(context.Background())
	lock, statement := s.LockTimeout, s.StatementTimeout
	if lock <= 0 {
		lock = 2 * time.Second
	}
	if statement <= 0 {
		statement = 5 * time.Second
	}
	if _, e = tx.Exec(ctx, `SELECT set_config('lock_timeout',$1,true),set_config('statement_timeout',$2,true)`, strconv.FormatInt(lock.Milliseconds(), 10), strconv.FormatInt(statement.Milliseconds(), 10)); e != nil {
		return Result{}, e
	}
	if e = authorize(ctx, tx, actor, command, object, in); e != nil {
		return Result{}, e
	}
	if _, e = tx.Exec(ctx, `SELECT set_config('eczanet.actor',$1,true)`, actor); e != nil {
		return Result{}, e
	}
	hash := digest(in)
	scope := command + ":" + object
	if !previewCommand(command) {
		_, e = tx.Exec(ctx, `INSERT INTO "pl-idempotency"(actor_id,command,location_id,key,digest) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, actor, scope, in.LocationID, key, hash)
		if e != nil {
			return Result{}, e
		}
		var old string
		var status *int
		var body []byte
		e = tx.QueryRow(ctx, `SELECT digest,status,result FROM "pl-idempotency" WHERE actor_id=$1 AND command=$2 AND location_id=$3 AND key=$4 FOR UPDATE`, actor, scope, in.LocationID, key).Scan(&old, &status, &body)
		if e != nil {
			return Result{}, e
		}
		if old != hash {
			return Result{}, Reject(409, "IDEMPOTENCY_CONFLICT", "Ayni anahtar farkli icerikle kullanildi")
		}
		if status != nil {
			if e = authorizeReplay(ctx, tx, command, in, body); e != nil {
				return Result{}, e
			}
			return Result{Status: *status, Body: canonical(body)}, nil
		}
	}
	if e = lockTransferOrder(ctx, tx, command, object, in); e != nil {
		return Result{}, e
	}
	var value any
	oldValue := []byte(`{}`)
	if command == "product.disable" {
		var active bool
		e = tx.QueryRow(ctx, `SELECT active FROM "pl-products" WHERE id=$1 FOR UPDATE`, object).Scan(&active)
		if e != nil {
			return Result{}, e
		}
		oldValue, _ = json.Marshal(map[string]bool{"active": active})
	}
	if command == "transfer.send" || command == "transfer.receive" || command == "transfer.cancel" {
		var state string
		e = tx.QueryRow(ctx, `SELECT status FROM "pl-transfers" WHERE id=$1 FOR UPDATE`, object).Scan(&state)
		if e != nil {
			return Result{}, e
		}
		oldValue, _ = json.Marshal(map[string]string{"status": state})
	}
	status := 201
	switch command {
	case "suggestion.policy":
		value, e = setSuggestionPolicy(ctx, tx, in)
	case "allocation.eta":
		value, e = setProcurementETA(ctx, tx, object, in)
	case "suggestion.calculate":
		value, e = calculateSuggestions(ctx, tx, in.AsOf)
	case "suggestion.need", "suggestion.return":
		value, e = consumeSuggestion(ctx, tx, actor, object, command, in)
	case "need.create", "need.approve", "order.create", "order.approve", "order.cancel":
		value, e = procurementCommand(ctx, tx, actor, command, object, in)
	case "count.start", "count.finalize", "count.cancel", "count.extend":
		value, e = s.countCommand(ctx, tx, actor, command, object, in)
	case "return.quote", "return.create":
		value, e = s.returnSale(ctx, tx, actor, command, in)
		if previewCommand(command) {
			status = 200
		}
	case "quarantine.decide":
		value, e = s.decideQuarantine(ctx, tx, actor, object, in)
	case "prescription.create":
		value, e = loadPrescription(ctx, tx, in)
	case "price.create":
		value, e = schedulePrice(ctx, tx, in)
	case "product.create":
		value, e = createProduct(ctx, tx, in)
	case "product.disable":
		_, e = tx.Exec(ctx, `UPDATE "pl-products" SET active=false WHERE id=$1`, object)
		value = map[string]string{"id": object, "status": "INACTIVE"}
		status = 200
	case "receipt.create":
		value, e = receipt(ctx, tx, in)
	case "transfer.create":
		value, e = createTransfer(ctx, tx, in)
	case "transfer.send", "transfer.receive", "transfer.cancel":
		if command == "transfer.receive" {
			value, e = s.receiveTransfer(ctx, tx, actor, object, in)
		} else if command == "transfer.cancel" {
			value, e = cancelTransfer(ctx, tx, object, in)
		} else {
			value, e = transfer(ctx, tx, command, object, in)
		}
		status = 200
	case "sale.quote", "sale.create":
		value, e = s.sale(ctx, tx, actor, command, in)
		if previewCommand(command) {
			status = 200
		}
	default:
		return Result{}, Reject(404, "NOT_FOUND", "Komut bulunamadi")
	}
	if e != nil {
		return Result{}, e
	}
	body, e := json.Marshal(value)
	if e != nil {
		return Result{}, e
	}
	body = canonical(body)
	if previewCommand(command) {
		return Result{Status: status, Body: body}, nil
	}
	if !previewCommand(command) {
		var obj struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &obj)
		if object == "" {
			object = obj.ID
		}
		auditValue, _ := json.Marshal(map[string]any{"input": in, "result": value})
		_, e = tx.Exec(ctx, `INSERT INTO "pl-audit-events"(actor_id,command,object_id,request_id,old_value,new_value) VALUES($1,$2,$3,$4,$5,$6)`, actor, command, object, requestID, oldValue, auditValue)
		if e != nil {
			return Result{}, e
		}
		_, e = tx.Exec(ctx, `UPDATE "pl-idempotency" SET status=$5,result=$6 WHERE actor_id=$1 AND command=$2 AND location_id=$3 AND key=$4`, actor, scope, in.LocationID, key, status, body)
		if e != nil {
			return Result{}, e
		}
	}
	if beforeCommit != nil {
		if e = beforeCommit(); e != nil {
			return Result{}, e
		}
	}
	commitStarted := time.Now()
	if e = tx.Commit(ctx); e != nil {
		return Result{}, e
	}
	return Result{Status: status, Body: body, CommitMS: float64(time.Since(commitStarted).Microseconds()) / 1000}, nil
}
func authorize(ctx context.Context, tx Tx, actor, cmd, obj string, in Input) error {
	var role, kind string
	var active bool
	if e := tx.QueryRow(ctx, `SELECT role,active FROM "pl-wy-users" WHERE id=$1`, actor).Scan(&role, &active); e != nil {
		return e
	}
	if !active {
		return Reject(401, "UNAUTHENTICATED", "Oturum gerekli")
	}
	if e := tx.QueryRow(ctx, `SELECT l.kind FROM "pl-locations" l WHERE l.id=$2 AND ($3 OR EXISTS(SELECT 1 FROM "pl-user-locations" WHERE user_id=$1 AND location_id=l.id))`, actor, in.LocationID, role == "accountant" && cmd == "price.create").Scan(&kind); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			return Reject(403, "FORBIDDEN", "Lokasyona yetkiniz yok")
		}
		return e
	}
	allowed := false
	if strings.HasPrefix(cmd, "need.") || strings.HasPrefix(cmd, "order.") {
		return authorizeProcurement(ctx, tx, actor, role, kind, cmd, obj, in)
	}
	switch cmd {
	case "suggestion.policy":
		allowed = role == "manager" && kind == "branch"
	case "allocation.eta":
		allowed = role == "warehouse" && kind == "warehouse"
	case "suggestion.calculate":
		allowed = role == "warehouse" || role == "manager"
	case "suggestion.need":
		allowed = (role == "manager" || role == "pharmacist") && kind == "branch"
	case "suggestion.return":
		allowed = role == "manager" && kind == "branch"
	case "product.create", "product.disable", "receipt.create":
		allowed = role == "warehouse" && kind == "warehouse"
	case "transfer.create", "transfer.send", "transfer.cancel":
		allowed = (role == "warehouse" && kind == "warehouse") || (role == "manager" && kind == "branch")
	case "transfer.receive":
		allowed = ((role == "pharmacist" || role == "manager") && kind == "branch") || (role == "warehouse" && kind == "warehouse")
	case "prescription.create":
		allowed = (role == "pharmacist" || role == "technician") && kind == "branch"
	case "price.create":
		allowed = role == "accountant"
	case "count.start", "count.finalize", "count.cancel", "count.extend":
		allowed = (role == "manager" && kind == "branch") || (role == "warehouse" && kind == "warehouse")
	case "return.quote", "return.create":
		allowed = role == "pharmacist" && kind == "branch"
	case "quarantine.decide":
		allowed = (role == "pharmacist" && kind == "branch") || (role == "warehouse" && kind == "warehouse")
	case "sale.quote", "sale.create":
		allowed = (role == "pharmacist" || role == "technician") && kind == "branch"
	}
	if role == "simulator" {
		allowed = (kind == "branch" && (cmd == "sale.quote" || cmd == "sale.create" || cmd == "prescription.create")) || (kind == "warehouse" && cmd == "receipt.create")
	}
	if cmd == "sale.create" && in.PrescriptionID != "" && role != "pharmacist" && role != "simulator" {
		allowed = false
	}
	if !allowed {
		return Reject(403, "FORBIDDEN", "Bu komuta yetkiniz yok")
	}
	if cmd == "transfer.send" || cmd == "transfer.receive" || cmd == "transfer.cancel" {
		if _, e := Positive(obj); e != nil {
			return e
		}
		var source, target string
		if e := tx.QueryRow(ctx, `SELECT source_id::text,target_id::text FROM "pl-transfers" WHERE id=$1`, obj).Scan(&source, &target); e != nil {
			return e
		}
		expected := source
		if cmd == "transfer.receive" {
			expected = target
		}
		if in.LocationID != expected {
			return Reject(403, "FORBIDDEN", "Transfer tarafina yetkiniz yok")
		}
	}
	if cmd == "return.quote" || cmd == "return.create" || cmd == "quarantine.decide" {
		if e := authorizeReturn(ctx, tx, cmd, obj, in); e != nil {
			return e
		}
	}
	if strings.HasPrefix(cmd, "count.") && cmd != "count.start" {
		if _, e := Positive(obj); e != nil {
			return e
		}
		var location string
		if e := tx.QueryRow(ctx, `SELECT location_id::text FROM "pl-counts" WHERE id=$1`, obj).Scan(&location); e != nil {
			return e
		}
		if location != in.LocationID {
			return Reject(403, "FORBIDDEN", "Sayim lokasyonuna yetkiniz yok")
		}
	}
	if cmd == "product.disable" {
		if _, e := Positive(obj); e != nil {
			return e
		}
	}
	return nil
}
func guards(ctx context.Context, tx Tx, location string, products []string) error {
	if e := lockGuards(ctx, tx, location, products); e != nil {
		return e
	}
	for _, id := range products {
		if _, e := tx.Exec(ctx, `UPDATE "pl-counts" SET status='EXPIRED' WHERE location_id=$1 AND product_id=$2 AND status='OPEN' AND expires_at<=clock_timestamp()`, location, id); e != nil {
			return e
		}
		var frozen bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "pl-counts" WHERE location_id=$1 AND product_id=$2 AND status='OPEN' AND expires_at>clock_timestamp())`, location, id).Scan(&frozen); e != nil {
			return e
		}
		if frozen {
			return Reject(409, "COUNT_IN_PROGRESS", "Urun aktif sayimda donduruldu")
		}
	}
	return nil
}

// Count metadata belongs to its scope guard; count lifecycle never locks a header
// before the guard. All other business documents retain their earlier lock class.
func lockGuards(ctx context.Context, tx Tx, location string, products []string) error {
	unique := map[int64]string{}
	for _, id := range products {
		n, e := Positive(id)
		if e != nil {
			return e
		}
		unique[n] = id
	}
	keys := []int64{}
	for k := range unique {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		id := unique[k]
		if _, e := tx.Exec(ctx, `INSERT INTO "pl-inventory-guards" VALUES($1,$2) ON CONFLICT DO NOTHING`, location, id); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `SELECT 1 FROM "pl-inventory-guards" WHERE location_id=$1 AND product_id=$2 FOR UPDATE`, location, id); e != nil {
			return e
		}
	}
	return nil
}
func createProduct(ctx context.Context, tx Tx, in Input) (any, error) {
	if in.Product == nil {
		return nil, Invalid()
	}
	p := *in.Product
	e := tx.QueryRow(ctx, `INSERT INTO "pl-products"(barcode,name,dose,form,base_unit,pack_size,blister_size,sale_multiple,prescription_required,controlled_group) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id::text`, p.Barcode, p.Name, p.Dose, p.Form, p.BaseUnit, p.PackSize, p.BlisterSize, p.SaleMultiple, p.PrescriptionRequired, p.ControlledGroup).Scan(&p.ID)
	if e != nil {
		return nil, e
	}
	for _, name := range p.Ingredients {
		var id string
		e = tx.QueryRow(ctx, `INSERT INTO "pl-ingredients"(name) VALUES($1) ON CONFLICT(name) DO NOTHING RETURNING id::text`, name).Scan(&id)
		if errors.Is(e, pgx.ErrNoRows) {
			e = tx.QueryRow(ctx, `SELECT id::text FROM "pl-ingredients" WHERE name=$1`, name).Scan(&id)
		}
		if e != nil {
			return nil, e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO "pl-product-ingredients" VALUES($1,$2)`, p.ID, id); e != nil {
			return nil, e
		}
	}
	if p.PackPrice != "" {
		if _, e = tx.Exec(ctx, `INSERT INTO "pl-price-periods"(product_id,valid_from,pack_price) VALUES($1,clock_timestamp(),$2)`, p.ID, p.PackPrice); e != nil {
			return nil, e
		}
	}
	p.Active = true
	return p, nil
}

// movement writes both accounts and append-only entries in one transaction; the deferred DB checks independently reconcile.
func movement(ctx context.Context, tx Tx, kind, reference, fromLoc, toLoc, lot, from, to string, qty int64) error {
	var event string
	if e := tx.QueryRow(ctx, `INSERT INTO "pl-stock-events"(kind,reference) VALUES($1,$2) RETURNING id::text`, kind, reference).Scan(&event); e != nil {
		return e
	}
	for _, v := range []struct {
		loc, account string
		delta        int64
	}{{fromLoc, from, -qty}, {toLoc, to, qty}} {
		if _, e := tx.Exec(ctx, `INSERT INTO "pl-stock-balances"(location_id,lot_id,account) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, v.loc, lot, v.account); e != nil {
			return e
		}
		tag, e := tx.Exec(ctx, `UPDATE "pl-stock-balances" SET quantity=quantity+$4 WHERE location_id=$1 AND lot_id=$2 AND account=$3 AND ($3 IN ('supply','sold','count') OR quantity+$4>=0)`, v.loc, lot, v.account, v.delta)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return Reject(409, "INSUFFICIENT_STOCK", "Yeterli satilabilir stok yok")
		}
		if _, e = tx.Exec(ctx, `INSERT INTO "pl-stock-entries"(event_id,location_id,lot_id,account,quantity) VALUES($1,$2,$3,$4,$5)`, event, v.loc, lot, v.account, v.delta); e != nil {
			return e
		}
	}
	return nil
}
func receipt(ctx context.Context, tx Tx, in Input) (any, error) {
	if in.ReceiptMode != "" && in.ReceiptMode != "quarantine" {
		return nil, Invalid()
	}
	if in.OrderID != "" {
		if _, e := Positive(in.OrderID); e != nil {
			return nil, e
		}
		if e := lockOrder(ctx, tx, in.OrderID); e != nil {
			return nil, e
		}
	}

	if _, e := Positive(in.SupplierID); e != nil {
		return nil, e
	}
	if in.DocumentNo == "" || len(in.DocumentNo) > 100 || in.Reason == "" || len(in.Reason) > 500 || len(in.Lines) == 0 {
		return nil, Invalid()
	}
	hash := digest(in)
	var id string
	// Document uniqueness precedes guard locks, including concurrent different-key retries.
	e := tx.QueryRow(ctx, `INSERT INTO "pl-goods-receipts"(supplier_id,document_no,location_id,reason,digest) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING RETURNING id::text`, in.SupplierID, in.DocumentNo, in.LocationID, in.Reason, hash).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		var old string
		e = tx.QueryRow(ctx, `SELECT id::text,digest FROM "pl-goods-receipts" WHERE supplier_id=$1 AND document_no=$2`, in.SupplierID, in.DocumentNo).Scan(&id, &old)
		if e != nil {
			return nil, e
		}
		if old != hash {
			return nil, Reject(409, "DOCUMENT_CONFLICT", "Irsaliye icerigi farkli")
		}
		return map[string]string{"id": id, "status": "RECEIVED"}, nil
	}
	if e != nil {
		return nil, e
	}
	if in.OrderID != "" {
		if e = checkReceiptOrder(ctx, tx, in); e != nil {
			return nil, e
		}
	}
	products := []string{}
	for _, l := range in.Lines {
		products = append(products, l.ProductID)
	}
	if e = guards(ctx, tx, in.LocationID, products); e != nil {
		return nil, e
	}
	var today string
	if e = tx.QueryRow(ctx, `SELECT (clock_timestamp() AT TIME ZONE 'Europe/Istanbul')::date::text`).Scan(&today); e != nil {
		return nil, e
	}
	for _, l := range in.Lines {
		if l.ExpiresOn < today && in.ReceiptMode != "quarantine" {
			return nil, Reject(422, "EXPIRED_LOT", "Gecmis SKT normal kabule uygun degil")
		}
		var active bool
		if e = tx.QueryRow(ctx, `SELECT active FROM "pl-products" WHERE id=$1`, l.ProductID).Scan(&active); e != nil {
			return nil, e
		}
		if !active {
			return nil, Invalid()
		}
		var lot, expiry string
		e = tx.QueryRow(ctx, `INSERT INTO "pl-lots"(product_id,supplier_id,number,expires_on) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING RETURNING id::text`, l.ProductID, in.SupplierID, l.Lot, l.ExpiresOn).Scan(&lot)
		if errors.Is(e, pgx.ErrNoRows) {
			e = tx.QueryRow(ctx, `SELECT id::text,expires_on::text FROM "pl-lots" WHERE product_id=$1 AND supplier_id=$2 AND number=$3`, l.ProductID, in.SupplierID, l.Lot).Scan(&lot, &expiry)
			if expiry != l.ExpiresOn {
				return nil, Reject(422, "LOT_CONFLICT", "Lot SKT bilgisi celisiyor")
			}
		}
		if e != nil {
			return nil, e
		}
		q, _ := Positive(l.Quantity)
		if _, e = tx.Exec(ctx, `INSERT INTO "pl-goods-receipt-lines" VALUES($1,$2,$3)`, id, lot, q); e != nil {
			return nil, e
		}
		account, kind := "physical", "receipt"
		if in.ReceiptMode == "quarantine" {
			account, kind = "quarantine", "receipt.quarantine"
		}
		if e = movement(ctx, tx, kind, id, in.LocationID, in.LocationID, lot, "supply", account, q); e != nil {
			return nil, e
		}
		if in.ReceiptMode == "quarantine" {
			if _, e = tx.Exec(ctx, `INSERT INTO "pl-quarantine-items"(event_id,location_id,lot_id,quantity) SELECT max(id),$2,$3,$4 FROM "pl-stock-events" WHERE kind=$5 AND reference=$1`, id, in.LocationID, lot, q, kind); e != nil {
				return nil, e
			}
		}
		if in.OrderID != "" {
			if e = applyOrderReceipt(ctx, tx, id, lot, l.ProductID, q, in); e != nil {
				return nil, e
			}
		}

	}
	if in.OrderID != "" && in.ReceiptMode != "quarantine" {
		if _, e = tx.Exec(ctx, `UPDATE "pl-orders" o SET status=CASE WHEN EXISTS(SELECT 1 FROM "pl-order-lines" WHERE order_id=o.id AND received+cancelled<quantity) THEN 'PARTIALLY_RECEIVED' ELSE 'RECEIVED' END WHERE o.id=$1`, in.OrderID); e != nil {
			return nil, e
		}
	}
	return map[string]string{"id": id, "status": "RECEIVED"}, nil
}

type allocation struct {
	Lot      string `json:"lot_id"`
	Product  string `json:"product_id"`
	Quantity int64  `json:"-"`
	Qty      string `json:"quantity"`
}

func allocate(ctx context.Context, tx Tx, location, product string, quantity int64, at time.Time) ([]allocation, error) {
	rows, e := tx.Query(ctx, `SELECT b.lot_id::text,(b.quantity-coalesce((SELECT sum(a.quantity-a.shipped) FROM "pl-allocation-lots" a JOIN "pl-order-allocations" oa ON oa.id=a.allocation_id JOIN "pl-order-lines" ol ON ol.id=oa.line_id JOIN "pl-orders" o ON o.id=ol.order_id WHERE a.lot_id=b.lot_id AND o.location_id=b.location_id),0))::bigint FROM "pl-stock-balances" b JOIN "pl-lots" l ON l.id=b.lot_id WHERE b.location_id=$1 AND l.product_id=$2 AND b.account='physical' AND b.quantity>0 AND l.expires_on>=($3::timestamptz AT TIME ZONE 'Europe/Istanbul')::date ORDER BY l.expires_on,l.id FOR UPDATE OF b`, location, product, at)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	a := []allocation{}
	for rows.Next() {
		var lot string
		var q int64
		if e = rows.Scan(&lot, &q); e != nil {
			return nil, e
		}
		if quantity <= 0 || q <= 0 {
			continue
		}
		if q > quantity {
			q = quantity
		}
		a = append(a, allocation{lot, product, q, strconv.FormatInt(q, 10)})
		quantity -= q
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if quantity > 0 {
		return nil, Reject(409, "INSUFFICIENT_STOCK", "Yeterli satilabilir stok yok")
	}
	return a, nil
}

type priced struct {
	Product     string       `json:"product_id"`
	Quantity    string       `json:"quantity"`
	PackSize    string       `json:"pack_size"`
	BlisterSize string       `json:"blister_size"`
	BaseUnit    string       `json:"base_unit"`
	PriceID     string       `json:"price_id"`
	PackPrice   string       `json:"pack_price"`
	Total       string       `json:"total"`
	Allocations []allocation `json:"allocations,omitempty"`
	Calculation *Calculation `json:"calculation,omitempty"`
}

func items(ctx context.Context, tx Tx, in Input, at time.Time, normal bool) ([]priced, int64, error) {
	if len(in.Items) == 0 {
		return nil, 0, Invalid()
	}
	lines := []priced{}
	total := int64(0)
	for _, i := range in.Items {
		var pack, blister, multiple int64
		var active, rx bool
		var group, base string
		e := tx.QueryRow(ctx, `SELECT pack_size,blister_size,sale_multiple,active,prescription_required,controlled_group,base_unit FROM "pl-products" WHERE id=$1`, i.ProductID).Scan(&pack, &blister, &multiple, &active, &rx, &group, &base)
		if e != nil {
			return nil, 0, e
		}
		if !active || normal && (rx || group != "") {
			return nil, 0, Reject(422, "PRODUCT_NOT_ELIGIBLE", "Urun normal kasa icin uygun degil")
		}
		q, _ := Positive(i.Quantity)
		q, e = Convert(q, pack, blister, multiple, i.Unit)
		if e != nil {
			return nil, 0, e
		}
		if len(lines) > 0 && lines[len(lines)-1].Product == i.ProductID {
			last := &lines[len(lines)-1]
			old, _ := Positive(last.Quantity)
			if old > math.MaxInt64-q {
				return nil, 0, Invalid()
			}
			last.Quantity = strconv.FormatInt(old+q, 10)
			continue
		}
		lines = append(lines, priced{Product: i.ProductID, Quantity: strconv.FormatInt(q, 10), PackSize: strconv.FormatInt(pack, 10), BlisterSize: strconv.FormatInt(blister, 10), BaseUnit: base})
	}
	if !normal && in.PrescriptionID == "" {
		return lines, 0, nil
	}
	for n := range lines {
		l := &lines[n]
		var price int64
		e := tx.QueryRow(ctx, `SELECT id::text,pack_price FROM "pl-price-periods" WHERE product_id=$1 AND valid_from<=$2 AND valid_to>$2`, l.Product, at).Scan(&l.PriceID, &price)
		if errors.Is(e, pgx.ErrNoRows) {
			return nil, 0, Reject(422, "PRICE_MISSING", "Gecerli fiyat bulunamadi")
		}
		if e != nil {
			return nil, 0, e
		}
		q, _ := Positive(l.Quantity)
		pack, _ := Positive(l.PackSize)
		amount, e := Price(price, q, pack)
		if e != nil || total > math.MaxInt64-amount {
			return nil, 0, Invalid()
		}
		l.PackPrice = strconv.FormatInt(price, 10)
		l.Total = strconv.FormatInt(amount, 10)
		total += amount
	}
	return lines, total, nil
}
func createTransfer(ctx context.Context, tx Tx, in Input) (any, error) {
	if _, e := Positive(in.TargetID); e != nil {
		return nil, e
	}
	var kind string
	if e := tx.QueryRow(ctx, `SELECT kind FROM "pl-locations" WHERE id=$1`, in.TargetID).Scan(&kind); e != nil {
		return nil, e
	}
	if (kind != "branch" && kind != "warehouse") || in.TargetID == in.LocationID {
		return nil, Invalid()
	}
	// Draft fixes the FEFO allocation; send revalidates expiry and stock under the shared guard.
	p := []string{}
	for _, i := range in.Items {
		p = append(p, i.ProductID)
	}
	if e := guards(ctx, tx, in.LocationID, p); e != nil {
		return nil, e
	}
	var at time.Time
	if e := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&at); e != nil {
		return nil, e
	}
	lines, _, e := items(ctx, tx, in, at, false)
	if e != nil {
		return nil, e
	}
	var id string
	if e = tx.QueryRow(ctx, `INSERT INTO "pl-transfers"(source_id,target_id,status) VALUES($1,$2,'DRAFT') RETURNING id::text`, in.LocationID, in.TargetID).Scan(&id); e != nil {
		return nil, e
	}
	for _, l := range lines {
		q, _ := Positive(l.Quantity)
		var a []allocation
		if in.AllocationID == "" {
			a, e = allocate(ctx, tx, in.LocationID, l.Product, q, at)
			if e != nil {
				return nil, e
			}
		}
		if in.AllocationID != "" {
			a, e = allocateReserved(ctx, tx, in.AllocationID, in.LocationID, in.TargetID, l.Product, q, at)
			if e != nil {
				return nil, e
			}
		}
		for _, v := range a {
			if _, e = tx.Exec(ctx, `INSERT INTO "pl-transfer-lines" VALUES($1,$2,$3)`, id, v.Lot, v.Quantity); e != nil {
				return nil, e
			}
		}
	}
	if in.AllocationID != "" {
		if _, e = tx.Exec(ctx, `INSERT INTO "pl-transfer-order-links" VALUES($1,$2)`, id, in.AllocationID); e != nil {
			return nil, e
		}
	}
	return map[string]string{"id": id, "status": "DRAFT"}, nil
}
func transfer(ctx context.Context, tx Tx, cmd, id string, in Input) (any, error) {
	if len(in.Items) > 0 || len(in.Lines) > 0 {
		return nil, Reject(422, "FULL_RECEIPT_ONLY", "A2 yalniz saglam tam teslim destekler")
	}
	var source, target, status string
	if e := tx.QueryRow(ctx, `SELECT source_id::text,target_id::text,status FROM "pl-transfers" WHERE id=$1 FOR UPDATE`, id).Scan(&source, &target, &status); e != nil {
		return nil, e
	}
	expected, next := "DRAFT", "SENT"
	if cmd == "transfer.receive" {
		expected, next = "SENT", "CLOSED"
	}
	if status != expected {
		return nil, Reject(409, "INVALID_STATE", "Transfer bu islem icin uygun durumda degil")
	}
	rows, e := tx.Query(ctx, `SELECT l.lot_id::text,b.product_id::text,l.quantity FROM "pl-transfer-lines" l JOIN "pl-lots" b ON b.id=l.lot_id WHERE transfer_id=$1 ORDER BY b.product_id,l.lot_id`, id)
	if e != nil {
		return nil, e
	}
	a := []allocation{}
	p := []string{}
	for rows.Next() {
		var v allocation
		if e = rows.Scan(&v.Lot, &v.Product, &v.Quantity); e != nil {
			rows.Close()
			return nil, e
		}
		a = append(a, v)
		p = append(p, v.Product)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	loc := source
	if cmd == "transfer.receive" {
		loc = target
	}
	if e = guards(ctx, tx, loc, p); e != nil {
		return nil, e
	}
	for _, v := range a {
		from, to := "physical", "transit:"+id
		fl, tl := source, source
		if cmd == "transfer.receive" {
			from, to = "transit:"+id, "physical"
			tl = target
		} else {
			var valid bool
			if e = tx.QueryRow(ctx, `SELECT expires_on>=(clock_timestamp() AT TIME ZONE 'Europe/Istanbul')::date FROM "pl-lots" WHERE id=$1`, v.Lot).Scan(&valid); e != nil {
				return nil, e
			}
			if !valid {
				return nil, Reject(422, "EXPIRED_LOT", "Gecmis lot sevk edilemez")
			}
		}
		if cmd == "transfer.send" {
			if e = reserveShipment(ctx, tx, id, source, v.Lot, v.Quantity); e != nil {
				return nil, e
			}
		}
		if e = movement(ctx, tx, cmd, id, fl, tl, v.Lot, from, to, v.Quantity); e != nil {
			return nil, e
		}
	}
	if _, e = tx.Exec(ctx, `UPDATE "pl-transfers" SET status=$2 WHERE id=$1`, id, next); e != nil {
		return nil, e
	}
	return map[string]string{"id": id, "status": next}, nil
}
func (s Service) sale(ctx context.Context, tx Tx, actor, cmd string, in Input) (any, error) {
	if in.PaymentMethod == "" {
		in.PaymentMethod = "cash"
	}
	if in.PaymentMethod != "cash" && in.PaymentMethod != "card" {
		return nil, Invalid()
	}
	rx, e := s.prepareRX(ctx, tx, &in)
	if e != nil {
		return nil, e
	}
	p := []string{}
	for _, i := range in.Items {
		p = append(p, i.ProductID)
	}
	// Shared product locks serialize price decisions with the publisher's
	// FOR UPDATE. Hold through commit, before guards, effective_at and prices.
	if e := lockSalePrices(ctx, tx, p); e != nil {
		return nil, e
	}
	if e := guards(ctx, tx, in.LocationID, p); e != nil {
		return nil, e
	}
	// All eligibility decisions use one instant after RX, daily counter, product price and stock guard locks.
	var at time.Time
	at, e = s.now(ctx, tx)
	if e != nil {
		return nil, e
	}
	if e = rx.validate(ctx, tx, at); e != nil {
		return nil, e
	}
	lines, total, e := items(ctx, tx, in, at, rx == nil)
	if e != nil {
		return nil, e
	}
	patient := total
	institution := int64(0)
	policy := "A2-normal-v1"
	if rx != nil {
		if e = tx.QueryRow(ctx, `SELECT version FROM "pl-pricing-policies" WHERE valid_from<=$1 AND valid_to>$1`, at).Scan(&policy); e != nil {
			return nil, e
		}
		if policy != "demo-rx-v1" {
			return nil, Reject(422, "POLICY_UNSUPPORTED", "Fiyat politika surumu desteklenmiyor")
		}
		total = 0
		patient = 0
		rate := patientRate(rx.birth, at, rx.reportFrom, rx.reportTo)
		for n := range lines {
			l := &lines[n]
			price, _ := Positive(l.PackPrice)
			q, _ := Positive(l.Quantity)
			pack, _ := Positive(l.PackSize)
			c, err := calculate(price, q, pack, true, rate)
			if err != nil {
				return nil, err
			}
			l.Calculation = &c
			l.Total = c.Total
			t, _ := strconv.ParseInt(c.Total, 10, 64)
			p, _ := strconv.ParseInt(c.Patient, 10, 64)
			if total > math.MaxInt64-t {
				return nil, Invalid()
			}
			total += t
			patient += p
		}
		institution = total - patient
	}
	quote := digest(lines)
	if cmd == "sale.quote" {
		return map[string]any{"quote": quote, "total": strconv.FormatInt(total, 10), "lines": lines, "policy": policy, "patient_total": strconv.FormatInt(patient, 10), "institution_total": strconv.FormatInt(institution, 10)}, nil
	}
	if in.Quote == "" || in.Quote != quote {
		return nil, &Error{Status: 409, Code: "PRICE_CHANGED", Message: "Teklif degisti; yeni teklifi alip acikca onaylayin", NewQuote: map[string]any{"quote": quote, "total": strconv.FormatInt(total, 10), "patient_total": strconv.FormatInt(patient, 10), "institution_total": strconv.FormatInt(institution, 10), "policy": policy, "lines": lines}}
	}
	for n := range lines {
		q, _ := Positive(lines[n].Quantity)
		lines[n].Allocations, e = allocate(ctx, tx, in.LocationID, lines[n].Product, q, at)
		if e != nil {
			return nil, e
		}
	}
	var id string
	if e = tx.QueryRow(ctx, `INSERT INTO "pl-sales"(location_id,actor_id,total,effective_at,prescription_id,patient_total,institution_total,payment_method) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`, in.LocationID, actor, total, at, nullable(in.PrescriptionID), patient, institution, in.PaymentMethod).Scan(&id); e != nil {
		return nil, e
	}
	for _, l := range lines {
		var lineID string
		snapshotData := map[string]any{"actor_id": actor, "effective_at": at, "dispensed_product_id": l.Product, "pricing": l.Calculation, "policy": policy, "prescription_id": in.PrescriptionID, "pack_size": l.PackSize, "blister_size": l.BlisterSize, "base_unit": l.BaseUnit, "pack_price": l.PackPrice, "price_id": l.PriceID, "quantity": l.Quantity, "total": l.Total}
		if rx != nil {
			snapshotData["birth_date"] = rx.birth.Format("2006-01-02")
			snapshotData["report_from"] = rx.reportFrom
			snapshotData["report_to"] = rx.reportTo
			snapshotData["issued_at"] = rx.issued
			snapshotData["control_factor"] = "1/1"
			uses := []map[string]any{}
			for _, u := range rx.uses {
				if u.product == l.Product {
					uses = append(uses, map[string]any{"prescription_line_id": u.line, "quantity": strconv.FormatInt(u.quantity, 10)})
				}
			}
			snapshotData["prescription_uses"] = uses
		}
		snapshot, _ := json.Marshal(snapshotData)
		e = tx.QueryRow(ctx, `INSERT INTO "pl-sale-lines"(sale_id,product_id,quantity,pack_size,blister_size,base_unit,price_id,pack_price,total,snapshot,policy) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id::text`, id, l.Product, l.Quantity, l.PackSize, l.BlisterSize, l.BaseUnit, l.PriceID, l.PackPrice, l.Total, snapshot, policy).Scan(&lineID)
		if e != nil {
			return nil, e
		}
		if rx != nil {
			for _, u := range rx.uses {
				if u.product == l.Product {
					if _, e = tx.Exec(ctx, `INSERT INTO "pl-dispensations"(prescription_line_id,sale_line_id,quantity) VALUES($1,$2,$3)`, u.line, lineID, u.quantity); e != nil {
						return nil, e
					}
					if _, e = tx.Exec(ctx, `UPDATE "pl-prescription-lines" SET fulfilled_base=fulfilled_base+$2 WHERE id=$1`, u.line, u.quantity); e != nil {
						return nil, e
					}
				}
			}
		}
		for _, a := range l.Allocations {
			if _, e = tx.Exec(ctx, `INSERT INTO "pl-sale-allocations" VALUES($1,$2,$3)`, lineID, a.Lot, a.Quantity); e != nil {
				return nil, e
			}
			if rx != nil {
				var group string
				if e = tx.QueryRow(ctx, `SELECT controlled_group FROM "pl-products" WHERE id=$1`, l.Product).Scan(&group); e != nil {
					return nil, e
				}
				if group != "" {
					if _, e = tx.Exec(ctx, `INSERT INTO "pl-controlled-ledger"(patient_id,prescription_id,sale_line_id,product_id,lot_id,quantity,group_name,policy_id,location_id,actor_id,business_day,effective_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, rx.patient, rx.id, lineID, l.Product, a.Lot, a.Quantity, group, rx.policies[group], in.LocationID, actor, rx.day, at); e != nil {
						return nil, e
					}
				}
			}
			if e = movement(ctx, tx, "sale", id, in.LocationID, in.LocationID, a.Lot, "physical", "sold", a.Quantity); e != nil {
				return nil, e
			}
		}
	}
	if rx != nil {
		if _, e = tx.Exec(ctx, `UPDATE "pl-prescriptions" SET owner_id=$2 WHERE id=$1`, rx.id, in.LocationID); e != nil {
			return nil, e
		}
		for g, q := range rx.groups {
			if _, e = tx.Exec(ctx, `UPDATE "pl-daily-controls" SET consumed=consumed+$4 WHERE patient_id=$1 AND group_name=$2 AND business_day=$3`, rx.patient, g, rx.day, q); e != nil {
				return nil, e
			}
		}
	}
	return map[string]any{"id": id, "total": strconv.FormatInt(total, 10), "patient_total": strconv.FormatInt(patient, 10), "institution_total": strconv.FormatInt(institution, 10), "payment_method": in.PaymentMethod, "effective_at": at, "lines": lines}, nil
}

func lockSalePrices(ctx context.Context, tx Tx, products []string) error {
	ids := map[int64]bool{}
	for _, id := range products {
		n, e := Positive(id)
		if e != nil {
			return e
		}
		ids[n] = true
	}
	ordered := make([]int64, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	for _, id := range ordered {
		var found string
		if e := tx.QueryRow(ctx, `SELECT id::text FROM "pl-products" WHERE id=$1 FOR SHARE`, id).Scan(&found); e != nil {
			return e
		}
	}
	return nil
}

// The idempotency row is already locked. Object access must be serialized with
// RX ownership changes before returning a cached, possibly clinical response.
func authorizeReplay(ctx context.Context, tx Tx, cmd string, in Input, body []byte) error {
	id := ""
	switch cmd {
	case "prescription.create":
		var result struct {
			ID string `json:"id"`
		}
		if e := json.Unmarshal(body, &result); e != nil {
			return e
		}
		id = result.ID
	case "sale.create":
		id = in.PrescriptionID
	}
	if id == "" {
		return nil
	}
	var owner *string
	if e := tx.QueryRow(ctx, `SELECT owner_id::text FROM "pl-prescriptions" WHERE id=$1 FOR SHARE`, id).Scan(&owner); e != nil {
		return e
	}
	if owner != nil && *owner != in.LocationID {
		return Reject(403, "PRESCRIPTION_BRANCH", "Recete ilk karsilandigi subeye bagli")
	}
	return nil
}

func canonical(body []byte) []byte {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return body
	}
	b, _ := json.Marshal(v)
	return b
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
