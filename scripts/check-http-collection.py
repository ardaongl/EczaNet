"""Execute the collection's basic journey verbatim in the small isolated UI target.
Other A3-A5 blocks deliberately need operator-selected IDs; route/reference parity
is checked separately and their real behavior is covered by full verify.
"""
import argparse,http.cookiejar,json,re,time,urllib.request,urllib.error,urllib.parse
from pathlib import Path
root=Path(__file__).resolve().parents[1];parser=argparse.ArgumentParser();parser.add_argument('--out',default='artifacts/http-collection.json');args=parser.parse_args();out=(root/args.out).resolve();out.parent.mkdir(parents=True,exist_ok=True)
if out.exists():raise SystemExit('new evidence path required')
text=(root/'requests.http').read_text(encoding='utf-8');variables=dict(re.findall(r'^@([A-Za-z0-9_]+)\s*=\s*(.+)$',text,re.M));variables.update(base='http://localhost:18087',origin='http://localhost:8080',run='collection-'+str(time.time_ns()),barcode='00339'+str(time.time_ns()))
# Credentials remain in memory only; never persist request headers or login bodies.
for l in (root/'.env').read_text().splitlines():
 if l.startswith('DEMO_PASSWORD='):variables['password']=l.split('=',1)[1]
responses={};opener=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()));rows=[];csrf=''
def expand(s):
 def value(m):
  expression=m[1]
  if '.response.body.$' in expression:
   name,path=expression.split('.response.body.$',1);v=responses[name]
   for key in re.findall(r'\.([A-Za-z_0-9]+)|\[([0-9]+)\]',path):v=v[key[0]] if key[0] else v[int(key[1])]
   return str(v)
  return variables[expression]
 return re.sub(r'\{\{([^}]+)\}\}',value,s)
for block in text.split('### A3:',1)[0].split('###'):
 lines=block.splitlines();start=next((i for i,l in enumerate(lines) if re.match(r'^(GET|POST) ',l)),None)
 if start is None:continue
 method,url=expand(lines[start]).split(' ',1);headers={};i=start+1
 while i<len(lines) and lines[i].strip():
  k,v=expand(lines[i]).split(':',1);headers[k]=v.strip();i+=1
 body=expand('\n'.join(lines[i+1:]).strip());body=body.encode() if body else None
 req=urllib.request.Request(url,data=body,headers=headers,method=method)
 try:r=opener.open(req,timeout=20)
 except urllib.error.HTTPError as e:r=e
 with r:
  status=r.status;raw=r.read();stamp=r.headers.get('X-DDO-Stamp');data=json.loads(raw) if raw and 'json' in r.headers.get('Content-Type','') else {}
 parsed=urllib.parse.urlsplit(url);path=parsed.path+('?' + parsed.query if parsed.query else '');want=200 if method=='GET' or path.endswith(('/quote','/send','/receive','/login')) else 201
 if path.endswith('/logout'):want=204
 if path.endswith('/locations/2') or path.endswith('/disable'):want=403
 if path.endswith('/auth/me') and not csrf:want=401
 if method=='POST' and path.endswith('/login') and status==200:csrf=data['csrf_token']
 if path.endswith('/logout') and status==204:csrf=''
 name=re.search(r'^# @name (\w+)',block,re.M)
 if name:responses[name[1]]=data
 rows.append({'method':method,'path':path,'status':status,'expected':want,'stamp':stamp})
 assert status==want and stamp=='a33906',rows[-1]
out.write_text(json.dumps({'target':'eczanet-a6-ui','requests':rows,'passed':True,'scope':'requests.http blocks through A2; remaining ID-dependent blocks are manual, structurally checked, and behavior covered by full API verify'},indent=2));print('PASS collection real basic journey',len(rows),'requests')
