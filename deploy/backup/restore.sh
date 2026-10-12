#!/bin/sh
# usage: RESTORE_CONFIRM=<dbname> restore.sh <object key, e.g. backups/daily/2026-10-01.dump> [target DATABASE_URL]
#
# Replaces the target database with the dump: downloads and checks the dump,
# then (connected to the `postgres` maintenance database on the same server)
# DROP DATABASE <db> WITH (FORCE) and CREATE DATABASE <db>, then restores into
# the empty database in a single transaction. Nothing created after the backup
# survives, and pg_restore never has to drop objects one by one (which, with
# --clean, could fail halfway on post-backup objects and leave the live
# database gutted). The target defaults to this container's $DATABASE_URL and
# must be a postgres:// URL. Stop the app before restoring over its database.
# Encrypted keys (*.age) need AGE_IDENTITY: the path to the recovery or restore-test age identity.
# A plaintext key (a legacy .dump from before encryption) needs RESTORE_ALLOW_PLAINTEXT=1:
# pg_restore runs a dump's SQL as the superuser, and nothing shows who wrote a
# plaintext object, so anyone who can write to the bucket could plant one.
set -eu
[ $# -ge 1 ] && [ $# -le 2 ] || { echo "usage: RESTORE_CONFIRM=<dbname> restore.sh <key> [target-database-url]" >&2; exit 2; }
key=$1
target=${2:-${DATABASE_URL:-}}
[ -n "$target" ] || { echo "restore: no target: pass a database URL or set DATABASE_URL" >&2; exit 2; }

# Split the URL: <prefix>/<dbname>[?params]. The maintenance URL is the same
# server and credentials with the database swapped for `postgres`.
base=${target%%\?*}
params=${target#"$base"}
dbname=${base##*/}
prefix=${base%/*}
case "$prefix" in
  postgres://*|postgresql://*) ;;
  *) echo "restore: target must be a postgres:// URL ending in /<dbname>" >&2; exit 2 ;;
esac
case "$dbname" in
  ""|*:*|*@*) echo "restore: no database name in the target URL" >&2; exit 2 ;;
  postgres|template0|template1) echo "restore: refusing to restore over the $dbname system database" >&2; exit 2 ;;
esac
admin_url="$prefix/postgres$params"

# The whole database is dropped and recreated below, so require the caller to
# name the database they intend to overwrite before anything is downloaded or
# touched. This catches a copy-pasted or stale URL pointed at the wrong
# database before it does any damage.
if [ "${RESTORE_CONFIRM:-}" != "$dbname" ]; then
  echo "refusing to restore over $dbname: set RESTORE_CONFIRM=$dbname to confirm" >&2
  exit 2
fi

# An encrypted backup needs an identity, and a plaintext one an explicit
# opt-in: refuse before downloading anything.
encrypted=false
case "$key" in
  *.age)
    encrypted=true
    [ -n "${AGE_IDENTITY:-}" ] || { echo "restore: encrypted backup; set AGE_IDENTITY to your recovery or restore-test key; database untouched" >&2; exit 2; }
    [ -f "$AGE_IDENTITY" ] && [ -r "$AGE_IDENTITY" ] || { echo "restore: AGE_IDENTITY ($AGE_IDENTITY) is not a readable file; database untouched" >&2; exit 2; }
    ;;
  *)
    [ "${RESTORE_ALLOW_PLAINTEXT:-}" = 1 ] || { echo "restore: $key is not encrypted (*.age); its SQL would run as the superuser with nothing to show where it came from. Set RESTORE_ALLOW_PLAINTEXT=1 only for a dump you know your own backup job wrote; database untouched" >&2; exit 2; }
    ;;
esac

: "${BUCKET_ENDPOINT:?}" "${BUCKET_NAME:?}" "${BUCKET_ACCESS_KEY:?}" "${BUCKET_SECRET_KEY:?}"
scheme=https
[ "${BUCKET_USE_SSL:-true}" = "false" ] && scheme=http
mc alias set store "$scheme://$BUCKET_ENDPOINT" "$BUCKET_ACCESS_KEY" "$BUCKET_SECRET_KEY" >/dev/null
tmp=$(mktemp /tmp/restore.XXXXXX)
trap 'rm -f "$tmp" "$tmp.dump"' EXIT

# Download and check the dump before touching the database.
mc cp --quiet "store/$BUCKET_NAME/$key" "$tmp"
# age authenticates its ciphertext: a wrong key or a changed byte fails
# here, before the database is touched. It doesn't show who encrypted it:
# anyone with a recipient's public key can make a file that decrypts, so
# the public keys are kept confidential (docs/operations.md, "Threat model").
if [ "$encrypted" = true ]; then
  age -d -i "$AGE_IDENTITY" -o "$tmp.dump" "$tmp" || { echo "restore: $key could not be decrypted with AGE_IDENTITY (wrong key or damaged file); database untouched" >&2; exit 1; }
  mv "$tmp.dump" "$tmp"
fi
[ -s "$tmp" ] || { echo "restore: downloaded dump $key is empty; database untouched" >&2; exit 1; }
pg_restore --list "$tmp" >/dev/null || { echo "restore: $key is not a readable pg_dump archive; database untouched" >&2; exit 1; }
# --list reads only the table of contents; reading every data block to
# /dev/null catches a truncated or corrupt dump before anything is dropped.
pg_restore --file=/dev/null "$tmp" || { echo "restore: $key has unreadable data; database untouched" >&2; exit 1; }

# Refuse while anything is still connected (normally the app): WITH (FORCE)
# would kill it silently, and an app restarting mid-restore could run its
# migrations into the fresh database before pg_restore does.
active=$(psql "$admin_url" -X -Atq -v ON_ERROR_STOP=1 -v db="$dbname" <<'SQL'
SELECT count(*) FROM pg_stat_activity WHERE datname = :'db';
SQL
)
if [ "$active" != "0" ]; then
  echo "restore: $active connection(s) to $dbname are open; stop the app first (docker compose stop app); database untouched" >&2
  exit 1
fi

# :"db" makes psql quote the name as an identifier. DROP/CREATE DATABASE
# cannot run inside a transaction, so they run as two autocommit statements.
psql "$admin_url" -X -q -v ON_ERROR_STOP=1 -v db="$dbname" <<'SQL'
SET client_min_messages = warning;
DROP DATABASE IF EXISTS :"db" WITH (FORCE);
CREATE DATABASE :"db" TEMPLATE template0;
SQL

pg_restore --single-transaction --exit-on-error --no-owner --dbname="$target" "$tmp"

# --no-owner leaves every object owned by the restoring superuser. Hand the
# database back to tp_owner, as migrations and ingest expect (the grants to
# tp_app came back with the dump). A cluster from before the roles existed
# has no tp_owner; skip it there.
has_owner=$(psql "$target" -X -Atq -c "SELECT count(*) FROM pg_roles WHERE rolname = 'tp_owner'")
if [ "$has_owner" = "1" ]; then
  psql "$target" -X -q -v ON_ERROR_STOP=1 -f /usr/local/share/thymeandplace/own-objects.sql
fi
echo "restore ok: $key → $dbname"
