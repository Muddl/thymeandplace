-- name: InsertPendingChangeset :one
INSERT INTO pending_changeset (payload, base_version, score_before, score_after)
VALUES (@payload, @base_version, @score_before, @score_after)
RETURNING id, expires_at;

-- name: GetPendingChangeset :one
SELECT * FROM pending_changeset WHERE id = @id AND expires_at > now();

-- name: GetPendingChangesetForUpdate :one
SELECT * FROM pending_changeset WHERE id = @id AND expires_at > now() FOR UPDATE;

-- name: DeletePendingChangeset :exec
DELETE FROM pending_changeset WHERE id = @id;

-- name: DeleteExpiredChangesets :exec
DELETE FROM pending_changeset WHERE expires_at <= now();

-- name: LockCatalogVersion :one
SELECT version FROM catalog_meta WHERE id FOR UPDATE;

-- name: InsertCalibrationRun :exec
INSERT INTO calibration_run (data_version, score, compared) VALUES (@data_version, @score, @compared);

-- name: LatestCalibrationRun :one
SELECT * FROM calibration_run ORDER BY id DESC LIMIT 1;
