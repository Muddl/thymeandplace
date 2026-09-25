package dataversion_test

import (
	"context"
	"testing"

	"thymeandplace/internal/dataversion"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/store"
)

func TestCompute(t *testing.T) {
	a := dataversion.Compute(3, []int64{9, 2})
	if len(a) != 16 {
		t.Fatalf("len = %d", len(a))
	}
	if a != dataversion.Compute(3, []int64{2, 9}) {
		t.Fatal("ID order must not matter")
	}
	if a == dataversion.Compute(4, []int64{2, 9}) || a == dataversion.Compute(3, []int64{2, 10}) {
		t.Fatal("different inputs must differ")
	}
	// Known value guards the format: sha256("catalog:0;datasets:")[:16].
	if got := dataversion.Compute(0, nil); got != "9073c2c64186c6b3" {
		t.Fatalf("Compute(0, nil) = %q", got)
	}
}

func TestCurrent(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	v, err := dataversion.Current(ctx, store.New(pool))
	if err != nil || v != dataversion.Compute(0, nil) {
		t.Fatalf("v=%q err=%v", v, err)
	}
}
