"""Isolated, retained A5 benchmark database. Never truncates or changes the main DB.
Run: python scripts/a5-benchmark.py [--measure-only] [--restart-db]
The optional restart is only PostgreSQL process restart (OS page cache remains warm).
"""
import json, subprocess, time, sys, hashlib
from pathlib import Path
root=Path(__file__).resolve().parents[1]
out=root/next((a.split('=',1)[1] for a in sys.argv if a.startswith('--output-root=')), 'artifacts/a5-benchmark')
out.mkdir(exist_ok=True,parents=True)
database='eczanet_a5_bench17'
for arg in sys.argv[1:]:
    if arg.startswith('--label='):
        out=out/arg.split('=',1)[1]
        out.mkdir(exist_ok=False,parents=True)
def sql(query,db=database):
    p=subprocess.run(['docker','compose','exec','-T','db','psql','-X','-qAt','-v','ON_ERROR_STOP=1','-U','eczanet_owner','-d',db],input=query,text=True,capture_output=True,cwd=root)
    if p.returncode:
        (out/'failed-sql-output.txt').write_text(p.stdout+'\n'+p.stderr)
        raise RuntimeError(p.stderr)
    return p.stdout.strip()
if '--measure-only' not in sys.argv:
    exists=sql("SELECT count(*) FROM pg_database WHERE datname='"+database+"'",'eczanet_demo')
    if exists!='0': raise SystemExit('Database already exists; use --measure-only. Never overwriting retained benchmark.')
    sql('CREATE DATABASE '+database,'eczanet_demo')
    start=time.perf_counter()
    for p in sorted((root/'migrations').glob('*.sql')):
        sql(p.read_text(encoding='utf-8').split('-- +goose Down')[0])
    (out/'benchmark-migrations.json').write_text(json.dumps({'ms':(time.perf_counter()-start)*1000,'database':database}))
    start=time.perf_counter()
    sql((root/'tests/a5-benchmark-seed.sql').read_text())
    (out/'benchmark-preparation.json').write_text(json.dumps({'fixture_ms':(time.perf_counter()-start)*1000,'database':database},indent=2))
source={str(p.relative_to(root)):hashlib.sha256(p.read_bytes()).hexdigest() for p in [root/'migrations/00013_suggestions.sql',root/'internal/stock/suggestions.go',root/'tests/a5-benchmark-seed.sql',root/'internal/stock/suggestion_calculation.sql',root/'internal/stock/suggestion_read.go']}
counts=json.loads(sql('''SELECT json_build_object('products',(SELECT count(*) FROM "pl-products" WHERE active),'branches',(SELECT count(*) FROM "pl-locations" WHERE kind='branch'),'sales',(SELECT count(*) FROM "pl-sales"),'sale_lines',(SELECT count(*) FROM "pl-sale-lines"),'allocations',(SELECT count(*) FROM "pl-sale-allocations"),'lots',(SELECT count(*) FROM "pl-lots"),'balances',(SELECT count(*) FROM "pl-stock-balances"),'days',(SELECT count(DISTINCT (effective_at AT TIME ZONE 'Europe/Istanbul')::date) FROM "pl-sales"))'''))
assert counts['products']==5000 and counts['branches']==20 and counts['days']==90,counts
analyze_started=time.perf_counter();sql('ANALYZE;');analyze_ms=(time.perf_counter()-analyze_started)*1000
if '--restart-db' in sys.argv:
    subprocess.run(['docker','compose','restart','db'],cwd=root,check=True,capture_output=True)
    for _ in range(30):
        try: sql('SELECT 1');break
        except RuntimeError: time.sleep(1)
summary_start=time.perf_counter();sql('BEGIN ISOLATION LEVEL REPEATABLE READ; SELECT "pl-refresh-daily-sales"(); COMMIT;');summary_ms=(time.perf_counter()-summary_start)*1000
insert='''INSERT INTO "pl-order-suggestions"(run_id,location_id,product_id,observed_days,gross,returned,rate,horizon,season,minimum,target,physical,risk,effective,pending,excluded_pending,unknown_lot,suggested,boxes,return_quantity,defaults,history_unknown,policy_version) SELECT RUNID,x.* FROM ('''+(root/'internal/stock/suggestion_calculation.sql').read_text().replace('$2::date',"date '2026-10-25'")+''') x ORDER BY x.product_id,x.location_id'''
results=[]
for i in range(6):
    query='''BEGIN ISOLATION LEVEL REPEATABLE READ;
SET LOCAL ROLE eczanet_runtime;
SET LOCAL jit=off; SET LOCAL work_mem='64MB';
INSERT INTO "pl-suggestion-runs"(as_of,source,summary_source,summary_ms) VALUES('2026-10-25',"pl-suggestion-source"(),"pl-sales-source"(),0) RETURNING id \gset
SELECT clock_timestamp() started \gset
INSERT INTO "pl-suggestion-run-products"(run_id,product_id) SELECT :id,id FROM "pl-products" WHERE active;
INSERT INTO "pl-suggestion-run-locations" SELECT :id,id FROM "pl-locations" WHERE kind='branch';
'''+insert.replace('RUNID',':id')+''';
UPDATE "pl-suggestion-runs" SET published=true,row_count=(SELECT count(*) FROM "pl-order-suggestions" WHERE run_id=:id),calculation_ms=extract(epoch FROM clock_timestamp()-:'started'::timestamptz)*1000 WHERE id=:id;
SELECT json_build_object('run',id,'rows',row_count,'calculation_ms',calculation_ms) FROM "pl-suggestion-runs" WHERE id=:id;
\\timing on
COMMIT;'''
    t=time.perf_counter();raw=sql(query);data=json.loads(next(line for line in raw.splitlines() if line.startswith('{')));import re;data['commit_ms']=float(re.search(r'Time: ([0-9.]+) ms',raw).group(1));data['calculation_commit_ms']=data['calculation_ms']+data['commit_ms'];data['client_transaction_ms']=(time.perf_counter()-t)*1000;data['kind']='first_process_cold_os_cache_retained' if i==0 and '--restart-db' in sys.argv else 'first_after_fixture_os_cache_warm' if i==0 else 'warm';results.append(data)
    (out/'benchmark-results.json').write_text(json.dumps({'database':database,'counts':counts,'analyze_client_ms':analyze_ms,'summary_client_ms':summary_ms,'runs':results,'source':source,'all_under_5s':all(r['calculation_commit_ms']<5000 and r['rows']==100000 for r in results)},indent=2))
    print(data,flush=True)
plan=sql("BEGIN; SET LOCAL jit=off; SET LOCAL work_mem='64MB'; EXPLAIN(ANALYZE,BUFFERS,SETTINGS,FORMAT JSON) "+insert.replace('RUNID',str(results[-1]['run'])) .replace('INSERT INTO','INSERT INTO',1)+' ON CONFLICT(run_id,location_id,product_id) DO NOTHING; ROLLBACK;')
(out/'benchmark-explain.json').write_text(plan)
environment=subprocess.run(['docker','info','--format','{{json .}}'],text=True,capture_output=True,check=True)
info=json.loads(environment.stdout)
(out/'benchmark-environment.json').write_text(json.dumps({k:info.get(k) for k in ['NCPU','MemTotal','Architecture','OperatingSystem','KernelVersion','ServerVersion']},indent=2))
print('All six full calculation + writes under 5s:',all(r['calculation_commit_ms']<5000 for r in results))

raise SystemExit(0 if len(results)==6 and all(r["calculation_commit_ms"]<5000 and r["rows"]==100000 for r in results) else 1)
