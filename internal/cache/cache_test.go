package cache

import (
	"testing"
	"time"
)

func TestVersionChangeMakesOldKeysUnreachable(t *testing.T) {
	c := New[Key, string](10)
	k := Key{CellID: 1, Month: time.May, DataVersion: "v1"}
	c.Add(k, "old")
	if v, ok := c.Get(k); !ok || v != "old" {
		t.Fatal("hit expected")
	}
	k2 := k
	k2.DataVersion = "v2"
	if _, ok := c.Get(k2); ok {
		t.Fatal("new version must miss")
	}
}

func TestEviction(t *testing.T) {
	c := New[Key, int](2)
	for i := 0; i < 3; i++ {
		c.Add(Key{CellID: i, Month: time.May, DataVersion: "v"}, i)
	}
	if c.Len() != 2 {
		t.Fatalf("len = %d", c.Len())
	}
	if _, ok := c.Get(Key{CellID: 0, Month: time.May, DataVersion: "v"}); ok {
		t.Fatal("oldest key should be evicted")
	}
}

func TestCellKeyIgnoresMonthButNotVersion(t *testing.T) {
	c := New[CellKey, string](10)
	c.Add(CellKey{CellID: 7, DataVersion: "v1"}, "inputs")
	if v, ok := c.Get(CellKey{CellID: 7, DataVersion: "v1"}); !ok || v != "inputs" {
		t.Fatal("hit expected")
	}
	if _, ok := c.Get(CellKey{CellID: 7, DataVersion: "v2"}); ok {
		t.Fatal("new version must miss")
	}
	if _, ok := c.Get(CellKey{CellID: 8, DataVersion: "v1"}); ok {
		t.Fatal("another cell must miss")
	}
}
