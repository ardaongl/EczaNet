"""Six complete calculations through the isolated LOCAL benchmark API.
Credentials stay in memory; evidence contains only aggregate timing/run metadata.
Run after benchmark-accounts.py and compose.a5-bench.yaml: python scripts/a5-api-benchmark.py UNIQUE_LABEL
"""
import argparse
import http.cookiejar
import json
from pathlib import Path
import re
import sys
import time
import urllib.error
import urllib.request
import uuid

root=Path(__file__).resolve().parents[1]
parser=argparse.ArgumentParser();parser.add_argument('label');parser.add_argument('--output-root',default='artifacts/a5-benchmark');parser.add_argument('--cache-description',default='Not reset by this script; caller must record process/cache state');args=parser.parse_args()
out=root/args.output_root/args.label
out.mkdir(parents=True,exist_ok=False)
password=re.search(r'^DEMO_PASSWORD=(.*)$',(root/'.env').read_text(),re.M).group(1).strip().strip('"').strip("'")
client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
base='http://localhost:8085/api/v1'
def request(path,body=None,csrf=''):
    req=urllib.request.Request(base+path,data=json.dumps(body).encode() if body is not None else None,headers={'Content-Type':'application/json','Origin':'http://localhost:8080','X-CSRF-Token':csrf,'Idempotency-Key':str(uuid.uuid4())})
    try:
        with client.open(req,timeout=30) as response:
            return response.status,({} if response.status==204 else json.load(response)),dict(response.headers)
    except urllib.error.HTTPError as error:
        return error.code,json.load(error),dict(error.headers)
status,prep,_=request('/auth/csrf')
assert status==200,'CSRF preparation failed'
status,login,_=request('/auth/login',{'email':'depo@ddo-case.invalid','password':password},prep['csrf_token'])
assert status==200,'Local benchmark login failed'
csrf=login['csrf_token']
results=[]
try:
    for i in range(6):
        started=time.perf_counter()
        status,body,headers=request('/suggestion-runs',{'location_id':'21','as_of':'2026-10-25'},csrf)
        elapsed=(time.perf_counter()-started)*1000
        row={'iteration':i+1,'status':status,'api_ms':elapsed,**{k:body[k] for k in ['id','rows','as_of','version','summary_ms','preparation_ms','scope_ms','summary_rebuilt','calculation_ms','published'] if k in body}}
        if status!=201:row['error']=body.get('error')
        match=re.search(r'commit;dur=([0-9.]+)',headers.get('Server-Timing',''))
        row['commit_ms']=float(match.group(1)) if match else None
        row['calculation_commit_ms']=body['calculation_ms']+row['commit_ms'] if 'calculation_ms' in body and row['commit_ms'] is not None else None
        row['calculation_finalization_commit_upper_ms']=elapsed-body['preparation_ms'] if 'preparation_ms' in body else None
        row['passed']=status==201 and body.get('rows')=='100000' and row['calculation_commit_ms'] is not None and row['calculation_commit_ms']<5000 and row['calculation_finalization_commit_upper_ms'] is not None and row['calculation_finalization_commit_upper_ms']<5000
        results.append(row)
        (out/'results.json').write_text(json.dumps({'cache_description':args.cache_description,'runs':results,'all_passed':all(r['passed'] for r in results)},indent=2))
        print(row,flush=True)
finally:
    request('/auth/logout',{},csrf)
raise SystemExit(0 if len(results)==6 and all(r['passed'] for r in results) else 1)
