// Package ingest implements the versioned reference-data pipeline:
// fetch → transform → load → validate → swap, plus rollback.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/store"
)

// Source is one file a dataset fetches. Its content is trusted either by a
// pin (SHA256, checked on every fetch whatever the URL) or not at all, with
// Unpinned saying why. A test requires exactly one of the two.
type Source struct {
	URL, File string
	SHA256    string // expected hex sha256; empty only when Unpinned is set
	Unpinned  string // why the content can't be pinned; empty when SHA256 is set
}

// LicenseUnresolved marks a License() whose terms nobody has confirmed yet.
// A version whose recorded license contains it can't be approved and never
// passes validation, even if license_reviewed is set by hand
// (docs/reference-data-licenses.md).
const LicenseUnresolved = "UNRESOLVED:"

// Dataset is one reference source.
type Dataset interface {
	Name() string   // "climate" | "ocean" | "places"
	View() string   // live view, e.g. "place"
	Prefix() string // versioned table prefix, e.g. "place_v"
	License() string
	// Attribution lists the plain-text credit lines the About the data page
	// must carry for this dataset (internal/web/views/about.templ; a test
	// ties the two together).
	Attribution() []string
	Sources() []Source
	// Transform reads versionDir/raw and writes load-ready CSV to w.
	Transform(ctx context.Context, versionDir string, w io.Writer) error
	CreateSQL(table string) string
	Load(ctx context.Context, tx pgx.Tx, table string, csv io.Reader) error
	// Check runs dataset-specific validation against table; each string is one failure.
	Check(ctx context.Context, db store.DBTX, table string) []string
}

// ExternalTransformError means the transform must run in another container.
// Cmd is meant to be run from the deploy/ directory (next to compose.yaml),
// where docs/operations.md runs every other `docker compose` step.
type ExternalTransformError struct{ Cmd string }

func (e *ExternalTransformError) Error() string {
	return "transform output not found; run (from the deploy/ directory): " + e.Cmd + " — then run this step again"
}

// ValidationError lists every failed check. Live data is unchanged.
type ValidationError struct {
	Dataset  string
	Failures []string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s validation failed: %s", e.Dataset, strings.Join(e.Failures, "; "))
}

type Pipeline struct {
	DS      Dataset
	Pool    *pgxpool.Pool
	Blob    Blob
	WorkDir string
	HTTP    *http.Client
	Log     *slog.Logger
}

func (p *Pipeline) q() *store.Queries { return store.New(p.Pool) }

func (p *Pipeline) versionDir(v int64) string {
	return filepath.Join(p.WorkDir, p.DS.Name(), "v"+strconv.FormatInt(v, 10))
}

func rawKey(ds string, v int64, file string) string { return fmt.Sprintf("raw/%s/v%d/%s", ds, v, file) }
func csvKey(ds string, v int64) string              { return fmt.Sprintf("transformed/%s/v%d/data.csv", ds, v) }

// version loads a dataset_versions row and checks it belongs to this dataset.
func (p *Pipeline) version(ctx context.Context, v int64) (store.DatasetVersion, error) {
	dv, err := p.q().GetDatasetVersion(ctx, v)
	if errors.Is(err, pgx.ErrNoRows) {
		return dv, fmt.Errorf("version %d does not exist", v)
	}
	if err != nil {
		return dv, err
	}
	if dv.Dataset != p.DS.Name() {
		return dv, fmt.Errorf("version %d belongs to %s, not %s", v, dv.Dataset, p.DS.Name())
	}
	return dv, nil
}

// Latest returns the newest version ID for this dataset.
func (p *Pipeline) Latest(ctx context.Context) (int64, error) {
	dv, err := p.q().LatestDatasetVersion(ctx, p.DS.Name())
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("no %s versions yet; run fetch", p.DS.Name())
	}
	return dv.ID, err
}

// Fetch downloads every source, records a new version and uploads the raw files.
func (p *Pipeline) Fetch(ctx context.Context) (int64, error) {
	name := p.DS.Name()
	incoming := filepath.Join(p.WorkDir, name, "incoming")
	_ = os.RemoveAll(incoming)
	if err := os.MkdirAll(incoming, 0o755); err != nil {
		return 0, err
	}
	var urls, sums []string
	for _, src := range p.DS.Sources() {
		sum, err := p.get(ctx, src, filepath.Join(incoming, src.File))
		if err != nil {
			return 0, fmt.Errorf("download %s: %w", src.URL, err)
		}
		// The pin is checked before any row is written or file uploaded.
		if src.SHA256 != "" && sum != src.SHA256 {
			return 0, fmt.Errorf("%s: sha256 %s does not match the pin %s; live data unchanged; if the publisher changed this file on purpose, review it and update the pin in internal/ingest/%s.go",
				src.File, sum, src.SHA256, name)
		}
		urls = append(urls, src.URL)
		sums = append(sums, src.File+"="+sum)
	}
	sourceURL := strings.Join(urls, "\n")
	reviewed, err := p.q().LicenseReviewedBefore(ctx, store.LicenseReviewedBeforeParams{
		Dataset: name, License: p.DS.License(), SourceUrl: sourceURL})
	if err != nil {
		return 0, err
	}
	id, err := p.q().CreateDatasetVersion(ctx, store.CreateDatasetVersionParams{
		Dataset: name, SourceUrl: sourceURL, SourceChecksum: "sha256:" + strings.Join(sums, ";"),
		License: p.DS.License(), LicenseReviewed: reviewed,
	})
	if err != nil {
		return 0, err
	}
	// From here on, any failure must not leave a half-fetched dataset_versions
	// row behind: Latest/PreviousPromotedVersion must never pick it up.
	if err := p.finishFetch(ctx, id, name, incoming); err != nil {
		if derr := p.q().DeleteDatasetVersion(ctx, id); derr != nil {
			p.Log.Error("cleanup after failed fetch: could not delete dataset_versions row", "dataset", name, "version", id, "error", derr)
		}
		return 0, err
	}
	p.Log.Info("fetched", "dataset", name, "version", id, "license", p.DS.License(), "license_reviewed", reviewed)
	return id, nil
}

// finishFetch sets the table name and uploads the raw files for a
// newly-created dataset_versions row.
func (p *Pipeline) finishFetch(ctx context.Context, id int64, name, incoming string) error {
	table := p.DS.Prefix() + strconv.FormatInt(id, 10)
	if err := p.q().SetDatasetTable(ctx, store.SetDatasetTableParams{ID: id, TableName: table}); err != nil {
		return err
	}
	raw := filepath.Join(p.versionDir(id), "raw")
	if err := os.MkdirAll(raw, 0o755); err != nil {
		return err
	}
	for _, src := range p.DS.Sources() {
		local := filepath.Join(raw, src.File)
		if err := os.Rename(filepath.Join(incoming, src.File), local); err != nil {
			return err
		}
		if err := p.Blob.PutFile(ctx, rawKey(name, id, src.File), local); err != nil {
			return fmt.Errorf("upload %s: %w", src.File, err)
		}
	}
	return nil
}

// Transform produces the load-ready CSV once per version.
func (p *Pipeline) Transform(ctx context.Context, v int64) error {
	if _, err := p.version(ctx, v); err != nil {
		return err
	}
	name, key := p.DS.Name(), csvKey(p.DS.Name(), v)
	if ok, err := p.Blob.Exists(ctx, key); err != nil {
		return err
	} else if ok {
		p.Log.Info("already transformed", "dataset", name, "version", v)
		return nil
	}
	dir := p.versionDir(v)
	for _, src := range p.DS.Sources() {
		local := filepath.Join(dir, "raw", src.File)
		if _, err := os.Stat(local); errors.Is(err, os.ErrNotExist) {
			if err := p.Blob.GetFile(ctx, rawKey(name, v, src.File), local); err != nil {
				return fmt.Errorf("download raw %s: %w", src.File, err)
			}
		}
	}
	out := filepath.Join(dir, "data.csv")
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	err = p.DS.Transform(ctx, dir, f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(out)
		return err
	}
	return p.Blob.PutFile(ctx, key, out)
}

// Load (re)creates the version table and fills it. It refuses the live version.
func (p *Pipeline) Load(ctx context.Context, v int64) error {
	dv, err := p.version(ctx, v)
	if err != nil {
		return err
	}
	if dv.IsLive {
		return fmt.Errorf("refusing to reload live version %d", v)
	}
	if !store.ValidIdent(dv.TableName) {
		return fmt.Errorf("bad table name %q", dv.TableName)
	}
	local := filepath.Join(p.versionDir(v), "data.csv")
	if _, err := os.Stat(local); errors.Is(err, os.ErrNotExist) {
		if err := p.Blob.GetFile(ctx, csvKey(p.DS.Name(), v), local); err != nil {
			return fmt.Errorf("transformed file missing (run transform): %w", err)
		}
	}
	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()

	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, "DROP TABLE IF EXISTS "+dv.TableName); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, p.DS.CreateSQL(dv.TableName)); err != nil {
		return fmt.Errorf("create table: %w", err)
	}
	if err := p.DS.Load(ctx, tx, dv.TableName, f); err != nil {
		return fmt.Errorf("load: %w", err)
	}
	if err := store.New(tx).MarkDatasetLoaded(ctx, v); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	p.Log.Info("loaded", "dataset", p.DS.Name(), "version", v, "table", dv.TableName)
	return nil
}

// ApproveLicense marks a version's license as reviewed by a human.
func (p *Pipeline) ApproveLicense(ctx context.Context, v int64) (store.DatasetVersion, error) {
	dv, err := p.version(ctx, v)
	if err != nil {
		return dv, err
	}
	if strings.TrimSpace(dv.License) == "" {
		return dv, errors.New("no license recorded")
	}
	if strings.Contains(dv.License, LicenseUnresolved) {
		return dv, errors.New("license terms not confirmed; see docs/reference-data-licenses.md")
	}
	return dv, p.q().ApproveDatasetLicense(ctx, v)
}

// Validate runs the common and dataset-specific checks on version v.
func (p *Pipeline) Validate(ctx context.Context, v int64) error {
	dv, err := p.version(ctx, v)
	if err != nil {
		return err
	}
	if !store.ValidIdent(dv.TableName) {
		return fmt.Errorf("bad table name %q", dv.TableName)
	}
	var fails []string
	if strings.TrimSpace(dv.License) == "" {
		fails = append(fails, "no license recorded")
	} else if strings.Contains(dv.License, LicenseUnresolved) {
		fails = append(fails, "license unresolved (terms not confirmed; see docs/reference-data-licenses.md)")
	} else if !dv.LicenseReviewed {
		fails = append(fails, fmt.Sprintf("license not reviewed (run: ingest %s approve-license --version %d --yes)", p.DS.Name(), v))
	}
	var exists bool
	if err := p.Pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, dv.TableName).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		fails = append(fails, "table not loaded (run load)")
		return &ValidationError{Dataset: p.DS.Name(), Failures: fails}
	}
	var n int64
	if err := p.Pool.QueryRow(ctx, "SELECT count(*) FROM "+dv.TableName).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		fails = append(fails, "table is empty")
	}
	live, err := p.q().LiveDatasetVersion(ctx, p.DS.Name())
	switch {
	case errors.Is(err, pgx.ErrNoRows): // first load: no row-count comparison
	case err != nil:
		return err
	case live.ID != v:
		if !store.ValidIdent(live.TableName) {
			return fmt.Errorf("bad table name %q", live.TableName)
		}
		var liveN int64
		if err := p.Pool.QueryRow(ctx, "SELECT count(*) FROM "+live.TableName).Scan(&liveN); err != nil {
			return err
		}
		if liveN > 0 {
			diff := float64(n-liveN) / float64(liveN)
			if diff < -0.05 || diff > 0.05 {
				fails = append(fails, fmt.Sprintf("row count %d differs from live %d by %.1f%% (limit ±5%%)", n, liveN, diff*100))
			}
		}
	}
	fails = append(fails, p.DS.Check(ctx, p.Pool, dv.TableName)...)
	if len(fails) > 0 {
		return &ValidationError{Dataset: p.DS.Name(), Failures: fails}
	}
	p.Log.Info("validated", "dataset", p.DS.Name(), "version", v, "rows", n)
	return nil
}

// Swap validates v and makes it live in one transaction.
func (p *Pipeline) Swap(ctx context.Context, v int64) error {
	if err := p.Validate(ctx, v); err != nil {
		return err
	}
	dv, err := p.version(ctx, v)
	if err != nil {
		return err
	}
	return p.point(ctx, dv, 0)
}

// Rollback repoints the view at the most recent earlier promoted version.
func (p *Pipeline) Rollback(ctx context.Context) error {
	live, err := p.q().LiveDatasetVersion(ctx, p.DS.Name())
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("no live version to roll back from")
	}
	if err != nil {
		return err
	}
	prev, err := p.q().PreviousPromotedVersion(ctx, store.PreviousPromotedVersionParams{Dataset: p.DS.Name(), Before: live.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("no earlier promoted version before %d", live.ID)
	}
	if err != nil {
		return err
	}
	if !store.ValidIdent(prev.TableName) {
		return fmt.Errorf("bad table name %q", prev.TableName)
	}
	var exists bool
	if err := p.Pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, prev.TableName).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("version %d table %s was dropped", prev.ID, prev.TableName)
	}
	// live is rolled back away from: clear its promoted_at so a later
	// PreviousPromotedVersion query skips it instead of landing back on it.
	return p.point(ctx, prev, live.ID)
}

// point repoints the live view at dv in one transaction. When clearPromoted
// is non-zero (a rollback), that version's promoted_at is cleared first so
// PreviousPromotedVersion will not return to it again.
func (p *Pipeline) point(ctx context.Context, dv store.DatasetVersion, clearPromoted int64) error {
	if !store.ValidIdent(dv.TableName) || !store.ValidIdent(p.DS.View()) {
		return fmt.Errorf("bad identifier")
	}
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, fmt.Sprintf("CREATE OR REPLACE VIEW %s AS SELECT * FROM %s", p.DS.View(), dv.TableName)); err != nil {
		return fmt.Errorf("repoint view: %w", err)
	}
	q := store.New(tx)
	if clearPromoted != 0 {
		if err := q.ClearPromoted(ctx, clearPromoted); err != nil {
			return err
		}
	}
	if err := q.ClearLive(ctx, p.DS.Name()); err != nil {
		return err
	}
	if err := q.MarkLive(ctx, dv.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('data_version_changed', 'dataset')`); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	p.Log.Info("live", "dataset", p.DS.Name(), "version", dv.ID, "table", dv.TableName)
	return nil
}

// All runs every step for v, fetching a new version first when v == 0.
func (p *Pipeline) All(ctx context.Context, v int64) error {
	if v == 0 {
		var err error
		if v, err = p.Fetch(ctx); err != nil {
			return err
		}
	}
	if err := p.Transform(ctx, v); err != nil {
		return err
	}
	if err := p.Load(ctx, v); err != nil {
		return err
	}
	return p.Swap(ctx, v)
}

// copyStage streams CSV into a staging table with COPY.
func copyStage(ctx context.Context, tx pgx.Tx, stage string, r io.Reader) error {
	_, err := tx.Conn().PgConn().CopyFrom(ctx, r, "COPY "+stage+" FROM STDIN WITH (FORMAT csv, HEADER true)")
	return err
}

func download(ctx context.Context, client *http.Client, url, path string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), resp.Body); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
