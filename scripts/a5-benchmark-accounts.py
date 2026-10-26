"""Copy six LOCAL demo login hashes in memory to the retained isolated A5 benchmark.
No hashes, passwords, DSNs or sessions are printed or written to evidence files.
Run after scripts/a5-benchmark.py has prepared eczanet_a5_bench17.
"""
import json
import subprocess

base=['docker','compose','exec','-T','db','psql','-X','-qAt','-v','ON_ERROR_STOP=1','-U','eczanet_owner','-d']
result=subprocess.run(base+['eczanet_demo'],input='SELECT json_agg(t) FROM (SELECT id,email,password_hash,role FROM "pl-wy-users" WHERE id BETWEEN 1 AND 6) t;',text=True,capture_output=True,check=True)
users=json.loads(result.stdout)
assert len(users)==6
def literal(value):
    return "'"+str(value).replace("'","''")+"'"
query='BEGIN;'
for user in users:
    query+='INSERT INTO "pl-wy-users"(id,email,password_hash,role) VALUES('+','.join(literal(user[k]) for k in ['id','email','password_hash','role'])+') ON CONFLICT(id) DO UPDATE SET email=excluded.email,password_hash=excluded.password_hash,role=excluded.role;'
query+='INSERT INTO "pl-user-locations" VALUES(1,1),(2,1),(3,21),(4,1),(6,2) ON CONFLICT DO NOTHING; COMMIT;'
subprocess.run(base+['eczanet_a5_bench17'],input=query,text=True,capture_output=True,check=True)
print('Six local benchmark demo accounts prepared; no credentials logged.')
