package changeset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/store"
)

type CasesFunc func(context.Context) ([]calibration.Case, error)

type Pending struct {
	ID                      int64
	Changeset               Changeset
	BaseVersion             int64
	ScoreBefore, ScoreAfter float64
	ExpiresAt               time.Time
}

type writeFunc func(ctx context.Context, q *store.Queries, ups []catalog.Ingredient, dels []string) error

type Service struct {
	pool  *pgxpool.Pool
	eco   EcoregionChecker
	cases CasesFunc
	write writeFunc // catalog.Write; replaced in tests to inject failures
}

func NewService(pool *pgxpool.Pool, eco EcoregionChecker, cases CasesFunc) *Service {
	return &Service{pool: pool, eco: eco, cases: cases, write: catalog.Write}
}

// Preview validates cs against the catalog as stored now, stores it for one
// hour and returns the preview. Problems come back as *InvalidError.
func (s *Service) Preview(ctx context.Context, cs Changeset) (Pending, Preview, error) {
	cur, err := catalog.Load(ctx, s.pool)
	if err != nil {
		return Pending{}, Preview{}, fmt.Errorf("load catalog: %w", err)
	}
	pv, err := s.build(ctx, cs, cur)
	if err != nil {
		return Pending{}, Preview{}, err
	}
	payload, err := json.Marshal(cs)
	if err != nil {
		return Pending{}, Preview{}, err
	}
	q := store.New(s.pool)
	_ = q.DeleteExpiredChangesets(ctx)
	row, err := q.InsertPendingChangeset(ctx, store.InsertPendingChangesetParams{
		Payload: payload, BaseVersion: cur.Version(), ScoreBefore: pv.Before.Score, ScoreAfter: pv.After.Score,
	})
	if err != nil {
		return Pending{}, Preview{}, fmt.Errorf("store preview: %w", err)
	}
	return Pending{
		ID: row.ID, Changeset: cs, BaseVersion: cur.Version(),
		ScoreBefore: pv.Before.Score, ScoreAfter: pv.After.Score, ExpiresAt: row.ExpiresAt.Time,
	}, pv, nil
}

// Reopen rebuilds the preview of a stored changeset.
func (s *Service) Reopen(ctx context.Context, id int64) (Pending, Preview, error) {
	row, err := store.New(s.pool).GetPendingChangeset(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Pending{}, Preview{}, ErrNotFound
	}
	if err != nil {
		return Pending{}, Preview{}, err
	}
	p, err := pendingFromRow(row)
	if err != nil {
		return Pending{}, Preview{}, err
	}
	cur, err := catalog.Load(ctx, s.pool)
	if err != nil {
		return Pending{}, Preview{}, err
	}
	if cur.Version() != p.BaseVersion {
		return p, Preview{}, ErrStale
	}
	pv, err := s.build(ctx, p.Changeset, cur)
	return p, pv, err
}

func (s *Service) build(ctx context.Context, cs Changeset, cur *catalog.Snapshot) (Preview, error) {
	proposed, problems, err := Validate(ctx, cs, cur, s.eco)
	if err != nil {
		return Preview{}, err
	}
	if len(problems) > 0 {
		return Preview{}, &InvalidError{Problems: problems}
	}
	cases, err := s.cases(ctx)
	if err != nil {
		return Preview{}, fmt.Errorf("calibration inputs: %w", err)
	}
	return BuildPreview(cur, proposed, cases), nil
}

func pendingFromRow(row store.PendingChangeset) (Pending, error) {
	var cs Changeset
	if err := json.Unmarshal(row.Payload, &cs); err != nil {
		return Pending{}, fmt.Errorf("decode pending changeset %d: %w", row.ID, err)
	}
	return Pending{
		ID: row.ID, Changeset: cs, BaseVersion: row.BaseVersion,
		ScoreBefore: row.ScoreBefore, ScoreAfter: row.ScoreAfter, ExpiresAt: row.ExpiresAt.Time,
	}, nil
}
