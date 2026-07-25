-- name: CreateSession :one
INSERT INTO sessions (
    id, user_id, token_hash, user_agent, ip, created_at, last_seen_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- Authenticating a request is one indexed lookup that also returns the user, so
-- the middleware needs a single query. Expiry is checked in SQL rather than in
-- Go so an expired session can never be treated as valid by mistake.
-- name: GetSessionByToken :one
SELECT sqlc.embed(sessions), sqlc.embed(users)
FROM sessions
JOIN users ON users.id = sessions.user_id
WHERE sessions.token_hash = ? AND sessions.expires_at > ?;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = ? WHERE id = ?;

-- name: ExtendSession :exec
UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?;

-- name: DeleteSessionByToken :exec
DELETE FROM sessions WHERE token_hash = ?;

-- name: DeleteSession :exec
DELETE FROM sessions WHERE id = ?;

-- DeleteUserSessions logs a user out everywhere, used after a password change.
-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = ?;

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions WHERE expires_at <= ?;

-- name: ListUserSessions :many
SELECT * FROM sessions WHERE user_id = ? ORDER BY last_seen_at DESC;
