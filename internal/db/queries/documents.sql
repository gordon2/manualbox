-- Uploading the same bytes against the same device twice is the same document,
-- enforced by documents_device_blob_idx. DO NOTHING plus a follow-up lookup makes
-- the upload handler idempotent without the caller having to check first.
-- name: CreateDocument :execrows
INSERT INTO documents (id, device_id, blob_sha256, filename, media_type, kind, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(device_id, blob_sha256) DO NOTHING;

-- name: GetDocument :one
SELECT * FROM documents WHERE id = ?;

-- name: GetDocumentByDeviceAndBlob :one
SELECT * FROM documents WHERE device_id = ? AND blob_sha256 = ?;

-- name: ListDocumentsForDevice :many
SELECT * FROM documents WHERE device_id = ? ORDER BY created_at DESC;

-- name: ListDocumentsByState :many
SELECT * FROM documents WHERE state = ? ORDER BY created_at DESC;

-- name: SetDocumentState :exec
UPDATE documents SET state = ?, last_error = ?, updated_at = ? WHERE id = ?;

-- Records everything stages 0 and 1 discovered, in one statement. Writing the
-- probe result and the new state together keeps a crash from leaving a document
-- that claims to be probed but has no page count.
-- name: RecordDocumentProbe :exec
UPDATE documents
SET page_count            = ?,
    encrypted             = ?,
    tagged                = ?,
    has_text_layer        = ?,
    median_chars_per_page = ?,
    content_start_page    = ?,
    content_end_page      = ?,
    state                 = ?,
    last_error            = '',
    probed_at             = ?,
    updated_at            = ?
WHERE id = ?;

-- name: DeleteDocument :exec
DELETE FROM documents WHERE id = ?;

-- name: CountDocuments :one
SELECT CAST(count(*) AS INTEGER) AS total FROM documents;

-- Used to decide whether a blob is still referenced before deleting it, since
-- two devices can legitimately share one uploaded file.
-- name: CountDocumentsForBlob :one
SELECT CAST(count(*) AS INTEGER) AS total FROM documents WHERE blob_sha256 = ?;
