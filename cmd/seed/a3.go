package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"eczanet/internal/stock"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

// This offline migrator-only path creates deterministic fixture stock and RX,
// never exposes a backdated-sale or clock override to the API/simulator.
func seedA3(ctx context.Context, tx *sql.Tx, date, seed string) error {
	// Service identity is always explicitly scoped, including fresh small demos.
	if _, e := tx.ExecContext(ctx, `INSERT INTO "pl-user-locations" SELECT u.id,l FROM "pl-wy-users" u CROSS JOIN (VALUES(1::bigint),(21::bigint)) x(l) WHERE u.email='simulator@ddo-case.invalid' AND u.role='simulator' ON CONFLICT DO NOTHING`); e != nil {
		return e
	}
	var oldDate, oldSeed string
	e := tx.QueryRowContext(ctx, `SELECT demo_date::text,demo_seed FROM "pl-feature-seeds" WHERE version='a3-v1'`).Scan(&oldDate, &oldSeed)
	if e == nil {
		if oldDate != date || oldSeed != seed {
			return errSeedMismatch
		}
		return nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	day, _ := time.Parse("2006-01-02", date)
	products := []stock.Product{
		{Barcode: "000339060301", Name: "A3-DEMO-20", Dose: "500mg", PackSize: "20"},
		{Barcode: "000339060302", Name: "A3-DEMO-30", Dose: "0.5g", PackSize: "30"},
		{Barcode: "000339060303", Name: "A3-CONTROL-20", Dose: "500mg", PackSize: "20", ControlledGroup: "CONTROL-TAB"},
		{Barcode: "000339060304", Name: "A3-DIFFERENT-250", Dose: "250mg", PackSize: "20"},
		{Barcode: "000339060305", Name: "A3-COMBINATION", Dose: "500mg+5mg", PackSize: "20"},
	}
	ids := []string{}
	for _, p := range products {
		p.Form = "tablet"
		p.BaseUnit = "tablet"
		p.BlisterSize = "5"
		p.SaleMultiple = "1"
		p.PrescriptionRequired = true
		p.Ingredients = []string{"a3-fixture-a"}
		if p.Name == "A3-COMBINATION" {
			p.Ingredients = append(p.Ingredients, "a3-fixture-b")
		}
		if e = p.Normalize(); e != nil {
			return e
		}
		var id string
		e = tx.QueryRowContext(ctx, `INSERT INTO "pl-products"(barcode,name,dose,form,base_unit,pack_size,blister_size,sale_multiple,prescription_required,controlled_group) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id::text`, p.Barcode, p.Name, p.Dose, p.Form, p.BaseUnit, p.PackSize, p.BlisterSize, p.SaleMultiple, true, p.ControlledGroup).Scan(&id)
		if e != nil {
			return e
		}
		ids = append(ids, id)
		for _, name := range p.Ingredients {
			var ingredient string
			e = tx.QueryRowContext(ctx, `INSERT INTO "pl-ingredients"(name) VALUES($1) ON CONFLICT(name) DO UPDATE SET name=EXCLUDED.name RETURNING id::text`, name).Scan(&ingredient)
			if e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO "pl-product-ingredients" VALUES($1,$2)`, id, ingredient); e != nil {
				return e
			}
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO "pl-price-periods"(product_id,valid_from,pack_price) VALUES($1,$2,20000)`, id, day); e != nil {
			return e
		}
		var lot string
		e = tx.QueryRowContext(ctx, `INSERT INTO "pl-lots"(product_id,supplier_id,number,expires_on) VALUES($1,1,'A3-SEED',$2) RETURNING id::text`, id, day.AddDate(1, 0, 0)).Scan(&lot)
		if e != nil {
			return e
		}
		for _, loc := range []string{"1", "2", "21"} {
			if _, e = tx.ExecContext(ctx, `INSERT INTO "pl-inventory-guards" VALUES($1,$2)`, loc, id); e != nil {
				return e
			}
			var event string
			e = tx.QueryRowContext(ctx, `INSERT INTO "pl-stock-events"(kind,reference) VALUES('seed.a3','a3-v1') RETURNING id::text`).Scan(&event)
			if e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO "pl-stock-balances" VALUES($1,$2,'physical',100),($1,$2,'supply',-100)`, loc, lot); e != nil {
				return e
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO "pl-stock-entries"(event_id,location_id,lot_id,account,quantity) VALUES($1,$2,$3,'physical',100),($1,$2,$3,'supply',-100)`, event, loc, lot); e != nil {
				return e
			}
		}
	}
	for n, birth := range []time.Time{time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC), day.AddDate(-18, 0, 0), day.AddDate(-65, 0, 0)} {
		external := "A3-H" + strconv.Itoa(n+1)
		var patient string
		e = tx.QueryRowContext(ctx, `INSERT INTO "pl-patients"(external_id,birth_date) VALUES($1,$2) RETURNING id::text`, external, birth).Scan(&patient)
		if e != nil {
			return e
		}
		rxExternal := []string{"A3-RX30", "A3-RX-H2", "A3-RX-H3"}[n]
		in := stock.PrescriptionInput{ExternalID: rxExternal, PatientID: external, BirthDate: birth.Format("2006-01-02"), IssuedAt: day.Add(9 * time.Hour), Lines: []stock.PrescriptionLineInput{{ProductID: ids[1], Quantity: "30"}}}
		if n == 2 {
			from := day.Add(8 * time.Hour)
			to := day.Add(72 * time.Hour)
			in.ReportFrom = &from
			in.ReportTo = &to
		}
		raw, _ := json.Marshal(in)
		hash := sha256.Sum256(raw)
		var rx string
		e = tx.QueryRowContext(ctx, `INSERT INTO "pl-prescriptions"(external_id,patient_id,issued_at,report_from,report_to,digest) VALUES($1,$2,$3,$4,$5,$6) RETURNING id::text`, rxExternal, patient, in.IssuedAt, in.ReportFrom, in.ReportTo, hex.EncodeToString(hash[:])).Scan(&rx)
		if e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO "pl-prescription-lines"(prescription_id,prescribed_product_id,prescribed_base) VALUES($1,$2,30)`, rx, ids[1]); e != nil {
			return e
		}
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO "pl-feature-seeds" VALUES('a3-v1',$1,$2)`, date, seed)
	return e
}
