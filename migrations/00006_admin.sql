-- +goose Up
CREATE TABLE admin_passkey (
  credential_id bytea PRIMARY KEY,
  public_key    bytea NOT NULL,
  sign_count    bigint NOT NULL DEFAULT 0,
  credential    jsonb NOT NULL,          -- full webauthn.Credential (flags, transports, AAGUID)
  name          text NOT NULL CHECK (length(name) BETWEEN 1 AND 60),
  created_at    timestamptz NOT NULL,
  last_used_at  timestamptz
);

CREATE TABLE admin_backup (
  id              boolean PRIMARY KEY DEFAULT true CHECK (id),
  password_hash   text NOT NULL,
  totp_secret_enc bytea NOT NULL,
  last_totp_step  bigint NOT NULL DEFAULT 0,
  failed_count    integer NOT NULL DEFAULT 0,
  lock_level      integer NOT NULL DEFAULT 0,
  locked_until    timestamptz
);

CREATE TABLE admin_session (
  token_hash   bytea PRIMARY KEY,
  csrf_token   text NOT NULL,
  method       text NOT NULL,
  created_at   timestamptz NOT NULL,
  last_seen_at timestamptz NOT NULL,
  auth_at      timestamptz NOT NULL
);

CREATE TABLE admin_enrollment_token (
  token_hash bytea PRIMARY KEY,
  created_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  used_at    timestamptz
);

CREATE TABLE admin_login_log (
  id      bigserial PRIMARY KEY,
  at      timestamptz NOT NULL,
  ip      text NOT NULL,
  method  text NOT NULL CHECK (method IN ('passkey', 'backup', 'enroll')),
  outcome text NOT NULL
);
CREATE INDEX admin_login_log_ip_at ON admin_login_log (ip, at DESC);

-- +goose Down
DROP TABLE admin_login_log;
DROP TABLE admin_enrollment_token;
DROP TABLE admin_session;
DROP TABLE admin_backup;
DROP TABLE admin_passkey;
