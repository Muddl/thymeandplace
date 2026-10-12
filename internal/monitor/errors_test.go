package monitor

import (
	"bytes"
	"log/slog"
	"reflect"
	"testing"
)

func TestErrorCounter(t *testing.T) {
	var out bytes.Buffer
	ec := NewErrorCounter(slog.NewTextHandler(&out, nil))
	log := slog.New(ec)
	log.Warn("season lookup failed", "cell_id", 1)
	log.Error("admin prune failed", "err", "secret detail 10.0.0.1")
	log.With("request_id", "r1").Error("admin prune failed")
	log.WithGroup("g").Error("lookup failed for 51.5,-0.1")
	log.Error("catalog reload failed; keeping previous snapshot")
	log.Error("apply failed")
	n, msgs := ec.Take()
	want := []string{"admin prune failed", "(message withheld)", "catalog reload failed; keeping previous snapshot"}
	if n != 5 || !reflect.DeepEqual(msgs, want) {
		t.Fatalf("n=%d msgs=%q; want 5, %q", n, msgs, want)
	}
	if n, msgs := ec.Take(); n != 0 || msgs != nil {
		t.Fatal("Take must reset")
	}
	if !bytes.Contains(out.Bytes(), []byte("secret detail")) {
		t.Fatal("records must still reach the wrapped handler unchanged")
	}
}
