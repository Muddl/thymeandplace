-- name: ListLiveDatasetIDs :many
SELECT id FROM dataset_versions WHERE is_live ORDER BY id;

-- name: ExistingEcoregions :many
SELECT ecoregion_id FROM ocean_ecoregion WHERE ecoregion_id = ANY(@ids::int[]) ORDER BY ecoregion_id;
