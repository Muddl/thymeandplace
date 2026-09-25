package catalog_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"thymeandplace/internal/catalog"
	"thymeandplace/internal/catalog/catalogtest"
	"thymeandplace/internal/dbtest"
	"thymeandplace/internal/store"
)

type histRow struct {
	Actor    string
	ChangeID *int64
	Action   string
	Table    string
	Key      map[string]any
	Before   map[string]any
	After    map[string]any
}

func history(t *testing.T, pool *pgxpool.Pool) []histRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT actor, change_id, action, table_name, row_key, before, after FROM catalog_history ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []histRow
	for rows.Next() {
		var r histRow
		var key, before, after []byte
		if err := rows.Scan(&r.Actor, &r.ChangeID, &r.Action, &r.Table, &key, &before, &after); err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(key, &r.Key)
		_ = json.Unmarshal(before, &r.Before)
		_ = json.Unmarshal(after, &r.After)
		out = append(out, r)
	}
	return out
}

func version(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	v, err := store.New(pool).GetCatalogVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func writeAs(t *testing.T, pool *pgxpool.Pool, actor string, changeID int64, meta string, ups []catalog.Ingredient, dels []string) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT set_config('app.actor', $1, true), set_config('app.change_id', $2, true), set_config('app.meta', $3, true)`,
		actor, itoa(changeID), meta); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Write(ctx, store.New(tx), ups, dels); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func itoa(v int64) string { b, _ := json.Marshal(v); return string(b) }

func TestHistoryInsertUpdate(t *testing.T) {
	pool := dbtest.New(t)
	writeAs(t, pool, "owner", 42, "", []catalog.Ingredient{catalogtest.Hardy()}, nil)

	h := history(t, pool)
	if len(h) != 1 || h[0].Actor != "owner" || h[0].ChangeID == nil || *h[0].ChangeID != 42 ||
		h[0].Action != "insert" || h[0].Table != "ingredient" || h[0].Key["slug"] != "kale" || h[0].After["name"] != "Kale" {
		t.Fatalf("insert history = %+v", h)
	}

	k := catalogtest.Hardy()
	k.Name = "Curly kale"
	writeAs(t, pool, "owner", 43, "", []catalog.Ingredient{k}, nil)
	h = history(t, pool)
	last := h[len(h)-1]
	if last.Action != "update" || last.Before["name"] != "Kale" || last.After["name"] != "Curly kale" {
		t.Fatalf("update history = %+v", last)
	}
}

// Review Focus 2.
func TestUnchangedUpsertWritesNoHistory(t *testing.T) {
	pool := dbtest.New(t)
	writeTx(t, pool, catalogtest.SmallCatalog(), nil)
	before := len(history(t, pool))
	writeTx(t, pool, catalogtest.SmallCatalog(), nil)
	if after := len(history(t, pool)); after != before {
		t.Fatalf("unchanged upsert wrote %d history rows", after-before)
	}
}

func TestDirectSQLActor(t *testing.T) {
	pool := dbtest.New(t)
	writeTx(t, pool, []catalog.Ingredient{catalogtest.Hardy()}, nil)
	if _, err := pool.Exec(context.Background(), `UPDATE ingredient SET note = 'x' WHERE slug = 'kale'`); err != nil {
		t.Fatal(err)
	}
	h := history(t, pool)
	if h[len(h)-1].Actor != "sql" || h[len(h)-1].ChangeID != nil {
		t.Fatalf("got %+v", h[len(h)-1])
	}
}

// Review Focus 3.
func TestDeleteCascadesPairingsWithHistory(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	writeTx(t, pool, catalogtest.SmallCatalog(), nil) // tomato ↔ basil
	writeAs(t, pool, "owner", 7, "", nil, []string{"basil"})

	var cascaded int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM catalog_history
		WHERE change_id = 7 AND table_name = 'ingredient_pairing' AND action = 'delete'`).Scan(&cascaded)
	if err != nil || cascaded != 2 {
		t.Fatalf("cascaded pairing history rows = %d (%v), want 2", cascaded, err)
	}
	snap, err := catalog.Load(ctx, pool)
	if err != nil {
		t.Fatalf("reload after cascade: %v", err)
	}
	if tm, _ := snap.Get("tomato"); len(tm.Pairings) != 0 {
		t.Fatalf("tomato still pairs: %v", tm.Pairings)
	}
}

func TestMetaMergedIntoAfter(t *testing.T) {
	pool := dbtest.New(t)
	writeAs(t, pool, "owner", 9, `{"override":true}`, []catalog.Ingredient{catalogtest.Hardy()}, nil)
	writeAs(t, pool, "owner", 10, `{"override":true}`, nil, []string{"kale"})
	h := history(t, pool)
	meta := h[0].After["_meta"].(map[string]any)
	if meta["override"] != true {
		t.Fatalf("insert meta = %v", h[0].After)
	}
	del := h[len(h)-1]
	if del.Action != "delete" || del.After["_meta"].(map[string]any)["override"] != true {
		t.Fatalf("delete meta = %+v", del)
	}
}

func TestVersionBumpsOncePerStatement(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	v0 := version(t, pool)
	writeTx(t, pool, []catalog.Ingredient{catalogtest.Hardy(), catalogtest.YearRound()}, nil)
	if _, err := pool.Exec(ctx, `UPDATE ingredient SET note = 'n'`); err != nil { // one statement, two rows
		t.Fatal(err)
	}
	v1 := version(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE ingredient SET note = 'm'`); err != nil {
		t.Fatal(err)
	}
	if v2 := version(t, pool); v2 != v1+1 {
		t.Fatalf("two-row statement bumped %d times", v2-v1)
	}
	if v1 <= v0 {
		t.Fatal("writes did not bump version")
	}
}

// Review Focus 5: NOTIFY on commit only.
func TestNotifyOnCommitOnly(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN data_version_changed"); err != nil {
		t.Fatal(err)
	}

	tx, _ := pool.Begin(ctx)
	if err := catalog.Write(ctx, store.New(tx), []catalog.Ingredient{catalogtest.Hardy()}, nil); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	wctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if n, err := conn.Conn().WaitForNotification(wctx); err == nil {
		t.Fatalf("rolled-back write notified: %+v", n)
	}

	writeTx(t, pool, []catalog.Ingredient{catalogtest.Hardy()}, nil)
	wctx2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()
	n, err := conn.Conn().WaitForNotification(wctx2)
	if err != nil || n.Channel != "data_version_changed" || n.Payload != "catalog" {
		t.Fatalf("commit notification = %+v, %v", n, err)
	}
}
