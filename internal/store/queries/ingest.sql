-- name: CreateDatasetVersion :one
INSERT INTO dataset_versions (dataset, table_name, source_url, source_checksum, license, license_reviewed)
VALUES (@dataset, '', @source_url, @source_checksum, @license, @license_reviewed)
RETURNING id;

-- name: SetDatasetTable :exec
UPDATE dataset_versions SET table_name = @table_name WHERE id = @id;

-- name: GetDatasetVersion :one
SELECT * FROM dataset_versions WHERE id = @id;

-- name: LatestDatasetVersion :one
SELECT * FROM dataset_versions WHERE dataset = @dataset ORDER BY id DESC LIMIT 1;

-- name: LiveDatasetVersion :one
SELECT * FROM dataset_versions WHERE dataset = @dataset AND is_live;

-- name: PreviousPromotedVersion :one
SELECT * FROM dataset_versions
WHERE dataset = @dataset AND id < @before AND promoted_at IS NOT NULL
ORDER BY id DESC LIMIT 1;

-- name: ApproveDatasetLicense :exec
UPDATE dataset_versions SET license_reviewed = true WHERE id = @id;

-- name: LicenseReviewedBefore :one
SELECT EXISTS (
  SELECT 1 FROM dataset_versions
  WHERE dataset = @dataset AND license = @license AND source_url = @source_url AND license_reviewed
);

-- name: MarkDatasetLoaded :exec
UPDATE dataset_versions SET loaded_at = now() WHERE id = @id;

-- name: ClearLive :exec
UPDATE dataset_versions SET is_live = false WHERE dataset = @dataset AND is_live;

-- name: MarkLive :exec
UPDATE dataset_versions SET is_live = true, promoted_at = coalesce(promoted_at, now()) WHERE id = @id;
