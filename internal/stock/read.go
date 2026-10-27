package stock

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"strings"
)

type Page struct {
	Items []json.RawMessage `json:"items"`
	Next  string            `json:"next_cursor"`
}

func (s Service) Read(ctx context.Context, actor, resource, location, search, cursor string, limit int) (Page, error) {
	if resource == "suggestions" || strings.HasPrefix(resource, "report-") {
		return s.suggestionRead(ctx, actor, resource, location, search, cursor, limit)
	}
	out := Page{Items: []json.RawMessage{}}
	if limit < 1 || limit > 200 {
		return out, Reject(400, "INVALID_PAGE", "Sayfa limiti 1..200 olmali")
	}
	tx, e := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if e != nil {
		return out, classified(e)
	}
	defer tx.Rollback(ctx)
	var role string
	e = tx.QueryRow(ctx, `SELECT role FROM "pl-wy-users" WHERE id=$1 AND active`, actor).Scan(&role)
	if e != nil {
		return out, classified(e)
	}
	if role == "simulator" && resource != "products" && resource != "inventory" && resource != "equivalents" {
		return out, Reject(403, "FORBIDDEN", "Servis hesabi okuma siniri")
	}
	scope := digest([]string{actor, resource, location, search})
	after := "0"
	if cursor != "" {
		b, e := base64.RawURLEncoding.DecodeString(cursor)
		parts := strings.Split(string(b), ":")
		if e != nil || len(parts) != 2 || parts[0] != scope {
			return out, Reject(400, "INVALID_CURSOR", "Cursor filtre/lokasyonla uyusmuyor")
		}
		if _, e = Positive(parts[1]); e != nil {
			return out, Reject(400, "INVALID_CURSOR", "Cursor gecersiz")
		}
		after = parts[1]
	}
	if resource != "products" && resource != "suppliers" && resource != "targets" && resource != "prices" && resource != "equivalents" {
		if _, e = Positive(location); e != nil {
			return out, e
		}
		var ok bool
		e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "pl-user-locations" WHERE user_id=$1 AND location_id=$2)`, actor, location).Scan(&ok)
		if e != nil {
			return out, classified(e)
		}
		if (!ok && !(role == "accountant" && (resource == "orders" || resource == "allocations"))) || (role == "accountant" && resource != "orders" && resource != "allocations") {
			return out, Reject(403, "FORBIDDEN", "Lokasyona yetkiniz yok")
		}
	}
	if (resource == "prescriptions" && role != "pharmacist" && role != "technician") || (resource == "controlled-ledger" && role != "pharmacist" && role != "manager") || (resource == "prices" && role != "accountant") {
		return out, Reject(403, "FORBIDDEN", "Bu kayitlara yetkiniz yok")
	}
	if (resource == "needs" || resource == "orders" || resource == "allocations") && role != "warehouse" && role != "manager" && role != "pharmacist" && role != "accountant" {
		return out, Reject(403, "FORBIDDEN", "Tedarik kayitlarina yetkiniz yok")
	}
	var locationKind string
	if resource == "needs" || resource == "orders" || resource == "allocations" {
		if e = tx.QueryRow(ctx, `SELECT kind FROM "pl-locations" WHERE id=$1`, location).Scan(&locationKind); e != nil {
			return out, classified(e)
		}
	}
	var eqKey string
	if resource == "equivalents" {
		if _, err := Positive(search); err != nil {
			return out, err
		}
		p, err := productIdentity(ctx, tx, search)
		if err != nil {
			return out, classified(err)
		}
		eqKey, err = composition(p)
		if err != nil {
			return out, err
		}
	}
	var query string
	args := []any{after, limit + 1}
	switch resource {
	case "products", "equivalents":
		query = `SELECT p.id::text,jsonb_build_object('id',p.id::text,'barcode',p.barcode,'name',p.name,'dose',p.dose,'form',p.form,'base_unit',p.base_unit,'pack_size',p.pack_size::text,'blister_size',p.blister_size::text,'sale_multiple',p.sale_multiple::text,'prescription_required',p.prescription_required,'controlled_group',p.controlled_group,'active',p.active,'ingredients',coalesce((SELECT jsonb_agg(i.name ORDER BY i.name) FROM "pl-product-ingredients" pi JOIN "pl-ingredients" i ON i.id=pi.ingredient_id WHERE pi.product_id=p.id),'[]'::jsonb)) FROM "pl-products" p WHERE p.id>$1 AND (p.barcode ILIKE '%'||$3||'%' OR p.name ILIKE '%'||$3||'%' OR EXISTS(SELECT 1 FROM "pl-product-ingredients" pi JOIN "pl-ingredients" i ON i.id=pi.ingredient_id WHERE pi.product_id=p.id AND i.name ILIKE '%'||$3||'%')) ORDER BY p.id LIMIT $2`
		if resource == "equivalents" {
			query = strings.TrimSuffix(query, " LIMIT $2")
			query = strings.ReplaceAll(query, "p.id>$1", "p.id>$1 AND p.active AND $2>0")
			search = ""
		}
		args = append(args, search)
	case "needs":
		query = `SELECT n.id::text,jsonb_build_object('id',n.id::text,'location_id',n.location_id::text,'product_id',n.product_id::text,'quantity',n.quantity::text,'status',CASE WHEN EXISTS(SELECT 1 FROM "pl-order-allocations" WHERE need_id=n.id) THEN 'ORDERED' ELSE n.status END,'reason',n.reason) FROM "pl-needs" n WHERE n.id>$1 AND (n.location_id=$3 OR $4) ORDER BY n.id LIMIT $2`
		args = append(args, location, role == "warehouse" && locationKind == "warehouse")
	case "orders":
		query = `SELECT o.id::text,jsonb_build_object('id',o.id::text,'location_id',o.location_id::text,'supplier_id',o.supplier_id::text,'status',o.status,'reason',o.reason,'lines',(SELECT jsonb_agg(jsonb_build_object('id',l.id::text,'product_id',l.product_id::text,'quantity',l.quantity::text,'received',l.received::text,'cancelled',l.cancelled::text,'remaining',(l.quantity-l.received-l.cancelled)::text,'allocations',coalesce((SELECT jsonb_agg(jsonb_build_object('id',a.id::text,'need_id',a.need_id::text,'location_id',a.location_id::text,'quantity',a.quantity::text,'received',a.received::text,'cancelled',a.cancelled::text) ORDER BY a.id) FROM "pl-order-allocations" a WHERE a.line_id=l.id AND ($4 OR a.location_id=$3)),'[]'::jsonb)) ORDER BY l.id) FROM "pl-order-lines" l WHERE l.order_id=o.id AND ($4 OR EXISTS(SELECT 1 FROM "pl-order-allocations" a WHERE a.line_id=l.id AND a.location_id=$3)))) FROM "pl-orders" o WHERE o.id>$1 AND ($6='' OR o.id::text=$6) AND ($5 OR o.location_id=$3 OR EXISTS(SELECT 1 FROM "pl-order-lines" l JOIN "pl-order-allocations" a ON a.line_id=l.id WHERE l.order_id=o.id AND a.location_id=$3)) ORDER BY o.id LIMIT $2`
		args = append(args, location, role == "accountant" || (role == "warehouse" && locationKind == "warehouse"), role == "accountant", search)
	case "allocations":
		query = `SELECT p.allocation_id::text,jsonb_build_object('id',p.allocation_id::text,'order_id',p.order_id::text,'warehouse_id',p.warehouse_id::text,'branch_id',p.branch_id::text,'product_id',p.product_id::text,'expected',p.expected::text,'at_warehouse',p.at_warehouse::text,'in_transit',p.in_transit::text,'delivered',p.delivered::text,'exceptions',p.exceptions::text,'cancelled',p.cancelled::text,'quantity',p.quantity::text) FROM "pl-procurement-pipeline" p WHERE p.allocation_id>$1 AND (p.warehouse_id=$3 OR p.branch_id=$3 OR $4) ORDER BY p.allocation_id LIMIT $2`
		args = append(args, location, role == "accountant")
	case "counts":
		query = `SELECT c.id::text,jsonb_build_object('id',c.id::text,'product_id',c.product_id::text,'status',c.status,'expires_at',c.expires_at,'lines',coalesce((SELECT jsonb_agg(jsonb_build_object('lot_id',l.lot_id::text,'account',l.account,'expected',l.expected::text) ORDER BY l.lot_id,l.account) FROM "pl-count-lines" l WHERE l.count_id=c.id),'[]'::jsonb),'results',coalesce((SELECT jsonb_agg(jsonb_build_object('lot_id',l.lot_id::text,'account',l.account,'expected',l.expected::text,'actual',l.actual::text) ORDER BY l.lot_id,l.account) FROM "pl-count-results" l WHERE l.count_id=c.id),'[]'::jsonb)) FROM "pl-counts" c WHERE c.id>$1 AND c.location_id=$3 ORDER BY c.id LIMIT $2`
		args = append(args, location)
	case "returns":
		query = `SELECT r.id::text,jsonb_build_object('id',r.id::text,'sale_id',r.sale_id::text,'reason',r.reason,'patient_total',r.patient_total::text,'institution_total',r.institution_total::text,'effective_at',r.effective_at,'lines',(SELECT jsonb_agg(jsonb_build_object('id',l.id::text,'sale_line_id',l.sale_line_id::text,'lot_id',l.lot_id::text,'quantity',l.quantity::text,'patient_total',l.patient_total::text,'institution_total',l.institution_total::text) ORDER BY l.id) FROM "pl-return-lines" l WHERE l.return_id=r.id)) FROM "pl-returns" r WHERE r.id>$1 AND r.location_id=$3 ORDER BY r.id LIMIT $2`
		args = append(args, location)
	case "quarantine":
		query = `SELECT i.id::text,jsonb_build_object('id',i.id::text,'event_id',i.event_id::text,'return_line_id',i.return_line_id::text,'sale_id',(SELECT sale_id::text FROM "pl-return-lines" WHERE id=i.return_line_id),'lot_id',i.lot_id::text,'lot',l.number,'expires_on',l.expires_on::text,'quantity',i.quantity::text,'remaining',(i.quantity-coalesce((SELECT sum(d.quantity) FROM "pl-quarantine-decisions" d WHERE d.item_id=i.id),0)-coalesce((SELECT sum(c.quantity) FROM "pl-quarantine-count-losses" c WHERE c.item_id=i.id),0))::text,'decisions',coalesce((SELECT jsonb_agg(jsonb_build_object('id',d.id::text,'action',d.action,'quantity',d.quantity::text,'reason',d.reason,'effective_at',d.effective_at) ORDER BY d.id) FROM "pl-quarantine-decisions" d WHERE d.item_id=i.id),'[]'::jsonb)) FROM "pl-quarantine-items" i JOIN "pl-lots" l ON l.id=i.lot_id WHERE i.id>$1 AND i.location_id=$3 ORDER BY i.id LIMIT $2`
		args = append(args, location)
	case "prices":
		query = `SELECT id::text,jsonb_build_object('id',id::text,'product_id',product_id::text,'pack_price',pack_price::text,'valid_from',valid_from::text,'valid_to',valid_to::text) FROM "pl-price-periods" WHERE id>$1 ORDER BY id LIMIT $2`
	case "prescriptions":
		query = `SELECT r.id::text,jsonb_build_object('id',r.id::text,'external_id',r.external_id,'patient_external_id',p.external_id,'birth_date',p.birth_date::text,'issued_at',r.issued_at,'report_from',r.report_from,'report_to',r.report_to,'owner_id',r.owner_id::text,'lines',(SELECT jsonb_agg(jsonb_build_object('id',l.id::text,'prescribed_product_id',l.prescribed_product_id::text,'prescribed_base',l.prescribed_base::text,'fulfilled_base',l.fulfilled_base::text,'remaining_base',(l.prescribed_base-l.fulfilled_base)::text) ORDER BY l.id) FROM "pl-prescription-lines" l WHERE l.prescription_id=r.id)) FROM "pl-prescriptions" r JOIN "pl-patients" p ON p.id=r.patient_id WHERE r.id>$1 AND (r.owner_id IS NULL OR r.owner_id=$3) AND (r.external_id=$4 OR $4='') ORDER BY r.id LIMIT $2`
		args = append(args, location, search)
	case "controlled-ledger":
		query = `SELECT id::text,jsonb_build_object('id',id::text,'patient_id',patient_id::text,'prescription_id',prescription_id::text,'sale_line_id',sale_line_id::text,'product_id',product_id::text,'lot_id',lot_id::text,'quantity',quantity::text,'group_name',group_name,'policy_id',policy_id::text,'business_day',business_day::text,'effective_at',effective_at,'returns',coalesce((SELECT jsonb_agg(jsonb_build_object('return_line_id',r.return_line_id::text,'quantity',r.quantity::text) ORDER BY r.id) FROM "pl-controlled-returns" r WHERE r.ledger_id="pl-controlled-ledger".id),'[]'::jsonb)) FROM "pl-controlled-ledger" WHERE id>$1 AND location_id=$3 ORDER BY "pl-controlled-ledger".id LIMIT $2`
		args = append(args, location)
	case "suppliers":
		query = `SELECT id::text,jsonb_build_object('id',id::text,'name',name) FROM "pl-suppliers" WHERE id>$1 ORDER BY id LIMIT $2`
	case "targets":
		if role != "warehouse" && role != "manager" {
			return out, Reject(403, "FORBIDDEN", "Depo yetkisi gerekli")
		}
		query = `SELECT id::text,jsonb_build_object('id',id::text,'name',name) FROM "pl-locations" WHERE id>$1 ORDER BY id LIMIT $2`
	case "inventory":
		query = `SELECT l.id::text,jsonb_build_object('id',l.id::text,'product_id',l.product_id::text,'name',p.name,'barcode',p.barcode,'lot',l.number,'expires_on',l.expires_on::text,'base_unit',p.base_unit,'reserved',(coalesce((SELECT sum(x.quantity-x.shipped) FROM "pl-allocation-lots" x JOIN "pl-order-allocations" a ON a.id=x.allocation_id JOIN "pl-order-lines" ol ON ol.id=a.line_id JOIN "pl-orders" o ON o.id=ol.order_id WHERE x.lot_id=l.id AND o.location_id=$3),0))::text,'free_physical',(coalesce((SELECT quantity FROM "pl-stock-balances" WHERE location_id=$3 AND lot_id=l.id AND account='physical'),0)-coalesce((SELECT sum(x.quantity-x.shipped) FROM "pl-allocation-lots" x JOIN "pl-order-allocations" a ON a.id=x.allocation_id JOIN "pl-order-lines" ol ON ol.id=a.line_id JOIN "pl-orders" o ON o.id=ol.order_id WHERE x.lot_id=l.id AND o.location_id=$3),0))::text,'balances',(SELECT jsonb_object_agg(account,quantity::text) FROM "pl-stock-balances" WHERE lot_id=l.id AND location_id=$3)) FROM "pl-lots" l JOIN "pl-products" p ON p.id=l.product_id WHERE l.id>$1 AND (p.name ILIKE '%'||$4||'%' OR p.barcode ILIKE '%'||$4||'%' OR l.number ILIKE '%'||$4||'%') AND EXISTS(SELECT 1 FROM "pl-stock-balances" WHERE lot_id=l.id AND location_id=$3) ORDER BY l.id LIMIT $2`
		args = append(args, location, search)
	case "movements":
		query = `SELECT e.id::text,jsonb_build_object('id',e.id::text,'event_id',e.event_id::text,'kind',s.kind,'reference',s.reference,'lot_id',e.lot_id::text,'lot',l.number,'name',p.name,'barcode',p.barcode,'base_unit',p.base_unit,'account',e.account,'quantity',e.quantity::text,'created_at',s.created_at) FROM "pl-stock-entries" e JOIN "pl-stock-events" s ON s.id=e.event_id JOIN "pl-lots" l ON l.id=e.lot_id JOIN "pl-products" p ON p.id=l.product_id WHERE e.id>$1 AND e.location_id=$3 AND (p.name ILIKE '%'||$4||'%' OR p.barcode ILIKE '%'||$4||'%' OR l.number ILIKE '%'||$4||'%' OR s.reference ILIKE '%'||$4||'%' OR s.kind ILIKE '%'||$4||'%') ORDER BY e.id LIMIT $2`
		args = append(args, location, search)
	case "transfers":
		query = `SELECT t.id::text,jsonb_build_object('id',t.id::text,'source_id',t.source_id::text,'target_id',t.target_id::text,'status',t.status,'order_id',(SELECT l.order_id::text FROM "pl-transfer-order-links" x JOIN "pl-order-allocations" a ON a.id=x.allocation_id JOIN "pl-order-lines" l ON l.id=a.line_id WHERE x.transfer_id=t.id),'allocation_id',(SELECT allocation_id::text FROM "pl-transfer-order-links" WHERE transfer_id=t.id),'lines',(SELECT jsonb_agg(jsonb_build_object('lot_id',l.lot_id::text,'quantity',l.quantity::text,'good',coalesce((SELECT sum(r.good) FROM "pl-transfer-receipt-lines" r WHERE r.transfer_id=l.transfer_id AND r.lot_id=l.lot_id),CASE WHEN t.status='CLOSED' THEN l.quantity ELSE 0 END)::text,'damaged',coalesce((SELECT sum(r.damaged) FROM "pl-transfer-receipt-lines" r WHERE r.transfer_id=l.transfer_id AND r.lot_id=l.lot_id),0)::text,'missing',coalesce((SELECT sum(r.missing) FROM "pl-transfer-receipt-lines" r WHERE r.transfer_id=l.transfer_id AND r.lot_id=l.lot_id),0)::text)) FROM "pl-transfer-lines" l WHERE l.transfer_id=t.id)) FROM "pl-transfers" t WHERE t.id>$1 AND (t.source_id=$3 OR t.target_id=$3) ORDER BY t.id LIMIT $2`
		args = append(args, location)
	case "sales":
		query = `SELECT s.id::text,jsonb_build_object('id',s.id::text,'total',s.total::text,'effective_at',s.effective_at,'payment_method',s.payment_method,'prescription_id',s.prescription_id::text,'patient_total',coalesce(s.patient_total,s.total)::text,'institution_total',coalesce(s.institution_total,0)::text,'lines',(SELECT jsonb_agg(jsonb_build_object('id',l.id::text,'product_id',l.product_id::text,'quantity',l.quantity::text,'pack_size',l.pack_size::text,'base_unit',l.base_unit,'pack_price',l.pack_price::text,'total',l.total::text,'policy',l.policy,'snapshot',l.snapshot,'allocations',(SELECT jsonb_agg(jsonb_build_object('lot_id',a.lot_id::text,'quantity',a.quantity::text)) FROM "pl-sale-allocations" a WHERE a.line_id=l.id))) FROM "pl-sale-lines" l WHERE l.sale_id=s.id)) FROM "pl-sales" s WHERE s.id>$1 AND s.location_id=$3 AND ($4='' OR s.id::text=$4) ORDER BY s.id LIMIT $2`
		args = append(args, location, search)
	default:
		return out, Reject(404, "NOT_FOUND", "Liste bulunamadi")
	}
	rows, e := tx.Query(ctx, query, args...)
	if e != nil {
		return out, classified(e)
	}
	defer rows.Close()
	last := ""
	for rows.Next() {
		var id string
		var b []byte
		if e = rows.Scan(&id, &b); e != nil {
			return out, classified(e)
		}
		if resource == "equivalents" {
			var p Product
			if json.Unmarshal(b, &p) != nil {
				return out, Invalid()
			}
			key, err := composition(p)
			if err != nil || key != eqKey {
				continue
			}
		}
		if len(out.Items) == limit {
			out.Next = base64.RawURLEncoding.EncodeToString([]byte(scope + ":" + last))
			break
		}
		out.Items = append(out.Items, json.RawMessage(b))
		last = id
	}
	if e = rows.Err(); e != nil {
		return out, classified(e)
	}
	return out, nil
}
func (s Service) SaleDetail(ctx context.Context, actor, id string) (json.RawMessage, error) {
	if _, e := Positive(id); e != nil {
		return nil, e
	}
	var location string
	e := s.Pool.QueryRow(ctx, `SELECT location_id::text FROM "pl-sales" WHERE id=$1`, id).Scan(&location)
	if e != nil {
		return nil, classified(e)
	}
	// Reuse the same scoped projection; no unscoped detail query bypasses authorization.
	p, e := s.Read(ctx, actor, "sales", location, id, "", 200)
	if e != nil {
		return nil, e
	}
	for {
		for _, b := range p.Items {
			var v struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(b, &v)
			if v.ID == id {
				return b, nil
			}
		}
		if p.Next == "" {
			break
		}
		p, e = s.Read(ctx, actor, "sales", location, id, p.Next, 200)
		if e != nil {
			return nil, e
		}
	}
	return nil, Reject(404, "NOT_FOUND", "Satis bulunamadi")
}
