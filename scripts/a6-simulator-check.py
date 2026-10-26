"""Restart and lost-progress replay fault, restricted to A6 simulator-state volume."""
import argparse,json,os,time
from pathlib import Path
import a6
parser=argparse.ArgumentParser();parser.add_argument('--out',default='artifacts/a6-simulator');args=parser.parse_args();out=(a6.ROOT/args.out).resolve()
if out.exists():raise SystemExit('new evidence path required')
out.mkdir(parents=True);a6.assert_target();os.environ['A6_SIMULATOR_ENABLED']='true';os.environ['SIMULATOR_SPEED']='100'
a6.compose('up','-d','--no-deps','simulator');a=a6.API()
def status():
 code,m,_=a.call('GET','/dashboard');assert code==200;return m['simulator']
try:
 start=time.monotonic()
 for _ in range(120):
  m=status();p=m.get('progress',{})
  if p.get('next',0)>=12:break
  time.sleep(.1)
 else:raise RuntimeError('12 deterministic events not completed')
 a6.compose('stop','simulator')
 raw=a6.compose('run','--rm','--no-deps','--entrypoint','cat','simulator','/state/progress.json').stdout
 state=json.loads(raw);assert not state.get('pending_sale'), 'stop left a pending sale; inspect restart behavior'
 n=state['next'];last=state['last_event'];assert n>=12 and state['expected_rejections']>=6 and state['unexpected_errors']==0
 key=last['event_id']+'-sale';sale_id=last['sale_id'];assert str(sale_id).isdigit()
 # Model the durable business commit succeeding just before progress completion
 # is persisted. Restore only the disposable progress file, never business data.
 body=json.loads(a6.sql("SELECT new_value->'input' FROM \"pl-audit-events\" WHERE actor_id=7 AND command='sale.create' AND object_id='"+sale_id+"' ORDER BY id DESC LIMIT 1"))
 state['next']=n-1;state['pending_sale']=body;state['pending_at']=os.environ.get('DEMO_DATE','2026-10-24')+'T09:00:00Z'
 a6.compose('run','--rm','--no-deps','--entrypoint','/bin/sh','simulator','-c','cat > /state/progress.json',input=json.dumps(state))
 os.environ['SIMULATOR_SPEED']='1';a6.compose('up','-d','--no-deps','simulator')
 for _ in range(60):
  m=status();p=m.get('progress',{})
  if p.get('next')==n and p.get('last_event',{}).get('kind')=='replayed-sale':break
  time.sleep(.1)
 else:raise RuntimeError('pending same-key replay did not complete')
 assert p['last_event']['sale_id']==sale_id and p['unexpected_errors']==0
 checks=json.loads(a6.sql("SELECT json_build_object('key_rows',(SELECT count(*) FROM \"pl-idempotency\" WHERE actor_id=7 AND key='"+key+"'),'sales',(SELECT count(*) FROM \"pl-sales\" WHERE actor_id=7),'sale_events',(SELECT count(*) FROM \"pl-audit-events\" WHERE actor_id=7 AND command='sale.create'),'rx_duplicates',(SELECT count(*) FROM (SELECT external_id FROM \"pl-prescriptions\" WHERE external_id LIKE 'sim-%' GROUP BY external_id HAVING count(*)>1)x))"))
 assert checks['key_rows']==1 and checks['sales']==checks['sale_events']==n and checks['rx_duplicates']==0
 result={'speed_test':100,'normal_interval_seconds':28.8,'completed_events':n,'seconds_accelerated':time.monotonic()-start,'same_key_replay_sale_id':sale_id,'last_status':m,'checks':checks,'fault':'lost progress file completion after durable API commit; only isolated state file restored','business_data_rewritten':False}
 (out/'result.json').write_text(json.dumps(result,indent=2));print('simulator API events/rejections/restart/lost-progress replay PASS',n)
finally:
 # Continue the demo at normal speed, even if a test assertion needs attention.
 os.environ['A6_SIMULATOR_ENABLED']='true';os.environ['SIMULATOR_SPEED']='1';a6.compose('up','-d','--no-deps','simulator');a.close()
