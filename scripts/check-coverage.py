"""Check coverage of the implemented domain packages; absent future packages stay explicit."""
import json
import sys
from pathlib import Path
root=Path(__file__).resolve().parents[1]
manifest=json.loads((root/'tests/coverage-manifest.json').read_text(encoding='utf-8'))
counts={}
for line in Path(sys.argv[1]).read_text(encoding='utf-8').splitlines()[1:]:
    block,statements,hits=line.split()
    package=block.split(':')[0].rsplit('/',1)[0].removeprefix('eczanet/')
    covered,total=counts.get(package,(0,0))
    counts[package]=(covered+(int(statements) if int(hits)>0 else 0),total+int(statements))
failed=False
for package in manifest['required_domain_packages']:
    if package not in manifest['implemented_domain_packages']:
        print('EKSIK future domain:',package)
        continue
    covered,total=counts.get(package,(0,0));percent=100*covered/total if total else 0
    print(f'{package}: {covered}/{total} statements = {percent:.1f}%')
    if percent<manifest['minimum_domain_coverage_percent']:
        failed=True
if failed:raise SystemExit(1)
