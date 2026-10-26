"""One empty isolated buildx builder + fresh A6 volumes; no global cache cleanup."""
import json,os,subprocess,sys,time,uuid
from pathlib import Path
import a6
label=sys.argv[1] if len(sys.argv)>1 else 'cold-install'
if not label.startswith('cold-') or not all(c.isalnum() or c=='-' for c in label):raise SystemExit('invalid evidence label')
out=a6.ROOT/'artifacts/a6-cold'/label
if out.exists():raise SystemExit('new evidence directory required')
out.mkdir(parents=True);a6.command([sys.executable,'scripts/source-manifest.py',str(out/'source-manifest.sha256')]);a6.assert_target();builder='eczanet-a6-cold-'+uuid.uuid4().hex[:8]
# Refuse active verification before the explicitly scoped teardown.
a6.compose('stop','simulator');a6.compose('down','--volumes')
started=time.monotonic();steps=[]
def run(label,args):
 tick=time.monotonic();p=a6.command(args,check=False);(out/(label+'.txt')).write_text(p.stdout+p.stderr,encoding='utf-8');steps.append({'step':label,'seconds':time.monotonic()-tick,'exit':p.returncode})
 if p.returncode:raise RuntimeError(label+' failed')
try:
 run('builder-create',['docker','buildx','create','--name',builder,'--driver','docker-container'])
 run('app-build',['docker','buildx','build','--builder',builder,'--no-cache','--progress','plain','--load','-t','eczanet-a6-app:local','.'])
 run('web-build',['docker','buildx','build','--builder',builder,'--no-cache','--progress','plain','--load','-t','eczanet-a6-web:local','./web'])
 os.environ['A6_SIMULATOR_ENABLED']='false'
 run('ready',a6.COMPOSE+['up','-d','--wait','--wait-timeout','300'])
 elapsed=time.monotonic()-started
 result={'seconds':elapsed,'under_300_seconds':elapsed<300,'builder':builder,'empty_builder':True,'fresh_A6_volumes':True,'base_layers':'fresh builder downloads registry layers; Docker Desktop engine already has unrelated images','network':'online; actual registry/download steps included in logs','steps':steps}
 (out/'result.json').write_text(json.dumps(result,indent=2));print(json.dumps(result))
finally:
 # Deletes only this run's builder and its own cache, never global engine cache.
 p=a6.command(['docker','buildx','rm',builder],check=False);(out/'builder-cleanup.txt').write_text(p.stdout+p.stderr)
