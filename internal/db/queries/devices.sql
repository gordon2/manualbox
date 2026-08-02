-- name: CreateDevice :one
INSERT INTO devices (id, name, brand, model, category, location_id, notes, purchased_at, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetDevice :one
SELECT * FROM devices WHERE id = ?;

-- name: ListDevices :many
SELECT * FROM devices ORDER BY name;

-- Filtering by location is a separate query rather than a nullable parameter on
-- ListDevices. CONTRIBUTING.md: an "IS NULL OR =" filter defeats sqlc's type
-- inference and reads worse than two explicit queries.
-- name: ListDevicesByLocation :many
SELECT * FROM devices WHERE location_id = ? ORDER BY name;

-- name: UpdateDevice :one
UPDATE devices
SET name = ?, brand = ?, model = ?, category = ?, location_id = ?, notes = ?,
    purchased_at = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeleteDevice :exec
DELETE FROM devices WHERE id = ?;

-- name: CountDevices :one
SELECT CAST(count(*) AS INTEGER) AS total FROM devices;
