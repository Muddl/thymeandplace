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
		// The labels describe the recorded row, which may predate today's
		// pins (a version fetched before a deploy, or a hand-edited row).
		sum := sums[src.File]
		var trust string
		switch {
		case src.SHA256 == "":
			trust = "unpinned: " + src.Unpinned
		case sum == src.SHA256:
			trust = "pinned"
		default:
			trust = fmt.Sprintf("pin mismatch (pin %s…)", short(src.SHA256))
		}
		if sum == "" {
			fmt.Fprintf(&b, "  %s  %s\n    sha256 (no record)  %s\n", src.File, u, trust)
			continue
		}
		cmp := "no live version"
		if liveSums != nil {
			switch l := liveSums[src.File]; {
			case l == sum:
				cmp = fmt.Sprintf("same as live v%d", liveID)
			case l != "":
				cmp = fmt.Sprintf("changed (live v%d: %s…)", liveID, short(l))
			default:
				cmp = fmt.Sprintf("changed (live v%d has no record of this file)", liveID)
			}
		}
		fmt.Fprintf(&b, "  %s  %s\n    sha256 %s  %s  %s\n", src.File, u, sum, trust, cmp)
	}
	return b.String(), nil
}

// short is the first 12 characters of a hex checksum, for display.
func short(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}
