"""Write the A2 source fingerprint without reading secrets, build output or evidence."""
from pathlib import Path
import hashlib
import sys
root=Path(__file__).resolve().parents[1]
paths=[]
for folder in ['cmd','internal','migrations','api','web/src','web/tests','tests','scripts','.github']:
    paths.extend(p for p in (root/folder).rglob('*') if p.is_file() and p.suffix in ('.go','.sql','.yaml','.yml','.json','.tsx','.ts','.css','.py','.ps1'))
paths.extend(root/p for p in ['Dockerfile','.dockerignore','requirements-dev.txt','compose.yaml','go.mod','go.sum','.env.example','.gitignore','Makefile','web/Dockerfile','web/nginx.conf','web/package.json','web/package-lock.json','web/playwright.config.ts'])
out=root/(sys.argv[1] if len(sys.argv)>1 else 'artifacts/source-manifest.sha256')
out.parent.mkdir(parents=True,exist_ok=True)
out.write_text(''.join(f'{hashlib.sha256(p.read_bytes()).hexdigest()}  {p.relative_to(root).as_posix()}\n' for p in sorted(set(paths))),encoding='utf-8')
print('PASS source manifest:',len(set(paths)),'files')
