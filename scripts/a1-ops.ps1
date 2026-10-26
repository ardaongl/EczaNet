$ErrorActionPreference = 'Stop'
function Assert-Exit([string]$Step) { if ($LASTEXITCODE -ne 0) { throw "$Step failed" } }
function Check-HTTP([string]$Path, [int]$Expected) {
    $timer = [Diagnostics.Stopwatch]::StartNew()
    $response = Invoke-WebRequest "http://localhost:8080$Path" -SkipHttpErrorCheck -TimeoutSec 5
    $timer.Stop()
    if ([int]$response.StatusCode -ne $Expected -or $response.Headers['X-DDO-Stamp'] -ne 'a33906') { throw "$Path unexpected HTTP/stamp" }
    "PASS $Path status=$($response.StatusCode) duration_ms=$($timer.ElapsedMilliseconds) stamp=a33906"
}
$checksumQuery = @'
SELECT md5(
 (SELECT jsonb_agg(jsonb_build_array(id,email,role,active) ORDER BY id)::text FROM "pl-wy-users") ||
 (SELECT jsonb_agg(jsonb_build_array(id,name,kind) ORDER BY id)::text FROM "pl-locations") ||
 (SELECT jsonb_agg(jsonb_build_array(user_id,location_id) ORDER BY user_id,location_id)::text FROM "pl-user-locations") ||
 (SELECT jsonb_agg(jsonb_build_array(version,demo_date,demo_seed) ORDER BY version)::text FROM "pl-seed-versions")
);
'@
function Checksum { $result = $checksumQuery | docker compose exec -T db psql -U eczanet_owner -d eczanet_demo -At; Assert-Exit 'checksum'; return $result }
$before = Checksum
docker compose run --rm migrate-seed
Assert-Exit 'second seed'
$after = Checksum
if ($before -ne $after) { throw 'Seed logical checksum changed' }
"PASS repeated seed checksum=$after"
docker compose run --rm -e DEMO_DATE=2026-10-25 migrate-seed
if ($LASTEXITCODE -eq 0) { throw 'Seed mismatch was accepted' }
if ((Checksum) -ne $before) { throw 'Rejected seed changed data' }
'PASS changed seed date refused; original data preserved'
docker compose run --rm migrate-seed
Assert-Exit 'restore seed input'
foreach ($service in @('api','web','simulator','db')) {
    "$service UID:"
    docker compose exec -T $service id
    Assert-Exit "$service UID"
}
Check-HTTP '/health' 200
try {
    docker compose stop db
    Assert-Exit 'stop db'
    Check-HTTP '/health' 503
    Check-HTTP '/_hz/a339' 200
} finally {
    docker compose start db
    Assert-Exit 'restart db'
}
$ready = $false
for ($i=0; $i -lt 30; $i++) {
    try {
        $r = Invoke-WebRequest 'http://localhost:8080/health' -SkipHttpErrorCheck -TimeoutSec 2
        if ([int]$r.StatusCode -eq 200) { $ready=$true; break }
    } catch { }
    Start-Sleep -Milliseconds 500
}
if (!$ready) { throw 'DB recovery timed out' }
Check-HTTP '/health' 200
'PASS DB recovery'
docker compose stop api
Assert-Exit 'graceful stop'
$logs = docker compose logs --no-log-prefix api --tail 5
if (!($logs -match 'shutdown_complete')) { throw 'No graceful shutdown marker' }
'PASS API SIGTERM shutdown_complete'
docker compose up -d --wait
Assert-Exit 'service recovery'
Check-HTTP '/health' 200
