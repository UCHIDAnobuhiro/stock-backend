-- name: ListActiveSymbols :many
SELECT code, name, market, timezone, logo_url, logo_updated_at, is_active, created_at, updated_at
FROM symbols
WHERE is_active = TRUE
ORDER BY code ASC;

-- name: SymbolExists :one
SELECT EXISTS (
  SELECT 1 FROM symbols WHERE code = $1
) AS exists;

-- name: UpdateSymbolLogoURL :execrows
UPDATE symbols
SET logo_url = $2,
    logo_updated_at = $3,
    updated_at = now()
WHERE code = $1;
