// Package cache is the versioned in-process LRU for season results.
package cache

import (
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

const DefaultSize = 50_000

// Key includes the data version, so a version change makes older entries
// unreachable; LRU eviction reclaims them.
type Key struct {
	CellID      int
	Month       time.Month
	DataVersion string
}

type Cache[V any] struct{ c *lru.Cache[Key, V] }

func New[V any](size int) *Cache[V] {
	c, err := lru.New[Key, V](size)
	if err != nil {
		panic(err) // only for size <= 0
	}
	return &Cache[V]{c: c}
}

func (c *Cache[V]) Get(k Key) (V, bool) { return c.c.Get(k) }
func (c *Cache[V]) Add(k Key, v V)      { c.c.Add(k, v) }
func (c *Cache[V]) Len() int            { return c.c.Len() }
