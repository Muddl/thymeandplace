package changeset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
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
	// Best-effort: every read of pending_changeset filters expires_at > now(),
	// so a failed cleanup only leaves dead rows behind, never a correctness bug.
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
	if err != nil {
		return p, Preview{}, err
	}
	// Apply enforces the override decision from the scores stored at preview
	// time (row.ScoreBefore/ScoreAfter), not a fresh recompute against
	// possibly-changed calibration cases; keep the flag shown here consistent
	// with what Apply will actually check.
	pv.NeedsOverride = NeedsOverride(p.ScoreBefore, p.ScoreAfter)
	return p, pv, nil
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

type ApplyOptions struct {
	Override    bool   // "Apply despite lower calibration score"
	ConfirmSlug string // typed slug, required for delete changesets
}

// Apply writes a previewed changeset in one transaction. On any error nothing
// is written and the pending row is kept (except when it no longer exists).
func (s *Service) Apply(ctx context.Context, id int64, actor string, opts ApplyOptions) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := store.New(tx)

	row, err := q.GetPendingChangesetForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	version, err := q.LockCatalogVersion(ctx)
	if err != nil {
		return err
	}
	if version != row.BaseVersion {
		return ErrStale
	}
	p, err := pendingFromRow(row)
	if err != nil {
		return err
	}
	cs := p.Changeset
	if cs.Source == SourceDelete && (len(cs.Deletes) != 1 || opts.ConfirmSlug != cs.Deletes[0]) {
		return ErrConfirmMismatch
	}

	// catalog_meta is locked, so no other writer can commit until we finish;
	// a fresh read sees exactly the previewed base version. catalog.Load takes
	// a second connection from s.pool (the transaction above already holds
	// one), so the pool must allow more connections than there are concurrent
	// Apply calls; fine while there is a single admin today, but worth
	// revisiting if that changes.
	cur, err := catalog.Load(ctx, s.pool)
	if err != nil {
		return err
	}
	if cur.Version() != version {
		return ErrStale
	}
	if _, problems, err := Validate(ctx, cs, cur, s.eco); err != nil {
		return err
	} else if len(problems) > 0 {
		return &InvalidError{Problems: problems}
	}

	override := NeedsOverride(p.ScoreBefore, p.ScoreAfter)
	if override && !opts.Override {
		return ErrOverrideRequired
	}
	meta := ""
	if override {
		b, _ := json.Marshal(map[string]any{"override": true, "score_before": p.ScoreBefore, "score_after": p.ScoreAfter})
		meta = string(b)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.actor', $1, true), set_config('app.change_id', $2, true), set_config('app.meta', $3, true)`,
		actor, strconv.FormatInt(id, 10), meta); err != nil {
		return err
	}
	if err := s.write(ctx, q, changedUpserts(cur, cs.Upserts), cs.Deletes); err != nil {
		return fmt.Errorf("apply changeset %d: %w", id, err)
	}
	if err := q.DeletePendingChangeset(ctx, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// changedUpserts drops upserts identical to what is stored, so unchanged
// import rows produce no history.
func changedUpserts(cur *catalog.Snapshot, ups []catalog.Ingredient) []catalog.Ingredient {
	var out []catalog.Ingredient
	for _, u := range ups {
		n := catalog.Normalize(u)
		if old, ok := cur.Get(n.Slug); ok && reflect.DeepEqual(old, n) {
			continue
		}
		out = append(out, n)
	}
	return out
}
