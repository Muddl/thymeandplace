-- name: GetCatalogVersion :one
SELECT version FROM catalog_meta WHERE id;

-- name: ListIngredients :many
SELECT * FROM ingredient ORDER BY slug;

-- name: ListPairings :many
SELECT slug, pairs_with FROM ingredient_pairing ORDER BY slug, pairs_with;

-- name: ListSeafoodRegions :many
SELECT slug, ecoregion_id, months, peak_months FROM seafood_region ORDER BY slug, ecoregion_id;

-- name: UpsertIngredient :exec
INSERT INTO ingredient (
  slug, name, category, kind, frost, grow_temp_min_c, grow_temp_max_c, months_to_harvest,
  storage_months, chill_months_min, warmup_temp_c, months_bloom_to_harvest, harvest_span_months,
  fruiting_temp_min_c, fruiting_temp_max_c, min_precip_mm, tip_choose, tip_store, note
) VALUES (
  @slug, @name, @category, @kind, @frost, @grow_temp_min_c, @grow_temp_max_c, @months_to_harvest,
  @storage_months, @chill_months_min, @warmup_temp_c, @months_bloom_to_harvest, @harvest_span_months,
  @fruiting_temp_min_c, @fruiting_temp_max_c, @min_precip_mm, @tip_choose, @tip_store, @note
)
ON CONFLICT (slug) DO UPDATE SET
  name = EXCLUDED.name, category = EXCLUDED.category, kind = EXCLUDED.kind, frost = EXCLUDED.frost,
  grow_temp_min_c = EXCLUDED.grow_temp_min_c, grow_temp_max_c = EXCLUDED.grow_temp_max_c,
  months_to_harvest = EXCLUDED.months_to_harvest, storage_months = EXCLUDED.storage_months,
  chill_months_min = EXCLUDED.chill_months_min, warmup_temp_c = EXCLUDED.warmup_temp_c,
  months_bloom_to_harvest = EXCLUDED.months_bloom_to_harvest, harvest_span_months = EXCLUDED.harvest_span_months,
  fruiting_temp_min_c = EXCLUDED.fruiting_temp_min_c, fruiting_temp_max_c = EXCLUDED.fruiting_temp_max_c,
  min_precip_mm = EXCLUDED.min_precip_mm, tip_choose = EXCLUDED.tip_choose, tip_store = EXCLUDED.tip_store,
  note = EXCLUDED.note, updated_at = now()
WHERE (ingredient.name, ingredient.category, ingredient.kind, ingredient.frost,
       ingredient.grow_temp_min_c, ingredient.grow_temp_max_c, ingredient.months_to_harvest,
       ingredient.storage_months, ingredient.chill_months_min, ingredient.warmup_temp_c,
       ingredient.months_bloom_to_harvest, ingredient.harvest_span_months,
       ingredient.fruiting_temp_min_c, ingredient.fruiting_temp_max_c, ingredient.min_precip_mm,
       ingredient.tip_choose, ingredient.tip_store, ingredient.note)
  IS DISTINCT FROM
      (EXCLUDED.name, EXCLUDED.category, EXCLUDED.kind, EXCLUDED.frost,
       EXCLUDED.grow_temp_min_c, EXCLUDED.grow_temp_max_c, EXCLUDED.months_to_harvest,
       EXCLUDED.storage_months, EXCLUDED.chill_months_min, EXCLUDED.warmup_temp_c,
       EXCLUDED.months_bloom_to_harvest, EXCLUDED.harvest_span_months,
       EXCLUDED.fruiting_temp_min_c, EXCLUDED.fruiting_temp_max_c, EXCLUDED.min_precip_mm,
       EXCLUDED.tip_choose, EXCLUDED.tip_store, EXCLUDED.note);

-- name: DeleteIngredient :execrows
DELETE FROM ingredient WHERE slug = @slug;

-- name: PrunePairings :exec
DELETE FROM ingredient_pairing
WHERE slug = @slug AND NOT (pairs_with = ANY(coalesce(@keep::text[], '{}'::text[])));

-- name: AddPairing :exec
INSERT INTO ingredient_pairing (slug, pairs_with) VALUES (@slug, @pairs_with)
ON CONFLICT DO NOTHING;

-- name: PruneSeafoodRegions :exec
DELETE FROM seafood_region
WHERE slug = @slug AND NOT (ecoregion_id = ANY(coalesce(@keep::int[], '{}'::int[])));

-- name: UpsertSeafoodRegion :exec
INSERT INTO seafood_region (slug, ecoregion_id, months, peak_months)
VALUES (@slug, @ecoregion_id, @months, @peak_months)
ON CONFLICT (slug, ecoregion_id) DO UPDATE SET months = EXCLUDED.months, peak_months = EXCLUDED.peak_months
WHERE (seafood_region.months, seafood_region.peak_months) IS DISTINCT FROM (EXCLUDED.months, EXCLUDED.peak_months);
