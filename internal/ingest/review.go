package ingest

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ReviewSummary is what an operator reads before approving version v: the
// license, and for each file its URL, sha256, how it is trusted, and how it
// compares with the live version.
func (p *Pipeline) ReviewSummary(ctx context.Context, v int64) (string, error) {
	dv, err := p.version(ctx, v)
	if err != nil {
		return "", err
	}
	sums, err := parseChecksums(dv.SourceChecksum)
	if err != nil {
		return "", err
	}
	var liveID int64
	var liveSums map[string]string
	live, err := p.q().LiveDatasetVersion(ctx, p.DS.Name())
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return "", err
	default:
		liveID = live.ID
		if liveSums, err = parseChecksums(live.SourceChecksum); err != nil {
			return "", fmt.Errorf("live version %d: %w", live.ID, err)
		}
	}
	urls := strings.Split(dv.SourceUrl, "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "license:\n  %s\nfiles:\n", dv.License)
	for i, src := range p.DS.Sources() {
		u := src.URL
		if i < len(urls) {
			u = urls[i]
		}
		trust := "pinned"
		if src.SHA256 == "" {
			trust = "unpinned: " + src.Unpinned
		}
		cmp := "no live version"
		if liveSums != nil {
			switch l := liveSums[src.File]; {
			case l == sums[src.File]:
				cmp = fmt.Sprintf("same as live v%d", liveID)
			case len(l) >= 12:
				cmp = fmt.Sprintf("changed (live v%d: %s…)", liveID, l[:12])
			default:
				cmp = fmt.Sprintf("changed (live v%d has no record of this file)", liveID)
			}
		}
		fmt.Fprintf(&b, "  %s  %s\n    sha256 %s  %s  %s\n", src.File, u, sums[src.File], trust, cmp)
	}
	return b.String(), nil
}
