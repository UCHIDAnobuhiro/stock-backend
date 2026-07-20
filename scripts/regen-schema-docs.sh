#!/usr/bin/env bash
#
# docs/schema/ (tbls生成のER図・テーブル定義書) を再生成するスクリプト。
#
# なぜ使い回しの開発用DB（stock-postgres）ではなく毎回まっさらなDBを使うのか:
# tblsのPostgreSQLドライバは制約（PRIMARY KEY / FOREIGN KEY）を
# `ORDER BY cons.conindid, cons.conname` で取得しており、第1キー conindid は
# 制約を支えるインデックスのOID＝DBの作成履歴に依存する物理的な値。
# CIは毎回フレッシュなDBにマイグレーションを一括適用するのに対し、
# up/down を繰り返した使い回しDBはOIDの配置が異なるため、論理的に同一の
# スキーマでも制約の表示順序が食い違い、CIの Schema Doc Drift が
# 意図せず失敗する。これを避けるため、本スクリプトは使い捨てのPostgreSQL
# コンテナに対してマイグレーションを一括適用してから tbls を実行する。
# 開発用の stock-postgres / stock_postgres_data ボリュームには一切触れない。
set -euo pipefail

# tblsのバージョンは以下3箇所を揃えること:
#   .github/workflows/ci.yaml（schema-doc-check ジョブの k1LoW/setup-tbls）
#   docker/docker-compose.yml（tbls サービスの ghcr.io/k1low/tbls イメージタグ）
#   本スクリプト
TBLS_VERSION="v1.94.4"

# CIの postgres サービスコンテナ・docker-compose.yml の db サービスと揃える
POSTGRES_IMAGE="postgres:18"

# 衝突しにくい固定名（開発用の stock-postgres / stock-backend とは別名にする）
CONTAINER_NAME="stock-tbls-regen-db"
NETWORK_NAME="stock-tbls-regen-net"

# 開発用DB（5432番）と衝突しないポート。環境変数で上書き可能。
HOST_PORT="${REGEN_DB_PORT:-55432}"

DB_USER="appuser"
DB_PASSWORD="apppass"
DB_NAME="app"

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

cleanup() {
  docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
  docker network rm "$NETWORK_NAME" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# 前回異常終了時の残骸が残っていれば先に掃除する
cleanup

echo "==> 一時ネットワーク ${NETWORK_NAME} を作成"
docker network create "$NETWORK_NAME" >/dev/null

echo "==> 使い捨てPostgreSQLコンテナ ${CONTAINER_NAME} を起動（${POSTGRES_IMAGE}, 127.0.0.1:${HOST_PORT}）"
docker run -d \
  --name "$CONTAINER_NAME" \
  --network "$NETWORK_NAME" \
  -e "POSTGRES_DB=${DB_NAME}" \
  -e "POSTGRES_USER=${DB_USER}" \
  -e "POSTGRES_PASSWORD=${DB_PASSWORD}" \
  -p "127.0.0.1:${HOST_PORT}:5432" \
  "$POSTGRES_IMAGE" >/dev/null

echo "==> PostgreSQLの起動待ち"
READY=""
for _ in $(seq 1 30); do
  if docker exec "$CONTAINER_NAME" pg_isready -U "$DB_USER" -d "$DB_NAME" >/dev/null 2>&1; then
    READY="1"
    break
  fi
  sleep 1
done
if [ -z "$READY" ]; then
  echo "エラー: PostgreSQLが30秒以内に起動しませんでした" >&2
  exit 1
fi

echo "==> マイグレーション適用（go run ./cmd/migrate up）"
DB_HOST=127.0.0.1 \
DB_PORT="$HOST_PORT" \
DB_USER="$DB_USER" \
DB_PASSWORD="$DB_PASSWORD" \
DB_NAME="$DB_NAME" \
  go run ./cmd/migrate up

TBLS_DSN="postgres://${DB_USER}:${DB_PASSWORD}@${CONTAINER_NAME}:5432/${DB_NAME}?sslmode=disable"

echo "==> tbls doc --force で docs/schema/ を再生成"
docker run --rm \
  --network "$NETWORK_NAME" \
  -v "$REPO_ROOT":/work \
  -w /work \
  -e "TBLS_DSN=${TBLS_DSN}" \
  "ghcr.io/k1low/tbls:${TBLS_VERSION}" \
  doc --config /work/docs/tbls.yml --force

echo "==> tbls diff で乖離ゼロを確認（CIと同じ検証）"
docker run --rm \
  --network "$NETWORK_NAME" \
  -v "$REPO_ROOT":/work \
  -w /work \
  -e "TBLS_DSN=${TBLS_DSN}" \
  "ghcr.io/k1low/tbls:${TBLS_VERSION}" \
  diff --config /work/docs/tbls.yml

echo "==> 完了: docs/schema/ を再生成しました。git diff で差分を確認してコミットしてください。"
