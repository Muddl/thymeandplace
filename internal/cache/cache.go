// Package cache is the versioned in-process LRU for season results and
// per-cell reference-data inputs.
package cache

import (
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

const DefaultSize = 50_000

// CellSize is the capacity of the per-cell input cache (/ingredient).
const CellSize = 20_000

// Key includes the data version, so a version change makes older entries
// unreachable; LRU eviction reclaims them.
type Key struct {
	CellID      int
	Month       time.Month
	DataVersion string
}

// CellKey keys values that depend only on the grid cell and the reference
// data, not the month (a cell's climate normals and ecoregions).
type CellKey struct {
	CellID      int
	DataVersion string
}

type Cache[K comparable, V any] struct{ c *lru.Cache[K, V] }

func New[K comparable, V any](size int) *Cache[K, V] {
	c, err := lru.New[K, V](size)
	if err != nil {
		panic(err) // only for size <= 0
	}
	return &Cache[K, V]{c: c}
}

func (c *Cache[K, V]) Get(k K) (V, bool) { return c.c.Get(k) }
func (c *Cache[K, V]) Add(k K, v V)      { c.c.Add(k, v) }
func (c *Cache[K, V]) Len() int          { return c.c.Len() }
