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
RETURNING user_id, provider, provider_uid, created_at;

-- name: FindOAuthAccountByProvider :one
SELECT user_id, provider, provider_uid, created_at
FROM oauth_accounts
WHERE provider = $1 AND provider_uid = $2
LIMIT 1;
