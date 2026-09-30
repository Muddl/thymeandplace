package places_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/places"
)

func svc(t *testing.T) *places.Service {
	pool := dbtest.New(t)
	dbtest.LoadReference(t, pool)
	return places.NewService(pool)
}

func TestSearchRanking(t *testing.T) {
	s := svc(t)
	ctx := context.Background()
	got, err := s.Search(ctx, "london")
	if err != nil || len(got) < 2 || got[0].Country != "GB" || got[1].Country != "CA" {
		t.Fatalf("london: %+v %v", got, err)
	}
	got, _ = s.Search(ctx, "Paris")
	if len(got) < 2 || got[0].Country != "FR" {
		t.Fatalf("paris: %+v", got)
	}
	got, _ = s.Search(ctx, "Filler")
	if len(got) != places.SearchLimit {
		t.Fatalf("limit: got %d", len(got))
	}
}

// Review Focus 1.
func TestSearchAccentsAndTypos(t *testing.T) {
	s := svc(t)
	ctx := context.Background()
	for _, q := range []string{"Zürich", "zurich", "São Paulo", "sao paulo", "lisbn"} {
		got, err := s.Search(ctx, q)
		if err != nil || len(got) == 0 {
			t.Fatalf("%q: %v %v", q, got, err)
		}
	}
	got, _ := s.Search(ctx, "lisbn")
	if got[0].Name != "Lisbon" {
		t.Fatalf("typo top = %q", got[0].Name)
	}
}

// Review Focus 3.
func TestSearchHostileInput(t *testing.T) {
	s := svc(t)
	ctx := context.Background()
	for _, q := range []string{"", "   ", strings.Repeat("a", 5000), "%", "_", "'; DROP TABLE place; --", `\`, "%_%", "a\x00b", "\xff", "Lis\xffbon", "%%lis", "li_s", "lis\\"} {
		if _, err := s.Search(ctx, q); err != nil {
			t.Fatalf("%q: %v", q[:min(len(q), 20)], err)
		}
	}
	if got, _ := s.Search(ctx, "  "); got != nil {
		t.Fatal("blank query must return nil")
	}
	if got, _ := s.Search(ctx, "London"); len(got) == 0 {
		t.Fatal("table damaged by hostile input")
	}
	if got, _ := s.Search(ctx, "Lis\xffbon"); len(got) == 0 || got[0].Name != "Lisbon" {
		t.Fatalf("Lis\\xffbon must still find Lisbon: %+v", got)
	}
}

func TestNearestAndByID(t *testing.T) {
	s := svc(t)
	ctx := context.Background()
	p, ok, err := s.Nearest(ctx, dbtest.Lisbon.Lat+0.01, dbtest.Lisbon.Lng)
	if err != nil || !ok || p.Name != "Lisbon" || p.DistanceKm > 2 {
		t.Fatalf("nearest: %+v %v %v", p, ok, err)
	}
	if p.Label() != "Lisbon, PT" {
		t.Fatalf("label = %q", p.Label())
	}
	g, ok, _ := s.ByID(ctx, 6)
	if !ok || g.Label() != "London, England, GB" {
		t.Fatalf("byID: %+v", g)
	}
	if _, ok, err := s.ByID(ctx, 424242); ok || err != nil {
		t.Fatalf("missing ID: %v %v", ok, err)
	}
}

func TestTooShort(t *testing.T) {
	// "a\u0301" is a decomposed á: the combining accent is neither a letter
	// nor a digit, so it does not count.
	short := []string{"", "a", "é", "a\u0301", "1", "%", "--", "a.", " a ", "á", "_a_", "a\x00", "\ta\n"}
	long := []string{"bo", "Ii", "Aš", "12", "東京", "Αθ", "lis", "áb", "a\u0301b", "a1"}
	for _, q := range short {
		if !places.TooShort(places.Normalize(q)) {
			t.Errorf("TooShort(%q) = false, want true", q)
		}
	}
	for _, q := range long {
		if places.TooShort(places.Normalize(q)) {
			t.Errorf("TooShort(%q) = true, want false", q)
		}
	}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"  Lisbon ":  "Lisbon",
		"Lis\xffbon": "Lisbon",
		"a\x00b":     "ab",
		"\ta\n":      "a",
	} {
		if got := places.Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if got := places.Normalize(strings.Repeat("é", 500)); len([]rune(got)) != places.MaxQueryRunes {
		t.Fatalf("not truncated: %d runes", len([]rune(got)))
	}
}

// noQueryDB fails the test on any SQL: short queries must never reach it.
// Test-local copy of the pattern in internal/ingest/places_test.go:15-32.
type noQueryDB struct{ t *testing.T }

func (d noQueryDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	d.t.Fatal("unexpected Exec")
	return pgconn.CommandTag{}, nil
}

func (d noQueryDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	d.t.Fatal("unexpected Query")
	return nil, nil
}

func (d noQueryDB) QueryRow(context.Context, string, ...any) pgx.Row {
	d.t.Fatal("unexpected QueryRow")
	return nil
}

func TestSearchTooShortSkipsDB(t *testing.T) {
	s := places.NewService(noQueryDB{t})
	for _, q := range []string{"a", "é", "1", "%", "--", "a.", " a ", "_", `\`} {
		if got, err := s.Search(context.Background(), q); got != nil || err != nil {
			t.Fatalf("%q: %v %v", q, got, err)
		}
	}
}
