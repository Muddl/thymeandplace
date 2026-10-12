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

	// OnEvent, when set, is told about security events the owner must hear
	// about: "backup_login", "backup_lockout", "login_log_growth". It must
	// not block. Monitoring hooks it (internal/monitor).
	OnEvent func(kind string)
}

type Service struct {
	pool   *pgxpool.Pool
	wa     *webauthn.WebAuthn
	sealer *Sealer
	backup bool
	now    func() time.Time
	log    *slog.Logger

	onEvent func(string) // Config.OnEvent; nil is a no-op

	growthWarn int64 // Prune warns above this many login-log rows a day; tests lower it
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
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	return &Service{pool: pool, wa: wa, sealer: sealer, backup: cfg.BackupEnabled, now: cfg.Now, log: cfg.Log, onEvent: cfg.OnEvent, growthWarn: loginLogGrowthWarn}, nil
}

// event tells the monitoring hook, if any, about a security event.
func (s *Service) event(kind string) {
	if s.onEvent != nil {
		s.onEvent(kind)
	}
}

func (s *Service) q() *store.Queries { return store.New(s.pool) }

func (s *Service) BackupEnabled() bool { return s.backup }

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

const (
	AttemptLimit  = 5
	AttemptWindow = 15 * time.Minute
)

// Caller is who is making a sign-in attempt. IP is the full address, stored
// in the audit row; Key is what the attempt budget counts by (the IPv4
// address, or the /64 for IPv6, from web.LimiterKey), so a client can't get
// fresh attempts by rotating addresses inside its /64.
type Caller struct{ IP, Key string }

// budgets maps an audited method to the methods whose failures share its
// budget: passkey and backup sign-in share one, enrollment has its own
// (controller ruling E: failed logins must not block the owner enrolling).
var budgets = map[string][]string{
	"passkey": {"passkey", "backup"},
	"backup":  {"passkey", "backup"},
	"enroll":  {"enroll"},
}

func budget(method string) []string {
	if b, ok := budgets[method]; ok {
		return b
	}
	return []string{method}
}

// AllowAttempt reports whether c may make another attempt at method: fewer
// than AttemptLimit failures in method's budget in the last AttemptWindow.
// Any outcome except success and rate_limited is a failure.
func (s *Service) AllowAttempt(ctx context.Context, c Caller, method string) (bool, error) {
	n, err := s.q().CountRecentLoginAttempts(ctx, store.CountRecentLoginAttemptsParams{
		IpKey: c.Key, Since: ts(s.now().Add(-AttemptWindow)), Methods: budget(method),
	})
	if err != nil {
		return false, err
	}
	return n < AttemptLimit, nil
}

// logAuditFailure logs a failed audit write. Sign-in routes are reachable by
// anonymous callers and ERROR pages the owner, so a write that failed because
// the request's context was already done (the client went away, or the server
// is shutting down) is a WARN. Real store failures stay ERROR.
func (s *Service) logAuditFailure(ctx context.Context, err error) {
	if ctx.Err() != nil {
		s.log.Warn("login audit write failed", "err", err)
		return
	}
	s.log.Error("login audit write failed", "err", err)
}

// LogAttempt writes the audit row. Failures to log are themselves logged.
func (s *Service) LogAttempt(ctx context.Context, c Caller, method, outcome string) {
	err := s.q().InsertLoginAttempt(ctx, store.InsertLoginAttemptParams{At: ts(s.now()), Ip: c.IP, IpKey: c.Key, Method: method, Outcome: outcome})
	if err != nil {
		s.logAuditFailure(ctx, err)
	}
	if method == "backup" && outcome == "success" {
		s.log.Warn("backup login used", "ip", c.IP)
		s.event("backup_login")
	}
}

// LogRefusal records that c hit method's budget, at most once per key and
// budget per AttemptWindow, so refused attempts can't grow the log without
// bound. It logs a WARN only when it wrote the row.
func (s *Service) LogRefusal(ctx context.Context, c Caller, method string) {
	now := s.now()
	n, err := s.q().InsertRefusalOnce(ctx, store.InsertRefusalOnceParams{
		At: ts(now), Ip: c.IP, IpKey: c.Key, Method: method, Methods: budget(method), Since: ts(now.Add(-AttemptWindow)),
	})
	if err != nil {
		s.logAuditFailure(ctx, err)
		return
	}
	if n > 0 {
		s.log.Warn("admin sign-in budget reached", "ip", c.IP, "method", method)
	}
}

var (
	ErrBackupDisabled  = errors.New("backup login is disabled")
	ErrBackupNotSet    = errors.New("backup login is not set up")
	ErrBadCredentials  = errors.New("password or code is wrong")
	ErrNoPasskeys      = errors.New("no passkeys registered")
	ErrLastPasskey     = errors.New("cannot remove the only passkey")
	ErrPasskeyNotFound = errors.New("passkey not found")
	ErrBadEnrollment   = errors.New("enrollment link is invalid or has expired")
	ErrCeremony        = errors.New("passkey check failed")
)

type LockedError struct{ Until time.Time }

func (e *LockedError) Error() string {
	return "backup login locked until " + e.Until.Format(time.RFC3339)
}
