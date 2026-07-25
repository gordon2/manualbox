-- name: CreateLocation :one
INSERT INTO locations (id, name, parent_id, notes, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetLocation :one
SELECT * FROM locations WHERE id = ?;

-- name: ListLocations :many
SELECT * FROM locations ORDER BY name;

-- name: UpdateLocation :one
UPDATE locations
SET name = ?, parent_id = ?, notes = ?, updated_at = ?
WHERE id = ?
RETURNING *;

-- name: DeleteLocation :exec
DELETE FROM locations WHERE id = ?;

-- name: CountLocations :one
SELECT CAST(count(*) AS INTEGER) AS total FROM locations;
