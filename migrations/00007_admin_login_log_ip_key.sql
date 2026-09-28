-- +goose Up
-- ip_key is the client key the sign-in budget counts by: the IPv4 address,
-- or the /64 for IPv6 (web.LimiterKey). ip keeps the full address. The
-- default keeps a rolled-back binary's inserts working; only rows inside
-- the budget window matter, so only the last hour is backfilled.
ALTER TABLE admin_login_log ADD COLUMN ip_key text NOT NULL DEFAULT '';
UPDATE admin_login_log SET ip_key = ip WHERE at > now() - interval '1 hour';
CREATE INDEX admin_login_log_ip_key_at ON admin_login_log (ip_key, at DESC);
-- Serves the daily prune and the growth count.
CREATE INDEX admin_login_log_at ON admin_login_log (at);

-- +goose Down
DROP INDEX admin_login_log_at;
DROP INDEX admin_login_log_ip_key_at;
ALTER TABLE admin_login_log DROP COLUMN ip_key;
