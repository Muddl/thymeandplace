package auth

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"thymeandplace/internal/store"
)

// LoginLogRetention is how long admin_login_log rows are kept (core spec §15).
const LoginLogRetention = 90 * 24 * time.Hour

// loginLogGrowthWarn is the number of login-log rows in 24 hours above which
// Prune warns: the per-client budget bounds each client, but not a flood from
// many addresses.
const loginLogGrowthWarn = 10_000

type PruneResult struct {
	LoginLog, Sessions, EnrollmentTokens int64
	LoginLogLastDay                      int64
}

// Prune deletes admin rows the app no longer needs: login-log rows past
// LoginLogRetention, sessions LookupSession would refuse (the same strict
// comparisons), and enrollment tokens that are used or expired (the exact
// complement of EnrollmentTokenValid). It then warns if the login log grew
// by more than loginLogGrowthWarn rows in the last day. The deletes are
// idempotent, so replicas pruning at once is harmless.
func (s *Service) Prune(ctx context.Context) (PruneResult, error) {
	now := s.now()
	q := s.q()
	var r PruneResult
	var err error
	// The login log goes through prune_admin_login_log, which compares with
	// the database clock (the app role can't delete those rows directly).
	if r.LoginLog, err = q.PruneLoginLog(ctx, pgtype.Interval{Microseconds: LoginLogRetention.Microseconds(), Valid: true}); err != nil {
		return r, err
	}
	if r.Sessions, err = q.PruneSessions(ctx, store.PruneSessionsParams{
		IdleBefore: ts(now.Add(-IdleTimeout)), CreatedBefore: ts(now.Add(-AbsoluteTimeout)),
	}); err != nil {
		return r, err
	}
	if r.EnrollmentTokens, err = q.PruneEnrollmentTokens(ctx, ts(now)); err != nil {
		return r, err
	}
	if r.LoginLogLastDay, err = q.CountLoginLogSince(ctx, ts(now.Add(-24*time.Hour))); err != nil {
		return r, err
	}
	if r.LoginLogLastDay > s.growthWarn {
		s.log.Warn("admin login log growing fast", "rows_last_24h", r.LoginLogLastDay)
	}
	return r, nil
}
