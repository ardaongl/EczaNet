#!/bin/sh
set -eu
# psql variables quote credentials as SQL literals; no shell interpolation in SQL.
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set=runtime_password="$RUNTIME_PASSWORD" --set=migrator_password="$MIGRATOR_PASSWORD" <<'SQL'
CREATE ROLE eczanet_runtime LOGIN PASSWORD :'runtime_password' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT;
CREATE ROLE eczanet_migrator LOGIN PASSWORD :'migrator_password' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT;
REVOKE ALL ON DATABASE eczanet_demo FROM PUBLIC;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT CONNECT ON DATABASE eczanet_demo TO eczanet_runtime, eczanet_migrator;
GRANT TEMPORARY ON DATABASE eczanet_demo TO eczanet_migrator;
GRANT USAGE, CREATE ON SCHEMA public TO eczanet_migrator;
GRANT USAGE ON SCHEMA public TO eczanet_runtime;
SQL
