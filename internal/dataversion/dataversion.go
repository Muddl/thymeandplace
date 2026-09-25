// Package dataversion computes the data version that keys caches:
// a hash of the catalog version and the live reference-dataset IDs.
package dataversion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Compute returns the first 16 hex characters of
// sha256("catalog:<v>;datasets:<ids ascending, comma-separated>").
func Compute(catalogVersion int64, liveDatasetIDs []int64) string {
	ids := slices.Clone(liveDatasetIDs)
	slices.Sort(ids)
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("catalog:%d;datasets:%s", catalogVersion, strings.Join(parts, ","))))
	return hex.EncodeToString(sum[:])[:16]
}

// Reader is satisfied by *store.Queries.
type Reader interface {
	GetCatalogVersion(ctx context.Context) (int64, error)
	ListLiveDatasetIDs(ctx context.Context) ([]int64, error)
}

// Current reads the inputs from the database and computes the data version.
func Current(ctx context.Context, r Reader) (string, error) {
	v, err := r.GetCatalogVersion(ctx)
	if err != nil {
		return "", fmt.Errorf("catalog version: %w", err)
	}
	ids, err := r.ListLiveDatasetIDs(ctx)
	if err != nil {
		return "", fmt.Errorf("live datasets: %w", err)
	}
	return Compute(v, ids), nil
}
