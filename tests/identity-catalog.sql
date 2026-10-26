SELECT tablename,tableowner FROM pg_tables WHERE schemaname='public' ORDER BY tablename;
SELECT count(*) AS users,count(*) FILTER(WHERE password_hash LIKE '$2%') AS bcrypt_hashes FROM "pl-wy-users";
SELECT id,email,role FROM "pl-wy-users" ORDER BY id LIMIT 1;
SELECT kind,count(*) FROM "pl-locations" GROUP BY kind ORDER BY kind;
SELECT version,demo_date,demo_seed FROM "pl-seed-versions";
SELECT rolname,rolsuper,rolcreatedb,rolcreaterole FROM pg_roles WHERE rolname IN ('eczanet_runtime','eczanet_migrator') ORDER BY rolname;
SELECT version_id,is_applied FROM "pl-goose-db-version" ORDER BY id;
