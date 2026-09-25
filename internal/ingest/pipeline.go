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

type Source struct{ URL, File string }

// Dataset is one reference source.
type Dataset interface {
	Name() string   // "climate" | "ocean" | "places"
	View() string   // live view, e.g. "place"
	Prefix() string // versioned table prefix, e.g. "place_v"
	License() string
	Sources() []Source
	// Transform reads versionDir/raw and writes load-ready CSV to w.
	Transform(ctx context.Context, versionDir string, w io.Writer) error
	CreateSQL(table string) string
	Load(ctx context.Context, tx pgx.Tx, table string, csv io.Reader) error
	// Check runs dataset-specific validation against table; each string is one failure.
	Check(ctx context.Context, db store.DBTX, table string) []string
}

// ExternalTransformError means the transform must run in another container.
type ExternalTransformError struct{ Cmd string }

func (e *ExternalTransformError) Error() string {
	return "transform output not found; run: " + e.Cmd + " — then run this step again"
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
		sum, err := download(ctx, p.HTTP, src.URL, filepath.Join(incoming, src.File))
		if err != nil {
			return 0, fmt.Errorf("download %s: %w", src.URL, err)
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
	table := p.DS.Prefix() + strconv.FormatInt(id, 10)
	if err := p.q().SetDatasetTable(ctx, store.SetDatasetTableParams{ID: id, TableName: table}); err != nil {
		return 0, err
	}
	raw := filepath.Join(p.versionDir(id), "raw")
	if err := os.MkdirAll(raw, 0o755); err != nil {
		return 0, err
	}
	for _, src := range p.DS.Sources() {
		local := filepath.Join(raw, src.File)
		if err := os.Rename(filepath.Join(incoming, src.File), local); err != nil {
			return 0, err
		}
		if err := p.Blob.PutFile(ctx, rawKey(name, id, src.File), local); err != nil {
			return 0, fmt.Errorf("upload %s: %w", src.File, err)
		}
	}
	p.Log.Info("fetched", "dataset", name, "version", id, "license", p.DS.License(), "license_reviewed", reviewed)
	return id, nil
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
	return dv, p.q().ApproveDatasetLicense(ctx, v)
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
