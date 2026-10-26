CREATE TEMP TABLE "pl-a3-checks"(name text,violations bigint);
INSERT INTO "pl-a3-checks"
 SELECT 'rx_fulfilled_vs_dispensed',count(*) FROM "pl-prescription-lines" l WHERE l.fulfilled_base<>(SELECT coalesce(sum(d.quantity),0) FROM "pl-dispensations" d WHERE d.prescription_line_id=l.id)
 UNION ALL
 SELECT 'rx_owner_vs_sales',count(*) FROM "pl-sales" s JOIN "pl-prescriptions" r ON r.id=s.prescription_id WHERE r.owner_id IS DISTINCT FROM s.location_id
 UNION ALL
 SELECT 'dispensation_sale_rx_quantity',count(*) FROM "pl-sale-lines" l JOIN "pl-sales" s ON s.id=l.sale_id WHERE s.prescription_id IS NOT NULL AND (l.quantity<>(SELECT coalesce(sum(d.quantity),0) FROM "pl-dispensations" d WHERE d.sale_line_id=l.id) OR EXISTS(SELECT 1 FROM "pl-dispensations" d JOIN "pl-prescription-lines" r ON r.id=d.prescription_line_id WHERE d.sale_line_id=l.id AND r.prescription_id<>s.prescription_id))
 UNION ALL
 SELECT 'counter_vs_controlled_ledger',count(*) FROM "pl-daily-controls" c WHERE c.consumed<>(SELECT coalesce(sum(l.quantity),0) FROM "pl-controlled-ledger" l WHERE (l.patient_id,l.group_name,l.business_day)=(c.patient_id,c.group_name,c.business_day)) OR c.consumed>10
 UNION ALL
 SELECT 'ledger_missing_counter',count(*) FROM "pl-controlled-ledger" l WHERE NOT EXISTS(SELECT 1 FROM "pl-daily-controls" c WHERE (c.patient_id,c.group_name,c.business_day)=(l.patient_id,l.group_name,l.business_day))
 UNION ALL
 SELECT 'ledger_allocation_time_scope',count(*) FROM "pl-controlled-ledger" c JOIN "pl-sale-lines" l ON l.id=c.sale_line_id JOIN "pl-sales" s ON s.id=l.sale_id JOIN "pl-prescriptions" r ON r.id=s.prescription_id WHERE c.patient_id<>r.patient_id OR c.prescription_id<>r.id OR c.actor_id<>s.actor_id OR c.location_id<>s.location_id OR c.effective_at<>s.effective_at OR c.business_day<>(s.effective_at AT TIME ZONE 'Europe/Istanbul')::date OR c.product_id<>l.product_id OR NOT EXISTS(SELECT 1 FROM "pl-sale-allocations" a WHERE a.line_id=l.id AND a.lot_id=c.lot_id AND a.quantity=c.quantity)
 UNION ALL
 SELECT 'controlled_sale_vs_ledger',count(*) FROM "pl-sale-lines" l JOIN "pl-products" p ON p.id=l.product_id WHERE p.controlled_group<>'' AND l.quantity<>(SELECT coalesce(sum(quantity),0) FROM "pl-controlled-ledger" WHERE sale_line_id=l.id)
 UNION ALL
 SELECT 'sale_line_total_and_shares',count(*) FROM "pl-sales" s WHERE s.total<>(SELECT coalesce(sum(l.total),0) FROM "pl-sale-lines" l WHERE l.sale_id=s.id) OR coalesce(patient_total,total)+coalesce(institution_total,0)<>total
 UNION ALL
 SELECT 'snapshot_matches_persisted_line',count(*) FROM "pl-sale-lines" l WHERE l.snapshot IS NOT NULL AND ((l.snapshot->>'total')::bigint<>l.total OR (l.snapshot->>'pack_price')::bigint<>l.pack_price OR (l.snapshot->>'pack_size')::bigint<>l.pack_size OR (l.snapshot->>'quantity')::bigint<>l.quantity OR l.snapshot->>'policy'<>l.policy)
 UNION ALL
 SELECT 'rx_sale_time_window',count(*) FROM "pl-sales" s JOIN "pl-prescriptions" r ON r.id=s.prescription_id WHERE s.effective_at<r.issued_at OR s.effective_at>=r.issued_at+interval '168 hours'
 UNION ALL
 SELECT 'new_sale_price_and_lot_time',count(*) FROM "pl-sale-lines" l JOIN "pl-sales" s ON s.id=l.sale_id JOIN "pl-price-periods" p ON p.id=l.price_id WHERE l.snapshot IS NOT NULL AND (s.effective_at<p.valid_from OR s.effective_at>=p.valid_to OR EXISTS(SELECT 1 FROM "pl-sale-allocations" a JOIN "pl-lots" lot ON lot.id=a.lot_id WHERE a.line_id=l.id AND lot.expires_on<(s.effective_at AT TIME ZONE 'Europe/Istanbul')::date));
TABLE "pl-a3-checks";
DO $$BEGIN
 IF EXISTS(SELECT 1 FROM "pl-a3-checks" WHERE violations<>0) THEN RAISE EXCEPTION 'A3 reconciliation failed'; END IF;
END$$;