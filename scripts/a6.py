"""A6 operations: destructive commands accept only the fixed isolated project.
No main-project mutation, generic volume deletion, prune, or secret output.
"""
import argparse,concurrent.futures,hashlib,http.cookiejar,json,os,platform,statistics,subprocess,sys,time,urllib.request,urllib.error,uuid
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
COMPOSE=['docker','compose','-p','eczanet-a6','-f','compose.yaml','-f','tests/compose.a6.yaml']
BASE='http://localhost:18086'
def command(args,check=True,input=None):
 p=subprocess.run(args,cwd=ROOT,input=input,text=True,capture_output=True)
 if check and p.returncode:raise RuntimeError('command failed: '+args[0]+' (exit '+str(p.returncode)+')')
 return p
def compose(*args,**kw):return command(COMPOSE+list(args),**kw)
def sql(q):return compose('exec','-T','db','psql','-X','-v','ON_ERROR_STOP=1','-At','-U','eczanet_owner','-d','eczanet_demo',input=q).stdout.strip()
def assert_target():
 # No user-supplied path/name is interpolated into a destructive command.
 p=compose('ps','-q','db');cid=p.stdout.strip()
 if cid:
  labels=json.loads(command(['docker','inspect','--format','{{json .Config.Labels}}',cid]).stdout)
  if labels.get('com.docker.compose.project')!='eczanet-a6':raise RuntimeError('isolated identity mismatch')
 for volume in ['eczanet-a6_db-data','eczanet-a6_simulator-state']:
  p=command(['docker','volume','inspect',volume],check=False)
  if p.returncode==0:
   v=json.loads(p.stdout)[0]
   if v['Name']!=volume or v.get('Labels',{}).get('com.docker.compose.project')!='eczanet-a6':raise RuntimeError('volume label mismatch')
 active=command(['docker','ps','-q','--filter','label=com.docker.compose.project=eczanet-a6','--filter','label=com.docker.compose.service=verify']).stdout.strip()
 if active:raise RuntimeError('active verify: reset refused')
def reset(out):
 assert_target();started=time.monotonic()
 cid=compose('ps','-q','simulator').stdout.strip()
 if cid:
  values=json.loads(command(['docker','inspect','--format','{{json .Config.Env}}',cid]).stdout)
  config=dict(v.split('=',1) for v in values if '=' in v)
  os.environ['A6_SIMULATOR_ENABLED']=config.get('SIMULATOR_ENABLED','false')
  os.environ['SIMULATOR_SPEED']=config.get('SIMULATOR_SPEED','1')
 # Scope is fixed and audited above. This removes only the two isolated volumes.
 compose('stop','simulator');compose('down','--volumes')
 p=compose('up','-d','--wait','--wait-timeout','300',check=False)
 (out/'reset.txt').write_text(p.stdout+p.stderr,encoding='utf-8')
 result={'exit':p.returncode,'ready_seconds':time.monotonic()-started,'project':'eczanet-a6','cache':'existing images; reset is NOT cold build'}
 (out/'reset.json').write_text(json.dumps(result,indent=2));print(json.dumps(result))
 if p.returncode:raise RuntimeError('reset startup failed; inspect reset.txt')
def checksum(out):
 tables=json.loads(sql("SELECT json_agg(tablename ORDER BY tablename) FROM pg_tables WHERE schemaname='public' AND tablename LIKE 'pl-%' AND tablename NOT IN ('pl-goose-db-version','pl-sessions');"))
 data={}
 for t in tables:
  # Exclude security random material and wall-clock bookkeeping only.
  q=f"""SELECT md5(coalesce(string_agg(h,',' ORDER BY h),'')) FROM (SELECT md5((to_jsonb(t)-ARRAY['password_hash','created_at','refreshed_at'])::text) h FROM \"{t}\" t) x;"""
  data[t]=sql(q)
 result={'tables':data,'logical_checksum':hashlib.sha256(json.dumps(data,sort_keys=True).encode()).hexdigest(),'excluded_fields':['password_hash','created_at','refreshed_at'],'project':'eczanet-a6'}
 (out/'checksum.json').write_text(json.dumps(result,indent=2));print('logical_checksum',result['logical_checksum'])
def verify(out):
 assert_target();compose('stop','simulator');started=time.monotonic()
 try:
  p=compose('--profile','verify','run','--rm','--no-deps','verify',check=False)
  (out/'verify.txt').write_text(p.stdout+p.stderr,encoding='utf-8')
  result={'exit':p.returncode,'seconds':time.monotonic()-started,'project':'eczanet-a6','pass_lines':[l for l in p.stdout.splitlines() if l.startswith('PASS')],'fail_lines':[l for l in p.stdout.splitlines() if l.startswith(('FAIL','EKSIK'))]}
  (out/'verify.json').write_text(json.dumps(result,indent=2));print(json.dumps(result,ensure_ascii=False))
  if p.returncode:raise RuntimeError('verify failed; original stdout retained')
 finally:compose('start','simulator')
class API:
 def __init__(self,account='seed.yildiz'):
  self.jar=http.cookiejar.CookieJar();self.opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.jar));self.csrf=''
  self.csrf=self.call('GET','/auth/csrf')[1]['csrf_token']
  # Read only; never log .env or credentials.
  password=os.environ.get('DEMO_PASSWORD')
  if not password:
   password=next(l.split('=',1)[1] for l in (ROOT/'.env').read_text().splitlines() if l.startswith('DEMO_PASSWORD='))
  status,m,_=self.call('POST','/auth/login',{'email':account+'@ddo-case.invalid','password':password});assert status==200
  self.csrf=m['csrf_token']
 def call(self,method,path,body=None,key=None):
  url=BASE+('/api/v1'+path if not path.startswith('/health') else path)
  req=urllib.request.Request(url,data=json.dumps(body).encode() if body is not None else None,method=method,headers={'Content-Type':'application/json','Origin':'http://localhost:8080','X-CSRF-Token':self.csrf,'Idempotency-Key':key or 'a6-'+uuid.uuid4().hex})
  started=time.monotonic()
  try:r=self.opener.open(req,timeout=20)
  except urllib.error.HTTPError as e:r=e
  with r:
   raw=r.read();data=json.loads(raw) if raw else {};return r.status,data,(time.monotonic()-started)*1000
 def close(self):self.call('POST','/auth/logout')
def normal_load(out):
 started=time.monotonic();samples=[]
 def worker(vu):
  a=API();rows=[]
  try:
   n=-1
   while time.monotonic()-started<30:
    n+=1
    if n%5==0:
     product=str(6+vu);body={'location_id':'1','items':[{'product_id':product,'quantity':'1','unit':'unit'}]}
     status,q,ms=a.call('POST','/sales/quote',body);rows.append((status,ms,'quote'))
     if status==200:
      body['quote']=q['quote'];status,_,ms=a.call('POST','/sales',body);rows.append((status,ms,'sale'))
    else:
     path=['/products?q=DEMO','/inventory?location_id=1','/sales?location_id=1','/auth/me'][n%4]
     status,_,ms=a.call('GET',path);rows.append((status,ms,'read'))
  finally:a.close()
  return rows
 with concurrent.futures.ThreadPoolExecutor(max_workers=5) as pool:
  for rows in pool.map(worker,range(5)):samples+=rows
 lat=sorted(r[1] for r in samples);expected=sum(1 for s,_,_ in samples if s==409);unexpected=sum(1 for s,_,_ in samples if s not in (200,201,409))
 result={'scenario':'normal mixed read+quote+sale','VU':5,'seconds':time.monotonic()-started,'requests':len(samples),'p50_ms':lat[int(.5*(len(lat)-1))],'p95_ms':lat[int(.95*(len(lat)-1))],'p99_ms':lat[int(.99*(len(lat)-1))],'expected_409':expected,'unexpected':unexpected,'5xx':sum(s>=500 for s,_,_ in samples),'unexpected_rate':unexpected/len(samples),'host':platform.platform(),'raw':samples}
 (out/'load.json').write_text(json.dumps(result,indent=2));print(json.dumps({k:v for k,v in result.items() if k!='raw'}));assert result['unexpected_rate']<.01

def explain(out):
 # Transactional DROP affects only the dedicated copy; ROLLBACK restores index.
 queries=[('sales','pl-sales_location_id_id_idx','SELECT id,total FROM "pl-sales" WHERE location_id=1 AND id>260000 ORDER BY id LIMIT 50'),('ledger','pl-stock-entries_location_id_lot_id_account_id_idx',"SELECT id,quantity FROM \"pl-stock-entries\" WHERE location_id=1 AND lot_id=6 AND account='physical' ORDER BY id")]
 # Resolve actual generated index names from catalog rather than assume truncation.
 queries=[('sales',sql("SELECT indexname FROM pg_indexes WHERE tablename='pl-sales' AND indexdef LIKE '%(location_id, id)%' LIMIT 1"),queries[0][2]),('ledger',sql("SELECT indexname FROM pg_indexes WHERE tablename='pl-stock-entries' AND indexdef LIKE '%(location_id, lot_id, account, id)%' LIMIT 1"),queries[1][2])]
 for label,index,q in queries:
  assert index and all(c.isalnum() or c in '_-' for c in index)
  outputs=[]
  for repeat in range(3):
   for indexed in [True,False]:
    raw=sql('BEGIN; '+('' if indexed else 'DROP INDEX "'+index+'"; ')+'EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) '+q+'; ROLLBACK;')
    plan=json.loads(raw[raw.index('['):raw.rindex(']')+1])[0]
    outputs.append({'repeat':repeat+1,'indexed':indexed,'index':index,'query':q,'plan':plan})
  (out/(label+'-plans.json')).write_text(json.dumps(outputs,indent=2))
 print('two query pairs x3; optional indexes restored by rollback')
def chaos(out):
 assert_target();compose('stop','simulator');a=API();body={'location_id':'1','items':[{'product_id':'6','quantity':'1','unit':'unit'}]};status,q,_=a.call('POST','/sales/quote',body);assert status==200;body['quote']=q['quote'];key='chaos-'+uuid.uuid4().hex
 first=None;blocked_pid=None;blocker=None
 try:
  # Hold only this known fixture's guard, then observe the HTTP transaction waiting
  # on this exact blocker before stopping the isolated DB.
  blocker=subprocess.Popen(COMPOSE+['exec','-T','-e','PGAPPNAME=a6-chaos-blocker','db','psql','-X','-v','ON_ERROR_STOP=1','-U','eczanet_owner','-d','eczanet_demo'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,cwd=ROOT)
  blocker.stdin.write('BEGIN; SELECT 1 FROM "pl-inventory-guards" WHERE location_id=1 AND product_id=6 FOR UPDATE; SELECT pg_sleep(30); ROLLBACK;');blocker.stdin.close()
  for _ in range(40):
   blocker_pid=sql("SELECT pid FROM pg_stat_activity WHERE application_name='a6-chaos-blocker' AND wait_event='PgSleep'")
   if blocker_pid:break
   time.sleep(.05)
  assert blocker_pid,'guard blocker not observed'
  with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
   pending=pool.submit(a.call,'POST','/sales',body,key)
   for _ in range(35):
    blocked_pid=sql('SELECT pid FROM pg_stat_activity WHERE '+blocker_pid+'=ANY(pg_blocking_pids(pid))')
    if blocked_pid:break
    time.sleep(.025)
   assert blocked_pid,'HTTP transaction did not reach known guard'
   compose('stop','-t','1','db');first=pending.result()
  status,_,ms=a.call('POST','/sales',body,key);assert status==503 and ms<16000
  try:urllib.request.urlopen(BASE+'/health',timeout=3);raise AssertionError('DB down health200')
  except urllib.error.HTTPError as e:assert e.code==503
 finally:
  compose('start','db')
  if blocker:
   try:blocker.wait(timeout=5)
   except subprocess.TimeoutExpired:blocker.terminate()
  for _ in range(60):
   try:
    if urllib.request.urlopen(BASE+'/health',timeout=2).status==200:break
   except Exception:time.sleep(.5)
  else:raise RuntimeError('DB recovery failed')
  compose('start','simulator')
 second=a.call('POST','/sales',body,key);third=a.call('POST','/sales',body,key);assert second[0]==third[0]==201 and second[1]==third[1]
 count=int(sql("SELECT count(*) FROM \"pl-idempotency\" WHERE key='"+key+"' AND status=201"));assert count==1
 for name in ['a3','a4']:
  output=sql((ROOT/('tests/'+name+'-reconcile.sql')).read_text())
  (out/(name+'-reconcile.txt')).write_text(output)
 (out/'chaos.json').write_text(json.dumps({'first_status':first[0],'known_blocker_pid':blocker_pid,'blocked_HTTP_pid':blocked_pid,'down_status':status,'down_ms':ms,'recovered_status':second[0],'same_body':second[1]==third[1],'idempotency_rows':count,'sale_id':second[1]['id'],'key':key},indent=2));a.close();print('chaos: observed blocked HTTP/bounded503/recovery/same-key one result/general SQL PASS')

def main():
 parser=argparse.ArgumentParser();parser.add_argument('action',choices=['reset','verify','checksum','load','explain','chaos']);parser.add_argument('--project',required=True);parser.add_argument('--out',required=True);args=parser.parse_args()
 if args.project!='eczanet-a6':parser.error('only isolated eczanet-a6 target is allowed')
 out=Path(args.out).resolve()
 if ROOT.resolve() not in out.parents:parser.error('evidence must stay in workspace')
 out.mkdir(parents=True,exist_ok=True)
 if any(out.iterdir()):parser.error('new empty evidence directory required')
 globals()[{'load':'normal_load'}.get(args.action,args.action)](out)
if __name__=='__main__':main()
