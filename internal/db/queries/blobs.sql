-- Blobs are content-addressed, so re-adding identical bytes is a no-op rather
-- than a conflict. That is what makes uploading the same manual twice cheap.
-- name: UpsertBlob :exec
INSERT INTO blobs (sha256, size_bytes, media_type, created_at) VALUES (?, ?, ?, ?)
ON CONFLICT(sha256) DO NOTHING;

-- name: GetBlob :one
SELECT * FROM blobs WHERE sha256 = ?;

-- name: BlobExists :one
SELECT EXISTS(SELECT 1 FROM blobs WHERE sha256 = ?);

-- name: DeleteBlob :exec
DELETE FROM blobs WHERE sha256 = ?;

-- The CAST is load-bearing: without it sqlc cannot infer the type of an
-- aggregate in SQLite and generates interface{}, pushing a type assertion onto
-- every caller. Wrap aggregates in CAST(... AS INTEGER) throughout.
-- name: TotalBlobBytes :one
SELECT CAST(coalesce(sum(size_bytes), 0) AS INTEGER) AS total_bytes FROM blobs;
