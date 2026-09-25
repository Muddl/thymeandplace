# Thyme & Place — Plan 6: Admin Authentication and Security Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Secure `/admin` for a single owner: passkey (WebAuthn) sign-in, password + TOTP backup login with global lockout, sessions with idle/absolute expiry and a re-auth window, CSRF on every write, per-IP login rate limit, an audit log, strict admin headers, CLI enrollment and recovery, and the Security page for managing passkeys.

**Architecture:** `internal/auth` is the security core: crypto helpers (Argon2id, AES-GCM sealing with `ADMIN_SECRET_KEY`, token hashing), sessions, backup login, passkey ceremonies (`go-webauthn`), enrollment tokens and the login log. All timestamps come from an injected clock. `internal/admin` is the HTTP shell: middleware (security headers → body limit → 30 s timeout → session load → CSRF), login/enroll/security handlers, and `templ` views. Plan 7 adds the catalog screens to the same shell. WebAuthn ceremony state travels in a short-lived sealed cookie, so any app replica can finish a ceremony. `cmd/admin` provides `enroll`, `set-backup` and `revoke-sessions`.

**Tech Stack:** Go ≥ 1.25, `github.com/go-webauthn/webauthn`, `github.com/descope/virtualwebauthn` (tests), `github.com/pquerna/otp`, `golang.org/x/crypto/argon2`, `golang.org/x/term`, `github.com/mdp/qrterminal/v3`, templ, sqlc.

**Spec:** `docs/superpowers/specs/2026-09-24-thymeandplace-core-engine-design.md` (§9.1 login/security routes, §10, §13.4)

**Depends on:** Plans 1–5 (`web` middleware helpers, `static` package, `cmd/server` app).

## Global Constraints

- Relying party ID: `thymeand.place` in production, `localhost` in development (env `ADMIN_RP_ID`). Origin from `ADMIN_ORIGIN` (e.g. `https://thymeand.place`, `http://localhost:8080`). User verification **required**.
- Backup login: password **and** TOTP, both always required. Password stored as Argon2id (`m=65536 KiB, t=3, p=2`, 16-byte salt, 32-byte key, PHC string). TOTP secret encrypted at rest with AES-256-GCM using `ADMIN_SECRET_KEY` (32 bytes, base64). Disabled by `ADMIN_BACKUP_LOGIN=off`.
- Enrollment link: single-use, stored hashed, valid **15 minutes**, printed by `admin enroll`. `admin set-backup` replaces any existing backup credentials and prints the `otpauth://` URI and a terminal QR code. `admin revoke-sessions` ends all sessions.
- Session token: 32 random bytes; only SHA-256 stored in `admin_session`. Cookie `__Host-tp_admin`, `HttpOnly`, `Secure`, `SameSite=Strict`, `Path=/`. Expiry **30 min idle, 12 h absolute**. Adding or removing passkeys requires authentication within **5 minutes**.
- CSRF: per-session synchronizer token in every form and sent by HTMX as `X-CSRF-Token` (`hx-headers` on `<body>`), checked on every non-GET admin request. Pages before sign-in use a pre-session token in cookie `__Host-tp_pre` (double submit).
- Login rate limit: **5 attempts per 15 minutes per IP** across `/admin/login/*` (the passkey finish and backup POST are attempts), counted from `admin_login_log`. Rate-limited requests are logged but not counted.
- Backup lockout: after **5** consecutive failures the backup path locks globally for **15 min**, doubling on each repeat, capped at **24 h**. A success resets it. Passkey login keeps working during a lockout.
- Audit: every login attempt → `admin_login_log` (time, IP, method, outcome). Successful backup logins also log at WARN.
- Headers on `/admin`: CSP `default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, `Cache-Control: no-store`, `X-Robots-Tag: noindex`, `X-Content-Type-Options: nosniff`.
- Admin request body limit 6 MB (the import route enforces 5 MB itself); admin timeout 30 s.
- Env: `ADMIN_SECRET_KEY` (required to mount `/admin`), `ADMIN_RP_ID`, `ADMIN_ORIGIN`, `ADMIN_BACKUP_LOGIN`, `PUBLIC_BASE_URL` (enroll links; defaults to `ADMIN_ORIGIN`).
- Commit after every task. Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Removing your only passkey** is refused ("Add another passkey before removing this one"), because with backup login off it would lock the owner out. Pinned in Task 7.
2. **A TOTP code replayed within its 30-second window** (shoulder-surfed or reused) is rejected even with the right password, and counts toward lockout. Pinned in Task 3.
3. **An expired session cookie on an HTMX request** gets `HX-Redirect: /admin/login`, not a login page swapped into a table cell. Pinned in Task 5.
4. **Two enrollment finishes racing with the same link** register at most one passkey. The second gets "invalid or expired". Pinned in Task 6.
5. **An oversized or non-JSON body on a WebAuthn finish endpoint** returns a 4xx with a JSON error, never a 500, and still counts as a login attempt. Pinned in Task 6.

---

## File Structure

```
migrations/00006_admin.sql
internal/store/queries/admin.sql
internal/auth/crypto.go, crypto_test.go        # tokens, Argon2id, Sealer
internal/auth/service.go                       # Config, Service, clock, login log & rate limit
internal/auth/backup.go, backup_test.go        # SetBackup, BackupLogin (TOTP, lockout)
internal/auth/session.go, session_test.go      # sessions, cookie helpers
internal/auth/passkey.go, passkey_test.go      # WebAuthn ceremonies, enrollment tokens
internal/admin/admin.go                        # Deps, New, middleware, render
internal/admin/login.go                        # login, backup, logout, enroll handlers
internal/admin/security.go                     # security page, reauth, add/rename/remove
internal/admin/views/*.templ + models.go
internal/admin/*_test.go, testclient_test.go
static/admin.js, static/admin.css              # (static.go embed list updated)
cmd/admin/main.go, main_test.go
cmd/server/app.go                              # mount /admin
```

---

### Task 1: Admin schema and queries

**Files:**
- Create: `migrations/00006_admin.sql`, `internal/store/queries/admin.sql`

**Interfaces:**
- Consumes: sqlc setup (Plan 2).
- Produces: tables `admin_passkey`, `admin_backup`, `admin_session`, `admin_enrollment_token`, `admin_login_log`; sqlc methods listed in the query file below (generated names are authoritative).

- [ ] **Step 1: Write the migration**

Create `migrations/00006_admin.sql`:

```sql
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
```

- [ ] **Step 2: Write the queries and generate**

Create `internal/store/queries/admin.sql`:

```sql
-- name: ListPasskeys :many
SELECT * FROM admin_passkey ORDER BY created_at, credential_id;

-- name: InsertPasskey :exec
INSERT INTO admin_passkey (credential_id, public_key, sign_count, credential, name, created_at)
VALUES (@credential_id, @public_key, @sign_count, @credential, @name, @created_at);

-- name: UpdatePasskeyUse :exec
UPDATE admin_passkey SET sign_count = @sign_count, credential = @credential, last_used_at = @last_used_at
WHERE credential_id = @credential_id;

-- name: RenamePasskey :execrows
UPDATE admin_passkey SET name = @name WHERE credential_id = @credential_id;

-- name: DeletePasskey :execrows
DELETE FROM admin_passkey WHERE credential_id = @credential_id;

-- name: CountPasskeys :one
SELECT count(*) FROM admin_passkey;

-- name: GetBackup :one
SELECT * FROM admin_backup WHERE id;

-- name: GetBackupForUpdate :one
SELECT * FROM admin_backup WHERE id FOR UPDATE;

-- name: SetBackup :exec
INSERT INTO admin_backup (id, password_hash, totp_secret_enc) VALUES (true, @password_hash, @totp_secret_enc)
ON CONFLICT (id) DO UPDATE SET
  password_hash = EXCLUDED.password_hash, totp_secret_enc = EXCLUDED.totp_secret_enc,
  last_totp_step = 0, failed_count = 0, lock_level = 0, locked_until = NULL;

-- name: UpdateBackupState :exec
UPDATE admin_backup SET failed_count = @failed_count, lock_level = @lock_level,
  locked_until = @locked_until, last_totp_step = @last_totp_step
WHERE id;

-- name: InsertSession :exec
INSERT INTO admin_session (token_hash, csrf_token, method, created_at, last_seen_at, auth_at)
VALUES (@token_hash, @csrf_token, @method, @now, @now, @now);

-- name: GetSession :one
SELECT * FROM admin_session WHERE token_hash = @token_hash;

-- name: TouchSession :exec
UPDATE admin_session SET last_seen_at = @now WHERE token_hash = @token_hash;

-- name: SetSessionAuthAt :exec
UPDATE admin_session SET auth_at = @now WHERE token_hash = @token_hash;

-- name: DeleteSession :exec
DELETE FROM admin_session WHERE token_hash = @token_hash;

-- name: DeleteAllSessions :execrows
DELETE FROM admin_session;

-- name: InsertEnrollmentToken :exec
INSERT INTO admin_enrollment_token (token_hash, created_at, expires_at) VALUES (@token_hash, @created_at, @expires_at);

-- name: EnrollmentTokenValid :one
SELECT EXISTS (
  SELECT 1 FROM admin_enrollment_token WHERE token_hash = @token_hash AND used_at IS NULL AND expires_at > @now
);

-- name: ConsumeEnrollmentToken :execrows
UPDATE admin_enrollment_token SET used_at = @now
WHERE token_hash = @token_hash AND used_at IS NULL AND expires_at > @now;

-- name: InsertLoginAttempt :exec
INSERT INTO admin_login_log (at, ip, method, outcome) VALUES (@at, @ip, @method, @outcome);

-- name: CountRecentLoginAttempts :one
SELECT count(*) FROM admin_login_log WHERE ip = @ip AND at > @since AND outcome <> 'rate_limited';
```

Run: `./scripts/sqlc.sh generate && go build ./internal/store/`
Expected: compiles. Timestamps are `pgtype.Timestamptz`. `AdminBackup` has `LastTotpStep int64`, `FailedCount int32`, `LockLevel int32`, `LockedUntil pgtype.Timestamptz`.

- [ ] **Step 3: Verify the migration applies**

Run: `go test ./internal/dbtest/ -count=1`
Expected: `ok` (the template database now includes 00006).

- [ ] **Step 4: Commit**

```bash
git add migrations/00006_admin.sql internal/store
git commit -m "feat(store): admin auth schema and queries

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Crypto helpers

**Files:**
- Create: `internal/auth/crypto.go`, `internal/auth/crypto_test.go`

**Interfaces:**
- Produces:
  - `func auth.NewToken() (raw string, hash []byte)`: 32 random bytes, base64url (no padding); hash = SHA-256 of the raw string
  - `func auth.HashToken(raw string) []byte`
  - `func auth.HashPassword(pw string) (string, error)`, `func auth.VerifyPassword(encoded, pw string) (bool, error)`
  - `func auth.ParseSecretKey(b64 string) ([]byte, error)` (must decode to exactly 32 bytes)
  - `type auth.Sealer`; `func auth.NewSealer(key []byte) (*Sealer, error)`; `(*Sealer).Seal(plain []byte) []byte`; `(*Sealer).Open(sealed []byte) ([]byte, error)`

- [ ] **Step 1: Write the failing tests**

```bash
go get golang.org/x/crypto@latest
mkdir -p internal/auth
```

Create `internal/auth/crypto_test.go`:

```go
package auth

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestNewToken(t *testing.T) {
	a, ha := NewToken()
	b, _ := NewToken()
	if a == b || len(a) != 43 || !bytes.Equal(ha, HashToken(a)) || len(ha) != 32 {
		t.Fatalf("tokens %q %q", a, b)
	}
}

func TestPassword(t *testing.T) {
	enc, err := HashPassword("correct horse battery staple")
	if err != nil || !strings.HasPrefix(enc, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("enc=%q err=%v", enc, err)
	}
	if ok, _ := VerifyPassword(enc, "correct horse battery staple"); !ok {
		t.Fatal("right password rejected")
	}
	if ok, _ := VerifyPassword(enc, "wrong"); ok {
		t.Fatal("wrong password accepted")
	}
	if _, err := VerifyPassword("garbage", "x"); err == nil {
		t.Fatal("malformed hash must error")
	}
	other, _ := HashPassword("correct horse battery staple")
	if other == enc {
		t.Fatal("salt must differ")
	}
}

func TestSealer(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	s, err := NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed := s.Seal([]byte("JBSWY3DPEHPK3PXP"))
	if bytes.Contains(sealed, []byte("JBSWY3DP")) {
		t.Fatal("plaintext visible")
	}
	got, err := s.Open(sealed)
	if err != nil || string(got) != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("open: %q %v", got, err)
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := s.Open(sealed); err == nil {
		t.Fatal("tampering must be detected")
	}
	other, _ := NewSealer(bytes.Repeat([]byte{8}, 32))
	if _, err := other.Open(s.Seal([]byte("x"))); err == nil {
		t.Fatal("wrong key must fail")
	}
	if _, err := s.Open([]byte{1, 2}); err == nil {
		t.Fatal("short input must fail")
	}
}

func TestParseSecretKey(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	if k, err := ParseSecretKey(good); err != nil || len(k) != 32 {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "not base64!", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := ParseSecretKey(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/auth/`
Expected: FAIL to compile: `undefined: NewToken`.

- [ ] **Step 3: Implement**

Create `internal/auth/crypto.go`:

```go
// Package auth implements single-owner admin authentication.
package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

var b64u = base64.RawURLEncoding

// NewToken returns a random 32-byte token (base64url) and its SHA-256 hash.
func NewToken() (string, []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	raw := b64u.EncodeToString(b)
	return raw, HashToken(raw)
}

func HashToken(raw string) []byte {
	h := sha256.Sum256([]byte(raw))
	return h[:]
}

const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
)

// HashPassword returns an Argon2id PHC string.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks pw against an Argon2id PHC string in constant time.
func VerifyPassword(encoded, pw string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unsupported password hash")
	}
	var version int
	var m uint32
	var t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errors.New("unsupported argon2 version")
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false, errors.New("bad argon2 parameters")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// ParseSecretKey decodes ADMIN_SECRET_KEY (standard base64, 32 bytes).
func ParseSecretKey(b64 string) ([]byte, error) {
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("ADMIN_SECRET_KEY is not base64: %w", err)
	}
	if len(k) != 32 {
		return nil, fmt.Errorf("ADMIN_SECRET_KEY must decode to 32 bytes, got %d (generate with: openssl rand -base64 32)", len(k))
	}
	return k, nil
}

// Sealer encrypts small secrets with AES-256-GCM. Output: nonce || ciphertext.
type Sealer struct{ aead cipher.AEAD }

func NewSealer(key []byte) (*Sealer, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

func (s *Sealer) Seal(plain []byte) []byte {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return s.aead.Seal(nonce, nonce, plain, nil)
}

func (s *Sealer) Open(sealed []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(sealed) < n+s.aead.Overhead() {
		return nil, errors.New("sealed value too short")
	}
	return s.aead.Open(nil, sealed[:n], sealed[n:], nil)
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/auth/ -count=1`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/auth
git commit -m "feat(auth): token hashing, Argon2id passwords, AES-GCM sealer

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Auth service core, login log/rate limit and backup login

**Files:**
- Create: `internal/auth/service.go`, `internal/auth/backup.go`, `internal/auth/backup_test.go`, `internal/auth/testing_test.go`

**Interfaces:**
- Consumes: Task 1 queries, Task 2 helpers.
- Produces:
  - `type auth.Config struct { RPID string; RPOrigins []string; SecretKey []byte; BackupEnabled bool; Now func() time.Time; Log *slog.Logger }`
  - `func auth.NewService(pool *pgxpool.Pool, cfg Config) (*Service, error)`; `(*Service).BackupEnabled() bool`
  - `const auth.AttemptLimit = 5`, `auth.AttemptWindow = 15 * time.Minute`
  - `(*Service).AllowAttempt(ctx, ip string) (bool, error)`; `(*Service).LogAttempt(ctx, ip, method, outcome string)`. Outcomes: `success`, `failure`, `locked`, `rate_limited`, `disabled`
  - `(*Service).SetBackup(ctx, password string) (otpauthURI string, err error)` (password ≥ 12 characters)
  - `(*Service).BackupLogin(ctx, password, code string) error`: returns `ErrBackupDisabled`, `ErrBackupNotSet`, `*LockedError{Until time.Time}`, or `ErrBadCredentials`
  - `const auth.LockoutFailures = 5`, `auth.LockoutBase = 15 * time.Minute`, `auth.LockoutMax = 24 * time.Hour`

- [ ] **Step 1: Write the failing tests**

```bash
go get github.com/pquerna/otp@latest github.com/go-webauthn/webauthn@latest
```

Create `internal/auth/testing_test.go`:

```go
package auth

import (
	"bytes"
	"io"
	"log/slog"
	"testing"
	"time"

	"thymeandplace/internal/dbtest"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) add(d time.Duration)     { c.t = c.t.Add(d) }

func newTestService(t *testing.T, backup bool) (*Service, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)}
	s, err := NewService(dbtest.New(t), Config{
		RPID: "localhost", RPOrigins: []string{"https://localhost"},
		SecretKey: bytes.Repeat([]byte{9}, 32), BackupEnabled: backup,
		Now: c.now, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}
```

Create `internal/auth/backup_test.go`:

```go
package auth

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

const pw = "correct horse battery staple"

func setup(t *testing.T) (*Service, *clock, string) {
	t.Helper()
	s, c := newTestService(t, true)
	uri, err := s.SetBackup(context.Background(), pw)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(uri)
	if u.Scheme != "otpauth" || u.Host != "totp" {
		t.Fatalf("uri = %q", uri)
	}
	return s, c, u.Query().Get("secret")
}

func code(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	c, err := totp.GenerateCode(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestBackupLoginSuccessAndFailures(t *testing.T) {
	ctx := context.Background()
	s, c, secret := setup(t)
	if err := s.BackupLogin(ctx, "wrong password", code(t, secret, c.now())); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	if err := s.BackupLogin(ctx, pw, "000000"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong code: %v", err)
	}
	if err := s.BackupLogin(ctx, pw, code(t, secret, c.now())); err != nil {
		t.Fatalf("right credentials: %v", err)
	}
}

// Review Focus 2: a code cannot be used twice, even within its window.
func TestBackupRejectsReplayedCode(t *testing.T) {
	ctx := context.Background()
	s, c, secret := setup(t)
	cd := code(t, secret, c.now())
	if err := s.BackupLogin(ctx, pw, cd); err != nil {
		t.Fatal(err)
	}
	c.add(10 * time.Second)
	if err := s.BackupLogin(ctx, pw, cd); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("replayed code accepted: %v", err)
	}
	// The previous-step code (skew) is also spent once a later step was used.
	if err := s.BackupLogin(ctx, pw, code(t, secret, c.now().Add(-30*time.Second))); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("older step accepted: %v", err)
	}
	c.add(30 * time.Second)
	if err := s.BackupLogin(ctx, pw, code(t, secret, c.now())); err != nil {
		t.Fatalf("next step rejected: %v", err)
	}
}

func TestBackupLockoutAndBackoff(t *testing.T) {
	ctx := context.Background()
	s, c, secret := setup(t)
	fail5 := func() {
		for i := 0; i < LockoutFailures; i++ {
			_ = s.BackupLogin(ctx, "nope", "000000")
		}
	}
	fail5()
	var le *LockedError
	if err := s.BackupLogin(ctx, pw, code(t, secret, c.now())); !errors.As(err, &le) || !le.Until.Equal(c.now().Add(15*time.Minute)) {
		t.Fatalf("first lock: %v", err)
	}
	c.add(15*time.Minute + time.Second)
	fail5() // second lockout doubles
	if err := s.BackupLogin(ctx, pw, code(t, secret, c.now())); !errors.As(err, &le) || !le.Until.Equal(c.now().Add(30*time.Minute)) {
		t.Fatalf("second lock: %v", err)
	}
	for i := 0; i < 10; i++ { // many more lockouts: capped at 24h
		c.add(25 * time.Hour)
		fail5()
	}
	if err := s.BackupLogin(ctx, pw, code(t, secret, c.now())); !errors.As(err, &le) || le.Until.Sub(c.now()) != 24*time.Hour {
		t.Fatalf("cap: %v", err)
	}
	c.add(24*time.Hour + time.Second)
	if err := s.BackupLogin(ctx, pw, code(t, secret, c.now())); err != nil {
		t.Fatalf("after lock expiry: %v", err)
	}
	// Success reset the level: the next lockout is 15 minutes again.
	c.add(time.Minute)
	fail5()
	if err := s.BackupLogin(ctx, pw, code(t, secret, c.now())); !errors.As(err, &le) || le.Until.Sub(c.now()) != 15*time.Minute {
		t.Fatalf("reset: %v", err)
	}
}

func TestBackupDisabledAndNotSet(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, false)
	if err := s.BackupLogin(ctx, pw, "123456"); !errors.Is(err, ErrBackupDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	s2, _ := newTestService(t, true)
	if err := s2.BackupLogin(ctx, pw, "123456"); !errors.Is(err, ErrBackupNotSet) {
		t.Fatalf("not set: %v", err)
	}
	if _, err := s2.SetBackup(ctx, "short"); err == nil {
		t.Fatal("short password accepted")
	}
}

func TestAttemptRateLimit(t *testing.T) {
	ctx := context.Background()
	s, c := newTestService(t, true)
	for i := 0; i < AttemptLimit; i++ {
		if ok, _ := s.AllowAttempt(ctx, "203.0.113.5"); !ok {
			t.Fatalf("attempt %d refused", i)
		}
		s.LogAttempt(ctx, "203.0.113.5", "backup", "failure")
	}
	if ok, _ := s.AllowAttempt(ctx, "203.0.113.5"); ok {
		t.Fatal("6th attempt within 15 minutes allowed")
	}
	s.LogAttempt(ctx, "203.0.113.5", "backup", "rate_limited")
	if ok, _ := s.AllowAttempt(ctx, "198.51.100.1"); !ok {
		t.Fatal("other IP limited")
	}
	c.add(AttemptWindow + time.Second)
	if ok, _ := s.AllowAttempt(ctx, "203.0.113.5"); !ok {
		t.Fatal("window did not slide (rate_limited rows must not count)")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/auth/ -run 'Backup|Attempt'`
Expected: FAIL to compile: `undefined: NewService`.

- [ ] **Step 3: Implement the service core**

Create `internal/auth/service.go`:

```go
package auth

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/store"
)

type Config struct {
	RPID          string
	RPOrigins     []string
	SecretKey     []byte
	BackupEnabled bool
	Now           func() time.Time
	Log           *slog.Logger
}

type Service struct {
	pool   *pgxpool.Pool
	wa     *webauthn.WebAuthn
	sealer *Sealer
	backup bool
	now    func() time.Time
	log    *slog.Logger
}

func NewService(pool *pgxpool.Pool, cfg Config) (*Service, error) {
	sealer, err := NewSealer(cfg.SecretKey)
	if err != nil {
		return nil, err
	}
	timeout := webauthn.TimeoutConfig{Enforce: true, Timeout: 5 * time.Minute, TimeoutUVD: 5 * time.Minute}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          cfg.RPID,
		RPDisplayName: "Thyme & Place",
		RPOrigins:     cfg.RPOrigins,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationRequired,
		},
		Timeouts: webauthn.TimeoutsConfig{Login: timeout, Registration: timeout},
	})
	if err != nil {
		return nil, err
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Service{pool: pool, wa: wa, sealer: sealer, backup: cfg.BackupEnabled, now: cfg.Now, log: cfg.Log}, nil
}

func (s *Service) q() *store.Queries { return store.New(s.pool) }

func (s *Service) BackupEnabled() bool { return s.backup }

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

const (
	AttemptLimit  = 5
	AttemptWindow = 15 * time.Minute
)

// AllowAttempt reports whether ip may make another login attempt.
func (s *Service) AllowAttempt(ctx context.Context, ip string) (bool, error) {
	n, err := s.q().CountRecentLoginAttempts(ctx, store.CountRecentLoginAttemptsParams{Ip: ip, Since: ts(s.now().Add(-AttemptWindow))})
	if err != nil {
		return false, err
	}
	return n < AttemptLimit, nil
}

// LogAttempt writes the audit row. Failures to log are themselves logged.
func (s *Service) LogAttempt(ctx context.Context, ip, method, outcome string) {
	err := s.q().InsertLoginAttempt(ctx, store.InsertLoginAttemptParams{At: ts(s.now()), Ip: ip, Method: method, Outcome: outcome})
	if err != nil {
		s.log.Error("login audit write failed", "err", err)
	}
	if method == "backup" && outcome == "success" {
		s.log.Warn("backup login used", "ip", ip)
	}
}

var (
	ErrBackupDisabled = errors.New("backup login is disabled")
	ErrBackupNotSet   = errors.New("backup login is not set up")
	ErrBadCredentials = errors.New("password or code is wrong")
	ErrNoPasskeys     = errors.New("no passkeys registered")
	ErrLastPasskey    = errors.New("cannot remove the only passkey")
	ErrBadEnrollment  = errors.New("enrollment link is invalid or has expired")
	ErrCeremony       = errors.New("passkey check failed")
)

type LockedError struct{ Until time.Time }

func (e *LockedError) Error() string { return "backup login locked until " + e.Until.Format(time.RFC3339) }
```

The generated param field for `@ip` may be `Ip` or `IP` depending on sqlc's initialism rules; use whatever `CountRecentLoginAttemptsParams` declares.

- [ ] **Step 4: Implement backup login**

Create `internal/auth/backup.go`:

```go
package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"thymeandplace/internal/store"
)

const (
	LockoutFailures = 5
	LockoutBase     = 15 * time.Minute
	LockoutMax      = 24 * time.Hour
	totpPeriod      = 30
	minPasswordLen  = 12
)

var totpOpts = totp.ValidateOpts{Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}

// SetBackup replaces the backup credentials and returns the otpauth:// URI.
func (s *Service) SetBackup(ctx context.Context, password string) (string, error) {
	if utf8.RuneCountInString(password) < minPasswordLen {
		return "", fmt.Errorf("password must be at least %d characters", minPasswordLen)
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "Thyme & Place", AccountName: "admin", Period: totpPeriod})
	if err != nil {
		return "", err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return "", err
	}
	if err := s.q().SetBackup(ctx, store.SetBackupParams{PasswordHash: hash, TotpSecretEnc: s.sealer.Seal([]byte(key.Secret()))}); err != nil {
		return "", err
	}
	return key.URL(), nil
}

// BackupLogin checks password and TOTP together, with replay protection and
// a global lockout that doubles on each repeat.
func (s *Service) BackupLogin(ctx context.Context, password, code string) error {
	if !s.backup {
		return ErrBackupDisabled
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := store.New(tx)
	row, err := q.GetBackupForUpdate(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrBackupNotSet
	}
	if err != nil {
		return err
	}
	now := s.now()
	if row.LockedUntil.Valid && now.Before(row.LockedUntil.Time) {
		return &LockedError{Until: row.LockedUntil.Time}
	}

	pwOK, err := VerifyPassword(row.PasswordHash, password)
	if err != nil {
		return err
	}
	secret, err := s.sealer.Open(row.TotpSecretEnc)
	if err != nil {
		return fmt.Errorf("backup secret unreadable (wrong ADMIN_SECRET_KEY?): %w", err)
	}
	step, codeOK := checkTOTP(string(secret), code, now, row.LastTotpStep)

	if pwOK && codeOK {
		if err := q.UpdateBackupState(ctx, store.UpdateBackupStateParams{
			FailedCount: 0, LockLevel: 0, LockedUntil: pgtype.Timestamptz{}, LastTotpStep: step,
		}); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}

	failed, level, until := row.FailedCount+1, row.LockLevel, pgtype.Timestamptz{}
	if failed >= LockoutFailures {
		d := LockoutBase << level
		if d > LockoutMax || d <= 0 {
			d = LockoutMax
		}
		until = ts(now.Add(d))
		level++
		failed = 0
	}
	if err := q.UpdateBackupState(ctx, store.UpdateBackupStateParams{
		FailedCount: failed, LockLevel: level, LockedUntil: until, LastTotpStep: row.LastTotpStep,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return ErrBadCredentials
}

// checkTOTP accepts the current step ±1, but only steps after lastStep.
func checkTOTP(secret, code string, now time.Time, lastStep int64) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	cur := now.Unix() / totpPeriod
	for _, st := range []int64{cur - 1, cur, cur + 1} {
		if st <= lastStep {
			continue
		}
		want, err := totp.GenerateCodeCustom(secret, time.Unix(st*totpPeriod, 0), totpOpts)
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return st, true
		}
	}
	return 0, false
}
```

`LockoutBase << level` shifts a `time.Duration` by an `int32`. Convert with `LockoutBase << uint(level)` if the compiler asks.

- [ ] **Step 5: Run the tests**

Run: `go mod tidy && go test ./internal/auth/ -count=1`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/auth
git commit -m "feat(auth): backup password+TOTP login with replay protection, lockout and rate limit

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Sessions

**Files:**
- Create: `internal/auth/session.go`, `internal/auth/session_test.go`

**Interfaces:**
- Produces:
  - `const auth.CookieName = "__Host-tp_admin"`, `auth.IdleTimeout = 30 * time.Minute`, `auth.AbsoluteTimeout = 12 * time.Hour`, `auth.ReauthWindow = 5 * time.Minute`
  - `type auth.Session struct { TokenHash []byte; CSRF, Method string; CreatedAt, LastSeenAt, AuthAt time.Time }` with `(Session).RecentAuth(now time.Time) bool`
  - `(*Service).CreateSession(ctx, method string) (raw string, sess Session, err error)`
  - `(*Service).LookupSession(ctx, raw string) (Session, bool, error)`: deletes expired sessions, touches `last_seen_at` when more than a minute old
  - `(*Service).MarkReauthed(ctx, sess Session) error`; `DeleteSession(ctx, raw string) error`; `RevokeAllSessions(ctx) (int64, error)`
  - `(*Service).Now() time.Time`
  - `func auth.SetSessionCookie(w http.ResponseWriter, raw string)`, `func auth.ClearSessionCookie(w http.ResponseWriter)`

- [ ] **Step 1: Write the failing tests**

Create `internal/auth/session_test.go`:

```go
package auth

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSessionIdleAndAbsoluteExpiry(t *testing.T) {
	ctx := context.Background()
	s, c := newTestService(t, true)
	raw, sess, err := s.CreateSession(ctx, "passkey")
	if err != nil || len(sess.CSRF) < 32 {
		t.Fatal(err)
	}
	c.add(29 * time.Minute)
	if _, ok, _ := s.LookupSession(ctx, raw); !ok {
		t.Fatal("29 min idle should be fine")
	}
	c.add(29 * time.Minute) // touched at 29m, now 58m: 29m idle
	if _, ok, _ := s.LookupSession(ctx, raw); !ok {
		t.Fatal("activity must extend the idle timer")
	}
	c.add(31 * time.Minute)
	if _, ok, _ := s.LookupSession(ctx, raw); ok {
		t.Fatal("31 min idle must expire")
	}

	raw2, _, _ := s.CreateSession(ctx, "passkey")
	for i := 0; i < 36; i++ { // active every 20 minutes for 12 hours
		c.add(20 * time.Minute)
		if _, ok, _ := s.LookupSession(ctx, raw2); !ok {
			t.Fatalf("expired early at step %d", i)
		}
	}
	c.add(20 * time.Minute)
	if _, ok, _ := s.LookupSession(ctx, raw2); ok {
		t.Fatal("12 h absolute limit exceeded")
	}
}

func TestReauthWindow(t *testing.T) {
	ctx := context.Background()
	s, c := newTestService(t, true)
	raw, sess, _ := s.CreateSession(ctx, "passkey")
	if !sess.RecentAuth(c.now().Add(4 * time.Minute)) {
		t.Fatal("4 min after sign-in is recent")
	}
	c.add(6 * time.Minute)
	sess, _, _ = s.LookupSession(ctx, raw)
	if sess.RecentAuth(c.now()) {
		t.Fatal("6 min is not recent")
	}
	if err := s.MarkReauthed(ctx, sess); err != nil {
		t.Fatal(err)
	}
	sess, _, _ = s.LookupSession(ctx, raw)
	if !sess.RecentAuth(c.now()) {
		t.Fatal("re-auth should reset the window")
	}
}

func TestRevokeAndDelete(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, true)
	a, _, _ := s.CreateSession(ctx, "passkey")
	b, _, _ := s.CreateSession(ctx, "backup")
	if err := s.DeleteSession(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.LookupSession(ctx, a); ok {
		t.Fatal("deleted session still valid")
	}
	if n, err := s.RevokeAllSessions(ctx); err != nil || n != 1 {
		t.Fatalf("revoked %d %v", n, err)
	}
	if _, ok, _ := s.LookupSession(ctx, b); ok {
		t.Fatal("revoked session still valid")
	}
	if _, ok, _ := s.LookupSession(ctx, "garbage"); ok {
		t.Fatal("unknown token valid")
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	rec := httptest.NewRecorder()
	SetSessionCookie(rec, "tok")
	ck := rec.Result().Cookies()[0]
	if ck.Name != "__Host-tp_admin" || !ck.HttpOnly || !ck.Secure || ck.SameSite != 3 /* Strict */ || ck.Path != "/" || ck.Domain != "" {
		t.Fatalf("cookie = %+v", ck)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/auth/ -run 'Session|Reauth|Revoke'`
Expected: FAIL to compile: `s.CreateSession undefined`.

- [ ] **Step 3: Implement**

Create `internal/auth/session.go`:

```go
package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"thymeandplace/internal/store"
)

const (
	CookieName      = "__Host-tp_admin"
	IdleTimeout     = 30 * time.Minute
	AbsoluteTimeout = 12 * time.Hour
	ReauthWindow    = 5 * time.Minute
	touchEvery      = time.Minute
)

type Session struct {
	TokenHash                     []byte
	CSRF, Method                  string
	CreatedAt, LastSeenAt, AuthAt time.Time
}

func (s Session) RecentAuth(now time.Time) bool { return now.Sub(s.AuthAt) <= ReauthWindow }

func (s *Service) Now() time.Time { return s.now() }

func (s *Service) CreateSession(ctx context.Context, method string) (string, Session, error) {
	raw, hash := NewToken()
	csrf, _ := NewToken()
	now := s.now()
	err := s.q().InsertSession(ctx, store.InsertSessionParams{TokenHash: hash, CsrfToken: csrf, Method: method, Now: ts(now)})
	return raw, Session{TokenHash: hash, CSRF: csrf, Method: method, CreatedAt: now, LastSeenAt: now, AuthAt: now}, err
}

func (s *Service) LookupSession(ctx context.Context, raw string) (Session, bool, error) {
	if raw == "" {
		return Session{}, false, nil
	}
	q := s.q()
	row, err := q.GetSession(ctx, HashToken(raw))
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	sess := Session{
		TokenHash: row.TokenHash, CSRF: row.CsrfToken, Method: row.Method,
		CreatedAt: row.CreatedAt.Time, LastSeenAt: row.LastSeenAt.Time, AuthAt: row.AuthAt.Time,
	}
	now := s.now()
	if now.Sub(sess.LastSeenAt) > IdleTimeout || now.Sub(sess.CreatedAt) > AbsoluteTimeout {
		_ = q.DeleteSession(ctx, sess.TokenHash)
		return Session{}, false, nil
	}
	if now.Sub(sess.LastSeenAt) > touchEvery {
		if err := q.TouchSession(ctx, store.TouchSessionParams{TokenHash: sess.TokenHash, Now: ts(now)}); err != nil {
			return Session{}, false, err
		}
		sess.LastSeenAt = now
	}
	return sess, true, nil
}

func (s *Service) MarkReauthed(ctx context.Context, sess Session) error {
	return s.q().SetSessionAuthAt(ctx, store.SetSessionAuthAtParams{TokenHash: sess.TokenHash, Now: ts(s.now())})
}

func (s *Service) DeleteSession(ctx context.Context, raw string) error {
	return s.q().DeleteSession(ctx, HashToken(raw))
}

func (s *Service) RevokeAllSessions(ctx context.Context) (int64, error) {
	return s.q().DeleteAllSessions(ctx)
}

func SetSessionCookie(w http.ResponseWriter, raw string) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: raw, Path: "/", MaxAge: int(AbsoluteTimeout.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/auth/ -count=1`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/auth
git commit -m "feat(auth): hashed sessions with idle/absolute expiry and re-auth window

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Passkey ceremonies and enrollment tokens

**Files:**
- Create: `internal/auth/passkey.go`, `internal/auth/passkey_test.go`

**Interfaces:**
- Produces:
  - `type auth.Passkey struct { ID []byte; Name string; CreatedAt time.Time; LastUsedAt *time.Time }`
  - `(*Service).Passkeys(ctx) ([]Passkey, error)`
  - `(*Service).BeginLogin(ctx) (options []byte, state string, err error)` (`ErrNoPasskeys` when none)
  - `(*Service).FinishLogin(ctx, state string, r *http.Request) error` (wraps failures in `ErrCeremony`)
  - `(*Service).BeginRegistration(ctx) (options []byte, state string, err error)`
  - `(*Service).FinishRegistration(ctx, state string, r *http.Request, name, enrollToken string) error` (`enrollToken == ""` for a signed-in owner; otherwise the token is consumed in the same transaction and `ErrBadEnrollment` is returned if it is used or expired)
  - `(*Service).RenamePasskey(ctx, id []byte, name string) error`; `(*Service).RemovePasskey(ctx, id []byte) error` (`ErrLastPasskey`)
  - `(*Service).CreateEnrollmentToken(ctx) (raw string, expires time.Time, err error)` (15 min); `(*Service).EnrollmentValid(ctx, raw string) (bool, error)`
  - `const auth.StateCookie = "__Host-tp_wa"`; `func auth.SetStateCookie(w, state string)`, `func auth.ClearStateCookie(w)`

- [ ] **Step 1: Write the failing tests**

```bash
go get github.com/descope/virtualwebauthn@latest
```

Create `internal/auth/passkey_test.go`:

```go
package auth

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/descope/virtualwebauthn"
)

type device struct {
	rp   virtualwebauthn.RelyingParty
	auth virtualwebauthn.Authenticator
	cred virtualwebauthn.Credential
}

func newDevice() *device {
	return &device{
		rp:   virtualwebauthn.RelyingParty{Name: "Thyme & Place", ID: "localhost", Origin: "https://localhost"},
		auth: virtualwebauthn.NewAuthenticator(),
		cred: virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2),
	}
}

func (d *device) register(t *testing.T, s *Service, name, token string) error {
	t.Helper()
	ctx := context.Background()
	opts, state, err := s.BeginRegistration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ao, err := virtualwebauthn.ParseAttestationOptions(string(opts))
	if err != nil {
		t.Fatal(err)
	}
	body := virtualwebauthn.CreateAttestationResponse(d.rp, d.auth, d.cred, *ao)
	err = s.FinishRegistration(ctx, state, httptest.NewRequest("POST", "/", bytes.NewBufferString(body)), name, token)
	if err == nil {
		d.auth.AddCredential(d.cred)
	}
	return err
}

func (d *device) login(t *testing.T, s *Service) error {
	t.Helper()
	ctx := context.Background()
	opts, state, err := s.BeginLogin(ctx)
	if err != nil {
		return err
	}
	ao, err := virtualwebauthn.ParseAssertionOptions(string(opts))
	if err != nil {
		t.Fatal(err)
	}
	body := virtualwebauthn.CreateAssertionResponse(d.rp, d.auth, d.cred, *ao)
	return s.FinishLogin(ctx, state, httptest.NewRequest("POST", "/", bytes.NewBufferString(body)))
}

func TestEnrollRegisterLogin(t *testing.T) {
	ctx := context.Background()
	s, c := newTestService(t, true)
	if _, _, err := s.BeginLogin(ctx); !errors.Is(err, ErrNoPasskeys) {
		t.Fatalf("no passkeys: %v", err)
	}
	tok, exp, err := s.CreateEnrollmentToken(ctx)
	if err != nil || !exp.Equal(c.now().Add(15*time.Minute)) {
		t.Fatalf("token: %v %v", exp, err)
	}
	phone := newDevice()
	if err := phone.register(t, s, "Phone", tok); err != nil {
		t.Fatalf("register: %v", err)
	}
	if ok, _ := s.EnrollmentValid(ctx, tok); ok {
		t.Fatal("token must be single-use")
	}
	if err := phone.login(t, s); err != nil {
		t.Fatalf("login: %v", err)
	}
	pks, _ := s.Passkeys(ctx)
	if len(pks) != 1 || pks[0].Name != "Phone" || pks[0].LastUsedAt == nil {
		t.Fatalf("passkeys = %+v", pks)
	}

	stranger := newDevice()
	stranger.auth.AddCredential(stranger.cred)
	if err := stranger.login(t, s); !errors.Is(err, ErrCeremony) {
		t.Fatalf("unknown credential accepted: %v", err)
	}
}

func TestEnrollmentTokenExpiryAndReuse(t *testing.T) {
	ctx := context.Background()
	s, c := newTestService(t, true)
	tok, _, _ := s.CreateEnrollmentToken(ctx)
	c.add(15*time.Minute + time.Second)
	if ok, _ := s.EnrollmentValid(ctx, tok); ok {
		t.Fatal("expired token valid")
	}
	if err := newDevice().register(t, s, "Late", tok); !errors.Is(err, ErrBadEnrollment) {
		t.Fatalf("expired token registered: %v", err)
	}
}

func TestRenameRemoveAndLastPasskeyGuard(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, true)
	tok, _, _ := s.CreateEnrollmentToken(ctx)
	a := newDevice()
	_ = a.register(t, s, "Phone", tok)
	pks, _ := s.Passkeys(ctx)
	if err := s.RemovePasskey(ctx, pks[0].ID); !errors.Is(err, ErrLastPasskey) {
		t.Fatalf("removed the only passkey: %v", err)
	}
	b := newDevice()
	if err := b.register(t, s, "Key", ""); err != nil { // signed-in add: no token
		t.Fatal(err)
	}
	if err := s.RenamePasskey(ctx, pks[0].ID, "Old phone"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemovePasskey(ctx, pks[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := a.login(t, s); !errors.Is(err, ErrCeremony) {
		t.Fatal("removed passkey still works")
	}
	if err := b.login(t, s); err != nil {
		t.Fatalf("remaining passkey: %v", err)
	}
}

func TestStateTampering(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestService(t, true)
	tok, _, _ := s.CreateEnrollmentToken(ctx)
	d := newDevice()
	_ = d.register(t, s, "Phone", tok)
	if err := s.FinishLogin(ctx, "not-a-sealed-state", httptest.NewRequest("POST", "/", bytes.NewBufferString("{}"))); !errors.Is(err, ErrCeremony) {
		t.Fatalf("tampered state: %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/auth/ -run 'Enroll|Rename|State'`
Expected: FAIL to compile: `s.BeginRegistration undefined`.

- [ ] **Step 3: Implement**

Create `internal/auth/passkey.go`:

```go
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"thymeandplace/internal/store"
)

const (
	StateCookie     = "__Host-tp_wa"
	EnrollmentTTL   = 15 * time.Minute
	maxPasskeyName  = 60
)

var adminUserID = []byte("thymeandplace-admin")

type Passkey struct {
	ID         []byte
	Name       string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// adminUser adapts the single owner to webauthn.User.
type adminUser struct{ creds []webauthn.Credential }

func (u adminUser) WebAuthnID() []byte                         { return adminUserID }
func (u adminUser) WebAuthnName() string                       { return "admin" }
func (u adminUser) WebAuthnDisplayName() string                { return "Owner" }
func (u adminUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func (s *Service) loadUser(ctx context.Context) (adminUser, []store.AdminPasskey, error) {
	rows, err := s.q().ListPasskeys(ctx)
	if err != nil {
		return adminUser{}, nil, err
	}
	u := adminUser{}
	for _, r := range rows {
		var c webauthn.Credential
		if err := json.Unmarshal(r.Credential, &c); err != nil {
			return adminUser{}, nil, fmt.Errorf("passkey %x: %w", r.CredentialID, err)
		}
		c.Authenticator.SignCount = uint32(r.SignCount)
		u.creds = append(u.creds, c)
	}
	return u, rows, nil
}

func (s *Service) Passkeys(ctx context.Context) ([]Passkey, error) {
	_, rows, err := s.loadUser(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Passkey, len(rows))
	for i, r := range rows {
		out[i] = Passkey{ID: r.CredentialID, Name: r.Name, CreatedAt: r.CreatedAt.Time}
		if r.LastUsedAt.Valid {
			t := r.LastUsedAt.Time
			out[i].LastUsedAt = &t
		}
	}
	return out, nil
}

func (s *Service) seal(sd *webauthn.SessionData) (string, error) {
	b, err := json.Marshal(sd)
	if err != nil {
		return "", err
	}
	return b64u.EncodeToString(s.sealer.Seal(b)), nil
}

func (s *Service) unseal(state string) (webauthn.SessionData, error) {
	var sd webauthn.SessionData
	raw, err := b64u.DecodeString(state)
	if err != nil {
		return sd, err
	}
	plain, err := s.sealer.Open(raw)
	if err != nil {
		return sd, err
	}
	return sd, json.Unmarshal(plain, &sd)
}

func (s *Service) BeginLogin(ctx context.Context) ([]byte, string, error) {
	u, _, err := s.loadUser(ctx)
	if err != nil {
		return nil, "", err
	}
	if len(u.creds) == 0 {
		return nil, "", ErrNoPasskeys
	}
	assertion, sd, err := s.wa.BeginLogin(u, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return nil, "", err
	}
	opts, err := json.Marshal(assertion)
	if err != nil {
		return nil, "", err
	}
	state, err := s.seal(sd)
	return opts, state, err
}

func (s *Service) FinishLogin(ctx context.Context, state string, r *http.Request) error {
	sd, err := s.unseal(state)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCeremony, err)
	}
	u, _, err := s.loadUser(ctx)
	if err != nil {
		return err
	}
	cred, err := s.wa.FinishLogin(u, sd, r)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCeremony, err)
	}
	if cred.Authenticator.CloneWarning {
		return fmt.Errorf("%w: authenticator sign count went backwards (possible clone)", ErrCeremony)
	}
	b, _ := json.Marshal(cred)
	return s.q().UpdatePasskeyUse(ctx, store.UpdatePasskeyUseParams{
		CredentialID: cred.ID, SignCount: int64(cred.Authenticator.SignCount), Credential: b, LastUsedAt: ts(s.now()),
	})
}

func (s *Service) BeginRegistration(ctx context.Context) ([]byte, string, error) {
	u, _, err := s.loadUser(ctx)
	if err != nil {
		return nil, "", err
	}
	var exclude []protocol.CredentialDescriptor
	for _, c := range u.creds {
		exclude = append(exclude, c.Descriptor())
	}
	creation, sd, err := s.wa.BeginRegistration(u, webauthn.WithExclusions(exclude))
	if err != nil {
		return nil, "", err
	}
	opts, err := json.Marshal(creation)
	if err != nil {
		return nil, "", err
	}
	state, err := s.seal(sd)
	return opts, state, err
}

// FinishRegistration verifies the new credential and stores it. With an
// enrollment token, the token is consumed in the same transaction, so two
// racing finishes register at most one passkey.
func (s *Service) FinishRegistration(ctx context.Context, state string, r *http.Request, name, enrollToken string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Passkey"
	}
	if len([]rune(name)) > maxPasskeyName {
		name = string([]rune(name)[:maxPasskeyName])
	}
	if enrollToken != "" {
		if ok, err := s.EnrollmentValid(ctx, enrollToken); err != nil {
			return err
		} else if !ok {
			return ErrBadEnrollment
		}
	}
	sd, err := s.unseal(state)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCeremony, err)
	}
	u, _, err := s.loadUser(ctx)
	if err != nil {
		return err
	}
	cred, err := s.wa.FinishRegistration(u, sd, r)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCeremony, err)
	}
	b, err := json.Marshal(cred)
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := store.New(tx)
	now := s.now()
	if enrollToken != "" {
		n, err := q.ConsumeEnrollmentToken(ctx, store.ConsumeEnrollmentTokenParams{TokenHash: HashToken(enrollToken), Now: ts(now)})
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrBadEnrollment
		}
	}
	if err := q.InsertPasskey(ctx, store.InsertPasskeyParams{
		CredentialID: cred.ID, PublicKey: cred.PublicKey, SignCount: int64(cred.Authenticator.SignCount),
		Credential: b, Name: name, CreatedAt: ts(now),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) RenamePasskey(ctx context.Context, id []byte, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > maxPasskeyName {
		return fmt.Errorf("name must be 1 to %d characters", maxPasskeyName)
	}
	n, err := s.q().RenamePasskey(ctx, store.RenamePasskeyParams{CredentialID: id, Name: name})
	if err == nil && n == 0 {
		return errors.New("passkey not found")
	}
	return err
}

func (s *Service) RemovePasskey(ctx context.Context, id []byte) error {
	n, err := s.q().CountPasskeys(ctx)
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastPasskey
	}
	removed, err := s.q().DeletePasskey(ctx, id)
	if err == nil && removed == 0 {
		return errors.New("passkey not found")
	}
	return err
}

func (s *Service) CreateEnrollmentToken(ctx context.Context) (string, time.Time, error) {
	raw, hash := NewToken()
	now := s.now()
	exp := now.Add(EnrollmentTTL)
	err := s.q().InsertEnrollmentToken(ctx, store.InsertEnrollmentTokenParams{TokenHash: hash, CreatedAt: ts(now), ExpiresAt: ts(exp)})
	return raw, exp, err
}

func (s *Service) EnrollmentValid(ctx context.Context, raw string) (bool, error) {
	if raw == "" {
		return false, nil
	}
	return s.q().EnrollmentTokenValid(ctx, store.EnrollmentTokenValidParams{TokenHash: HashToken(raw), Now: ts(s.now())})
}

func SetStateCookie(w http.ResponseWriter, state string) {
	http.SetCookie(w, &http.Cookie{Name: StateCookie, Value: state, Path: "/", MaxAge: 300,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
}

func ClearStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: StateCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
}
```

- [ ] **Step 4: Run the tests**

Run: `go mod tidy && go test ./internal/auth/ -count=1`
Expected: `ok`. If virtualwebauthn's option parser rejects the `{"publicKey":…}` wrapper in your version, pass `string(opts)` through a helper that unwraps `publicKey` in the **test only**. The browser code sends the wrapped form.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/auth
git commit -m "feat(auth): passkey registration/login, single-use enrollment tokens, last-passkey guard

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Admin HTTP shell, login, backup, logout and enrollment

**Files:**
- Create: `internal/admin/admin.go`, `internal/admin/login.go`, `internal/admin/views/models.go`, `internal/admin/views/layout.templ`, `internal/admin/views/login.templ`, `internal/admin/testclient_test.go`, `internal/admin/login_test.go`, `static/admin.js`, `static/admin.css`
- Modify: `static/static.go` (embed `admin.js admin.css`)

**Interfaces:**
- Consumes: `auth.Service`, `web.ClientIP`, `web.Recover`, `web.Timeout`, `web.RequestIDFrom`.
- Produces:
  - `type admin.Deps struct { Auth *auth.Service; Log *slog.Logger; TrustProxy bool }` (Plan 7 adds fields)
  - `func admin.New(d Deps) http.Handler` (serves `/admin` and `/admin/…`)
  - `func admin.SessionFrom(ctx) (auth.Session, bool)`; `func admin.CSRFToken(ctx) string`
  - `var admin.writeRoutes []route`, where `route{Method, Path string}` lists every non-GET admin route, including the Task 7 security routes (for the CSRF test; Plan 7 adds its routes to the literal)
  - `const admin.PreCookie = "__Host-tp_pre"`
  - Views: `views.Layout(LayoutVM)` with children; `views.LoginPage(LoginVM)`, `views.BackupPage(BackupVM)`, `views.EnrollPage(EnrollVM)`, `views.Home()`
  - Routes: `GET /admin/login`, `POST /admin/login/passkey/begin`, `POST /admin/login/passkey/finish`, `GET|POST /admin/login/backup`, `POST /admin/logout`, `GET /admin/enroll?token=`, `POST /admin/enroll/begin?token=`, `POST /admin/enroll/finish?token=&name=`, `GET /admin` (placeholder home; Plan 7 replaces it)

- [ ] **Step 1: Write the browser WebAuthn helper and admin styles**

Create `static/admin.js`:

```js
// Admin: WebAuthn ceremonies over JSON, CSRF header on every POST.
(function () {
  var b64u = {
    enc: function (buf) {
      var s = "", bytes = new Uint8Array(buf);
      for (var i = 0; i < bytes.length; i++) s += String.fromCharCode(bytes[i]);
      return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
    },
    dec: function (s) {
      s = s.replace(/-/g, "+").replace(/_/g, "/");
      while (s.length % 4) s += "=";
      return Uint8Array.from(atob(s), function (c) { return c.charCodeAt(0); }).buffer;
    }
  };
  function csrf() { var m = document.querySelector('meta[name="csrf-token"]'); return m ? m.content : ""; }
  async function post(url, body) {
    var r = await fetch(url, {
      method: "POST", credentials: "same-origin",
      headers: { "X-CSRF-Token": csrf(), "Content-Type": "application/json" },
      body: JSON.stringify(body || {})
    });
    var j = {};
    try { j = await r.json(); } catch (e) { /* non-JSON error */ }
    if (!r.ok) throw new Error(j.error || "Something went wrong. Try again.");
    return j;
  }
  async function login(begin, finish) {
    var pk = (await post(begin)).publicKey;
    pk.challenge = b64u.dec(pk.challenge);
    (pk.allowCredentials || []).forEach(function (c) { c.id = b64u.dec(c.id); });
    var cred = await navigator.credentials.get({ publicKey: pk });
    return post(finish, {
      id: cred.id, rawId: b64u.enc(cred.rawId), type: cred.type,
      response: {
        clientDataJSON: b64u.enc(cred.response.clientDataJSON),
        authenticatorData: b64u.enc(cred.response.authenticatorData),
        signature: b64u.enc(cred.response.signature),
        userHandle: cred.response.userHandle ? b64u.enc(cred.response.userHandle) : null
      }
    });
  }
  async function register(begin, finish) {
    var pk = (await post(begin)).publicKey;
    pk.challenge = b64u.dec(pk.challenge);
    pk.user.id = b64u.dec(pk.user.id);
    (pk.excludeCredentials || []).forEach(function (c) { c.id = b64u.dec(c.id); });
    var cred = await navigator.credentials.create({ publicKey: pk });
    return post(finish, {
      id: cred.id, rawId: b64u.enc(cred.rawId), type: cred.type,
      response: {
        clientDataJSON: b64u.enc(cred.response.clientDataJSON),
        attestationObject: b64u.enc(cred.response.attestationObject),
        transports: cred.response.getTransports ? cred.response.getTransports() : []
      }
    });
  }
  document.addEventListener("click", async function (e) {
    var b = e.target.closest("[data-webauthn]");
    if (!b) return;
    e.preventDefault();
    var status = document.getElementById("wa-status");
    try {
      var finish = b.dataset.finish;
      if (b.dataset.nameInput) {
        var n = document.getElementById(b.dataset.nameInput).value.trim() || "Passkey";
        finish += (finish.indexOf("?") >= 0 ? "&" : "?") + "name=" + encodeURIComponent(n);
      }
      var res = b.dataset.webauthn === "register" ? await register(b.dataset.begin, finish) : await login(b.dataset.begin, finish);
      location.assign(res.redirect || location.href);
    } catch (err) {
      if (status) status.textContent = err.message || String(err);
    }
  });
})();
```

Create `static/admin.css`:

```css
body { margin:0; font:15px/1.5 system-ui, sans-serif; color:#1f2a1f; background:#f6f5f0; }
header.admin { display:flex; gap:1rem; align-items:center; padding:.6rem 1rem; background:#243024; color:#fff; flex-wrap:wrap; }
header.admin a { color:#fff; } header.admin form { margin-left:auto; }
main { max-width: 64rem; margin: 0 auto; padding: 1rem; }
.error, .field-error { color:#b3261e; } .ok { color:#2f7d32; }
table { border-collapse: collapse; width:100%; } th, td { text-align:left; padding:.3rem .5rem; border-bottom:1px solid #ddd; }
label { display:block; margin-top:.6rem; } input, select, textarea { font:inherit; padding:.3rem; }
button { font:inherit; padding:.4rem .8rem; cursor:pointer; }
.bars { display:grid; grid-template-columns: 10rem repeat(12, 1fr); gap:2px; font-size:.75rem; }
.bars span { padding:.15rem; text-align:center; background:#e3e0d6; }
.bars .s-peak { background:#2f7d32; color:#fff; } .bars .s-in { background:#7cb342; }
.bars .s-store { background:#c9a227; } .bars .s-year { background:#6d8fb3; color:#fff; }
.diff .before { background:#fde7e7; } .diff .after { background:#e6f4e6; }
.warn { border:1px solid #b3261e; padding:.6rem; border-radius:.4rem; }
```

Update `static/static.go`:

```go
//go:embed htmx.min.js app.css app.js admin.js admin.css
var FS embed.FS
```

- [ ] **Step 2: Write the admin views**

Create `internal/admin/views/models.go`:

```go
// Package views holds the admin templ components.
package views

import "encoding/json"

const htmxConfig = `{"responseHandling":[{"code":"204","swap":false},{"code":"[23]..","swap":true},{"code":"[45]..","swap":true,"error":true}],"includeIndicatorStyles":false}`

type LayoutVM struct {
	Title    string
	CSRF     string
	SignedIn bool
}

// csrfHeaders is the hx-headers value for <body>.
func csrfHeaders(token string) string {
	b, _ := json.Marshal(map[string]string{"X-CSRF-Token": token})
	return string(b)
}

type LoginVM struct {
	Layout        LayoutVM
	BackupEnabled bool
	Message       string
}

type BackupVM struct {
	Layout  LayoutVM
	Message string
}

type EnrollVM struct {
	Layout LayoutVM
	Token  string
	Valid  bool
}
```

Create `internal/admin/views/layout.templ`:

```templ
package views

templ Layout(vm LayoutVM) {
	<!DOCTYPE html>
	<html lang="en">
		<head>
			<meta charset="utf-8"/>
			<meta name="viewport" content="width=device-width, initial-scale=1"/>
			<meta name="robots" content="noindex"/>
			<meta name="csrf-token" content={ vm.CSRF }/>
			<meta name="htmx-config" content={ htmxConfig }/>
			<title>{ vm.Title } · Thyme &amp; Place admin</title>
			<link rel="stylesheet" href="/static/admin.css"/>
			<script src="/static/htmx.min.js" defer></script>
			<script src="/static/admin.js" defer></script>
		</head>
		<body hx-headers={ csrfHeaders(vm.CSRF) }>
			<header class="admin">
				<strong>Thyme &amp; Place admin</strong>
				if vm.SignedIn {
					<nav>
						<a href="/admin">Catalog</a>
						<a href="/admin/import">Import</a>
						<a href="/admin/export">Export</a>
						<a href="/admin/history">History</a>
						<a href="/admin/security">Security</a>
					</nav>
					<form method="post" action="/admin/logout">
						<input type="hidden" name="csrf" value={ vm.CSRF }/>
						<button type="submit">Sign out</button>
					</form>
				}
			</header>
			<main>
				{ children... }
			</main>
		</body>
	</html>
}

templ Home(vm LayoutVM) {
	@Layout(vm) {
		<h1>Signed in</h1>
		<p>The catalog screens arrive in the next plan.</p>
	}
}
```

Create `internal/admin/views/login.templ`:

```templ
package views

templ LoginPage(vm LoginVM) {
	@Layout(vm.Layout) {
		<h1>Sign in</h1>
		if vm.Message != "" {
			<p class="error">{ vm.Message }</p>
		}
		<button type="button" data-webauthn="login" data-begin="/admin/login/passkey/begin" data-finish="/admin/login/passkey/finish">Sign in with a passkey</button>
		<p id="wa-status" class="error" role="status"></p>
		if vm.BackupEnabled {
			<p><a href="/admin/login/backup">Use backup login</a></p>
		}
	}
}

templ BackupPage(vm BackupVM) {
	@Layout(vm.Layout) {
		<h1>Backup login</h1>
		if vm.Message != "" {
			<p class="error">{ vm.Message }</p>
		}
		<form method="post" action="/admin/login/backup">
			<input type="hidden" name="csrf" value={ vm.Layout.CSRF }/>
			<label>Password <input type="password" name="password" autocomplete="current-password" required/></label>
			<label>Authenticator code <input type="text" name="code" inputmode="numeric" pattern="[0-9]{6}" autocomplete="one-time-code" required/></label>
			<button type="submit">Sign in</button>
		</form>
		<p><a href="/admin/login">Use a passkey instead</a></p>
	}
}

templ EnrollPage(vm EnrollVM) {
	@Layout(vm.Layout) {
		<h1>Register a passkey</h1>
		if vm.Valid {
			<label>Name for this passkey <input id="pk-name" type="text" maxlength="60" placeholder="e.g. Phone"/></label>
			<button type="button" data-webauthn="register" data-name-input="pk-name"
				data-begin={ "/admin/enroll/begin?token=" + vm.Token } data-finish={ "/admin/enroll/finish?token=" + vm.Token }>Register passkey</button>
			<p id="wa-status" class="error" role="status"></p>
		} else {
			<p class="error">This enrollment link is invalid or has expired. Run <code>admin enroll</code> again.</p>
		}
	}
}
```

Run: `go tool templ generate && go build ./internal/admin/views/`
Expected: compiles.

- [ ] **Step 3: Write the admin shell and login handlers**

Create `internal/admin/admin.go`:

```go
// Package admin serves /admin for the single owner.
package admin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-h/templ"

	"thymeandplace/internal/auth"
	"thymeandplace/internal/web"
)

const (
	PreCookie    = "__Host-tp_pre"
	maxBody      = 6 << 20
	adminTimeout = 30 * time.Second
)

type Deps struct {
	Auth       *auth.Service
	Log        *slog.Logger
	TrustProxy bool
}

type Admin struct {
	d   Deps
	mux *http.ServeMux
}

type route struct{ Method, Path string }

// writeRoutes lists every non-GET route; the CSRF test walks it.
var writeRoutes = []route{
	{"POST", "/admin/login/passkey/begin"}, {"POST", "/admin/login/passkey/finish"},
	{"POST", "/admin/login/backup"}, {"POST", "/admin/logout"},
	{"POST", "/admin/enroll/begin?token=x"}, {"POST", "/admin/enroll/finish?token=x"},
	{"POST", "/admin/security/reauth/begin"}, {"POST", "/admin/security/reauth/finish"},
	{"POST", "/admin/security/passkeys/begin"}, {"POST", "/admin/security/passkeys/finish"},
	{"POST", "/admin/security/passkeys/AAAA/rename"}, {"POST", "/admin/security/passkeys/AAAA/remove"},
	// Plan 7 adds the catalog write routes here.
}

type ctxKey int

const (
	sessionKey ctxKey = iota
	csrfKey
)

func SessionFrom(ctx context.Context) (auth.Session, bool) {
	s, ok := ctx.Value(sessionKey).(auth.Session)
	return s, ok
}

func CSRFToken(ctx context.Context) string {
	s, _ := ctx.Value(csrfKey).(string)
	return s
}

func New(d Deps) http.Handler {
	a := &Admin{d: d, mux: http.NewServeMux()}
	m := a.mux
	m.HandleFunc("GET /admin/login", a.loginPage)
	m.HandleFunc("POST /admin/login/passkey/begin", a.passkeyBegin)
	m.HandleFunc("POST /admin/login/passkey/finish", a.passkeyFinish)
	m.HandleFunc("GET /admin/login/backup", a.backupPage)
	m.HandleFunc("POST /admin/login/backup", a.backupSubmit)
	m.HandleFunc("POST /admin/logout", a.logout)
	m.HandleFunc("GET /admin/enroll", a.enrollPage)
	m.HandleFunc("POST /admin/enroll/begin", a.enrollBegin)
	m.HandleFunc("POST /admin/enroll/finish", a.enrollFinish)

	m.Handle("GET /admin", a.requireAuth(http.HandlerFunc(a.home)))
	m.Handle("/admin/", a.requireAuth(http.NotFoundHandler()))
	a.registerSecurity() // Task 7
	a.registerCatalog()  // Plan 7; a no-op until then

	var h http.Handler = m
	h = a.csrf(h)
	h = a.loadSession(h)
	h = web.Timeout(adminTimeout)(h)
	h = http.MaxBytesHandler(h, maxBody)
	h = securityHeaders(h)
	h = web.Recover(d.Log, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Something went wrong. The error was logged.", http.StatusInternalServerError)
	})(h)
	return h
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Robots-Tag", "noindex")
		h.Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// loadSession puts a valid session and the right CSRF token in the context.
// Without a session it ensures the pre-session cookie exists.
func (a *Admin) loadSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if c, err := r.Cookie(auth.CookieName); err == nil {
			sess, ok, err := a.d.Auth.LookupSession(ctx, c.Value)
			if err != nil {
				a.d.Log.Error("session lookup failed", "err", err, "request_id", web.RequestIDFrom(ctx))
			}
			if ok {
				ctx = context.WithValue(ctx, sessionKey, sess)
				ctx = context.WithValue(ctx, csrfKey, sess.CSRF)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			auth.ClearSessionCookie(w)
		}
		pre := ""
		if c, err := r.Cookie(PreCookie); err == nil && len(c.Value) >= 32 {
			pre = c.Value
		} else if r.Method == http.MethodGet {
			pre, _ = auth.NewToken()
			http.SetCookie(w, &http.Cookie{Name: PreCookie, Value: pre, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, csrfKey, pre)))
	})
}

func (a *Admin) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		want := CSRFToken(r.Context())
		got := r.Header.Get("X-CSRF-Token")
		if got == "" {
			got = r.PostFormValue("csrf")
		}
		if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
			http.Error(w, "Your form expired. Reload the page and try again.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAuth redirects anonymous requests to the login page. HTMX requests
// get HX-Redirect so a login page is never swapped into a fragment.
func (a *Admin) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := SessionFrom(r.Context()); ok {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", "/admin/login")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Sign in again.", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
	})
}

func (a *Admin) render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := c.Render(r.Context(), w); err != nil {
		a.d.Log.Warn("admin render failed", "err", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (a *Admin) ip(r *http.Request) string { return web.ClientIP(r, a.d.TrustProxy) }

// registerCatalog is replaced in Plan 7.
func (a *Admin) registerCatalog() {}
```

Create `internal/admin/login.go`:

```go
package admin

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"

	"thymeandplace/internal/admin/views"
	"thymeandplace/internal/auth"
)

const maxCeremonyBody = 64 << 10

func (a *Admin) layout(r *http.Request, title string) views.LayoutVM {
	_, signedIn := SessionFrom(r.Context())
	return views.LayoutVM{Title: title, CSRF: CSRFToken(r.Context()), SignedIn: signedIn}
}

func (a *Admin) home(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, http.StatusOK, views.Home(a.layout(r, "Catalog")))
}

func (a *Admin) loginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := SessionFrom(r.Context()); ok {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	a.render(w, r, http.StatusOK, views.LoginPage(views.LoginVM{Layout: a.layout(r, "Sign in"), BackupEnabled: a.d.Auth.BackupEnabled()}))
}

func (a *Admin) startSession(w http.ResponseWriter, r *http.Request, method string) error {
	raw, _, err := a.d.Auth.CreateSession(r.Context(), method)
	if err != nil {
		return err
	}
	auth.SetSessionCookie(w, raw)
	auth.ClearStateCookie(w)
	return nil
}

func (a *Admin) passkeyBegin(w http.ResponseWriter, r *http.Request) {
	opts, state, err := a.d.Auth.BeginLogin(r.Context())
	if errors.Is(err, auth.ErrNoPasskeys) {
		jsonError(w, http.StatusConflict, "No passkeys are registered yet. Run admin enroll on the server.")
		return
	}
	if err != nil {
		a.d.Log.Error("passkey begin failed", "err", err)
		jsonError(w, http.StatusInternalServerError, "Couldn't start passkey sign-in.")
		return
	}
	auth.SetStateCookie(w, state)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(opts)
}

// limitCeremonyBody caps and pre-reads the WebAuthn JSON so a bad body is a
// 4xx, never a 500.
func limitCeremonyBody(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxCeremonyBody)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return nil
}

func (a *Admin) passkeyFinish(w http.ResponseWriter, r *http.Request) {
	ctx, ip := r.Context(), a.ip(r)
	if ok, err := a.d.Auth.AllowAttempt(ctx, ip); err != nil || !ok {
		a.d.Auth.LogAttempt(ctx, ip, "passkey", "rate_limited")
		jsonError(w, http.StatusTooManyRequests, "Too many sign-in attempts. Wait 15 minutes and try again.")
		return
	}
	state := ""
	if c, err := r.Cookie(auth.StateCookie); err == nil {
		state = c.Value
	}
	err := limitCeremonyBody(w, r)
	if err == nil {
		err = a.d.Auth.FinishLogin(ctx, state, r)
	}
	if err != nil {
		a.d.Auth.LogAttempt(ctx, ip, "passkey", "failure")
		if !errors.Is(err, auth.ErrCeremony) {
			a.d.Log.Warn("passkey finish failed", "err", err)
		}
		jsonError(w, http.StatusUnauthorized, "That passkey didn't work. Try again, or use backup login.")
		return
	}
	a.d.Auth.LogAttempt(ctx, ip, "passkey", "success")
	if s, ok := SessionFrom(ctx); ok { // re-auth from an existing session
		_ = a.d.Auth.MarkReauthed(ctx, s)
		writeJSON(w, http.StatusOK, map[string]string{"redirect": "/admin"})
		return
	}
	if err := a.startSession(w, r, "passkey"); err != nil {
		jsonError(w, http.StatusInternalServerError, "Signed in, but couldn't start a session.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect": "/admin"})
}

func (a *Admin) backupPage(w http.ResponseWriter, r *http.Request) {
	if !a.d.Auth.BackupEnabled() {
		http.NotFound(w, r)
		return
	}
	a.render(w, r, http.StatusOK, views.BackupPage(views.BackupVM{Layout: a.layout(r, "Backup login")}))
}

func (a *Admin) backupSubmit(w http.ResponseWriter, r *http.Request) {
	ctx, ip := r.Context(), a.ip(r)
	if !a.d.Auth.BackupEnabled() {
		a.d.Auth.LogAttempt(ctx, ip, "backup", "disabled")
		http.NotFound(w, r)
		return
	}
	page := func(status int, msg string) {
		a.render(w, r, status, views.BackupPage(views.BackupVM{Layout: a.layout(r, "Backup login"), Message: msg}))
	}
	if ok, err := a.d.Auth.AllowAttempt(ctx, ip); err != nil || !ok {
		a.d.Auth.LogAttempt(ctx, ip, "backup", "rate_limited")
		page(http.StatusTooManyRequests, "Too many sign-in attempts. Wait 15 minutes and try again.")
		return
	}
	err := a.d.Auth.BackupLogin(ctx, r.PostFormValue("password"), r.PostFormValue("code"))
	var locked *auth.LockedError
	switch {
	case err == nil:
		a.d.Auth.LogAttempt(ctx, ip, "backup", "success")
		if err := a.startSession(w, r, "backup"); err != nil {
			page(http.StatusInternalServerError, "Signed in, but couldn't start a session.")
			return
		}
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	case errors.As(err, &locked):
		a.d.Auth.LogAttempt(ctx, ip, "backup", "locked")
		page(http.StatusForbidden, fmt.Sprintf("Backup login is locked until %s UTC. Passkey sign-in still works.", locked.Until.UTC().Format("15:04")))
	case errors.Is(err, auth.ErrBackupNotSet):
		a.d.Auth.LogAttempt(ctx, ip, "backup", "failure")
		page(http.StatusUnauthorized, "Backup login isn't set up. Run admin set-backup on the server.")
	case errors.Is(err, auth.ErrBadCredentials):
		a.d.Auth.LogAttempt(ctx, ip, "backup", "failure")
		page(http.StatusUnauthorized, "Password or code is wrong.")
	default:
		a.d.Log.Error("backup login error", "err", err)
		page(http.StatusInternalServerError, "Something went wrong. Try again.")
	}
}

func (a *Admin) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.CookieName); err == nil {
		_ = a.d.Auth.DeleteSession(r.Context(), c.Value)
	}
	auth.ClearSessionCookie(w)
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func (a *Admin) enrollPage(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("token")
	ok, err := a.d.Auth.EnrollmentValid(r.Context(), tok)
	if err != nil {
		a.d.Log.Error("enrollment check failed", "err", err)
	}
	status := http.StatusOK
	if !ok {
		status = http.StatusBadRequest
	}
	a.render(w, r, status, views.EnrollPage(views.EnrollVM{Layout: a.layout(r, "Register a passkey"), Token: tok, Valid: ok}))
}

func (a *Admin) enrollBegin(w http.ResponseWriter, r *http.Request) {
	if ok, _ := a.d.Auth.EnrollmentValid(r.Context(), r.URL.Query().Get("token")); !ok {
		jsonError(w, http.StatusBadRequest, "This enrollment link is invalid or has expired. Run admin enroll again.")
		return
	}
	opts, state, err := a.d.Auth.BeginRegistration(r.Context())
	if err != nil {
		a.d.Log.Error("registration begin failed", "err", err)
		jsonError(w, http.StatusInternalServerError, "Couldn't start passkey registration.")
		return
	}
	auth.SetStateCookie(w, state)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(opts)
}

func (a *Admin) enrollFinish(w http.ResponseWriter, r *http.Request) {
	ctx, ip := r.Context(), a.ip(r)
	state := ""
	if c, err := r.Cookie(auth.StateCookie); err == nil {
		state = c.Value
	}
	err := limitCeremonyBody(w, r)
	if err == nil {
		err = a.d.Auth.FinishRegistration(ctx, state, r, r.URL.Query().Get("name"), r.URL.Query().Get("token"))
	}
	switch {
	case errors.Is(err, auth.ErrBadEnrollment):
		a.d.Auth.LogAttempt(ctx, ip, "enroll", "failure")
		jsonError(w, http.StatusBadRequest, "This enrollment link is invalid or has expired. Run admin enroll again.")
		return
	case err != nil:
		a.d.Auth.LogAttempt(ctx, ip, "enroll", "failure")
		jsonError(w, http.StatusBadRequest, "The passkey couldn't be registered. Try again.")
		return
	}
	a.d.Auth.LogAttempt(ctx, ip, "enroll", "success")
	if err := a.startSession(w, r, "enroll"); err != nil {
		jsonError(w, http.StatusInternalServerError, "Registered, but couldn't start a session. Sign in.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect": "/admin"})
}
```

`a.registerSecurity()` is implemented in Task 7. Until then, add `func (a *Admin) registerSecurity() {}` to `admin.go` and remove it in Task 7.

- [ ] **Step 4: Write the test client and login tests**

Create `internal/admin/testclient_test.go`:

```go
package admin

import (
	"bytes"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/descope/virtualwebauthn"
	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/auth"
	"thymeandplace/internal/dbtest"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

type env struct {
	h     http.Handler
	auth  *auth.Service
	clock *clock
	pool  *pgxpool.Pool
}

func newEnv(t *testing.T, backup bool) *env {
	t.Helper()
	c := &clock{t: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)}
	pool := dbtest.New(t)
	svc, err := auth.NewService(pool, auth.Config{
		RPID: "localhost", RPOrigins: []string{"https://localhost"},
		SecretKey: bytes.Repeat([]byte{3}, 32), BackupEnabled: backup, Now: c.now,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &env{h: New(Deps{Auth: svc, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}), auth: svc, clock: c, pool: pool}
}

var csrfRE = regexp.MustCompile(`name="csrf-token" content="([^"]*)"`)

// client keeps cookies and the latest CSRF token like a browser tab.
type client struct {
	t    *testing.T
	h    http.Handler
	jar  map[string]string
	csrf string
	ip   string
}

func (e *env) client(t *testing.T) *client {
	return &client{t: t, h: e.h, jar: map[string]string{}, ip: "198.51.100.7"}
}

func (c *client) do(method, path string, body io.Reader, hdr map[string]string) *httptest.ResponseRecorder {
	c.t.Helper()
	r := httptest.NewRequest(method, path, body)
	r.RemoteAddr = c.ip + ":1"
	for k, v := range c.jar {
		r.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	if c.csrf != "" && method != "GET" {
		r.Header.Set("X-CSRF-Token", c.csrf)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, r)
	for _, ck := range rec.Result().Cookies() {
		if ck.MaxAge < 0 || ck.Value == "" {
			delete(c.jar, ck.Name)
		} else {
			c.jar[ck.Name] = ck.Value
		}
	}
	if m := csrfRE.FindStringSubmatch(rec.Body.String()); m != nil {
		c.csrf = html.UnescapeString(m[1])
	}
	return rec
}

func (c *client) get(path string) *httptest.ResponseRecorder { return c.do("GET", path, nil, nil) }

func (c *client) postForm(path string, form url.Values) *httptest.ResponseRecorder {
	return c.do("POST", path, strings.NewReader(form.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
}

func (c *client) postJSON(path, body string) *httptest.ResponseRecorder {
	return c.do("POST", path, strings.NewReader(body), map[string]string{"Content-Type": "application/json"})
}

type device struct {
	rp   virtualwebauthn.RelyingParty
	auth virtualwebauthn.Authenticator
	cred virtualwebauthn.Credential
}

func newDevice() *device {
	return &device{
		rp:   virtualwebauthn.RelyingParty{Name: "Thyme & Place", ID: "localhost", Origin: "https://localhost"},
		auth: virtualwebauthn.NewAuthenticator(),
		cred: virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2),
	}
}

func (d *device) register(c *client, begin, finish string) *httptest.ResponseRecorder {
	c.t.Helper()
	rec := c.postJSON(begin, "{}")
	if rec.Code != 200 {
		return rec
	}
	opts, err := virtualwebauthn.ParseAttestationOptions(rec.Body.String())
	if err != nil {
		c.t.Fatal(err)
	}
	out := c.postJSON(finish, virtualwebauthn.CreateAttestationResponse(d.rp, d.auth, d.cred, *opts))
	if out.Code == 200 {
		d.auth.AddCredential(d.cred)
	}
	return out
}

func (d *device) login(c *client, begin, finish string) *httptest.ResponseRecorder {
	c.t.Helper()
	rec := c.postJSON(begin, "{}")
	if rec.Code != 200 {
		return rec
	}
	opts, err := virtualwebauthn.ParseAssertionOptions(rec.Body.String())
	if err != nil {
		c.t.Fatal(err)
	}
	return c.postJSON(finish, virtualwebauthn.CreateAssertionResponse(d.rp, d.auth, d.cred, *opts))
}

// enrolled returns a signed-in client and the device it registered.
func (e *env) enrolled(t *testing.T) (*client, *device) {
	t.Helper()
	tok, _, err := e.auth.CreateEnrollmentToken(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	c := e.client(t)
	if rec := c.get("/admin/enroll?token=" + tok); rec.Code != 200 {
		t.Fatalf("enroll page %d", rec.Code)
	}
	d := newDevice()
	if rec := d.register(c, "/admin/enroll/begin?token="+tok, "/admin/enroll/finish?token="+tok+"&name=Phone"); rec.Code != 200 {
		t.Fatalf("enroll finish %d %s", rec.Code, rec.Body.String())
	}
	c.get("/admin") // pick up the session CSRF token
	return c, d
}
```

Create `internal/admin/login_test.go`:

```go
package admin

import (
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestSecurityHeaders(t *testing.T) {
	e := newEnv(t, true)
	rec := e.client(t).get("/admin/login")
	h := rec.Header()
	if !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'self'") || h.Get("X-Frame-Options") != "DENY" ||
		h.Get("Referrer-Policy") != "no-referrer" || h.Get("Cache-Control") != "no-store" || h.Get("X-Robots-Tag") != "noindex" {
		t.Fatalf("headers = %v", h)
	}
}

func TestEnrollThenPasskeyLogin(t *testing.T) {
	e := newEnv(t, true)
	c, d := e.enrolled(t)
	if rec := c.get("/admin"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Signed in") {
		t.Fatalf("after enroll: %d", rec.Code)
	}
	if rec := c.postForm("/admin/logout", url.Values{}); rec.Code != 303 {
		t.Fatalf("logout %d", rec.Code)
	}
	if rec := c.get("/admin"); rec.Code != 303 {
		t.Fatal("session survived logout")
	}
	c.get("/admin/login")
	if rec := d.login(c, "/admin/login/passkey/begin", "/admin/login/passkey/finish"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"redirect":"/admin"`) {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.get("/admin"); rec.Code != 200 {
		t.Fatal("not signed in after passkey login")
	}
}

// Review Focus 4: racing enrollment finishes register one passkey at most.
func TestEnrollmentLinkSingleUseUnderRace(t *testing.T) {
	e := newEnv(t, true)
	tok, _, _ := e.auth.CreateEnrollmentToken(t.Context())
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := e.client(t)
			c.get("/admin/enroll?token=" + tok)
			codes[i] = newDevice().register(c, "/admin/enroll/begin?token="+tok, "/admin/enroll/finish?token="+tok).Code
		}(i)
	}
	wg.Wait()
	pks, _ := e.auth.Passkeys(t.Context())
	if len(pks) != 1 || !((codes[0] == 200) != (codes[1] == 200)) {
		t.Fatalf("codes=%v passkeys=%d", codes, len(pks))
	}
	c := e.client(t)
	if rec := c.get("/admin/enroll?token=" + tok); rec.Code != 400 || !strings.Contains(rec.Body.String(), "invalid or has expired") {
		t.Fatalf("used link: %d", rec.Code)
	}
}

func TestEnrollmentLinkExpires(t *testing.T) {
	e := newEnv(t, true)
	tok, _, _ := e.auth.CreateEnrollmentToken(t.Context())
	e.clock.add(16 * time.Minute)
	if rec := e.client(t).get("/admin/enroll?token=" + tok); rec.Code != 400 {
		t.Fatalf("expired link: %d", rec.Code)
	}
}

func TestBackupLoginFlow(t *testing.T) {
	e := newEnv(t, true)
	uri, err := e.auth.SetBackup(t.Context(), "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(uri)
	secret := u.Query().Get("secret")
	c := e.client(t)
	c.get("/admin/login/backup")
	if rec := c.postForm("/admin/login/backup", url.Values{"password": {"nope-nope-nope"}, "code": {"000000"}}); rec.Code != 401 ||
		!strings.Contains(rec.Body.String(), "Password or code is wrong.") {
		t.Fatalf("bad creds: %d", rec.Code)
	}
	cd, _ := totp.GenerateCode(secret, e.clock.now())
	if rec := c.postForm("/admin/login/backup", url.Values{"password": {"correct horse battery staple"}, "code": {cd}}); rec.Code != 303 {
		t.Fatalf("good creds: %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.get("/admin"); rec.Code != 200 {
		t.Fatal("not signed in via backup")
	}
}

func TestPasskeyWorksDuringBackupLockout(t *testing.T) {
	e := newEnv(t, true)
	_, d := e.enrolled(t)
	_, _ = e.auth.SetBackup(t.Context(), "correct horse battery staple")
	for i := 0; i < 5; i++ {
		_ = e.auth.BackupLogin(t.Context(), "wrong", "000000")
	}
	c := e.client(t)
	c.ip = "192.0.2.50" // separate IP: the lockout is global, the rate limit is per IP
	c.get("/admin/login/backup")
	if rec := c.postForm("/admin/login/backup", url.Values{"password": {"x"}, "code": {"000000"}}); !strings.Contains(rec.Body.String(), "Passkey sign-in still works") {
		t.Fatalf("lockout message missing: %s", rec.Body.String())
	}
	c.get("/admin/login")
	if rec := d.login(c, "/admin/login/passkey/begin", "/admin/login/passkey/finish"); rec.Code != 200 {
		t.Fatalf("passkey during lockout: %d", rec.Code)
	}
}

func TestBackupDisabled(t *testing.T) {
	e := newEnv(t, false)
	c := e.client(t)
	if rec := c.get("/admin/login"); strings.Contains(rec.Body.String(), "Use backup login") {
		t.Fatal("backup link shown while disabled")
	}
	if rec := c.get("/admin/login/backup"); rec.Code != 404 {
		t.Fatalf("backup page %d", rec.Code)
	}
}

func TestLoginRateLimitPerIP(t *testing.T) {
	e := newEnv(t, true)
	_, _ = e.auth.SetBackup(t.Context(), "correct horse battery staple")
	c := e.client(t)
	c.get("/admin/login/backup")
	for i := 0; i < 5; i++ {
		c.postForm("/admin/login/backup", url.Values{"password": {"x"}, "code": {"000000"}})
	}
	if rec := c.postForm("/admin/login/backup", url.Values{"password": {"x"}, "code": {"000000"}}); rec.Code != 429 {
		t.Fatalf("6th attempt: %d", rec.Code)
	}
	if rec := c.postJSON("/admin/login/passkey/finish", "{}"); rec.Code != 429 {
		t.Fatalf("limit must span all login routes: %d", rec.Code)
	}
}

// Review Focus 5: junk on a finish endpoint is a 4xx JSON error and counts.
func TestCeremonyGarbageIs4xx(t *testing.T) {
	e := newEnv(t, true)
	_, _ = e.enrolled(t)
	c := e.client(t)
	c.ip = "192.0.2.77"
	c.get("/admin/login")
	for _, body := range []string{"not json", strings.Repeat("x", 100<<10), `{"id":1}`} {
		rec := c.postJSON("/admin/login/passkey/finish", body)
		if rec.Code < 400 || rec.Code >= 500 || !strings.Contains(rec.Body.String(), `"error"`) {
			t.Fatalf("body %.10q: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	var n int
	_ = e.pool.QueryRow(t.Context(), `SELECT count(*) FROM admin_login_log WHERE ip = '192.0.2.77' AND method = 'passkey' AND outcome = 'failure'`).Scan(&n)
	if n != 3 {
		t.Fatalf("garbage attempts logged = %d, want 3", n)
	}
}

func TestUnauthenticatedRedirects(t *testing.T) {
	e := newEnv(t, true)
	c := e.client(t)
	for _, p := range []string{"/admin", "/admin/anything", "/admin/security"} {
		if rec := c.get(p); rec.Code != 303 || rec.Header().Get("Location") != "/admin/login" {
			t.Fatalf("%s: %d %s", p, rec.Code, rec.Header().Get("Location"))
		}
	}
	// Review Focus 3: HTMX requests get HX-Redirect.
	rec := c.do("GET", "/admin", nil, map[string]string{"HX-Request": "true"})
	if rec.Header().Get("HX-Redirect") != "/admin/login" {
		t.Fatalf("HX-Redirect missing: %v", rec.Header())
	}
}

func TestCSRFOnEveryWriteRoute(t *testing.T) {
	e := newEnv(t, true)
	signedIn, _ := e.enrolled(t)
	anon := e.client(t)
	anon.get("/admin/login")
	for _, c := range []*client{signedIn, anon} {
		for _, rt := range writeRoutes {
			for _, token := range []string{"", "wrong-token-wrong-token-wrong-token"} {
				saved := c.csrf
				c.csrf = ""
				hdr := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
				if token != "" {
					hdr["X-CSRF-Token"] = token
				}
				rec := c.do(rt.Method, rt.Path, strings.NewReader(""), hdr)
				c.csrf = saved
				if rec.Code != 403 {
					t.Fatalf("%s %s with token %q: %d", rt.Method, rt.Path, token, rec.Code)
				}
			}
		}
	}
}
```

- [ ] **Step 5: Run the tests**

Run: `go tool templ generate && go mod tidy && go test ./internal/admin/ -count=1`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum static internal/admin
git commit -m "feat(admin): hardened admin shell with passkey, backup, enrollment and CSRF

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Security page (passkeys, re-auth) and mounting /admin

**Files:**
- Create: `internal/admin/security.go`, `internal/admin/views/security.templ`, `internal/admin/security_test.go`
- Modify: `internal/admin/admin.go` (remove the `registerSecurity` stub), `internal/admin/views/models.go` (add `SecurityVM`, `PasskeyRow`), `cmd/server/app.go` (config + mount)

**Interfaces:**
- Consumes: `auth.Service` passkey methods, `auth.Session.RecentAuth`.
- Produces:
  - Routes: `GET /admin/security`; `POST /admin/security/reauth/begin`, `POST /admin/security/reauth/finish`; `POST /admin/security/passkeys/begin`, `POST /admin/security/passkeys/finish?name=`; `POST /admin/security/passkeys/{id}/rename`; `POST /admin/security/passkeys/{id}/remove` (`{id}` = base64url credential ID)
  - `cmd/server` config: `AdminSecretKey []byte`, `AdminRPID string`, `AdminOrigin string`, `AdminBackup bool`; `/admin` is mounted only when `ADMIN_SECRET_KEY` is set (otherwise a WARN log)

- [ ] **Step 1: Write the failing tests**

Create `internal/admin/security_test.go`:

```go
package admin

import (
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSecurityPageListsPasskeys(t *testing.T) {
	e := newEnv(t, true)
	c, _ := e.enrolled(t)
	rec := c.get("/admin/security")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Phone") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestAddPasskeyNeedsRecentAuth(t *testing.T) {
	e := newEnv(t, true)
	c, phone := e.enrolled(t)
	key := newDevice()
	if rec := key.register(c, "/admin/security/passkeys/begin", "/admin/security/passkeys/finish?name=Key"); rec.Code != 200 {
		t.Fatalf("add within 5 min: %d %s", rec.Code, rec.Body.String())
	}
	e.clock.add(6 * time.Minute)
	c.get("/admin/security")
	third := newDevice()
	if rec := third.register(c, "/admin/security/passkeys/begin", "/admin/security/passkeys/finish?name=Third"); rec.Code != 403 {
		t.Fatalf("add after 6 min must need re-auth: %d", rec.Code)
	}
	if rec := phone.login(c, "/admin/security/reauth/begin", "/admin/security/reauth/finish"); rec.Code != 200 {
		t.Fatalf("reauth: %d %s", rec.Code, rec.Body.String())
	}
	if rec := third.register(c, "/admin/security/passkeys/begin", "/admin/security/passkeys/finish?name=Third"); rec.Code != 200 {
		t.Fatalf("add after reauth: %d", rec.Code)
	}
}

// Review Focus 1: the only passkey cannot be removed.
func TestRemovePasskey(t *testing.T) {
	e := newEnv(t, true)
	c, _ := e.enrolled(t)
	pks, _ := e.auth.Passkeys(t.Context())
	id := base64.RawURLEncoding.EncodeToString(pks[0].ID)
	rec := c.postForm("/admin/security/passkeys/"+id+"/remove", url.Values{})
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "Add another passkey before removing this one") {
		t.Fatalf("remove only passkey: %d %s", rec.Code, rec.Body.String())
	}
	key := newDevice()
	key.register(c, "/admin/security/passkeys/begin", "/admin/security/passkeys/finish?name=Key")
	e.clock.add(6 * time.Minute)
	c.get("/admin/security")
	if rec := c.postForm("/admin/security/passkeys/"+id+"/remove", url.Values{}); rec.Code != 403 {
		t.Fatalf("remove needs re-auth: %d", rec.Code)
	}
	key.login(c, "/admin/security/reauth/begin", "/admin/security/reauth/finish")
	if rec := c.postForm("/admin/security/passkeys/"+id+"/remove", url.Values{}); rec.Code != 303 {
		t.Fatalf("remove after reauth: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRenamePasskey(t *testing.T) {
	e := newEnv(t, true)
	c, _ := e.enrolled(t)
	pks, _ := e.auth.Passkeys(t.Context())
	id := base64.RawURLEncoding.EncodeToString(pks[0].ID)
	e.clock.add(10 * time.Minute) // rename does not need re-auth
	c.get("/admin/security")
	if rec := c.postForm("/admin/security/passkeys/"+id+"/rename", url.Values{"name": {"Work phone"}}); rec.Code != 303 {
		t.Fatalf("rename: %d", rec.Code)
	}
	if rec := c.get("/admin/security"); !strings.Contains(rec.Body.String(), "Work phone") {
		t.Fatal("rename not shown")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/admin/ -run 'Security|Passkey'`
Expected: FAIL (routes return 404).

- [ ] **Step 3: Implement the security page**

Append to `internal/admin/views/models.go`:

```go
type PasskeyRow struct {
	ID, Name, Created, LastUsed string
}

type SecurityVM struct {
	Layout        LayoutVM
	Passkeys      []PasskeyRow
	RecentAuth    bool
	BackupEnabled bool
	Message       string
}
```

Create `internal/admin/views/security.templ`:

```templ
package views

templ SecurityPage(vm SecurityVM) {
	@Layout(vm.Layout) {
		<h1>Security</h1>
		if vm.Message != "" {
			<p class="error">{ vm.Message }</p>
		}
		<h2>Passkeys</h2>
		<table>
			<thead><tr><th>Name</th><th>Added</th><th>Last used</th><th></th></tr></thead>
			<tbody>
				for _, p := range vm.Passkeys {
					<tr>
						<td>
							<form method="post" action={ templ.SafeURL("/admin/security/passkeys/" + p.ID + "/rename") }>
								<input type="hidden" name="csrf" value={ vm.Layout.CSRF }/>
								<input type="text" name="name" value={ p.Name } maxlength="60" required/>
								<button type="submit">Rename</button>
							</form>
						</td>
						<td>{ p.Created }</td>
						<td>{ p.LastUsed }</td>
						<td>
							<form method="post" action={ templ.SafeURL("/admin/security/passkeys/" + p.ID + "/remove") }>
								<input type="hidden" name="csrf" value={ vm.Layout.CSRF }/>
								<button type="submit">Remove</button>
							</form>
						</td>
					</tr>
				}
			</tbody>
		</table>
		if vm.RecentAuth {
			<p>
				<label>Name for the new passkey <input id="pk-name" type="text" maxlength="60" placeholder="e.g. Security key"/></label>
				<button type="button" data-webauthn="register" data-name-input="pk-name" data-begin="/admin/security/passkeys/begin" data-finish="/admin/security/passkeys/finish">Add a passkey</button>
			</p>
		} else {
			<p>Adding or removing passkeys needs a fresh sign-in.</p>
			<button type="button" data-webauthn="login" data-begin="/admin/security/reauth/begin" data-finish="/admin/security/reauth/finish">Confirm it's you</button>
		}
		<p id="wa-status" class="error" role="status"></p>
		<h2>Backup login</h2>
		if vm.BackupEnabled {
			<p>Backup login (password + authenticator code) is on. Change it with <code>admin set-backup</code> on the server.</p>
		} else {
			<p>Backup login is off (<code>ADMIN_BACKUP_LOGIN=off</code>).</p>
		}
	}
}
```

Create `internal/admin/security.go` (and delete the `registerSecurity` stub from `admin.go`):

```go
package admin

import (
	"encoding/base64"
	"errors"
	"net/http"

	"thymeandplace/internal/admin/views"
	"thymeandplace/internal/auth"
)

func (a *Admin) registerSecurity() {
	m := a.mux
	m.Handle("GET /admin/security", a.requireAuth(http.HandlerFunc(a.securityPage)))
	m.Handle("POST /admin/security/reauth/begin", a.requireAuth(http.HandlerFunc(a.passkeyBegin)))
	m.Handle("POST /admin/security/reauth/finish", a.requireAuth(http.HandlerFunc(a.reauthFinish)))
	m.Handle("POST /admin/security/passkeys/begin", a.requireAuth(a.requireRecent(http.HandlerFunc(a.addBegin))))
	m.Handle("POST /admin/security/passkeys/finish", a.requireAuth(a.requireRecent(http.HandlerFunc(a.addFinish))))
	m.Handle("POST /admin/security/passkeys/{id}/rename", a.requireAuth(http.HandlerFunc(a.renamePasskey)))
	m.Handle("POST /admin/security/passkeys/{id}/remove", a.requireAuth(http.HandlerFunc(a.removePasskey)))
}

func (a *Admin) recent(r *http.Request) bool {
	s, ok := SessionFrom(r.Context())
	return ok && s.RecentAuth(a.d.Auth.Now())
}

func (a *Admin) requireRecent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.recent(r) {
			jsonError(w, http.StatusForbidden, "Confirm it's you first: sign in with a passkey again.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Admin) securityVM(r *http.Request, msg string) (views.SecurityVM, error) {
	pks, err := a.d.Auth.Passkeys(r.Context())
	if err != nil {
		return views.SecurityVM{}, err
	}
	vm := views.SecurityVM{Layout: a.layout(r, "Security"), RecentAuth: a.recent(r), BackupEnabled: a.d.Auth.BackupEnabled(), Message: msg}
	for _, p := range pks {
		row := views.PasskeyRow{ID: base64.RawURLEncoding.EncodeToString(p.ID), Name: p.Name, Created: p.CreatedAt.UTC().Format("2006-01-02"), LastUsed: "never"}
		if p.LastUsedAt != nil {
			row.LastUsed = p.LastUsedAt.UTC().Format("2006-01-02 15:04 UTC")
		}
		vm.Passkeys = append(vm.Passkeys, row)
	}
	return vm, nil
}

func (a *Admin) securityPage(w http.ResponseWriter, r *http.Request) {
	a.securityWithMessage(w, r, http.StatusOK, "")
}

func (a *Admin) securityWithMessage(w http.ResponseWriter, r *http.Request, status int, msg string) {
	vm, err := a.securityVM(r, msg)
	if err != nil {
		a.d.Log.Error("security page", "err", err)
		http.Error(w, "Couldn't load passkeys.", http.StatusInternalServerError)
		return
	}
	a.render(w, r, status, views.SecurityPage(vm))
}

func (a *Admin) reauthFinish(w http.ResponseWriter, r *http.Request) {
	state := ""
	if c, err := r.Cookie(auth.StateCookie); err == nil {
		state = c.Value
	}
	err := limitCeremonyBody(w, r)
	if err == nil {
		err = a.d.Auth.FinishLogin(r.Context(), state, r)
	}
	if err != nil {
		jsonError(w, http.StatusUnauthorized, "That passkey didn't work. Try again.")
		return
	}
	s, _ := SessionFrom(r.Context())
	if err := a.d.Auth.MarkReauthed(r.Context(), s); err != nil {
		jsonError(w, http.StatusInternalServerError, "Couldn't record the confirmation.")
		return
	}
	auth.ClearStateCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"redirect": "/admin/security"})
}

func (a *Admin) addBegin(w http.ResponseWriter, r *http.Request) {
	opts, state, err := a.d.Auth.BeginRegistration(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "Couldn't start passkey registration.")
		return
	}
	auth.SetStateCookie(w, state)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(opts)
}

func (a *Admin) addFinish(w http.ResponseWriter, r *http.Request) {
	state := ""
	if c, err := r.Cookie(auth.StateCookie); err == nil {
		state = c.Value
	}
	err := limitCeremonyBody(w, r)
	if err == nil {
		err = a.d.Auth.FinishRegistration(r.Context(), state, r, r.URL.Query().Get("name"), "")
	}
	if err != nil {
		jsonError(w, http.StatusBadRequest, "The passkey couldn't be registered. Try again.")
		return
	}
	auth.ClearStateCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"redirect": "/admin/security"})
}

func passkeyID(r *http.Request) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(r.PathValue("id"))
}

func (a *Admin) renamePasskey(w http.ResponseWriter, r *http.Request) {
	id, err := passkeyID(r)
	if err == nil {
		err = a.d.Auth.RenamePasskey(r.Context(), id, r.PostFormValue("name"))
	}
	if err != nil {
		a.securityWithMessage(w, r, http.StatusBadRequest, "Couldn't rename: "+err.Error())
		return
	}
	http.Redirect(w, r, "/admin/security", http.StatusSeeOther)
}

func (a *Admin) removePasskey(w http.ResponseWriter, r *http.Request) {
	if !a.recent(r) {
		a.securityWithMessage(w, r, http.StatusForbidden, "Confirm it's you before removing a passkey.")
		return
	}
	id, err := passkeyID(r)
	if err == nil {
		err = a.d.Auth.RemovePasskey(r.Context(), id)
	}
	switch {
	case errors.Is(err, auth.ErrLastPasskey):
		a.securityWithMessage(w, r, http.StatusConflict, "Add another passkey before removing this one.")
	case err != nil:
		a.securityWithMessage(w, r, http.StatusBadRequest, "Couldn't remove: "+err.Error())
	default:
		http.Redirect(w, r, "/admin/security", http.StatusSeeOther)
	}
}
```

In `TestRemovePasskey` the first removal attempt happens right after enrollment (within 5 minutes), so it reaches `RemovePasskey` and gets the 409 last-passkey message. The ordering in `removePasskey` (re-auth check first, then last-passkey guard) matches the test.

- [ ] **Step 4: Mount /admin in the server**

In `cmd/server/app.go`, extend `config` and `loadConfig`:

```go
type config struct {
	DatabaseURL    string
	Addr           string
	TrustProxy     bool
	AdminSecretKey []byte
	AdminRPID      string
	AdminOrigin    string
	AdminBackup    bool
}
```

```go
	if k := getenv("ADMIN_SECRET_KEY"); k != "" {
		key, err := auth.ParseSecretKey(k)
		if err != nil {
			return c, err
		}
		c.AdminSecretKey = key
	}
	c.AdminRPID, c.AdminOrigin = getenv("ADMIN_RP_ID"), getenv("ADMIN_ORIGIN")
	if c.AdminRPID == "" {
		c.AdminRPID = "thymeand.place"
	}
	if c.AdminOrigin == "" {
		c.AdminOrigin = "https://" + c.AdminRPID
	}
	c.AdminBackup = getenv("ADMIN_BACKUP_LOGIN") != "off"
```

In `newApp`, after the public mount:

```go
	if len(cfg.AdminSecretKey) == 32 {
		authSvc, err := auth.NewService(pool, auth.Config{
			RPID: cfg.AdminRPID, RPOrigins: []string{cfg.AdminOrigin}, SecretKey: cfg.AdminSecretKey,
			BackupEnabled: cfg.AdminBackup, Log: log,
		})
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("admin auth: %w", err)
		}
		a.auth = authSvc
		adminH := admin.New(admin.Deps{Auth: authSvc, Log: log, TrustProxy: cfg.TrustProxy})
		a.mux.Handle("/admin", adminH)
		a.mux.Handle("/admin/", adminH)
	} else {
		log.Warn("ADMIN_SECRET_KEY not set; /admin is disabled")
	}
```

Add `auth *auth.Service` to the `app` struct, and import `thymeandplace/internal/admin` and `thymeandplace/internal/auth`. Add to `TestLoadConfig`:

```go
	if _, err := loadConfig(func(k string) string {
		return map[string]string{"DATABASE_URL": "postgres://x", "ADMIN_SECRET_KEY": "c2hvcnQ="}[k]
	}); err == nil {
		t.Fatal("a short ADMIN_SECRET_KEY must be rejected")
	}
```

- [ ] **Step 5: Run the tests**

Run: `go tool templ generate && go vet ./... && go test ./internal/admin/ ./cmd/server/ -count=1`
Expected: `ok` for both.

- [ ] **Step 6: Commit**

```bash
git add internal/admin cmd/server
git commit -m "feat(admin): security page with re-auth window, passkey add/rename/remove; mount /admin

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: `admin` CLI (enroll, set-backup, revoke-sessions)

**Files:**
- Create: `cmd/admin/main.go`, `cmd/admin/main_test.go`

**Interfaces:**
- Consumes: `auth.Service` (`CreateEnrollmentToken`, `SetBackup`, `RevokeAllSessions`), `auth.ParseSecretKey`, `db.Open`.
- Produces: `func run(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer, readPassword func(prompt string) (string, error)) int`

- [ ] **Step 1: Write the failing test**

```bash
go get github.com/mdp/qrterminal/v3@latest golang.org/x/term@latest
```

Create `cmd/admin/main_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"thymeandplace/internal/dbtest"
)

func TestCLI(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	env := map[string]string{
		"DATABASE_URL":     pool.Config().ConnString(),
		"ADMIN_SECRET_KEY": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32)),
		"PUBLIC_BASE_URL":  "https://thymeand.place",
	}
	getenv := func(k string) string { return env[k] }
	pw := func(string) (string, error) { return "correct horse battery staple", nil }

	var out, errb bytes.Buffer
	if code := run(ctx, []string{"enroll"}, getenv, nil, &out, &errb, pw); code != 0 {
		t.Fatalf("enroll: %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "https://thymeand.place/admin/enroll?token=") || !strings.Contains(out.String(), "15 minutes") {
		t.Fatalf("enroll output: %s", out.String())
	}

	out.Reset()
	if code := run(ctx, []string{"set-backup"}, getenv, nil, &out, &errb, pw); code != 0 {
		t.Fatalf("set-backup: %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "otpauth://totp/") {
		t.Fatalf("set-backup output: %s", out.String())
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM admin_backup`).Scan(&n)
	if n != 1 {
		t.Fatal("backup row not stored")
	}

	mismatch := 0
	pwMismatch := func(string) (string, error) {
		mismatch++
		if mismatch == 1 {
			return "correct horse battery staple", nil
		}
		return "different horse battery staple", nil
	}
	if code := run(ctx, []string{"set-backup"}, getenv, nil, &out, &errb, pwMismatch); code == 0 {
		t.Fatal("mismatched confirmation accepted")
	}

	_, _ = pool.Exec(ctx, `INSERT INTO admin_session (token_hash, csrf_token, method, created_at, last_seen_at, auth_at) VALUES ('\x01','c','passkey',now(),now(),now())`)
	out.Reset()
	if code := run(ctx, []string{"revoke-sessions"}, getenv, nil, &out, &errb, pw); code != 0 || !strings.Contains(out.String(), "1 session") {
		t.Fatalf("revoke: %d %s", code, out.String())
	}

	if code := run(ctx, []string{"nope"}, getenv, nil, &out, &errb, pw); code != 2 {
		t.Fatal("unknown command must exit 2")
	}
	failPW := func(string) (string, error) { return "", errors.New("no tty") }
	if code := run(ctx, []string{"set-backup"}, getenv, nil, &out, &errb, failPW); code == 0 {
		t.Fatal("password read failure must fail")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/admin/`
Expected: FAIL to compile: `undefined: run`.

- [ ] **Step 3: Implement**

Create `cmd/admin/main.go`:

```go
// Command admin manages owner access from a server shell.
//
//	admin enroll            print a single-use passkey registration link (15 min)
//	admin set-backup        set the backup password and TOTP (replaces existing)
//	admin revoke-sessions   end every admin session
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/mdp/qrterminal/v3"
	"golang.org/x/term"

	"thymeandplace/internal/auth"
	"thymeandplace/internal/db"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr, terminalPassword))
}

func terminalPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return string(b), err
}

const usage = "usage: admin enroll | set-backup | revoke-sessions"

func run(ctx context.Context, args []string, getenv func(string) string, _ io.Reader, stdout, stderr io.Writer, readPassword func(string) (string, error)) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	cmd := args[0]
	if cmd != "enroll" && cmd != "set-backup" && cmd != "revoke-sessions" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "admin %s: %v\n", cmd, err)
		return 1
	}
	key, err := auth.ParseSecretKey(getenv("ADMIN_SECRET_KEY"))
	if err != nil {
		return fail(err)
	}
	pool, err := db.Open(ctx, getenv("DATABASE_URL"))
	if err != nil {
		return fail(err)
	}
	defer pool.Close()
	rpID := getenv("ADMIN_RP_ID")
	if rpID == "" {
		rpID = "thymeand.place"
	}
	origin := getenv("ADMIN_ORIGIN")
	if origin == "" {
		origin = "https://" + rpID
	}
	svc, err := auth.NewService(pool, auth.Config{
		RPID: rpID, RPOrigins: []string{origin}, SecretKey: key, BackupEnabled: true,
		Log: slog.New(slog.NewTextHandler(stderr, nil)),
	})
	if err != nil {
		return fail(err)
	}

	switch cmd {
	case "enroll":
		raw, exp, err := svc.CreateEnrollmentToken(ctx)
		if err != nil {
			return fail(err)
		}
		base := strings.TrimRight(getenv("PUBLIC_BASE_URL"), "/")
		if base == "" {
			base = origin
		}
		fmt.Fprintf(stdout, "Open this link within 15 minutes to register a passkey (single use):\n\n  %s/admin/enroll?token=%s\n\nExpires %s UTC.\n",
			base, raw, exp.UTC().Format("2006-01-02 15:04"))
	case "set-backup":
		pw, err := readPassword("New backup password (at least 12 characters): ")
		if err != nil {
			return fail(err)
		}
		again, err := readPassword("Repeat password: ")
		if err != nil {
			return fail(err)
		}
		if pw != again {
			return fail(errors.New("passwords do not match"))
		}
		uri, err := svc.SetBackup(ctx, pw)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "Backup login replaced. Add this to your authenticator app, then clear your terminal:\n\n  %s\n\n", uri)
		qrterminal.GenerateHalfBlock(uri, qrterminal.L, stdout)
	case "revoke-sessions":
		n, err := svc.RevokeAllSessions(ctx)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "Ended %d session(s).\n", n)
	}
	return 0
}
```

The test expects `"1 session"`; `"Ended 1 session(s)."` contains it.

- [ ] **Step 4: Run the tests**

Run: `go mod tidy && go test ./cmd/admin/ -count=1`
Expected: `ok`.

- [ ] **Step 5: Manual end-to-end check in a real browser (dev)**

```bash
export ADMIN_SECRET_KEY=$(openssl rand -base64 32)
export DATABASE_URL='postgres://tp:tp@localhost:55432/thymeandplace?sslmode=disable' ADMIN_RP_ID=localhost ADMIN_ORIGIN=http://localhost:8080
go run ./cmd/admin enroll
go run ./cmd/server
```

Open the printed link (it points at `https://localhost/...`: set `PUBLIC_BASE_URL=http://localhost:8080` before running `enroll` in dev), register a passkey, sign out, sign back in. Browsers treat `http://localhost` as a secure context, so `__Host-` cookies work.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum cmd/admin
git commit -m "feat(admin-cli): enroll, set-backup with QR, revoke-sessions

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Plan completion check

- [ ] `go tool templ generate && ./scripts/sqlc.sh generate && git diff --exit-code`
- [ ] `go vet ./... && go test ./... -count=1` passes.
- [ ] Every §13.4 bullet has a test: passkey register/login (virtual authenticator); backup with a fixed clock (wrong password, wrong TOTP, replay, lockout/backoff, `ADMIN_BACKUP_LOGIN=off`); idle/absolute expiry; revoke-sessions; re-auth window for add/remove; CSRF on every write route; enrollment single-use and 15-minute expiry; unauthenticated redirects.
