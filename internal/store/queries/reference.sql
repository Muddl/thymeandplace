-- name: ListLiveDatasetIDs :many
SELECT id FROM dataset_versions WHERE is_live ORDER BY id;
