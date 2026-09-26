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
	if cfg.Log == nil {
		cfg.Log = slog.Default()
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
