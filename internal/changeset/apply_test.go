package changeset

import (
	"context"
	"errors"
	"strings"
	"testing"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/store"
)

func loadSnap(t *testing.T, svc *Service) *catalog.Snapshot {
	t.Helper()
	s, err := catalog.Load(context.Background(), svc.pool)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestApplyWritesWithActorAndChangeID(t *testing.T) {
	ctx := context.Background()
	svc, pool := newTestService(t, true)
	k := catalogtest.Hardy()
	k.Name = "Curly kale"
	p, _, err := svc.Preview(ctx, FormChangeset(k))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := loadSnap(t, svc).Get("kale"); got.Name != "Curly kale" {
		t.Fatalf("kale = %q", got.Name)
	}
	var actor string
	var change int64
	err = pool.QueryRow(ctx, `SELECT actor, change_id FROM catalog_history WHERE table_name = 'ingredient' ORDER BY id DESC LIMIT 1`).Scan(&actor, &change)
	if err != nil || actor != "owner" || change != p.ID {
		t.Fatalf("history actor=%q change=%d err=%v", actor, change, err)
	}
	var left int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM pending_changeset WHERE id = $1`, p.ID).Scan(&left)
	if left != 0 {
		t.Fatal("pending row not deleted")
	}
}

// Review Focus 4: double apply.
func TestApplyTwice(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, true)
	k := catalogtest.Hardy()
	k.Note = "once"
	p, _, _ := svc.Preview(ctx, FormChangeset(k))
	if err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{})
	if !errors.Is(err, ErrNotFound) || UserMessage(err) == "" {
		t.Fatalf("second apply: %v", err)
	}
}

// Review Focus 3: expired preview.
func TestApplyExpired(t *testing.T) {
	ctx := context.Background()
	svc, pool := newTestService(t, true)
	p, _, _ := svc.Preview(ctx, FormChangeset(catalogtest.Hardy()))
	if _, err := pool.Exec(ctx, `UPDATE pending_changeset SET expires_at = now() - interval '1 second' WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestApplyStale(t *testing.T) {
	ctx := context.Background()
	svc, pool := newTestService(t, true)
	p, _, _ := svc.Preview(ctx, FormChangeset(catalogtest.Hardy()))
	if _, err := pool.Exec(ctx, `UPDATE ingredient SET note = 'someone else' WHERE slug = 'banana'`); err != nil {
		t.Fatal(err)
	}
	err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{})
	if !errors.Is(err, ErrStale) || UserMessage(err) != "The catalog changed since this preview. Preview again." {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := svc.Reopen(ctx, p.ID); !errors.Is(err, ErrStale) {
		t.Fatalf("reopen stale: %v", err)
	}
}

func TestApplyOverride(t *testing.T) {
	ctx := context.Background()
	svc, pool := newTestService(t, true)
	tomato := catalogtest.Annual()
	tomato.Pairings = []string{"basil"}
	tomato.MonthsToHarvest = catalogtest.P(1)
	p, pv, _ := svc.Preview(ctx, FormChangeset(tomato))
	if !pv.NeedsOverride {
		t.Fatal("expected override to be needed")
	}
	if err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{}); !errors.Is(err, ErrOverrideRequired) {
		t.Fatalf("without override: %v", err)
	}
	if err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{Override: true}); err != nil {
		t.Fatalf("with override: %v", err)
	}
	var override bool
	err := pool.QueryRow(ctx, `SELECT (after->'_meta'->>'override')::boolean FROM catalog_history
		WHERE change_id = $1 AND table_name = 'ingredient'`, p.ID).Scan(&override)
	if err != nil || !override {
		t.Fatalf("override not recorded: %v %v", override, err)
	}
}

func TestApplyDeleteNeedsTypedSlug(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, true)
	p, pv, _ := svc.Preview(ctx, DeleteChangeset("basil"))
	if len(pv.Diff.Deleted) != 1 || len(pv.Diff.Changed) != 1 { // tomato loses its pairing
		t.Fatalf("diff = %+v", pv.Diff)
	}
	if err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{ConfirmSlug: "basi"}); !errors.Is(err, ErrConfirmMismatch) {
		t.Fatalf("mismatch: %v", err)
	}
	if err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{ConfirmSlug: "basil"}); err != nil {
		t.Fatalf("confirmed: %v", err)
	}
	if _, ok := loadSnap(t, svc).Get("basil"); ok {
		t.Fatal("basil not deleted")
	}
}

func TestApplyIsAtomic(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, true)
	k := catalogtest.Hardy()
	k.Name = "Half-written kale"
	b := catalogtest.YearRound()
	b.Name = "Half-written banana"
	p, _, _ := svc.Preview(ctx, Changeset{Source: SourceImport, Upserts: []catalog.Ingredient{k, b}})

	svc.write = func(ctx context.Context, q *store.Queries, ups []catalog.Ingredient, dels []string) error {
		if err := catalog.Write(ctx, q, ups[:1], nil); err != nil {
			return err
		}
		return errors.New("boom after first write")
	}
	if err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{}); err == nil {
		t.Fatal("want failure")
	}
	s := loadSnap(t, svc)
	if g, _ := s.Get("kale"); g.Name != "Kale" {
		t.Fatalf("partial write leaked: kale = %q", g.Name)
	}
	if _, _, err := svc.Reopen(ctx, p.ID); err != nil {
		t.Fatalf("pending row should survive a failed apply: %v", err)
	}
}

func TestImportUpsertOnlyAndListReplacement(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t, true)
	file := `{"format":"thymeandplace-catalog/v1","ingredients":[
	  {"slug":"tomato","name":"Tomato","category":"vegetable","kind":"annual","frost":"tender",
	   "grow_temp_c":[16,30],"months_to_harvest":3,"storage_months":0,
	   "tips":{"choose":"Heavy for its size, glossy, fragrant at the stem.","store":"Stem-side down at room temperature."},
	   "pairings":[],"note":""}]}`
	cs, err := ImportChangeset(strings.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	p, pv, err := svc.Preview(ctx, cs)
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Diff.Deleted) != 0 || len(pv.Diff.Changed) != 1 {
		t.Fatalf("diff = %+v", pv.Diff)
	}
	if err := svc.Apply(ctx, p.ID, "owner", ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	s := loadSnap(t, svc)
	if s.Len() != 11 {
		t.Fatalf("import deleted items: len=%d", s.Len())
	}
	if tm, _ := s.Get("tomato"); len(tm.Pairings) != 0 {
		t.Fatalf("pairings not replaced: %v", tm.Pairings)
	}
}
