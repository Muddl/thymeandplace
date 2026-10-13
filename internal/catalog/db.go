package catalog

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"thymeandplace/internal/ocean"
	"thymeandplace/internal/store"
)

// TxBeginner is satisfied by *pgxpool.Pool and *pgx.Conn.
type TxBeginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// Write persists upserts and deletes. Call it inside a transaction.
// Ingredient rows are written first so pairings between new ingredients
// satisfy their foreign keys; unchanged rows are left untouched.
func Write(ctx context.Context, q *store.Queries, upserts []Ingredient, deletes []string) error {
	norm := make([]Ingredient, len(upserts))
	for i, ing := range upserts {
		norm[i] = Normalize(ing)
		if err := q.UpsertIngredient(ctx, toParams(norm[i])); err != nil {
			return fmt.Errorf("upsert %s: %w", ing.Slug, err)
		}
	}
	for _, ing := range norm {
		if err := q.PrunePairings(ctx, store.PrunePairingsParams{Slug: ing.Slug, Keep: ing.Pairings}); err != nil {
			return fmt.Errorf("prune pairings %s: %w", ing.Slug, err)
		}
		for _, p := range ing.Pairings {
			if err := q.AddPairing(ctx, store.AddPairingParams{Slug: ing.Slug, PairsWith: p}); err != nil {
				return fmt.Errorf("pairing %s→%s: %w", ing.Slug, p, err)
			}
		}
		keep := make([]int32, len(ing.Regions))
		for i, r := range ing.Regions {
			keep[i] = int32(r.EcoregionID)
		}
		if err := q.PruneSeafoodRegions(ctx, store.PruneSeafoodRegionsParams{Slug: ing.Slug, Keep: keep}); err != nil {
			return fmt.Errorf("prune regions %s: %w", ing.Slug, err)
		}
		for _, r := range ing.Regions {
			err := q.UpsertSeafoodRegion(ctx, store.UpsertSeafoodRegionParams{
				Slug: ing.Slug, EcoregionID: int32(r.EcoregionID),
				Months: toInt16s(r.Months), PeakMonths: toInt16s(r.PeakMonths),
			})
			if err != nil {
				return fmt.Errorf("region %s/%d: %w", ing.Slug, r.EcoregionID, err)
			}
		}
	}
	for _, slug := range deletes {
		if _, err := q.DeleteIngredient(ctx, slug); err != nil {
			return fmt.Errorf("delete %s: %w", slug, err)
		}
	}
	return nil
}

// Load reads the whole catalog in one consistent read-only transaction.
func Load(ctx context.Context, db TxBeginner) (*Snapshot, error) {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // read-only
	return LoadIn(ctx, store.New(tx))
}

// LoadIn reads the whole catalog through q. The caller must already be
// in Load's read-only repeatable-read transaction, or hold catalog_meta
// FOR UPDATE (Apply does, via LockCatalogVersion).
func LoadIn(ctx context.Context, q *store.Queries) (*Snapshot, error) {
	version, err := q.GetCatalogVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("catalog version: %w", err)
	}
	rows, err := q.ListIngredients(ctx)
	if err != nil {
		return nil, fmt.Errorf("ingredients: %w", err)
	}
	pairs, err := q.ListPairings(ctx)
	if err != nil {
		return nil, fmt.Errorf("pairings: %w", err)
	}
	regions, err := q.ListSeafoodRegions(ctx)
	if err != nil {
		return nil, fmt.Errorf("regions: %w", err)
	}

	bySlug := make(map[string]*Ingredient, len(rows))
	items := make([]Ingredient, len(rows))
	for i, r := range rows {
		items[i] = fromRow(r)
		bySlug[r.Slug] = &items[i]
	}
	for _, p := range pairs {
		if ing := bySlug[p.Slug]; ing != nil {
			ing.Pairings = append(ing.Pairings, p.PairsWith)
		}
	}
	for _, r := range regions {
		if ing := bySlug[r.Slug]; ing != nil {
			ing.Regions = append(ing.Regions, SeafoodRegion{
				EcoregionID: ocean.EcoregionID(r.EcoregionID),
				Months:      fromInt16s(r.Months), PeakMonths: fromInt16s(r.PeakMonths),
			})
		}
	}
	for i := range items {
		items[i] = Normalize(items[i])
	}
	return NewSnapshot(version, items)
}

func toParams(ing Ingredient) store.UpsertIngredientParams {
	return store.UpsertIngredientParams{
		Slug: ing.Slug, Name: ing.Name,
		Category: store.IngredientCategory(ing.Category), Kind: store.IngredientKind(ing.Kind),
		Frost:        store.NullFrostTolerance{FrostTolerance: store.FrostTolerance(ing.Frost), Valid: ing.Frost != ""},
		GrowTempMinC: f4(ing.GrowTempMinC), GrowTempMaxC: f4(ing.GrowTempMaxC),
		MonthsToHarvest: i2(ing.MonthsToHarvest), StorageMonths: int16(ing.StorageMonths),
		ChillMonthsMin: i2(ing.ChillMonthsMin), WarmupTempC: f4(ing.WarmupTempC),
		MonthsBloomToHarvest: i2(ing.MonthsBloomToHarvest), HarvestSpanMonths: i2(ing.HarvestSpanMonths),
		FruitingTempMinC: f4(ing.FruitingTempMinC), FruitingTempMaxC: f4(ing.FruitingTempMaxC),
		MinPrecipMm: f4(ing.MinPrecipMM),
		TipChoose:   ing.TipChoose, TipStore: ing.TipStore, Note: ing.Note,
	}
}

func fromRow(r store.Ingredient) Ingredient {
	ing := Ingredient{
		Slug: r.Slug, Name: r.Name, Category: Category(r.Category), Kind: Kind(r.Kind),
		GrowTempMinC: unf4(r.GrowTempMinC), GrowTempMaxC: unf4(r.GrowTempMaxC),
		MonthsToHarvest: uni2(r.MonthsToHarvest), StorageMonths: int(r.StorageMonths),
		ChillMonthsMin: uni2(r.ChillMonthsMin), WarmupTempC: unf4(r.WarmupTempC),
		MonthsBloomToHarvest: uni2(r.MonthsBloomToHarvest), HarvestSpanMonths: uni2(r.HarvestSpanMonths),
		FruitingTempMinC: unf4(r.FruitingTempMinC), FruitingTempMaxC: unf4(r.FruitingTempMaxC),
		MinPrecipMM: unf4(r.MinPrecipMm),
		TipChoose:   r.TipChoose, TipStore: r.TipStore, Note: r.Note,
	}
	if r.Frost.Valid {
		ing.Frost = Frost(r.Frost.FrostTolerance)
	}
	return ing
}

func f4(p *float64) pgtype.Float4 {
	if p == nil {
		return pgtype.Float4{}
	}
	return pgtype.Float4{Float32: float32(*p), Valid: true}
}

func unf4(v pgtype.Float4) *float64 {
	if !v.Valid {
		return nil
	}
	f := Real32(float64(v.Float32))
	return &f
}

func i2(p *int) pgtype.Int2 {
	if p == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: int16(*p), Valid: true}
}

func uni2(v pgtype.Int2) *int {
	if !v.Valid {
		return nil
	}
	i := int(v.Int16)
	return &i
}

func toInt16s(ms []int) []int16 {
	out := make([]int16, len(ms))
	for i, m := range ms {
		out[i] = int16(m)
	}
	return out
}

func fromInt16s(ms []int16) []int {
	out := make([]int, len(ms))
	for i, m := range ms {
		out[i] = int(m)
	}
	return out
}
