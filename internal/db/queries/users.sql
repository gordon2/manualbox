-- name: CreateUser :one
INSERT INTO users (
    id, email, email_folded, display_name, password_hash, role, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- Lookups go through email_folded so that addresses differing only in case
-- resolve to the same account.
-- name: GetUserByEmail :one
SELECT * FROM users WHERE email_folded = ?;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- CountUsers backs the first-run check: zero users means setup has not happened.
-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at;

-- name: TouchUserLogin :exec
UPDATE users SET last_login_at = ?, updated_at = ? WHERE id = ?;

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = ?;
