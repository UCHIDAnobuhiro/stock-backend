-- name: FindCandlesLimit :many
SELECT symbol_code, "interval", "time", open, high, low, close, volume
FROM candles
WHERE symbol_code = $1 AND "interval" = $2
ORDER BY "time" DESC
LIMIT $3;

-- name: FindLatestCandlesBySymbols :many
SELECT c.symbol_code, c."interval", c."time", c.open, c.high, c.low, c.close, c.volume
FROM unnest(sqlc.arg(symbol_codes)::text[]) AS s(code)
CROSS JOIN LATERAL (
  SELECT symbol_code, "interval", "time", open, high, low, close, volume
  FROM candles
  WHERE symbol_code = s.code AND "interval" = sqlc.arg(interval_filter)
  ORDER BY "time" DESC
  LIMIT sqlc.arg(max_rows)
) c;
