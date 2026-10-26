"""Local documentation, source requirement and OpenAPI structure checks (no network)."""
import hashlib
import re
from pathlib import Path
import yaml

root = Path(__file__).resolve().parents[1]
errors = []
for path in root.rglob('*.md'):
    if any(part in ('.internal', 'node_modules', 'dist', 'test-results', 'artifacts') for part in path.parts):
        continue
    content = path.read_text(encoding='utf-8')
    for link in re.findall(r'\[[^\]]*\]\(([^\s)]+)\)', content):
        if '://' in link or link.startswith('#'):
            continue
        target = link.split('#')[0]
        if target and not (path.parent / target).exists():
            errors.append(f'{path.relative_to(root)}: missing {target}')

assert (root / 'README.md').read_text(encoding='utf-8').splitlines()[-1] == 'DDO Case Kodu: YILDIZ-a33906'
assert hashlib.sha256((root / 'proje.md').read_bytes()).hexdigest() == '240f6569441fac6b274f6dfaaa2194c51edddb249a0b40ac74de5dbf0b1a53a3'
spec = yaml.safe_load((root / 'api/openapi.yaml').read_text(encoding='utf-8'))
assert spec['openapi'] == '3.0.3'
a1_paths = {'/api/v1/dashboard','/health','/_hz/a339','/metrics','/api/v1/auth/csrf','/api/v1/auth/login','/api/v1/auth/logout','/api/v1/auth/me','/api/v1/auth/locations','/api/v1/locations/{id}'}
assert a1_paths <= set(spec['paths'])
source = (root/'internal/platform/business.go').read_text(encoding='utf-8')
actual = re.findall(r'"(GET|POST) (/api/v1/[^" ]+)"\s*:',source)
for method,path in actual:
    assert method.lower() in spec['paths'][path], (method,path)
for path,operations in spec['paths'].items():
    if path not in a1_paths:
        for method in operations:
            assert (method.upper(),path) in actual, (method,path)
def check_refs(node):
    if isinstance(node, dict):
        if '$ref' in node:
            ref = node['$ref']
            assert ref.startswith('#/')
            target = spec
            for key in ref[2:].split('/'):
                target = target[key]
        for value in node.values():
            check_refs(value)
    elif isinstance(node, list):
        for value in node:
            check_refs(value)
check_refs(spec)
for ident in [*(f'F-{i}' for i in range(1, 8)), *(f'T-{i}' for i in range(1, 6))]:
    assert ident in (root / 'TESTING.md').read_text(encoding='utf-8')
for error in errors:
    print(error)
if errors:
    raise SystemExit(1)
print('PASS public Markdown links, all 12 F/T mappings, OpenAPI route parity and references, README last line, unchanged source specification SHA256')

# Every business handler must have a method/path example in the HTTP collection.
collection=(root/'requests.http').read_text(encoding='utf-8')
listed=set()
for method,path in re.findall(r'^(GET|POST) \{\{base\}\}([^\s]+)',collection,re.M):
    path=path.split('?')[0]
    path=re.sub(r'\{\{[^}]+\}\}','{id}',path)
    path=re.sub(r'/[0-9]+(?=/|$)','/{id}',path)
    listed.add((method,path))
for item in actual:
    assert item in listed, ('missing HTTP collection route',item)
assert ('GET','/api/v1/dashboard') in listed
print('PASS HTTP collection method/path coverage for all business handlers and dashboard')

variables=set(re.findall(r'^@([A-Za-z0-9_]+)\s*=',collection,re.M))
named=set(re.findall(r'^# @name ([A-Za-z0-9_]+)',collection,re.M))
for expression in re.findall(r'\{\{([^}]+)\}\}',collection):
    if '.response.body.$' in expression:
        assert expression.split('.response.body.$')[0] in named, ('undefined named response',expression)
    else:
        assert expression in variables, ('undefined HTTP variable',expression)
print('PASS HTTP collection variables and named response references')
