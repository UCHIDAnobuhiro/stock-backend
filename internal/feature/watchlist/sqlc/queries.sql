-- name: ListWatchlistByUser :many
SELECT id, user_id, symbol_code, sort_key, created_at, updated_at
FROM watchlists
WHERE user_id = $1
ORDER BY sort_key ASC;

-- name: InsertWatchlist :exec
INSERT INTO watchlists (user_id, symbol_code, sort_key)
VALUES ($1, $2, $3);

-- name: DeleteWatchlist :execrows
DELETE FROM watchlists
WHERE user_id = $1 AND symbol_code = $2;

-- name: MaxWatchlistSortKey :one
SELECT COALESCE(MAX(sort_key), -1)::bigint AS max_key
FROM watchlists
WHERE user_id = $1;

-- name: UpdateWatchlistSortKey :execrows
UPDATE watchlists
SET sort_key = $3,
    updated_at = now()
WHERE user_id = $1 AND symbol_code = $2;

-- name: LockWatchlistByUser :many
SELECT id
FROM watchlists
WHERE user_id = $1
ORDER BY id
FOR UPDATE;
