// Command ingest runs the reference-data pipeline.
//
//	ingest <dataset> fetch|transform|load|validate|swap|all [--version N]
//	ingest <dataset> rollback
//	ingest <dataset> approve-license --version N --yes
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"thymeandplace/internal/db"
	"thymeandplace/internal/ingest"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr, defaultDatasets(os.Getenv)))
}

func defaultDatasets(getenv func(string) string) map[string]func() ingest.Dataset {
	return map[string]func() ingest.Dataset{
		"places":  func() ingest.Dataset { return ingest.NewPlaces() },
		"ocean":   func() ingest.Dataset { return ingest.NewOcean(getenv("OCEAN_SOURCE_URL")) },
		"climate": func() ingest.Dataset { return ingest.NewClimate(getenv("CLIMATE_SOURCE_BASE")) },
	}
}

const usage = `usage:
  ingest <climate|ocean|places> fetch|transform|load|validate|swap|all [--version N]
  ingest <climate|ocean|places> rollback
  ingest <climate|ocean|places> approve-license --version N --yes`

// validSteps are the recognized step names, checked before any DB or blob
// store connection is made so a typo exits 2 without touching either.
var validSteps = map[string]bool{
	"fetch": true, "transform": true, "load": true, "validate": true,
	"swap": true, "all": true, "rollback": true, "approve-license": true,
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer, datasets map[string]func() ingest.Dataset) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	name, step := args[0], args[1]
	mk, ok := datasets[name]
	if !ok {
		fmt.Fprintf(stderr, "unknown dataset %q\n%s\n", name, usage)
		return 2
	}
	if !validSteps[step] {
		fmt.Fprintf(stderr, "unknown step %q\n%s\n", step, usage)
		return 2
	}
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	version := fs.Int64("version", 0, "dataset version (default: latest)")
	yes := fs.Bool("yes", false, "confirm license approval")
	if err := fs.Parse(args[2:]); err != nil {
		return 2
	}
	v := *version
	if step == "approve-license" && (v == 0 || !*yes) {
		fmt.Fprintln(stderr, "approve-license needs --version N and --yes after you have read the license")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fail := func(err error) int {
		fmt.Fprintf(stderr, "ingest %s %s: %v\n", name, step, err)
		var ext *ingest.ExternalTransformError
		if errors.As(err, &ext) {
			return 3
		}
		return 1
	}

	pool, err := db.Open(ctx, getenv("DATABASE_URL"))
	if err != nil {
		return fail(err)
	}
	defer pool.Close()
	blob, err := blobFromEnv(getenv)
	if err != nil {
		return fail(err)
	}
	work := getenv("INGEST_WORKDIR")
	if work == "" {
		work = "/work"
	}
	p := &ingest.Pipeline{
		DS: mk(), Pool: pool, Blob: blob, WorkDir: work,
		HTTP: &http.Client{Timeout: 2 * time.Hour},
		Log:  slog.New(slog.NewJSONHandler(stdout, nil)),
	}

	needsVersion := map[string]bool{"transform": true, "load": true, "validate": true, "swap": true}
	if v == 0 && needsVersion[step] {
		if v, err = p.Latest(ctx); err != nil {
			return fail(err)
		}
	}

	switch step {
	case "fetch":
		v, err = p.Fetch(ctx)
		if err == nil {
			fmt.Fprintf(stdout, "fetched %s version %d\n", name, v)
			fmt.Fprintf(stdout, "license:\n  %s\n", p.DS.License())
			for _, src := range p.DS.Sources() {
				fmt.Fprintf(stdout, "source: %s\n", src.URL)
			}
			fmt.Fprintf(stdout, "read the license above before running: ingest %s approve-license --version %d --yes\n", name, v)
		}
	case "transform":
		err = p.Transform(ctx, v)
	case "load":
		err = p.Load(ctx, v)
	case "validate":
		err = p.Validate(ctx, v)
	case "swap":
		err = p.Swap(ctx, v)
	case "all":
		err = p.All(ctx, v)
	case "rollback":
		err = p.Rollback(ctx)
	case "approve-license":
		dv, aerr := p.ApproveLicense(ctx, v)
		err = aerr
		if err == nil {
			fmt.Fprintf(stdout, "approved license for %s version %d:\n  %s\n  source: %s\n", name, v, dv.License, dv.SourceUrl)
		}
	}
	if err != nil {
		return fail(err)
	}
	return 0
}

func blobFromEnv(getenv func(string) string) (ingest.Blob, error) {
	if dir := getenv("BLOB_DIR"); dir != "" {
		return ingest.FSBlob{Root: dir}, nil
	}
	endpoint, bucket := getenv("BUCKET_ENDPOINT"), getenv("BUCKET_NAME")
	if endpoint == "" || bucket == "" {
		return nil, errors.New("set BLOB_DIR, or BUCKET_ENDPOINT and BUCKET_NAME")
	}
	return ingest.NewS3Blob(endpoint, bucket, getenv("BUCKET_ACCESS_KEY"), getenv("BUCKET_SECRET_KEY"), getenv("BUCKET_USE_SSL") != "false")
}
