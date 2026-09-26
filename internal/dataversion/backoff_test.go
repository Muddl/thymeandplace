package dataversion

import (
	"testing"
	"time"
)

func TestNextBackoffDoublesToCap(t *testing.T) {
	want := []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second,
	}
	d := minBackoff
	if d != 500*time.Millisecond {
		t.Fatalf("minBackoff = %v", d)
	}
	for i, w := range want {
		d = nextBackoff(d)
		if d != w {
			t.Fatalf("step %d: %v, want %v", i, d, w)
		}
	}
}
