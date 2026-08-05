-- name: CreateUser :one
INSERT INTO users (email, password_hash)
VALUES ($1, $2)
RETURNING id, email, password_hash, created_at, updated_at;

-- name: FindUserByEmail :one
SELECT id, email, password_hash, created_at, updated_at
FROM users
WHERE email = $1
LIMIT 1;

-- name: FindUserByID :one
SELECT id, email, password_hash, created_at, updated_at
FROM users
WHERE id = $1
LIMIT 1;

-- name: CreateOAuthAccount :one
INSERT INTO oauth_accounts (user_id, provider, provider_uid)
VALUES ($1, $2, $3)
RETURNING user_id, provider, provider_uid, created_at, updated_at;

-- name: FindOAuthAccountByProvider :one
SELECT user_id, provider, provider_uid, created_at, updated_at
FROM oauth_accounts
WHERE provider = $1 AND provider_uid = $2
LIMIT 1;

-- name: CreateRefreshSession :one
INSERT INTO refresh_sessions (
    id, family_id, user_id, token_hash, expires_at
)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, family_id, user_id, token_hash, expires_at,
          consumed_at, revoked_at, replaced_by, created_at;

-- name: FindRefreshSessionByTokenHash :one
SELECT id, family_id, user_id, token_hash, expires_at,
       consumed_at, revoked_at, replaced_by, created_at
FROM refresh_sessions
WHERE token_hash = $1
LIMIT 1;

-- name: LockRefreshSessionForRotation :one
SELECT rs.id, rs.family_id, rs.user_id, rs.token_hash, rs.expires_at,
       rs.consumed_at, rs.revoked_at, rs.replaced_by, rs.created_at,
       u.email
FROM refresh_sessions AS rs
JOIN users AS u ON u.id = rs.user_id
WHERE rs.token_hash = $1
LIMIT 1
FOR UPDATE OF rs;

-- name: LockRefreshSessionByTokenHash :one
SELECT id, family_id, user_id, token_hash, expires_at,
       consumed_at, revoked_at, replaced_by, created_at
FROM refresh_sessions
WHERE token_hash = $1
LIMIT 1
FOR UPDATE;

-- name: ConsumeRefreshSession :exec
UPDATE refresh_sessions
SET consumed_at = $2,
    replaced_by = $3
WHERE id = $1;

-- name: RevokeRefreshSessionFamily :exec
UPDATE refresh_sessions
SET revoked_at = $2
WHERE family_id = $1
  AND revoked_at IS NULL;

-- name: DeleteExpiredRefreshSessions :execrows
DELETE FROM refresh_sessions
WHERE expires_at < $1;
