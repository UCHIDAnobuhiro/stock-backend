-- name: FindCandlesLimit :many
SELECT symbol_code, "interval", "time", open, high, low, close, volume
FROM candles
WHERE symbol_code = $1 AND "interval" = $2
ORDER BY "time" DESC
LIMIT $3;
