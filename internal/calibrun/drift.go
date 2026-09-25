package calibrun

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/calibration"
	"thymeandplace/internal/catalog"
	"thymeandplace/internal/changeset"
	"thymeandplace/internal/store"
)

// Drift scores the live snapshot and records it, warning on drops that
// come from reference-data swaps rather than catalog edits.
type Drift struct {
	Pool        *pgxpool.Pool
	Snapshot    func() *catalog.Snapshot
	Cases       func(context.Context) ([]calibration.Case, error)
	DataVersion func() string
	Log         *slog.Logger
}

func (d *Drift) RunOnce(ctx context.Context) (calibration.Report, error) {
	snap := d.Snapshot()
	if snap == nil {
		return calibration.Report{}, errors.New("drift check: no catalog snapshot loaded")
	}
	cases, err := d.Cases(ctx)
	if err != nil {
		return calibration.Report{}, err
	}
	rep := calibration.Score(snap, cases)
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
	if hasPrev && changeset.NeedsOverride(prev.Score, rep.Score) {
		d.Log.Warn("calibration score dropped", "previous", prev.Score, "current", rep.Score,
			"previous_data_version", prev.DataVersion, "data_version", dv)
	} else {
		d.Log.Info("calibration drift check", "score", rep.Score, "compared", rep.Compared, "data_version", dv)
	}
	return rep, nil
}

func (d *Drift) Loop(ctx context.Context, every time.Duration) {
	run := func() {
		if _, err := d.RunOnce(ctx); err != nil && ctx.Err() == nil {
			d.Log.Error("calibration drift check failed", "err", err)
		}
	}
	run()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
