package stock

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

type PrescriptionInput struct {
	ExternalID string                  `json:"external_id"`
	PatientID  string                  `json:"patient_external_id"`
	BirthDate  string                  `json:"birth_date"`
	IssuedAt   time.Time               `json:"issued_at"`
	ReportFrom *time.Time              `json:"report_from,omitempty"`
	ReportTo   *time.Time              `json:"report_to,omitempty"`
	Lines      []PrescriptionLineInput `json:"lines"`
}
type PrescriptionLineInput struct {
	ProductID string `json:"prescribed_product_id"`
	Quantity  string `json:"prescribed_base"`
}
type DispenseItem struct {
	LineID    string `json:"prescription_line_id"`
	ProductID string `json:"dispensed_product_id"`
	Quantity  string `json:"quantity"`
	Unit      string `json:"unit"`
}
type PriceInput struct {
	ProductID string    `json:"product_id"`
	ValidFrom time.Time `json:"valid_from"`
	PackPrice string    `json:"pack_price"`
}

func loadPrescription(ctx context.Context, tx Tx, in Input) (any, error) {
	if in.Prescription == nil {
		return nil, Invalid()
	}
	p := *in.Prescription
	p.ExternalID = strings.TrimSpace(p.ExternalID)
	p.PatientID = strings.TrimSpace(p.PatientID)
	if len(p.ExternalID) == 0 || len(p.ExternalID) > 100 || len(p.PatientID) == 0 || len(p.PatientID) > 100 || len(p.Lines) == 0 || len(p.Lines) > 100 {
		return nil, Invalid()
	}
	birth, e := time.Parse("2006-01-02", p.BirthDate)
	if e != nil {
		return nil, Invalid()
	}
	var now time.Time
	if e = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return nil, e
	}
	if p.IssuedAt.IsZero() || p.IssuedAt.After(now) || p.BirthDate > businessDay(now) || birth.After(p.IssuedAt) {
		return nil, Invalid()
	}
	if (p.ReportFrom == nil) != (p.ReportTo == nil) || p.ReportFrom != nil && !p.ReportTo.After(*p.ReportFrom) {
		return nil, Invalid()
	}
	sort.Slice(p.Lines, func(i, j int) bool {
		a, _ := Positive(p.Lines[i].ProductID)
		b, _ := Positive(p.Lines[j].ProductID)
		return a < b
	})
	for i, l := range p.Lines {
		if _, e = Positive(l.ProductID); e != nil {
			return nil, e
		}
		if _, e = Positive(l.Quantity); e != nil {
			return nil, e
		}
		if i > 0 && l.ProductID == p.Lines[i-1].ProductID {
			return nil, Invalid()
		}
	}
	hash := digest(p)
	// Patient lock precedes RX lock only in the import command. Sale never locks patients.
	var patient string
	e = tx.QueryRow(ctx, `INSERT INTO "pl-patients"(external_id,birth_date) VALUES($1,$2) ON CONFLICT DO NOTHING RETURNING id::text`, p.PatientID, birth).Scan(&patient)
	if errors.Is(e, pgx.ErrNoRows) {
		var old string
		e = tx.QueryRow(ctx, `SELECT id::text,birth_date::text FROM "pl-patients" WHERE external_id=$1`, p.PatientID).Scan(&patient, &old)
		if e == nil && old != p.BirthDate {
			return nil, Reject(409, "PATIENT_CONFLICT", "Hasta demo kimligi farkli dogum tarihiyle kayitli")
		}
	}
	if e != nil {
		return nil, e
	}
	var id string
	e = tx.QueryRow(ctx, `INSERT INTO "pl-prescriptions"(external_id,patient_id,issued_at,report_from,report_to,digest) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING RETURNING id::text`, p.ExternalID, patient, p.IssuedAt, p.ReportFrom, p.ReportTo, hash).Scan(&id)
	if errors.Is(e, pgx.ErrNoRows) {
		var old string
		var owner *string
		e = tx.QueryRow(ctx, `SELECT id::text,digest,owner_id::text FROM "pl-prescriptions" WHERE external_id=$1 FOR SHARE`, p.ExternalID).Scan(&id, &old, &owner)
		if e == nil && old != hash {
			return nil, Reject(409, "PRESCRIPTION_CONFLICT", "Harici recete kimliginin icerigi farkli")
		}
		if e != nil {
			return nil, e
		}
		if owner != nil && *owner != in.LocationID {
			return nil, Reject(403, "PRESCRIPTION_BRANCH", "Recete ilk karsilandigi subeye bagli")
		}
		return prescriptionView(ctx, tx, id)
	}
	if e != nil {
		return nil, e
	}
	for _, l := range p.Lines {
		if _, e = tx.Exec(ctx, `INSERT INTO "pl-prescription-lines"(prescription_id,prescribed_product_id,prescribed_base) VALUES($1,$2,$3)`, id, l.ProductID, l.Quantity); e != nil {
			return nil, e
		}
	}
	return prescriptionView(ctx, tx, id)
}

func prescriptionView(ctx context.Context, tx Tx, id string) (json.RawMessage, error) {
	var b []byte
	e := tx.QueryRow(ctx, `SELECT jsonb_build_object('id',r.id::text,'external_id',r.external_id,'patient_external_id',p.external_id,'birth_date',p.birth_date::text,'issued_at',r.issued_at,'report_from',r.report_from,'report_to',r.report_to,'owner_id',r.owner_id::text,'lines',(SELECT jsonb_agg(jsonb_build_object('id',l.id::text,'prescribed_product_id',l.prescribed_product_id::text,'prescribed_base',l.prescribed_base::text,'fulfilled_base',l.fulfilled_base::text,'remaining_base',(l.prescribed_base-l.fulfilled_base)::text) ORDER BY l.id) FROM "pl-prescription-lines" l WHERE l.prescription_id=r.id)) FROM "pl-prescriptions" r JOIN "pl-patients" p ON p.id=r.patient_id WHERE r.id=$1`, id).Scan(&b)
	return json.RawMessage(b), e
}
func (s Service) now(ctx context.Context, tx Tx) (at time.Time, e error) {
	if s.clock != nil {
		return s.clock(ctx, tx)
	}
	e = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&at)
	return
}

var errDayChanged = errors.New("business day changed while waiting for locks")

type rxState struct {
	id, patient          string
	issued, birth        time.Time
	reportFrom, reportTo *time.Time
	owner                *string
	day                  string
	groups               map[string]int64
	policies             map[string]string
	uses                 []rxUse
}
type rxUse struct {
	line, product string
	quantity      int64
}

func productIdentity(ctx context.Context, tx Tx, id string) (Product, error) {
	var p Product
	e := tx.QueryRow(ctx, `SELECT p.dose,p.form,p.base_unit,p.controlled_group,coalesce((SELECT array_agg(i.name ORDER BY i.name) FROM "pl-product-ingredients" pi JOIN "pl-ingredients" i ON i.id=pi.ingredient_id WHERE pi.product_id=p.id),'{}') FROM "pl-products" p WHERE p.id=$1`, id).Scan(&p.Dose, &p.Form, &p.BaseUnit, &p.ControlledGroup, &p.Ingredients)
	return p, e
}
func equivalent(ctx context.Context, tx Tx, a, b string) error {
	if a == b {
		return nil
	}
	var ga, gb string
	if e := tx.QueryRow(ctx, `SELECT a.controlled_group,b.controlled_group FROM "pl-products" a,"pl-products" b WHERE a.id=$1 AND b.id=$2`, a, b).Scan(&ga, &gb); e != nil {
		return e
	}
	if ga != gb {
		return Reject(422, "NOT_EQUIVALENT", "Kontrollu ilac grubu ayni olmali")
	}
	p, e := productIdentity(ctx, tx, a)
	if e != nil {
		return e
	}
	q, e := productIdentity(ctx, tx, b)
	if e != nil {
		return e
	}
	x, e := composition(p)
	if e != nil {
		return Reject(422, "NOT_EQUIVALENT", "Urun bilesimi normalize edilemedi")
	}
	y, e := composition(q)
	if e != nil || x != y {
		return Reject(422, "NOT_EQUIVALENT", "Madde, doz, form ve temel birim ayni olmali")
	}
	return nil
}

// RX header is the single serialization point for ownership and all its line remainders.
func (s Service) prepareRX(ctx context.Context, tx Tx, in *Input) (*rxState, error) {
	if in.PrescriptionID == "" {
		if len(in.Dispense) > 0 {
			return nil, Invalid()
		}
		return nil, nil
	}
	if _, e := Positive(in.PrescriptionID); e != nil {
		return nil, e
	}
	if len(in.Items) > 0 || len(in.Dispense) == 0 || len(in.Dispense) > 100 {
		return nil, Invalid()
	}
	r := &rxState{id: in.PrescriptionID, groups: map[string]int64{}, policies: map[string]string{}}
	e := tx.QueryRow(ctx, `SELECT r.patient_id::text,r.issued_at,p.birth_date,r.report_from,r.report_to,r.owner_id::text FROM "pl-prescriptions" r JOIN "pl-patients" p ON p.id=r.patient_id WHERE r.id=$1 FOR UPDATE OF r`, r.id).Scan(&r.patient, &r.issued, &r.birth, &r.reportFrom, &r.reportTo, &r.owner)
	if e != nil {
		return nil, e
	}
	if r.owner != nil && *r.owner != in.LocationID {
		return nil, Reject(403, "PRESCRIPTION_BRANCH", "Recete ilk karsilandigi subeye bagli")
	}
	sums := map[string]int64{}
	products := map[string]string{}
	for _, v := range in.Dispense {
		if _, e = Positive(v.LineID); e != nil {
			return nil, e
		}
		if _, e = Positive(v.ProductID); e != nil {
			return nil, e
		}
		q, e := Positive(v.Quantity)
		if e != nil {
			return nil, e
		}
		var pack, blister, multiple int64
		var group, base string
		e = tx.QueryRow(ctx, `SELECT pack_size,blister_size,sale_multiple,controlled_group,base_unit FROM "pl-products" WHERE id=$1`, v.ProductID).Scan(&pack, &blister, &multiple, &group, &base)
		if e != nil {
			return nil, e
		}
		q, e = Convert(q, pack, blister, multiple, v.Unit)
		if e != nil {
			return nil, e
		}
		var prescribed string
		var remaining int64
		e = tx.QueryRow(ctx, `SELECT prescribed_product_id::text,prescribed_base-fulfilled_base FROM "pl-prescription-lines" WHERE id=$1 AND prescription_id=$2`, v.LineID, r.id).Scan(&prescribed, &remaining)
		if e != nil {
			return nil, e
		}
		if e = equivalent(ctx, tx, prescribed, v.ProductID); e != nil {
			return nil, e
		}
		if sums[v.LineID] > math.MaxInt64-q {
			return nil, Invalid()
		}
		sums[v.LineID] += q
		if sums[v.LineID] > remaining {
			return nil, Reject(422, "PRESCRIPTION_REMAINDER", "Recetede kalan miktar yetersiz")
		}
		// The same dispensed product may serve multiple RX lines, but is priced once per sale.
		key := v.LineID + ":" + v.ProductID
		found := false
		for n := range r.uses {
			u := &r.uses[n]
			if u.line+":"+u.product == key {
				u.quantity += q
				found = true
				break
			}
		}
		if !found {
			r.uses = append(r.uses, rxUse{v.LineID, v.ProductID, q})
		}
		in.Items = append(in.Items, Item{v.ProductID, strconv.FormatInt(q, 10), "unit"})
		products[v.ProductID] = base
		if group != "" {
			if group != "CONTROL-TAB" || base != "tablet" || r.groups[group] > math.MaxInt64-q {
				return nil, Invalid()
			}
			r.groups[group] += q
		}
	}
	// Sort products before the existing items() merge and FEFO allocation.
	sort.Slice(in.Items, func(i, j int) bool {
		a, _ := Positive(in.Items[i].ProductID)
		b, _ := Positive(in.Items[j].ProductID)
		return a < b
	})
	at, e := s.now(ctx, tx)
	if e != nil {
		return nil, e
	}
	r.day = businessDay(at)
	groups := []string{}
	for g := range r.groups {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	for _, g := range groups {
		if _, e = tx.Exec(ctx, `INSERT INTO "pl-daily-controls"(patient_id,group_name,business_day) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, r.patient, g, r.day); e != nil {
			return nil, e
		}
		if _, e = tx.Exec(ctx, `SELECT 1 FROM "pl-daily-controls" WHERE patient_id=$1 AND group_name=$2 AND business_day=$3 FOR UPDATE`, r.patient, g, r.day); e != nil {
			return nil, e
		}
	}
	return r, nil
}

func (r *rxState) validate(ctx context.Context, tx Tx, at time.Time) error {
	if r == nil {
		return nil
	}
	if businessDay(at) != r.day {
		return errDayChanged
	}
	if !prescriptionValid(r.issued, at) {
		return Reject(422, "PRESCRIPTION_EXPIRED", "Recete duzenlenme anindan itibaren 168 saat gecerlidir")
	}
	for g, q := range r.groups {
		var id string
		var limit, used int64
		e := tx.QueryRow(ctx, `SELECT id::text,daily_limit FROM "pl-control-policies" WHERE group_name=$1 AND base_unit='tablet' AND valid_from<=$2 AND valid_to>$2`, g, at).Scan(&id, &limit)
		if e != nil {
			return e
		}
		e = tx.QueryRow(ctx, `SELECT consumed FROM "pl-daily-controls" WHERE patient_id=$1 AND group_name=$2 AND business_day=$3`, r.patient, g, r.day).Scan(&used)
		if e != nil {
			return e
		}
		if q > limit || used > limit-q {
			return Reject(422, "DAILY_LIMIT", "Hasta icin zincir genelinde gunluk kontrollu ilac limiti asiliyor")
		}
		r.policies[g] = id
	}
	return nil
}

func schedulePrice(ctx context.Context, tx Tx, in Input) (any, error) {
	if in.Price == nil {
		return nil, Invalid()
	}
	p := in.Price
	if _, e := Positive(p.ProductID); e != nil {
		return nil, e
	}
	if _, e := Positive(p.PackPrice); e != nil {
		return nil, e
	}
	// Sales hold FOR SHARE on these same products until commit. Check the real
	// clock after this exclusive lock: an intent whose start passed while waiting
	// is rejected, never backdated or silently moved to another start instant.
	var id string
	if e := tx.QueryRow(ctx, `SELECT id::text FROM "pl-products" WHERE id=$1 FOR UPDATE`, p.ProductID).Scan(&id); e != nil {
		return nil, e
	}
	var now time.Time
	if e := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
		return nil, e
	}
	if !p.ValidFrom.After(now) {
		return nil, Reject(422, "FUTURE_PRICE_REQUIRED", "Yeni fiyat gelecekte baslamali")
	}
	var old, end string
	e := tx.QueryRow(ctx, `SELECT id::text,valid_to::text FROM "pl-price-periods" WHERE product_id=$1 AND valid_from<$2 AND valid_to>$2`, p.ProductID, p.ValidFrom).Scan(&old, &end)
	endArg := any("infinity")
	if e == nil {
		// pgx cannot scan PostgreSQL infinity into time.Time; use text below instead.
		endArg = end
	} else if !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	if old != "" {
		if _, e = tx.Exec(ctx, `UPDATE "pl-price-periods" SET valid_to=$2 WHERE id=$1`, old, p.ValidFrom); e != nil {
			return nil, e
		}
	}
	if e = tx.QueryRow(ctx, `INSERT INTO "pl-price-periods"(product_id,valid_from,valid_to,pack_price) VALUES($1,$2,$3::timestamptz,$4) RETURNING id::text`, p.ProductID, p.ValidFrom, endArg, p.PackPrice).Scan(&id); e != nil {
		return nil, e
	}
	return map[string]any{"id": id, "product_id": p.ProductID, "valid_from": p.ValidFrom, "pack_price": p.PackPrice}, nil
}
