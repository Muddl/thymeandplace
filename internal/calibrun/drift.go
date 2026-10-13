package calibrun

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/jobs"
	"thymeandplace/internal/store"
)

// ErrNoSnapshot is RunOnce's error when no catalog snapshot is loaded yet.
var ErrNoSnapshot = errors.New("drift check: no catalog snapshot loaded")

// Drift scores the live snapshot and records it, warning on drops that
// come from reference-data swaps rather than catalog edits.
type Drift struct {
	Pool        *pgxpool.Pool
	Snapshot    func() *catalog.Snapshot
	Cases       func(context.Context) ([]calibration.Case, error)
	DataVersion func() string
	Log         *slog.Logger
	Now         func() time.Time // nil → time.Now; tests place runs relative to it
	// Notify, when set, reports each run's outcome to the drift heartbeat
	// check. Summaries never carry raw error text (it can name a DB host).
	Notify func(ctx context.Context, failed bool, summary string)
}

func (d *Drift) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// RunOnce scores and records one drift check. Unless ctx was cancelled,
// it reports the outcome to Notify exactly once.
func (d *Drift) RunOnce(ctx context.Context) (rep calibration.Report, err error) {
	var summary string
	dropped := false
	defer func() {
		if d.Notify == nil || ctx.Err() != nil {
			return
		}
		switch {
		case errors.Is(err, ErrNoSnapshot):
			d.Notify(ctx, true, "drift check failed: no catalog snapshot loaded")
		case err != nil:
			d.Notify(ctx, true, "drift check failed: database or scoring error (see the app log)")
		default:
			d.Notify(ctx, dropped, summary)
		}
	}()
	snap := d.Snapshot()
	if snap == nil {
		return calibration.Report{}, ErrNoSnapshot
	}
	cases, err := d.Cases(ctx)
	if err != nil {
		return calibration.Report{}, err
	}
	rep = calibration.Score(snap, cases)
	q := store.New(d.Pool)
	prev, err := q.LatestCalibrationRun(ctx)
	hasPrev := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return rep, err
	}
	dv := d.DataVersion()
	if err := q.InsertCalibrationRun(ctx, store.InsertCalibrationRunParams{
		DataVersion: dv, Score: rep.Score, Compared: int32(rep.Compared),
	}); err != nil {
		return rep, err
	}
	if hasPrev && calibration.ScoreDropped(prev.Score, rep.Score) {
		dropped = true
		summary = fmt.Sprintf("calibration score dropped %.3f → %.3f (data version %s → %s)", prev.Score, rep.Score, prev.DataVersion, dv)
		d.Log.Warn("calibration score dropped", "previous", prev.Score, "current", rep.Score,
			"previous_data_version", prev.DataVersion, "data_version", dv)
	} else {
		summary = fmt.Sprintf("calibration score %.3f over %d pairs (data version %s)", rep.Score, rep.Compared, dv)
		d.Log.Info("calibration drift check", "score", rep.Score, "compared", rep.Compared, "data_version", dv)
	}
	return rep, nil
}

// Loop runs the drift check on schedule s (core spec §13.2). It waits,
// quietly, until a catalog snapshot is loaded; then runs s.Every after the
// last recorded run (at once if there is none that recent); and retries
// failures after s.RetryMin, doubling to s.RetryMax. A failed read of the
// last run backs off the same way; once a read succeeds the database is
// back, so a failed check after it retries from s.RetryMin again. It
// returns when ctx ends.
func (d *Drift) Loop(ctx context.Context, s jobs.Schedule) {
	for d.Snapshot() == nil {
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.RetryMin):
		}
	}
	logFail := func(err error) { d.Log.Error("calibration drift check failed", "err", err) }
	var first time.Duration
	for failures := 1; ; failures++ {
		last, err := store.New(d.Pool).LatestCalibrationRun(ctx)
		if err == nil {
			first = jobs.FirstDelay(last.RanAt.Time, true, d.now(), s.Every)
			break
		}
		if errors.Is(err, pgx.ErrNoRows) {
			break
		}
		if ctx.Err() != nil {
			return
		}
		logFail(err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(jobs.RetryDelay(failures, s.RetryMin, s.RetryMax)):
		}
	}
	jobs.Run(ctx, s, first, func(ctx context.Context) error {
		_, err := d.RunOnce(ctx)
		return err
	}, logFail)
}
