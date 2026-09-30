package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// bucketScheme marks a source read from the pipeline's own blob store (a
// reviewed copy, e.g. pinned/ocean/meow.geojson). It must be pinned.
const bucketScheme = "bucket:"

// get fetches src to dst and returns its hex sha256.
func (p *Pipeline) get(ctx context.Context, src Source, dst string) (string, error) {
	if key, ok := strings.CutPrefix(src.URL, bucketScheme); ok {
		if err := p.Blob.GetFile(ctx, key, dst); err != nil {
			return "", err
		}
		return hashFile(dst)
	}
	return download(ctx, p.HTTP, src.URL, dst)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// parseChecksums reads dataset_versions.source_checksum:
// "sha256:<file>=<hex>;<file>=<hex>".
func parseChecksums(s string) (map[string]string, error) {
	rest, ok := strings.CutPrefix(s, "sha256:")
	if !ok || rest == "" {
		return nil, errors.New("source_checksum is not in sha256:<file>=<hex>;… form")
	}
	out := map[string]string{}
	for _, part := range strings.Split(rest, ";") {
		file, sum, ok := strings.Cut(part, "=")
		if !ok || file == "" || sum == "" {
			return nil, fmt.Errorf("source_checksum entry %q is malformed", part)
		}
		out[file] = sum
	}
	return out, nil
}

// CheckTransport refuses sources whose integrity rests on nothing: plain
// HTTP and bucket: are allowed only with a sha256 pin; HTTPS is allowed
// either way. cmd/ingest calls it before any step that fetches.
func CheckTransport(ds Dataset) error {
	for _, s := range ds.Sources() {
		u, err := url.Parse(s.URL)
		if err != nil {
			return fmt.Errorf("%s: bad source URL: %w", s.File, err)
		}
		switch {
		case u.Scheme == "https":
		case (u.Scheme == "http" || u.Scheme+":" == bucketScheme) && s.SHA256 != "":
		case u.Scheme == "http" || u.Scheme+":" == bucketScheme:
			return fmt.Errorf("%s: %s sources must be pinned by sha256", s.File, u.Scheme)
		default:
			return fmt.Errorf("%s: unsupported source scheme %q", s.File, u.Scheme)
		}
	}
	return nil
}

// NewHTTPClient is the fetch client: it follows at most 10 redirects (a
// non-nil CheckRedirect replaces Go's default limit) and never follows one
// from an HTTPS request to a non-HTTPS URL.
func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return fmt.Errorf("refusing redirect from HTTPS to %s", req.URL.Scheme)
			}
			return nil
		},
	}
}
