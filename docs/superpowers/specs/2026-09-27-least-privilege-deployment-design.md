# Thyme & Place - Least-privilege database roles and per-service secrets - Design

- **Status:** Proposed
- **Date:** 2026-09-27
- **Source:** Architectural audit, 2026-09-27 (finding F02)
- **Related issues:** TBD
- **Amends:** core spec §4.1 (a one-shot `migrate` service), §10.5 (audit tables are append-only for the app), §13.3 and §13.5 (privilege tests), §14 (secrets are per service; migrations run as a separate step and role; database roles)

---

## 1. Problem

The app serves public traffic as a Postgres superuser, and every container gets every secret.

- **Superuser at runtime.** `deploy/env.example:5-8` sets `POSTGRES_USER=tp`, and `DATABASE_URL` connects as the same user. The official postgres image, which `postgis/postgis` extends, creates `POSTGRES_USER` as a superuser. So every public request runs as a superuser. One SQL injection or code-execution bug in the app would give full control of the database. That includes `COPY ... TO PROGRAM`, which runs a shell inside the `db` container, and rewriting the audit tables `catalog_history` and `admin_login_log`. Today's SQL uses sqlc bind parameters, and the few dynamic identifiers are checked by `store.ValidIdent` (`internal/store/handwritten.go:14-24`). The audit found no injection, so this design is defence in depth.
- **One env file for every container.** `deploy/compose.yaml:3-5` defines the `x-env` anchor (`env_file: ${TP_ENV_FILE:-/etc/thymeandplace/app.env}`). The `db` (`:17`) and `app` (`:28`) services merge it in, and so do `ingest` and `backup`. As a result:
  - Postgres receives `ADMIN_SECRET_KEY` and the bucket credentials.
  - The app receives `POSTGRES_PASSWORD` and `BUCKET_*`. The bucket holds the raw datasets and every nightly backup.

The design must keep these working:
- Migrations run at app startup (`cmd/server/app.go:109`) and need DDL rights.
- Ingest creates `*_v{N}` tables, runs `DROP TABLE IF EXISTS` (`internal/ingest/pipeline.go:234-240`) and `CREATE OR REPLACE VIEW` (`:378`). The last two need ownership.
- The catalog history and version triggers (`migrations/00004_catalog_history.sql`) are `SECURITY INVOKER` by default. So whoever writes the catalog must be able to insert into `catalog_history` and update `catalog_meta`.
- `Apply` locks `catalog_meta` with `SELECT ... FOR UPDATE` (`internal/store/queries/changeset.sql`, `LockCatalogVersion`). Postgres requires `UPDATE` privilege on at least one column for that.
- The admin CLI (`cmd/admin`) reads and writes the `admin_*` tables.
- `devseed` migrates and seeds.
- `pg_dump` must read everything. A restore drops and recreates the database (`deploy/backup/restore.sh`). `CREATE EXTENSION postgis` needs a superuser.
- `LISTEN`/`NOTIFY` for the data version.
- The e2e stack (`deploy/e2e.env`, `deploy/compose.e2e.yaml`) and the testcontainers tests (`internal/dbtest/dbtest.go`) use the same single-superuser pattern.

## 2. Goals and non-goals

Goals:

1. The app connects as a role with no superuser, no DDL rights, no `CREATE` on any schema, and no file or program access.
2. The app can't update or delete audit rows (`catalog_history`, `admin_login_log`), except through a narrow prune function that only removes rows past retention.
3. Each container gets only the secrets it uses.
4. Migrations and ingest keep working, run as an owner role that isn't a superuser.
5. The same layout runs in production, e2e and tests. Tests check the privileges.

Non-goals:

- Row-level security.
- Separate bucket credentials per purpose. The backup encryption design (`2026-09-27-backup-encryption-design.md`, "S9") covers bucket keys. This design only makes sure each container gets the keys meant for it.
- TLS between containers (they share one Docker network on one host).
- Secrets managers. Root-owned env files stay (core spec §14).

## 3. Decision log

| Decision | Chosen | Rejected and why |
|---|---|---|
| Roles | `tp_owner` (owns the schema, runs migrations, ingest and devseed), `tp_app` (runtime), `tp_backup` (member of `pg_read_all_data`). The bootstrap superuser is kept only for init and restore | A single non-superuser owner for everything (the app could then drop tables and rewrite the audit). A read-only role for the public side plus a writer for admin (two pools, and it splits the app for little gain) |
| Who runs migrations | A one-shot `migrate` compose service (the app image with `-migrate-only`) using `tp_owner`. `app` depends on it with `service_completed_successfully`. At startup, the app refuses to serve if migrations are pending | Keep migrating at startup with an owner URL in the app env (the app would hold DDL credentials again). Manual migration step (easy to forget) |
| Audit triggers | Make `catalog_history_row()` and `catalog_bump_version()` `SECURITY DEFINER`, owned by `tp_owner`, with `SET search_path = public, pg_temp` | Grant the app `INSERT` on `catalog_history` (the app could then forge history rows) |
| `catalog_meta` lock | `GRANT SELECT, UPDATE (id) ON catalog_meta TO tp_app`. The `FOR UPDATE` lock needs `UPDATE` on at least one column. `id` is `CHECK (id)` and can only ever be `true` | `UPDATE` on `version` (the app could then change the version counter directly) |
| Pruning the login log | A `SECURITY DEFINER` function `prune_admin_login_log(retention interval)` that refuses any retention under 30 days. `tp_app` gets `EXECUTE` | Grant `DELETE` (the app could erase recent evidence). No pruning (the table grows without bound; see S4) |
| Where roles are created | A `deploy/db/initdb/10-roles.sh` script mounted into `/docker-entrypoint-initdb.d` for fresh volumes, and a one-time `deploy/db/adopt-roles.sql` for an existing database. Migrations only grant privileges | Create roles in a goose migration (needs `CREATEROLE` on the migrating role, and roles are cluster-wide, not per database) |
| Reference tables created later by ingest | `ALTER DEFAULT PRIVILEGES FOR ROLE tp_owner IN SCHEMA public GRANT SELECT ON TABLES TO tp_app` | Re-grant after every ingest (easy to forget, and a swap would then break the site) |
| Env files | One file per service under `TP_ENV_DIR` (default `/etc/thymeandplace`): `db.env`, `migrate.env`, `app.env`, `ingest.env`, `backup.env`, `restore.env` | One file with everything (the current problem). Compose `secrets:` (the app reads env today, and `secrets:` needs file-reading code) |

## 4. Design

### 4.1 Roles

| Role | Attributes | Used by | Can |
|---|---|---|---|
| bootstrap (`POSTGRES_USER`) | superuser | `db` init, `restore`, `restore-test` | everything |
| `tp_owner` | `LOGIN`, no superuser, no `CREATEDB`, no `CREATEROLE` | `migrate`, `ingest`, `devseed` | owns the database, the `public` schema and every object in it |
| `tp_app` | `LOGIN`, nothing else | `app`, the admin CLI (`docker compose exec app /app/admin`) | the grants in §4.3 |
| `tp_backup` | `LOGIN`, member of `pg_read_all_data` | `backup` | read every table, for `pg_dump` |

The passwords are generated at first deploy (`openssl rand -hex 24`). Each one appears only in `db.env`, where the init script reads it, and in the one service file that connects as that role.

### 4.2 Creating the roles

**Fresh volume.** The `db` service mounts `deploy/db/initdb/` read-only at `/docker-entrypoint-initdb.d`. The image runs `10-roles.sh` as the bootstrap superuser, and only on an empty data directory. It:

1. Creates the three roles from `TP_OWNER_PASSWORD`, `TP_APP_PASSWORD` and `TP_BACKUP_PASSWORD`.
2. Runs `GRANT pg_read_all_data TO tp_backup`.
3. Runs `ALTER DATABASE "$POSTGRES_DB" OWNER TO tp_owner`. In Postgres 15 and later, the `public` schema is owned by `pg_database_owner`, so `tp_owner` owns it too.
4. Creates the `postgis` and `pg_trgm` extensions in `POSTGRES_DB`. Migration `00001` then finds them present (`IF NOT EXISTS`).

**Existing database.** `deploy/db/adopt-roles.sql` is run once as the bootstrap superuser:

```bash
docker compose exec -T db psql -U tp -d thymeandplace -v ON_ERROR_STOP=1 -f - < deploy/db/adopt-roles.sql
```

It creates the roles, if missing, and runs `REASSIGN OWNED BY tp TO tp_owner` in the application database. It then applies the same database and schema ownership as the init script. `REASSIGN OWNED` against PostGIS's extension objects isn't verified. The rollout (§7) runs it first on a scratch copy restored by `restore-test.sh`.

### 4.3 Grants

A new migration, `000NN_privileges.sql` (the next free number when it lands; the S4 migration may take the one before), runs as `tp_owner`:

1. **Guard.** It raises an error naming `deploy/db/adopt-roles.sql` if `tp_app` or `tp_backup` doesn't exist.
2. **Revoke.** It revokes everything on every table, sequence and function in `public` from `PUBLIC`.
3. **Grants to `tp_app`:**

| Object | Privileges |
|---|---|
| `ingredient`, `ingredient_pairing`, `seafood_region` | `SELECT, INSERT, UPDATE, DELETE` |
| `catalog_meta` | `SELECT, UPDATE (id)` |
| `catalog_history` | `SELECT` |
| `pending_changeset` | `SELECT, INSERT, DELETE` |
| `calibration_run` | `SELECT, INSERT` |
| `dataset_versions`, `goose_db_version` | `SELECT` |
| `climate_cell`, `ocean_ecoregion`, `place` (views) | `SELECT` |
| `admin_passkey`, `admin_session`, `admin_enrollment_token` | `SELECT, INSERT, UPDATE, DELETE` |
| `admin_backup` | `SELECT, INSERT, UPDATE` |
| `admin_login_log` | `SELECT, INSERT` |
| sequences behind `pending_changeset`, `calibration_run` and `admin_login_log` | `USAGE` |
| `prune_admin_login_log(interval)` | `EXECUTE` |

4. **Definer functions.** It runs `ALTER FUNCTION catalog_history_row() SECURITY DEFINER SET search_path = public, pg_temp` and the same for `catalog_bump_version()`. The functions are owned by `tp_owner`. `tp_app` needs no privilege on `catalog_history` beyond `SELECT`, or on `catalog_meta.version`.
5. **Prune function.** It creates `prune_admin_login_log(retention interval) RETURNS bigint`, `SECURITY DEFINER`, which raises an error when `retention < interval '30 days'`. It deletes rows with `at < now() - retention` and returns the count.
6. **Default privileges.** It runs `ALTER DEFAULT PRIVILEGES FOR ROLE tp_owner IN SCHEMA public GRANT SELECT ON TABLES TO tp_app`. `CREATE OR REPLACE VIEW` keeps the existing grants on the view.

Postgres advisory locks (`internal/auth/passkey.go:353`) and `pg_notify` need no grants. Views are checked with their owner's privileges on the underlying tables, so `tp_app` never needs direct access to the `*_v{N}` tables.

Down migration: revoke the grants, drop the prune function, and turn the two functions back into `SECURITY INVOKER`.

### 4.4 Migrations as a separate step

- `cmd/server` gets a `-migrate-only` flag. It runs `db.Migrate` and exits with 0 or 1.
- `newApp` no longer migrates. It calls a new `db.RequireMigrated(ctx, url)`, which builds the goose provider read-only and uses `HasPending`. If migrations are pending, it fails with "database has pending migrations; run the migrate service". `tp_app` reads `goose_db_version` for this, which is why it has `SELECT` on that table.
- `deploy/compose.yaml` gets a `migrate` service: the app image, `command: ["-migrate-only"]`, `restart: "no"`, `env_file: migrate.env`, and `depends_on: db: service_healthy`. `app.depends_on.migrate.condition` is `service_completed_successfully`, which needs Compose ≥ 2.24; `docs/operations.md` already requires that version.
- `cmd/devseed` still migrates, as `tp_owner`, from `ingest.env`.
- The advisory-lock comment in `internal/db/db.go:37-42` still holds for concurrent `migrate` runs.

### 4.5 Env files

| File | Service(s) | Contents |
|---|---|---|
| `db.env` | `db` | `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`, `TP_OWNER_PASSWORD`, `TP_APP_PASSWORD`, `TP_BACKUP_PASSWORD` |
| `migrate.env` | `migrate` | `DATABASE_URL` as `tp_owner` |
| `app.env` | `app` (and the admin CLI through `exec`) | `DATABASE_URL` as `tp_app`, `ADMIN_*`, `PUBLIC_BASE_URL`, plus later additions such as `DB_MAX_CONNS` (S1) and heartbeat URLs (the observability design) |
| `ingest.env` | `ingest` (`transform` gets no env file; it only reads the shared volume) | `DATABASE_URL` as `tp_owner`, the dataset `BUCKET_*`, `OCEAN_*`, `CLIMATE_SOURCE_BASE` |
| `backup.env` | `backup` | `DATABASE_URL` as `tp_backup`, the backup `BUCKET_*`, plus S9's recipients and the backup heartbeat |
| `restore.env` | `restore` (a new tools-profile service using the backup image with the `restore.sh` entrypoint) | `DATABASE_URL` as bootstrap superuser, bucket read credentials, plus S9's identity path |

`deploy/compose.yaml` replaces the `x-env` anchor with a per-service `env_file: ${TP_ENV_DIR:-/etc/thymeandplace}/<name>.env`. The `restore` and `restore-test` commands in `docs/operations.md` move from the `backup` service to `restore`. That keeps the superuser password out of the nightly job.

`deploy/env.example` becomes `deploy/env/*.example`, one file per service, each with only its own variables. The e2e stack gets committed, non-secret `deploy/e2e/*.env` files with the same names, and `TP_ENV_DIR=./e2e` replaces `TP_ENV_FILE=./e2e.env` in `deploy/smoke.sh` and `e2e/run.sh`.

### 4.6 Tests (`internal/dbtest`)

`dbtest.start()` creates the three roles in the container, then creates `tp_template` owned by `tp_owner`. It creates the extensions in the template as the superuser, then runs `db.Migrate` as `tp_owner`. That is the same order as production. `dbtest.New` still returns a superuser pool, because fixtures insert freely. A new `dbtest.NewAs(t, "tp_app")` returns a pool on the same per-test database connected as `tp_app`.

## 5. Implementation outline

1. `deploy/db/initdb/10-roles.sh` and `deploy/db/adopt-roles.sql`.
2. `migrations/000NN_privileges.sql` (§4.3).
3. `internal/db/db.go`: `RequireMigrated`. `cmd/server/main.go` and `app.go`: the `-migrate-only` flag, and replace `db.Migrate` with `db.RequireMigrated` in `newApp`.
4. `internal/dbtest/dbtest.go`: roles, owner-built template, `NewAs`.
5. `deploy/compose.yaml`: per-service `env_file`, the `migrate` and `restore` services, and the `db` init mount. `deploy/compose.e2e.yaml` and `deploy/e2e/*.env`. Update `deploy/smoke.sh`, `e2e/run.sh` and the CI `deploy` job's dummy env files (`.github/workflows/ci.yml:49-61`).
6. `deploy/env/*.example` replaces `deploy/env.example`. Update `.gitignore` (`!deploy/env/*.example`, `!deploy/e2e/*.env`) and `.dockerignore`.
7. `docs/operations.md`: first deploy, updating the app, the restore commands, and the adoption procedure (§7). Apply the core spec amendments.

## 6. Testing

- **Privileges (`internal/db/privileges_test.go`, testcontainers, `dbtest.NewAs(t, "tp_app")`):**
  - Allowed:
    - `catalog.Load`.
    - A full `changeset.Service.Apply`, which writes `catalog_history` rows with the right actor (the definer triggers work).
    - `InsertLoginAttempt`.
    - `LISTEN data_version_changed`.
    - Reading the three reference views.
    - `prune_admin_login_log('90 days')`.
    - `SELECT ... FOR UPDATE` on `catalog_meta`.
  - Refused with SQLSTATE `42501` (insufficient privilege):
    - `UPDATE` or `DELETE` on `admin_login_log` and `catalog_history`.
    - `UPDATE catalog_meta SET version = 0`.
    - `TRUNCATE ingredient`.
    - `CREATE TABLE x ()`.
    - `COPY (SELECT 1) TO PROGRAM 'true'`.
    - `SELECT pg_read_file('/etc/passwd')`.
    - `prune_admin_login_log('1 day')` raises the function's own error instead.
- **Default privileges:** as `tp_owner`, create `place_v99` and repoint the `place` view, as `ingest` does. `tp_app` can then read the view without a new grant.
- **Startup guard (`cmd/server/app_test.go`):** `newApp` against a database missing the newest migration fails with the pending-migrations error. `-migrate-only` exits 0 on a migrated database.
- **Backup role:** the local backup recipe in `docs/operations.md` runs `pg_dump` as `tp_backup` and restores through `restore` as the superuser. `restore-test` prints `restore-test ok`.
- **E2E:** `e2e/run.sh` and `deploy/smoke.sh` bring the stack up through `migrate`, and every existing scenario passes. The CI `deploy` job validates the compose files with the new per-service env files.
- **Existing suites:** every current test passes. Most keep the superuser pool.

## 7. Rollout, operations and rollback

- **Order:** ship before launch. If production has no data yet, the simplest path is a fresh volume: back up anything needed, then `docker compose down`, remove `dbdata`, write the six env files, and `docker compose up -d --build`.
- **Existing database (adoption):**
  1. Take a backup.
  2. Rehearse on a scratch copy restored by `restore-test`: run `adopt-roles.sql` there and check that it succeeds.
  3. Run `adopt-roles.sql` on production.
  4. Deploy the release. `migrate` runs the privileges migration as `tp_owner`, then the app starts as `tp_app`.
  5. Check the result. `docker compose exec app /app/admin revoke-sessions` must print `Ended N session(s)`, which shows the CLI can write admin tables as `tp_app`. `\dp admin_login_log` in `psql` must show `tp_app` with only `r` and `a` (read and append).
- **Docs:** rewrite "First deploy" steps 3–4 (six env files, file modes 600, directory 700) and "Updating the app" (`migrate` runs automatically). Add a "Database roles" section with the role table from §4.1, which role each command uses, and how to rotate a role's password (`ALTER ROLE ... PASSWORD`, then update its one env file).
- **Rollback:** the previous release migrates at startup as whatever `DATABASE_URL` says. Point `app.env`'s `DATABASE_URL` at `tp_owner` temporarily, not at the superuser, and redeploy the old image. Leave the grants and roles in place. They are additive, and the old code still works as `tp_owner`.

## 8. Open questions for the owner

1. **Is there a production database with data in it yet?** Recommended: if not, use the fresh-volume path. It avoids `REASSIGN OWNED` entirely.
2. **Admin CLI identity.** The CLI shares `tp_app`'s credentials through `exec app`. Recommended: keep it. The CLI needs only admin-table writes, which the app already has.
3. **Minimum login-log retention in the prune function.** Recommended: 30 days as a floor under S4's 90-day setting, so a compromised app can't erase the recent audit trail.
