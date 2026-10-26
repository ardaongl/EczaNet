-- Security salts, token/session rows and runtime timestamps are intentionally excluded.
SELECT md5(
 (SELECT jsonb_agg(jsonb_build_array(id,email,role,active) ORDER BY id)::text FROM "pl-wy-users") ||
 (SELECT jsonb_agg(jsonb_build_array(id,name,kind) ORDER BY id)::text FROM "pl-locations") ||
 (SELECT jsonb_agg(jsonb_build_array(user_id,location_id) ORDER BY user_id,location_id)::text FROM "pl-user-locations") ||
 (SELECT jsonb_agg(jsonb_build_array(version,demo_date,demo_seed) ORDER BY version)::text FROM "pl-seed-versions")
);
