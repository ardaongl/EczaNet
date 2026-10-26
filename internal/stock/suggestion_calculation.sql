
 WITH
 sales AS (SELECT location_id,product_id,sum(gross) gross,sum(returned) returned FROM "pl-daily-sales"
 WHERE day>=$2::date-90 AND day<$2::date GROUP BY 1,2),
 skeleton AS (SELECT l.id location_id,p.id product_id,p.pack_size,
 greatest(0,least(90,$2::date-coalesce(h.opened_on,$2::date-90))) days,
 coalesce(s.gross,0) gross,coalesce(s.returned,0) returned,
 coalesce(c.lead_days+c.review_days+c.safety_days,7) horizon,coalesce(c.season,1) season,
 coalesce(c.minimum,0) minimum,c.version IS NULL defaults,h.product_id IS NULL history_unknown,coalesce(c.version,'default-v1') policy_version
 FROM "pl-products" p CROSS JOIN "pl-locations" l LEFT JOIN "pl-product-history" h ON h.product_id=p.id
 LEFT JOIN "pl-suggestion-policy" c ON c.product_id=p.id AND c.location_id=l.id
 LEFT JOIN sales s ON s.product_id=p.id AND s.location_id=l.id WHERE p.active AND l.kind='branch'),
 demand AS (SELECT *,CASE WHEN days=0 THEN 0 ELSE (gross-returned)/days*season END rate,
 CASE WHEN days=0 THEN 0 ELSE (gross-returned)*season*10000 END rate_n,greatest(days,1)*10000 rate_d FROM skeleton),
 lots AS MATERIALIZED (SELECT b.location_id,l.product_id,l.id,l.expires_on,b.quantity
 FROM "pl-stock-balances" b JOIN "pl-lots" l ON l.id=b.lot_id
 WHERE b.account='physical' AND b.quantity>0 AND l.expires_on>=$2::date),
 base_stock AS (SELECT location_id,product_id,sum(quantity) physical,
 sum(CASE WHEN expires_on<=$2::date+30 THEN quantity ELSE 0 END) return_quantity
 FROM lots GROUP BY 1,2),
 capacities AS (SELECT a.*,x.rate_n,x.rate_d,x.rate_n*(a.expires_on-$2::date+1) cap,
 sum(a.quantity*x.rate_d) OVER(PARTITION BY a.location_id,a.product_id ORDER BY a.expires_on,a.id) prefix
 FROM lots a JOIN demand x USING(location_id,product_id)
 WHERE a.expires_on<=$2::date+89 AND a.expires_on-$2::date+1<=x.horizon),
 consumption AS (SELECT a.*,a.prefix-a.quantity*a.rate_d+
 least(0,coalesce(min(a.cap-a.prefix) OVER(PARTITION BY a.location_id,a.product_id ORDER BY a.expires_on,a.id ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING),0)) previous_used FROM capacities a),
 lot_risks AS (SELECT c.*,least(c.quantity*c.rate_d,greatest(0,c.cap-c.previous_used)) allocated FROM consumption c),
 risks AS (SELECT location_id,product_id,
 sum(least(quantity,div(greatest(0,quantity*rate_d-allocated)+rate_d-1,rate_d))) risk
 FROM lot_risks GROUP BY 1,2),
 stock AS (SELECT b.location_id,b.product_id,b.physical,coalesce(r.risk,0) risk,b.return_quantity
 FROM base_stock b LEFT JOIN risks r USING(location_id,product_id)),
 -- Preserve the A4 disjoint pipeline. Known lots are checked separately, never added to physical branch stock.
 stages AS (SELECT p.branch_id location_id,p.product_id,p.expected quantity,e.expected_on,true unknown
 FROM "pl-procurement-pipeline" p LEFT JOIN "pl-procurement-eta" e USING(allocation_id) WHERE p.branch_id IS NOT NULL
 UNION ALL
 SELECT a.location_id,l.product_id,x.quantity-x.shipped,e.expected_on,false FROM "pl-allocation-lots" x
 JOIN "pl-order-allocations" a ON a.id=x.allocation_id JOIN "pl-order-lines" l ON l.id=a.line_id
 JOIN "pl-lots" lot ON lot.id=x.lot_id LEFT JOIN "pl-procurement-eta" e ON e.allocation_id=a.id
 WHERE lot.expires_on>=greatest($2::date,e.expected_on)
 UNION ALL
 SELECT a.location_id,l.product_id,t.quantity-t.good-t.damaged-t.missing,e.expected_on,false FROM "pl-transfer-allocations" t
 JOIN "pl-order-allocations" a ON a.id=t.allocation_id JOIN "pl-order-lines" l ON l.id=a.line_id
 JOIN "pl-lots" lot ON lot.id=t.lot_id LEFT JOIN "pl-procurement-eta" e ON e.allocation_id=a.id
 WHERE lot.expires_on>=greatest($2::date,e.expected_on)),
 pipeline AS (SELECT x.location_id,x.product_id,
 sum(CASE WHEN expected_on BETWEEN $2::date AND $2::date+x.horizon-1 THEN t.quantity ELSE 0 END) pending,
 sum(CASE WHEN expected_on BETWEEN $2::date AND $2::date+x.horizon-1 THEN 0 ELSE t.quantity END) excluded,
 bool_or(t.unknown AND t.quantity>0 AND expected_on BETWEEN $2::date AND $2::date+x.horizon-1) unknown
 FROM stages t JOIN demand x USING(location_id,product_id) GROUP BY 1,2),
 result AS (SELECT x.*,greatest(x.minimum,div(x.rate_n*x.horizon+x.rate_d-1,x.rate_d)) target,coalesce(s.physical,0) physical,
 coalesce(s.risk,0) risk,coalesce(s.physical-s.risk,0) effective,coalesce(t.pending,0) pending,
 coalesce(t.excluded,0) excluded,coalesce(t.unknown,false) unknown,CASE WHEN x.gross=0 THEN coalesce(s.return_quantity,0) ELSE 0 END return_quantity
 FROM demand x LEFT JOIN stock s USING(location_id,product_id) LEFT JOIN pipeline t USING(location_id,product_id)),
 rounded AS (SELECT *,ceil(greatest(0,target-effective-pending)/pack_size) boxes FROM result)
 SELECT location_id,product_id,days,gross,returned,rate,horizon,season,minimum,target,physical,risk,effective,
 pending,excluded,unknown,boxes*pack_size AS suggested,boxes,return_quantity,defaults,history_unknown,policy_version FROM rounded 