"""One actual full INSERT with EXPLAIN ANALYZE/BUFFERS/WAL; rolled back.
Diagnostic instrumentation is not performance acceptance or prewarming.
The first query profile is retained with its actual FK and IO costs.
"""
import json
import re
from pathlib import Path
import subprocess
import sys
import time

root=Path(__file__).resolve().parents[1]
out=root/'artifacts/a5-profile'/sys.argv[1]
out.mkdir(parents=True,exist_ok=False)
body=(root/'internal/stock/suggestion_calculation.sql').read_text().replace('$2::date',"DATE '2026-10-25'")
columns='run_id,location_id,product_id,observed_days,gross,returned,rate,horizon,season,minimum,target,physical,risk,effective,pending,excluded_pending,unknown_lot,suggested,boxes,return_quantity,defaults,history_unknown,policy_version'
query='''BEGIN ISOLATION LEVEL REPEATABLE READ;
SET LOCAL track_io_timing=on;
SET LOCAL ROLE eczanet_runtime;
SET LOCAL jit=off; SET LOCAL work_mem='64MB';
INSERT INTO "pl-suggestion-runs"(as_of,source,summary_source,summary_ms) VALUES('2026-10-25','diagnostic','diagnostic',0) RETURNING id \gset
SELECT clock_timestamp() scope_started \gset
INSERT INTO "pl-suggestion-run-products" SELECT :id,id FROM "pl-products" WHERE active;
INSERT INTO "pl-suggestion-run-locations" SELECT :id,id FROM "pl-locations" WHERE kind='branch';
SELECT extract(epoch FROM clock_timestamp()-:'scope_started'::timestamptz)*1000 scope_ms \gset
EXPLAIN(ANALYZE,BUFFERS,WAL,SETTINGS,FORMAT JSON) INSERT INTO "pl-order-suggestions"('''+columns+''') SELECT :id,x.* FROM ('''+body+''') x ORDER BY x.product_id,x.location_id;
\echo SCOPE_MS :scope_ms
ROLLBACK;'''
started=time.perf_counter()
command=['docker','compose','exec','-T','db','psql','-X','-qAt','-v','ON_ERROR_STOP=1','-U','eczanet_owner','-d','eczanet_a5_bench17']
if '--runtime' in sys.argv:
    query=query.replace('SET LOCAL track_io_timing=on;','').replace('SET LOCAL ROLE eczanet_runtime;','')
    if '--api-prefix' in sys.argv:
        query=query.replace("SET LOCAL jit=off;", "SET LOCAL lock_timeout='2s'; SET LOCAL statement_timeout='5s'; SET LOCAL jit=off;")
        query=query.replace("'diagnostic','diagnostic',0", '"pl-suggestion-source"(),"pl-sales-source"(),0')
    command=['docker','compose','exec','-T','db','sh','-c','PGPASSWORD="$RUNTIME_PASSWORD" psql -h 127.0.0.1 -X -qAt -v ON_ERROR_STOP=1 -U eczanet_runtime -d eczanet_a5_bench17']
p=subprocess.run(command,input=query,text=True,capture_output=True)
(out/'stdout.txt').write_text(p.stdout)
(out/'stderr.txt').write_text(p.stderr)
assert p.returncode==0,p.stderr
decoded,_=json.JSONDecoder().raw_decode(p.stdout.lstrip());(out/'plan.json').write_text(json.dumps(decoded,indent=2));plan=decoded[0]
node=plan['Plan'];child=node['Plans'][0]
scope_ms=float(re.search(r'SCOPE_MS ([0-9.]+)',p.stdout).group(1))
summary={'scope_ms':scope_ms,'scope_plus_insert_ms':scope_ms+plan['Execution Time'],'rolled_back':True,'instrumented':True,'execution_ms':plan['Execution Time'],'query_ms':child['Actual Total Time'],'heap_index_insert_ms':node['Actual Total Time']-child['Actual Total Time'],'foreign_keys':plan.get('Triggers',[]),'buffers':{k:v for k,v in node.items() if any(x in k for x in ['Block','WAL','I/O'])},'docker_total_ms':(time.perf_counter()-started)*1000}
(out/'summary.json').write_text(json.dumps(summary,indent=2))
print(json.dumps(summary,indent=2))
