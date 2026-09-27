-- name: FindCandlesLimit :many
SELECT c.symbol_code, c."interval", c."time", c.open, c.high, c.low, c.close, c.volume, s.timezone
FROM candles AS c
JOIN symbols AS s ON s.code = c.symbol_code
WHERE c.symbol_code = $1 AND c."interval" = $2
ORDER BY c."time" DESC
LIMIT $3;
