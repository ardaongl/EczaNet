package stock

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"strings"
)

func (s Service) suggestionRead(ctx context.Context, actor, resource, location, search, cursor string, limit int) (Page, error) {
	out := Page{Items: []json.RawMessage{}}
	if limit < 1 || limit > 200 {
		return out, Reject(400, "INVALID_PAGE", "Sayfa limiti 1..200 olmali")
	}
	tx, e := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return out, classified(e)
	}
	defer tx.Rollback(ctx)
	var role string
	if e = tx.QueryRow(ctx, `SELECT role FROM "pl-wy-users" WHERE id=$1 AND active`, actor).Scan(&role); e != nil {
		return out, classified(e)
	}
	if role == "simulator" || role == "technician" || (resource == "report-finance" && role != "accountant" && role != "manager" && role != "pharmacist") {
		return out, Reject(403, "FORBIDDEN", "Rapora yetkiniz yok")
	}
	var kind string
	if role == "accountant" {
		if resource != "suggestions" && resource != "report-finance" {
			return out, Reject(403, "FORBIDDEN", "Rapora yetkiniz yok")
		}
		location = "0"
	} else {
		if _, e = Positive(location); e != nil {
			return out, e
		}
		if e = tx.QueryRow(ctx, `SELECT kind FROM "pl-locations" l WHERE id=$2 AND EXISTS(SELECT 1 FROM "pl-user-locations" WHERE user_id=$1 AND location_id=l.id)`, actor, location).Scan(&kind); e != nil {
			return out, Reject(403, "FORBIDDEN", "Lokasyona yetkiniz yok")
		}
	}
	if (resource == "suggestions" || resource == "report-critical") && search == "" {
		if e = tx.QueryRow(ctx, `SELECT coalesce(max(id),0)::text FROM "pl-suggestion-runs" WHERE published`).Scan(&search); e != nil {
			return out, classified(e)
		}
	}
	if (resource == "suggestions" || resource == "report-critical") && search == "0" {
		return out, Reject(409, "NO_SUGGESTION_RUN", "Henuz hesap yok; once hesaplatin")
	}
	scope := digest([]string{actor, resource, location, search})
	after := "0"
	if cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		parts := strings.Split(string(b), ":")
		if err != nil || len(parts) != 2 || parts[0] != scope {
			return out, Reject(400, "INVALID_CURSOR", "Cursor filtreyle uyusmuyor")
		}
		if _, err = Positive(parts[1]); err != nil {
			return out, Reject(400, "INVALID_CURSOR", "Cursor gecersiz")
		}
		after = parts[1]
	}
	all := role == "accountant" || (role == "warehouse" && kind == "warehouse" && resource == "suggestions")
	var q string
	args := []any{after, limit + 1, location, all}
	switch resource {
	case "suggestions", "report-critical":
		run := search
		if run == "" {
			if e = tx.QueryRow(ctx, `SELECT coalesce(max(id),0)::text FROM "pl-suggestion-runs" WHERE published`).Scan(&run); e != nil {
				return out, classified(e)
			}
		} else if _, e = Positive(run); e != nil {
			return out, e
		}
		args = append(args, run)
		q = `WITH state AS MATERIALIZED (SELECT "pl-suggestion-source"() source) SELECT s.id::text,to_jsonb(s)-'product_scope_key'-'id'-'run_id'-'location_id'-'product_id'||jsonb_build_object('id',s.id::text,'run_id',s.run_id::text,'gross',s.gross::text,'returned',s.returned::text,'rate',s.rate::text,'minimum',s.minimum::text,'target',s.target::text,'physical',s.physical::text,'risk',s.risk::text,'effective',s.effective::text,'pending',s.pending::text,'excluded_pending',s.excluded_pending::text,'suggested',s.suggested::text,'boxes',s.boxes::text,'return_quantity',s.return_quantity::text,'location_id',s.location_id::text,'product_id',s.product_id::text,'name',p.name,'as_of',r.as_of,'version',r.version,'summary_ms',r.summary_ms,'calculation_ms',r.calculation_ms,'stale',r.source<>state.source OR r.as_of<>(clock_timestamp() AT TIME ZONE 'Europe/Istanbul')::date,'need_document',(SELECT document_id::text FROM "pl-suggestion-consumptions" c WHERE c.suggestion_id=s.id AND kind='need'),'return_document',(SELECT document_id::text FROM "pl-suggestion-consumptions" c WHERE c.suggestion_id=s.id AND kind='return')) FROM "pl-order-suggestions" s JOIN "pl-suggestion-runs" r ON r.id=s.run_id JOIN "pl-products" p ON p.id=s.product_id CROSS JOIN state WHERE r.published AND s.id>$1 AND ($4 OR s.location_id=$3) AND s.run_id=$5`
		if resource == "report-critical" {
			q += ` AND s.effective<s.minimum`
		}
		q += ` ORDER BY s.id LIMIT $2`
	case "report-expiry":
		q = `SELECT l.id::text,jsonb_build_object('id',l.id::text,'product_id',l.product_id::text,'name',p.name,'lot',l.number,'expires_on',l.expires_on,'days',l.expires_on-(clock_timestamp() AT TIME ZONE 'Europe/Istanbul')::date,'balances',jsonb_agg(jsonb_build_object('location_id',b.location_id::text,'account',b.account,'quantity',b.quantity::text))) FROM "pl-lots" l JOIN "pl-products" p ON p.id=l.product_id JOIN "pl-stock-balances" b ON b.lot_id=l.id WHERE l.id>$1 AND ($4 OR b.location_id=$3) AND b.account IN ('physical','quarantine') AND b.quantity>0 AND l.expires_on<=(clock_timestamp() AT TIME ZONE 'Europe/Istanbul')::date+30 GROUP BY l.id,p.name ORDER BY l.id LIMIT $2`
	case "report-finance":
		q = `WITH refunds AS (SELECT sale_id,sum(patient_total) patient_returned,sum(institution_total) institution_returned FROM "pl-returns" GROUP BY sale_id), grouped AS (
 SELECT min(s.id) id,s.location_id,(s.effective_at AT TIME ZONE 'Europe/Istanbul')::date AS day,count(*) sale_count,
 sum(s.total) gross,sum(s.patient_total) patient,sum(s.institution_total) institution,
 sum(coalesce(r.patient_returned,0)) patient_returned,sum(coalesce(r.institution_returned,0)) institution_returned
 FROM "pl-sales" s LEFT JOIN refunds r ON r.sale_id=s.id WHERE ($4 OR s.location_id=$3) GROUP BY 2,3)
 SELECT id::text,jsonb_build_object('id',id::text,'location_id',location_id::text,'day',day,'sale_count',sale_count::text,'gross',gross::text,'patient',patient::text,'institution',institution::text,'patient_returned',patient_returned::text,'institution_returned',institution_returned::text,'returned',(patient_returned+institution_returned)::text,'net',(gross-patient_returned-institution_returned)::text) FROM grouped WHERE id>$1 ORDER BY id LIMIT $2`

	case "report-reconciliation":
		q = `WITH ledger AS (SELECT location_id,lot_id,account,sum(quantity) quantity FROM "pl-stock-entries" WHERE location_id=$3 GROUP BY 1,2,3)
 SELECT l.id::text,jsonb_build_object('id',l.id::text,'product_id',l.product_id::text,'name',p.name,'lot',l.number,'accounts',jsonb_agg(jsonb_build_object('account',b.account,'balance',b.quantity::text,'ledger',coalesce(e.quantity,0)::text,'difference',(b.quantity-coalesce(e.quantity,0))::text))) FROM "pl-lots" l JOIN "pl-products" p ON p.id=l.product_id JOIN "pl-stock-balances" b ON b.lot_id=l.id LEFT JOIN ledger e ON e.location_id=b.location_id AND e.lot_id=b.lot_id AND e.account=b.account WHERE l.id>$1 AND b.location_id=$3 AND NOT $4 GROUP BY l.id,p.name ORDER BY l.id LIMIT $2`
	case "report-transfers":
		q = `SELECT t.id::text,jsonb_build_object('id',t.id::text,'source_id',t.source_id::text,'target_id',t.target_id::text,'status',t.status,'lines',jsonb_agg(jsonb_build_object('lot_id',r.lot_id::text,'good',r.good::text,'damaged',r.damaged::text,'missing',r.missing::text))) FROM "pl-transfers" t JOIN "pl-transfer-receipt-lines" r ON r.transfer_id=t.id WHERE t.id>$1 AND ($4 OR t.source_id=$3 OR t.target_id=$3) AND (r.damaged>0 OR r.missing>0) GROUP BY t.id ORDER BY t.id LIMIT $2`
	default:
		return out, Reject(404, "NOT_FOUND", "Rapor bulunamadi")
	}
	rows, e := tx.Query(ctx, q, args...)
	if e != nil {
		return out, classified(e)
	}
	defer rows.Close()
	last := ""
	for rows.Next() {
		var id string
		var body json.RawMessage
		if e = rows.Scan(&id, &body); e != nil {
			return out, classified(e)
		}
		if len(out.Items) == limit {
			out.Next = base64.RawURLEncoding.EncodeToString([]byte(scope + ":" + last))
			break
		}
		last = id
		out.Items = append(out.Items, body)
	}
	if e = rows.Err(); e != nil {
		return out, classified(e)
	}
	return out, nil
}
