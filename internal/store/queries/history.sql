-- name: ListHistory :many
SELECT * FROM catalog_history
WHERE (sqlc.narg(slug)::text IS NULL OR row_key->>'slug' = sqlc.narg(slug)::text)
  AND (sqlc.narg(before)::bigint IS NULL OR id < sqlc.narg(before)::bigint)
ORDER BY id DESC
-- One extra row over the page size, so the caller can tell a full last page
-- apart from a page with more still to come without a second query.
LIMIT 101;

-- name: ChangeApplied :one
SELECT EXISTS (SELECT 1 FROM catalog_history WHERE change_id = $1) AS applied;
