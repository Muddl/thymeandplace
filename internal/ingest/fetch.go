package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
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
