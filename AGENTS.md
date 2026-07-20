# AGENTS.md

このファイルは、Codex（Codex.ai/code）がこのリポジトリのコードを扱う際のガイダンスを提供します。

## 開発コマンド

### ローカル開発
```bash
# APIサーバー起動（Airによるホットリロード付き）
docker compose -f docker/docker-compose.yml -p stock up backend

# バッチデータ取り込み実行（外部APIから株価データを取得）
docker compose -f docker/docker-compose.yml -p stock run --rm --no-deps candles

# ログ確認
docker logs -f stock-backend
```

### マイグレーション（goose）
スキーマは `db/migrations/*.sql` で管理し、`cmd/migrate` バイナリで適用します。
詳細は `db/README.md` を参照。

```bash
# ローカル PostgreSQL に最新スキーマを適用
DB_USER=appuser DB_PASSWORD=apppass DB_NAME=app DB_HOST=localhost DB_PORT=5432 \
  go run ./cmd/migrate up
# または Docker
docker compose -f docker/docker-compose.yml -p stock run --rm migrate

# 新規マイグレーションファイル作成
GOOSE_DRIVER=postgres GOOSE_MIGRATION_DIR=db/migrations \
  go tool goose create <snake_case_name> sql
```

### スキーマドキュメント再生成（tbls）
`db/migrations/` を変更した際は**必ず** `./scripts/regen-schema-docs.sh` を実行して
`docs/schema/`（ER図・テーブル定義書）を再生成し、同じ PR でコミットしてください。

```bash
./scripts/regen-schema-docs.sh
```

起動中の開発用DB（`stock-postgres`）に対して `tbls doc` を**直接実行しないでください**。
使い回しDBでは制約（PRIMARY KEY / FOREIGN KEY）の表示順序が非決定になり、CIの
`Schema Doc Drift` ジョブが失敗します。詳細な理由はスクリプト冒頭のコメントおよび
ルート `README.md` の「ER 図・テーブル定義書の生成（tbls）」を参照してください。

### sqlc コード生成
クエリ追加・変更時に再生成します。

```bash
go tool sqlc generate
```

各 feature の `internal/feature/<name>/sqlc/queries.sql` を編集 →
同ディレクトリに型安全コード（`package <name>sqlc`）が生成されます。

### テスト・リント
リポジトリテストは [testcontainers-go](https://golang.testcontainers.org/) で実 PostgreSQL を
立ち上げます。Docker daemon が利用できない環境では、ホストの PostgreSQL を `TEST_DB_DSN` で
指定すると testcontainers をスキップできます（DB ユーザーには `CREATEDB` 権限が必要）。

```bash
# 全テスト実行（レースコンディション検出・カバレッジ付き、Docker が必要）
go test ./... -v -race -cover

# ホストの PostgreSQL を使う場合
TEST_DB_DSN="postgres://appuser:apppass@localhost:5432/postgres?sslmode=disable" \
  go test ./... -race

# 特定パッケージのテスト実行
go test ./internal/feature/candles/... -v

# 特定テスト関数の実行
go test ./internal/feature/auth/... -v -run TestAuthUsecase_Login

# リンター実行（golangci-lint、no-gorm depguardルール）
golangci-lint run --timeout=5m

# アーキテクチャ依存ルールの検証（go-arch-lint、デフォルト拒否）
go tool go-arch-lint check

# 全パッケージのビルド
go build ./...
```

### 環境セットアップ
- `docker/example.env` を `docker/.env` にコピーして設定（GCP ADC を使う場合は ADC 関連の変数も設定）：
  - `TWELVE_DATA_API_KEY`: https://twelvedata.com/ から取得（無料枠: 8リクエスト/分）
  - `JWT_SECRET`: 本番環境では強力なシークレットを設定
  - DB・Redisの設定はローカル開発用

## アーキテクチャ概要

**Go/net/http (chi) REST API** で、**フィーチャーベースのクリーンアーキテクチャ**（垂直スライス）を採用しています。

### ディレクトリ構成

```
api/
├── openapi.yaml          # OpenAPI 3.0.4 仕様（APIコントラクトの単一ソース）
└── oapi-codegen.cfg.yaml # oapi-codegen設定（型のみ生成）

internal/
├── api/              # OpenAPIから自動生成された型定義（types.gen.go）
├── app/
│   ├── batch/        # バッチ実行ロジック（job_id ディスパッチ: candles / logo）
│   ├── config/       # 環境変数パースの純粋関数ヘルパー
│   ├── di/           # 依存性注入ファクトリ
│   ├── migrate/      # マイグレーション実行ロジック（goose サブコマンドディスパッチ）
│   └── router/       # HTTPルーター設定
├── feature/          # フィーチャーモジュール（垂直スライス）
│   ├── auth/
│   ├── candles/
│   ├── logodetection/
│   ├── symbollist/
│   └── watchlist/
├── transport/        # inbound HTTP 層（net/http ハンドラー/ミドルウェア、chi ルーター）
│   ├── csrf/         # CSRF保護（Double Submit Cookieパターン）
│   ├── handler/      # プラットフォームレベルのHTTPハンドラー（ヘルスチェック等）
│   ├── httpratelimit/ # Redisベースのスライディングウィンドウレートリミッター（HTTPミドルウェア）
│   ├── jwt/          # JWT生成・認証ミドルウェア（package jwt）
│   └── middleware/   # 共通HTTPミドルウェア（セキュリティヘッダー等）
├── infra/            # 技術基盤層（外部リソース接続・横断ユーティリティ）
│   ├── db/           # データベース初期化
│   ├── httpclient/   # 外部API呼び出し用HTTPクライアント設定（outbound）
│   ├── logging/      # 構造化ログ用ヘルパー（機密情報マスク等）
│   └── redis/        # Redisクライアントセットアップ
└── shared/           # 共有ユーティリティ（ドメイン横断、usecase からも利用可）
    └── clientratelimit/ # 外部API呼び出し用 in-memory レートリミッター
```

### フィーチャーモジュール構成

各フィーチャーは「1フィーチャー = 1パッケージ」を基本とし、ドメインモデル・ユースケース・
リポジトリ実装を**同一パッケージ**にまとめます（Go標準ライブラリ寄りの構成）。レイヤーは
ディレクトリではなく**ファイル分割**で表現します。`<name>http`（HTTPハンドラー）だけは、
`internal/api`（API型）への依存をこの層に閉じ込めるため別パッケージに分離します。

```
feature/<name>/                # package <name>（ドメイン+ユースケース+アダプタ）
├── <entity>.go                # ドメインモデル（例: candle.go の Candle 型）
├── usecase.go                 # HTTP 読み取り系ユースケース（リポジトリインターフェース定義含む）
├── ingest.go                  # バッチ書き込み系ユースケース（cmd/batch から起動。フィーチャーが持つ場合）
├── repository.go              # リポジトリ実装（PostgreSQL 等）
├── sqlc/                      # package <name>sqlc: sqlc 生成コード（手動編集禁止）
├── <external>/                # 外部APIアダプタ（例: candles/twelvedata, logodetection/gemini）
└── <name>http/                # package <name>http: HTTPハンドラー（net/http）
    └── handler.go
```

vertical slice として、各フィーチャーは**HTTP 読み取り**と**バッチ書き込み**の両ユースケースを所有します。
`<name>http/` が HTTP トリガ（`usecase.go`）、`ingest.go` がバッチトリガ（`cmd/batch` 経由）の入口です。
参照例: `candles.Candle` / `candles.NewUsecase` / `candleshttp.NewHandler` /
バッチは `candles.IngestUsecase`・`symbollist.LogoIngestUsecase`。
パッケージ名がフィーチャー名で一意になるため、import エイリアスは不要です。

**注意**: リクエスト/レスポンスの型は `internal/api/types.gen.go`（OpenAPI仕様から自動生成）を使用します。各フィーチャーにDTOは配置しません。

**注意**: Goの慣例に従い、**リポジトリインターフェースは利用者側のファイル**（`usecase.go` / `<name>http/handler.go`）で定義します。別途 domain/repository ディレクトリには配置しません。

### ファイル内の関数の並び順（トップダウン整列）

同一ファイル内の関数は、Go の慣例（新聞記事的なトップダウン）に従い、**呼ぶ側を先に・呼ばれる側を後に**、
**公開（大文字始まり）を先に・非公開ヘルパー（小文字始まり）を後に**並べます。読み手が上から下へ
読み進めれば理解できる順序にし、非公開ヘルパーを探して上へ戻らせないようにします。

- 型定義・コンストラクタ（`New*`）→ 公開API（エントリポイント）→ それらが使う非公開ヘルパー、の順
- 例: `internal/feature/auth/authhttp/handler.go` は
  `NewHandler` → `Signup` → `Login` → `Logout` → `setAuthCookie`（ヘルパーは末尾）
- 例: `internal/feature/candles/ingest.go` は
  `NewIngestUsecase` → `IngestAll`（公開エントリ）→ `ingestOne` → `dedupCandles`
- 例外: 複数の公開関数から共有される小さなキー生成ヘルパー等（`stateKey` / `blacklistKey` 等）は、
  コンストラクタ直後に置く定石を許容します

### 依存関係ルール（go-arch-lint で強制）

`.go-arch-lint.yml` で**デフォルト拒否**の宣言式に強制します（`go tool go-arch-lint check`）。
未宣言の内部依存はすべてエラーになります。

1. **フィーチャー分離**: 各フィーチャーパッケージは他のフィーチャーをインポート不可
2. **api 型境界**: フィーチャーコアは `internal/api` をインポート不可（`<name>http` 層のみ可）
3. **transport/infra 分離**: `transport/`（inbound HTTP）・`infra/`（技術基盤）は `feature/` をインポート不可
4. **外部アダプタの向き**: `twelvedata` / `gemini` / `vision` は自身のフィーチャーコアにのみ依存

サードパーティ/標準ライブラリは `allow.depOnAnyVendor: true` で一律許可し、内部依存のみを管理します。
これにより、ドメインロジックがインフラストラクチャの詳細から独立した状態を保ちます。

### 主要なアーキテクチャパターン

1. **リポジトリパターン**: すべてのデータアクセスは `usecase.go` で定義されたリポジトリインターフェースを経由します（Goの「インターフェースは利用者が定義する」慣例に従う）
2. **sqlc によるクエリ実装**: 各 feature の `sqlc/queries.sql` を `go tool sqlc generate` で生成し、`repository.go` が `*sql.DB`（pgx stdlib driver）から呼び出します。GORM は採用していません（ADR-0006 参照）。
3. **キャッシュ用デコレータパターン**: `feature/candles` の `CachingRepository` がベースリポジトリをラップ
   - `Repository`（読み取り）と `WriteRepository`（書き込み）の両インターフェースを実装
   - usecaseコードを変更せずにRedisキャッシュを透過的に追加
   - Redisが利用できない場合はグレースフルデグレード（警告ログを出力し、キャッシュなしで動作）
4. **依存性注入**: `cmd/api/main.go` で手動DI
   - Repositories → Usecases → Handlers のワイヤリングは主に main.go で直接実施
   - `internal/app/di/` には一部のファクトリ関数を配置（例: MarketRepositoryの生成）
5. **3つのエントリーポイント**:
   - `cmd/api/main.go`: REST APIサーバー（ポート8080）の起動・DIワイヤリング
     - 環境変数パースの純粋関数ヘルパーは `internal/app/config/`（`CORS_ALLOWED_ORIGINS` / `COOKIE_SECURE` 等）
   - `cmd/batch/main.go`: バッチジョブ統合エントリーポイント。コマンド引数 `job_id` で実行内容を切替（`candles`: TwelveData APIから株価データ取得 / `logo`: ロゴURL取得）
   - `cmd/migrate/main.go`: goose 埋め込みマイグレーションを適用する専用バイナリ（Cloud Run Job 等で起動）

### 外部依存
- TwelveData API（株価データ、8リクエスト/分制限） / PostgreSQL（database/sql + pgx/v5/stdlib） / Redis（キャッシュ）
- スキーマは `db/migrations/*.sql`、クエリは各 feature の `sqlc/queries.sql`
- 詳細なデータフローは各フィーチャーのドキュメント（`docs/features/`）を参照

### 認証
- JWT認証（`transport/jwt/AuthRequired()`）
- 公開: `/healthz`, `/v1/signup`, `/v1/login`, `/v1/logout`, `/v1/auth/oauth/{provider}`（+ `/callback`） / 保護: その他すべて

### テストに関する注意事項

テスト生成の詳細なルール（テーブル駆動テスト、モック定義、レイヤー別戦略等）は `/test-generate` スキル（`.claude/skills/test-generate/SKILL.md`）を参照してください。

## 新機能の追加

新機能を追加する際は、確立されたパターンに従ってください：

1. **フィーチャーディレクトリを作成**: `internal/feature/<feature-name>/`（`package <feature-name>`）
2. **ドメインモデルを定義**: `<entity>.go` にドメインモデルを作成（純粋なGo構造体）
3. **usecaseを実装**: `usecase.go`
   - ここでリポジトリインターフェースを定義（Goの慣例:「インターフェースは利用者が定義する」）
   - リポジトリを統合するビジネスロジックを実装
4. **リポジトリ実装を追加**: `repository.go` - usecase で定義したインターフェースの実装（PostgreSQL等）
   - SQL を `sqlc/queries.sql` に書き、`sqlc.yaml` の `sql:` リストに新 feature を追加（`out`/`queries` は `internal/feature/<name>/sqlc`）
   - `go tool sqlc generate` で型安全コードを生成（package は `<name>sqlc`）
   - リポジトリ実装は `*sql.DB` を受け取り、生成された `Queries` を呼ぶ
5. **HTTP層を追加**:
   - `<name>http/handler.go` - HTTPハンドラー（`package <name>http`。必要に応じてusecaseインターフェースもここで定義可）
   - リクエスト/レスポンス型は `api/openapi.yaml` に定義し、`go generate ./internal/api/...` で生成
6. **DBスキーマの変更が必要なら**: `go tool goose create <name> sql` で
   `db/migrations/NNNNN_<name>.sql` を作成し、Up/Down 両方を必ず実装
7. **依存関係をワイヤリング**: `cmd/api/main.go` または `cmd/batch/main.go` にて
8. **ルートを登録**: `internal/app/router/router.go` にて
9. **go-arch-lint にコンポーネントを追加**: `.go-arch-lint.yml` に以下を追加：
   - `components` に `<name>`（コア）、`<name>-http`（transport）、必要なら `<name>-sqlc` や外部アダプタ
   - `deps` に各コンポーネントの `mayDependOn`（コアは sqlc のみ、http層は `[<name>, api, transport, infra]` 等）
   - 合成ルート（`app` / `cmd`）の `mayDependOn` に新コンポーネントを追記

**重要**: 依存関係ルールを遵守すること - フィーチャーコアは他フィーチャーや `internal/api` をインポートできません。これは go-arch-lint で**デフォルト拒否**として強制されており、未宣言の内部依存はすべてエラーになります。`go tool go-arch-lint check` で確認してください。

## アーキテクチャ決定記録（ADR）

重要なアーキテクチャ上の決定は `docs/adr/` にADRとして記録します。

- ADRの作成: `/adr <決定トピック>` スキル（`.claude/skills/adr/SKILL.md`）を使用
- テンプレート: `docs/adr/template.md`
- 一覧・運用ルール: `docs/adr/README.md`

## コミット・PR作成の言語ルール

コミットメッセージおよびプルリクエストのタイトル・説明はすべて**日本語**で記述してください。

- コミット前のコードレビューは `/code-check` スキル（`.claude/skills/code-check/SKILL.md`）を参照

## Git ブランチ操作のルール

ブランチを切る・切り替える際は `git checkout` ではなく `git switch` を使用してください。

- 新しいブランチを作成して切り替える: `git switch -c <branch-name>`
- 既存のブランチに切り替える: `git switch <branch-name>`
