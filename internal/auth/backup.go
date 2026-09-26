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
		d := LockoutBase << uint(level)
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
