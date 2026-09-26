-- name: ListHistory :many
SELECT * FROM catalog_history
WHERE (sqlc.narg(slug)::text IS NULL OR row_key->>'slug' = sqlc.narg(slug)::text)
  AND (sqlc.narg(before)::bigint IS NULL OR id < sqlc.narg(before)::bigint)
ORDER BY id DESC
LIMIT 100;
