# データベースマイグレーション運用ガイド

このディレクトリは [goose](https://github.com/pressly/goose) によるスキーママイグレーションを管理します。

## ディレクトリ構成

```
db/
├── migrations/                  # goose 管理の SQL マイグレーションファイル
│   └── NNNNN_<name>.sql         # `-- +goose Up` と `-- +goose Down` を含む単一ファイル
├── seed/                        # 初期データ投入
│   ├── seed.sql                 # symbols の upsert（冪等）。docker-compose の seed サービスが流す
│   └── seed.sh                  # 手動再投入用ラッパー（docker compose exec で seed.sql を流す）
├── embed.go                     # migrations/*.sql を go:embed で取り込み、cmd/migrate などに提供
└── README.md
```

ファイル名は `NNNNN_<snake_case_name>.sql`（5桁連番）で統一します。

## マイグレーションの実行方法

### 1. cmd/migrate バイナリ（推奨・本番ジョブでも利用可能な経路）

埋め込み済み migrations を使うので、SQL ファイルを別途配布する必要がありません。

```bash
# ローカル
DB_USER=appuser DB_PASSWORD=apppass DB_NAME=app DB_HOST=localhost DB_PORT=5432 \
  go run ./cmd/migrate            # 引数省略時は `up`
go run ./cmd/migrate status
go run ./cmd/migrate down
go run ./cmd/migrate up-to 1

# Docker（Cloud Run Jobへデプロイ可能なものと同じイメージ）
docker compose -f docker/docker-compose.yml -p stock run --rm migrate         # up
docker compose -f docker/docker-compose.yml -p stock run --rm migrate status
```

サポートするサブコマンド: `up` / `up-by-one` / `up-to` / `down` / `down-to` / `redo` / `reset` / `status` / `version`

`create` / `fix` は開発者ローカルでの SQL ファイル作成・整理用なので、後述の `go tool goose` を使ってください。

### 2. goose CLI（新しいマイグレーション作成・開発時の検証）

`go tool goose` で実行できます（`tools.go` 不要、`go.mod` の `tool` ディレクティブで管理）。

```bash
export GOOSE_DRIVER=postgres
export GOOSE_DBSTRING="postgres://appuser:apppass@localhost:5432/app?sslmode=disable"
export GOOSE_MIGRATION_DIR=db/migrations

# 新しいマイグレーションファイル作成（NNNNN_<name>.sql）
go tool goose create <snake_case_name> sql

# ローカル DB で適用・確認
go tool goose status
go tool goose up
go tool goose down
```

## 新規マイグレーションの作成手順

1. `go tool goose create <name> sql` で雛形を作成
2. `-- +goose Up` セクションに `CREATE` / `ALTER` 等を記述
3. `-- +goose Down` セクションに**必ず**ロールバック SQL を記述
4. ローカル PostgreSQL で `go tool goose up` → `go tool goose down` → `go tool goose up` を試し、可逆性を確認
5. PR に含める

`db/migrations/` を変更したら、`./scripts/regen-schema-docs.sh` で `docs/schema/`
（ER図・テーブル定義書）を再生成し、同じ PR でコミットしてください。手順の詳細は
ルート [README.md](../README.md) の「ER 図・テーブル定義書の生成（tbls）」を参照してください。

## 本番・ステージング環境での適用

`docker/Dockerfile.migrate` でビルドした `migrate` バイナリは Cloud Run Job（`migrate`）として
デプロイされています。`.github/workflows/cd-api.yaml`（`workflow_dispatch`）を実行すると、APIの
デプロイ本体に進む前に `.github/workflows/cd-migrate.yaml` が自動的に呼び出され、`migrate up` を
実行します。マイグレーションが失敗した場合はAPIのデプロイに進みません。

`status` 確認や `down` ロールバックなど `up` 以外のサブコマンドを個別に実行したい場合は、
`.github/workflows/cd-migrate.yaml` を GitHub Actions 上で直接 `workflow_dispatch` 実行し、
`migrate_command` 入力（例: `status` / `down` / `up-to,1`。複数引数はカンマ区切り）を指定してください。

接続情報はサーバーと同じ環境変数（`DB_USER` / `DB_PASSWORD` / `DB_NAME` / `DB_HOST` / `DB_PORT` /
`INSTANCE_CONNECTION_NAME`）から読み取ります。

## シードデータの投入

`db/seed/seed.sql` は `symbols` の初期データを `INSERT ... ON CONFLICT` で upsert する冪等な SQL です。
通常は `backend` 起動時に docker-compose の `seed` サービスが `migrate` 完了後に自動で流すため、
手動操作は不要です。

DB を起動したまま手動で再投入したい場合は、ラッパースクリプトを使います。

```bash
bash db/seed/seed.sh
```

## sqlc コード生成

クエリ追加・変更時は以下を実行して再生成します。

```bash
go tool sqlc generate
```

各 feature の `internal/feature/<name>/sqlc/queries.sql` を編集 →
同ディレクトリに型安全コードが生成されます。
