package auth

import (
	"bytes"
	"io"
	"log/slog"
	"testing"
	"time"

	"thymeandplace/internal/dbtest"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func newTestService(t *testing.T, backup bool) (*Service, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)}
	s, err := NewService(dbtest.New(t), Config{
		RPID: "localhost", RPOrigins: []string{"https://localhost"},
		SecretKey: bytes.Repeat([]byte{9}, 32), BackupEnabled: backup,
		Now: c.now, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}
