# Stock View API (Go / net/http (chi) / クリーンアーキテクチャ)

## 概要

**株式データ配信・認証用バックエンドAPI**
Goの標準ライブラリ net/http（ルーティングに chi）で構築し、フロントエンドと連携します。
REST APIとして、ユーザー認証・株式データ配信・キャッシュ最適化を提供します。

## 主な機能

- **ユーザー認証**

  - メールアドレス/パスワードによるログイン
  - OAuth2 ソーシャルログイン（Google / GitHub。Google は PKCE 対応、GitHub は state による CSRF 保護。同メールの既存アカウントへの自動リンクは行わない）
  - JWTの発行（10分の短期アクセストークン + PostgreSQL管理のリフレッシュトークン）
  - トークン検証ミドルウェアによる認可

- **株式データ取得**

  - 外部API（Twelve Data）からの株式データ取得
  - 日足・週足・月足のローソク足データを返却
  - Redisによる直近データのキャッシュ

- **キャッシュ最適化**

  - ローソク足データのRedisキャッシュ
  - TTL設定とバッチ書き込み後のキャッシュ無効化
  - キャッシュミス時: PostgreSQLから取得してRedisへ保存

- **ロゴ検出・企業分析**

  - 画像からロゴを検出（Cloud Vision API）
  - 検出した企業の分析サマリーを生成（Gemini API / Vertex AI）

- **セキュリティ強化**

  - CSRF保護（Double Submit Cookieパターン、保護ルートに適用）
  - IPベースのレートリミット（Redisスライディングウィンドウ方式）
  - セキュリティヘッダー付与（X-Content-Type-Options等）
  - SameSite Cookie設定によるクロスサイトリクエスト制御

- **データベース永続化**
  - PostgreSQL / Cloud SQLによるデータ永続化
  - sqlc 生成コード + `database/sql` (pgx/v5 stdlib driver) によるアクセス
  - goose による SQL ファイルベースのスキーマ管理（`db/migrations/`）

---

## 技術スタック

| カテゴリ        | 技術                                                                |
| --------------- | ------------------------------------------------------------------- |
| 言語            | Go (1.27.0)                                                          |
| Webフレームワーク | net/http（標準ライブラリ）+ chi（ルーター）                         |
| DB アクセス     | sqlc + database/sql + pgx/v5 stdlib                                 |
| DB マイグレーション | goose（埋め込み SQL ベース）                                      |
| DB              | PostgreSQL / Cloud SQL                                              |
| キャッシュ      | Redis                                                               |
| AI / ML         | Cloud Vision API / Gemini API（Vertex AI）                          |
| 認証・セキュリティ | JWT / bcrypt / OAuth2（GoogleのみPKCE、Google・GitHubともstate検証）/ CSRF（Double Submit Cookie）/ レートリミット |
| API仕様         | OpenAPI 3.0.4 / oapi-codegen（型生成）                              |
| 設定管理        | **docker/.env（ローカル）/ Secret Manager（本番）+ os.Getenv()**|
| コンテナ        | Docker / Docker Compose                                             |
| クラウド        | Google Cloud Run / Cloud SQL / Secret Manager / Artifact Registry   |
| CI/CD           | GitHub Actions                                                      |

## ディレクトリ構成

```text
.
├── api/
│   ├── openapi.yaml            # OpenAPI 3.0.4 仕様（APIコントラクトの単一ソース）
│   └── oapi-codegen.cfg.yaml   # oapi-codegen設定（型のみ生成）
│
├── cmd/
│   ├── batch/                  # バッチジョブ（auth-session-cleanup / candles / logo）
│   ├── migrate/                # スキーマのマイグレーション専用バイナリ（CI / Cloud Run Job等で利用）
│   └── api/                    # APIサーバーのエントリーポイント（main.go）
│
├── internal/
│   ├── api/                    # OpenAPIから自動生成された型定義
│   │   ├── generate.go         # go:generateディレクティブ
│   │   └── types.gen.go        # 生成コード（手動編集不可）
│   │
│   ├── app/                    # アプリケーション基盤
│   │   ├── batch/              # バッチ実行ロジック（job_id ディスパッチ: auth-session-cleanup / candles / logo）
│   │   ├── config/             # 環境変数パースの純粋関数ヘルパー
│   │   ├── di/                 # 依存性注入
│   │   ├── migrate/            # マイグレーション実行ロジック（goose サブコマンドディスパッチ）
│   │   └── router/             # ルーティング設定
│   │
│   ├── feature/                # フィーチャーモジュール（垂直スライス、1機能=1パッケージ）
│   │   ├── auth/               # 認証機能（package auth: entity/usecase/repository）
│   │   │   ├── sqlc/           # sqlc 生成コード（package authsqlc）
│   │   │   └── authhttp/       # HTTPハンドラー（package authhttp）
│   │   │
│   │   ├── candles/            # ローソク足データ機能（package candles）
│   │   │   ├── sqlc/           # sqlc 生成コード（package candlessqlc）
│   │   │   ├── twelvedata/     # TwelveData APIクライアント（package twelvedata）
│   │   │   └── candleshttp/    # HTTPハンドラー（package candleshttp）
│   │   │
│   │   ├── logodetection/      # ロゴ検出・企業分析機能（package logodetection）
│   │   │   ├── gemini/             # Gemini APIクライアント（package gemini）
│   │   │   ├── vision/             # Cloud Vision APIクライアント（package vision）
│   │   │   └── logodetectionhttp/  # HTTPハンドラー（package logodetectionhttp）
│   │   │
│   │   ├── symbollist/         # シンボルリスト機能（package symbollist）
│   │   │   ├── sqlc/           # sqlc 生成コード（package symbollistsqlc）
│   │   │   └── symbollisthttp/ # HTTPハンドラー（package symbollisthttp）
│   │   │
│   │   └── watchlist/          # ウォッチリスト機能（package watchlist）
│   │       ├── sqlc/           # sqlc 生成コード（package watchlistsqlc）
│   │       └── watchlisthttp/  # HTTPハンドラー（package watchlisthttp）
│   │
│   ├── transport/             # inbound HTTP 層（net/http ハンドラー/ミドルウェア、chi ルーター）
│   │   ├── csrf/               # CSRF保護（Double Submit Cookieパターン）
│   │   ├── handler/            # ヘルスチェックハンドラー
│   │   ├── httpratelimit/      # Redisベースのスライディングウィンドウレートリミッター（HTTPミドルウェア）
│   │   ├── httpx/              # JSON・クライアントIP等のHTTP共通処理
│   │   ├── jwt/                # JWT生成/検証/ミドルウェア（package jwt）
│   │   ├── middleware/         # セキュリティヘッダー・アクセスログ等のミドルウェア
│   │   └── openapivalidate/    # OpenAPIリクエスト検証ミドルウェア
│   │
│   ├── infra/                  # 技術基盤層（外部リソース接続・横断ユーティリティ）
│   │   ├── db/                 # データベース接続初期化
│   │   ├── httpclient/         # 外部API呼び出し用HTTPクライアント設定
│   │   ├── logging/            # 構造化ログ用ヘルパー
│   │   └── redis/              # Redisクライアント実装
│   │
│   └── shared/                 # 共有ユーティリティ（usecase からも利用可）
│       └── clientratelimit/    # 外部API呼び出し用 in-memory レートリミッター
│
├── docker/                     # Docker関連ファイル
│   ├── Dockerfile.batch        # バッチ統合用Dockerfile（本番・job_idで3ジョブを切替）
│   ├── Dockerfile.api          # APIサーバー用Dockerfile（本番）
│   ├── Dockerfile.api.dev      # APIサーバー用Dockerfile（ローカル開発）
│   ├── Dockerfile.migrate      # マイグレーション用Dockerfile（Cloud Run Job で実行）
│   ├── docker-compose.yml      # ローカル開発用 compose 定義（サービス・ネットワーク設定）
│   ├── air.toml                # Air（ホットリロード）設定
│   ├── example.env             # 環境変数テンプレート（compose 変数置換 + コンテナにロード）
│   └── postgres/               # PostgreSQL初期化スクリプト
│
├── docs/
│   ├── adr/                    # アーキテクチャ決定記録（ADR）
│   ├── features/               # 各フィーチャーのドキュメント（設計・API・シーケンス図）
│   ├── schema/                 # tbls が生成する DB スキーマドキュメント
│   └── tbls.yml                # tbls（ER 図生成）設定
├── go.mod
├── go.sum
└── .github/
    └── workflows/              # CI/CD（テスト、ビルド、デプロイ）
```

## API仕様（OpenAPI）

API仕様は `api/openapi.yaml`（OpenAPI 3.0.4）で管理しています。
この仕様ファイルから [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) を使って Go の型定義を自動生成しています。

### 仕様の確認（Swagger UI）

`backend` の起動時に Swagger UI も依存として自動で立ち上がります：

```bash
docker compose -f docker/docker-compose.yml -p stock up backend
```

ブラウザで http://localhost:8081 を開くとAPI仕様を確認できます。

### 型の再生成

OpenAPI仕様（`api/openapi.yaml`）を変更した場合、以下のコマンドで Go の型定義を再生成してください：

```bash
go generate ./internal/api/...
```

生成されるファイル: `internal/api/types.gen.go`（手動編集不可）

## 認証・セキュリティ設計

### 現在の実装

- 10分のJWTアクセストークンによる認証（Cookieまたは`Authorization: Bearer <token>`ヘッダー）
- **リフレッシュトークン**: 30日有効の不透明トークンをPostgreSQLでハッシュ管理し、使用ごとにローテーション
- **OAuth2 ソーシャルログイン**: Google / GitHub（GoogleのみPKCE対応、state は両プロバイダーともRedis管理。同メールの既存アカウントへの自動リンクは行わず、フロントエンドへ `error=account_conflict` 付きでリダイレクト）。OAuth 環境変数が設定されている場合のみ有効
- **CSRF保護**: Double Submit Cookieパターン（`csrf_token` Cookie + `X-CSRF-Token` ヘッダーの一致を検証）
- **レートリミット**: Redisスライディングウィンドウ方式（signup: 5回/時、login: 10回/分、OAuth認可開始・callback: 各20回/分、logo API: ユーザー単位で各10回/日）
- **セキュリティヘッダー**: `X-Content-Type-Options`、`X-Frame-Options` 等を全レスポンスに付与
- **SameSite Cookie**: `Lax` 設定でクロスサイトリクエストを制御

## データフロー（例: 株価取得）

1. バッチプロセス（`cmd/batch candles`）が外部API（Twelve Data）から株式データを取得
2. 取得したローソク足データをPostgreSQL（またはCloud SQL）に保存
3. フロントエンドが `GET /v1/candles/AAPL?interval=1day&outputsize=200` をリクエスト（JWT Bearer トークン付き）
4. ハンドラーが `Usecase` を呼び出し
5. ユースケースがリポジトリ経由で **Redisキャッシュ** を確認
   - **キャッシュヒット**: Redisから即座に返却
   - **キャッシュミス**: PostgreSQLから取得 → Redisにキャッシュ → レスポンスを返却
6. フロントエンドにJSON形式で結果を返却

## APIエンドポイント

### ヘルスチェック

| メソッド | パス       | 認証   | 説明                                    |
| -------- | ---------- | ------ | --------------------------------------- |
| GET      | `/healthz` | 不要   | サービスのヘルスチェック（200 OKを返却） |

---

### 認証

| メソッド | パス           | 認証   | 説明                                              |
| -------- | -------------- | ------ | ------------------------------------------------- |
| POST     | `/v1/signup`   | 不要   | 新規ユーザー登録（IPレートリミット: 5回/時）      |
| POST     | `/v1/login`    | 不要   | ログイン（JWTアクセストークンを発行、10回/分）    |
| POST     | `/v1/auth/refresh` | 不要 | 認証トークン更新（Cookie + CSRF、30回/分）        |
| DELETE   | `/v1/logout`   | 不要   | ログアウト（期限切れトークンでも実行可能）        |

---

### OAuth認証（OAuth環境変数が設定されている場合のみ有効）

| メソッド | パス                                   | 認証   | 説明                                                    |
| -------- | -------------------------------------- | ------ | ------------------------------------------------------- |
| GET      | `/v1/auth/oauth/{provider}`             | 不要   | OAuth認可フローを開始（IPレートリミット: 20回/分）       |
| GET      | `/v1/auth/oauth/{provider}/callback`    | 不要   | OAuth認可コールバック（IPレートリミット: 20回/分）       |

---

### 株式データ（ローソク足 / シンボル）

| メソッド | パス                | 認証   | 説明                                              |
| -------- | ------------------- | ------ | ------------------------------------------------- |
| GET      | `/v1/symbols`       | 必要   | シンボルリストの取得                               |
| GET      | `/v1/candles/{code}` | 必要   | 指定コードのローソク足データを取得（例: AAPL）     |
| GET      | `/v1/quotes`        | 必要   | 複数銘柄の最新終値・前日比を一括取得（`codes`カンマ区切り、最大50件） |

---

### ロゴ検出・企業分析

| メソッド | パス                | 認証   | 説明                                              |
| -------- | ------------------- | ------ | ------------------------------------------------- |
| POST     | `/v1/logo/detect`   | 必要   | 画像からロゴを検出（multipart/form-data、ユーザー単位10回/日） |
| POST     | `/v1/logo/analyze`  | 必要   | 企業分析サマリーを生成（JSON、ユーザー単位10回/日）            |

---

### ウォッチリスト

| メソッド | パス                      | 認証 | 説明                          |
| -------- | ------------------------- | ---- | ----------------------------- |
| GET      | `/v1/watchlist`           | 必要 | ウォッチリスト一覧取得         |
| POST     | `/v1/watchlist`           | 必要 | ウォッチリストに銘柄を追加     |
| DELETE   | `/v1/watchlist/{code}`     | 必要 | ウォッチリストから銘柄を削除   |
| PUT      | `/v1/watchlist/order`     | 必要 | ウォッチリストの並び順を更新   |

### 補足

- `/v1/candles/*`、`/v1/quotes`、`/v1/symbols`、`/v1/watchlist*`、`/v1/logo/*` は **JWT認証** が必要です。`auth_token` Cookieを優先し、Cookieがなければ`Authorization: Bearer <token>`を使用します。
- CSRFトークンは、Cookie認証でPOST・PUT・PATCH・DELETEを呼ぶ場合に必要です。GET・HEAD・OPTIONSおよびBearer認証では検証をスキップします。
- `/v1/signup` と `/v1/login` には **IPベースのレートリミット** が適用されています。
- `/v1/auth/refresh` は `refresh_token` CookieとCSRFトークンを検証し、トークン一式をローテーションします。
- `/v1/auth/oauth/*` は OAuth 環境変数（`GOOGLE_CLIENT_ID` または `GITHUB_CLIENT_ID` 等）が設定されている場合のみ登録されます。詳細は [auth フィーチャーのドキュメント](docs/features/auth.md) を参照してください。

### Bruno から実行する

ローカル API を手軽に確認できる Bruno コレクションを [`bruno/`](bruno/README.md) に用意しています。Bruno で `bruno/` を Open Collection し、Signup → Login の順に実行すると、Cookie 認証と CSRF ヘッダーが以降のリクエストへ自動で引き継がれます。

## クラウドアーキテクチャ（Google Cloud）

- **Cloud Run**: Dockerイメージをデプロイ
- **Cloud SQL（PostgreSQL）**: アプリケーションデータの永続化
- **Redis（Cloud Memorystore）**: キャッシュ管理
- **Secret Manager**: APIキー・DBパスワード・JWTシークレット等を安全に管理し、Cloud Runの環境変数として注入
- アプリケーションは注入された値を起動時に `os.Getenv()` で読み込み
- **ローカル開発では `docker/.env` から読み込み**

## CI/CD

- **GitHub Actions** がプルリクエスト作成時に自動テストを実行
- ドキュメントのみの変更でもMarkdown内のローカルリンク切れを検証
- API用・batch用のCDワークフローは、mainへのマージ（コード変更を含むpush）で自動起動する。
  ドキュメントのみの変更（`**.md` / `docs/**` / `LICENSE`）では起動しない
- 手動実行（`workflow_dispatch`）も引き続き可能で、新規環境構築時や初回リソース作成時に使用する
- 自動起動したCDワークフローがGitHub Actions上でDockerイメージをビルドし、**Artifact Registry** に保存
- **Workload Identity Federation** を使用してGitHubからGCPへ安全にデプロイ
- Cloud RunサービスとJobs、環境変数、Secret参照、ネットワーク、ランタイムSAは **stock-infraのTerraform** が管理
- backendのCDはcommit SHA付きイメージのpushと、既存Cloud Runリソースのイメージ更新・traffic切替だけを担当
- API用CD（`cd-api.yaml`）は、前段で `cd-migrate.yaml` を呼び出し、`migrate up` が成功した場合のみAPIイメージを更新。
  push起動時は `db/migrations/` に変更がある場合だけmigrateを実行し、変更がなければスキップしてそのままAPIをデプロイする
- migrateのサブコマンドはJob定義を書き換えず、実行時の `--args` overrideとして渡す
- batch用CD（`cd-batch.yaml`）は単一のCloud Run Job `batch` を更新し、`execute=true` の場合だけ選択した `job_id`（`candles` / `logo` / `auth-session-cleanup`）を実行時の `--args` overrideとして渡す。
  push起動時は常にイメージ更新のみを行い、バッチの実行は行わない

各batchプロセスは処理開始前にPostgreSQLのセッションレベルadvisory lockを`job_id`単位で取得する。
Schedulerの再試行や手動実行が重なった場合、同じ`job_id`の後続Executionは処理本体を実行せず、
`event=batch_skipped`、`reason=already_running`をログへ記録して終了コード0で安全に終了する。
異なる`job_id`は並行実行でき、先行Executionの終了後は同じ`job_id`を再実行できる。

新規環境では、TerraformでCloud Runを作成する前にAPI・batch・migrateの各ワークフローを
`publish_only=true` で手動実行（`workflow_dispatch`）し、初回作成に使うイメージをArtifact Registryへpushします。
通常運用ではmainへのマージにより自動実行され、`publish_only=false` 相当で動作します。
CDから環境変数やSecret参照を変更してはいけません。
手動実行でバッチイメージの更新だけを行う場合は `execute=false`、更新後にバッチも起動する場合は
`execute=true` と実行対象の `job_id` を指定します。

## セットアップ

### 前提条件

- Docker / Docker Compose がインストール済みであること
- 通常のAPI・バッチ起動だけならGoのインストールは不要。スキーマ文書再生成やホスト上でのテストにはGoが必要
- `docker/.env` にローカル環境変数を設定

---

### 手順

```bash
# リポジトリをクローン
git clone https://github.com/UCHIDAnobuhiro/stock-backend.git
cd stock-backend

# 環境変数ファイルをコピー
cp docker/example.env docker/.env
```

### Twelve Data APIキーの取得

このアプリケーションは [Twelve Data API](https://twelvedata.com/) を使用しています。
株式データの取得には無料のAPIキーが必要です。

1. Twelve Dataのウェブサイトでアカウントを作成
2. 「Dashboard > API Keys」からキーを発行
3. `docker/.env` に `TWELVE_DATA_API_KEY` として設定
   例: `TWELVE_DATA_API_KEY=your_api_key_here`

### Twelve Data 無料プランの制限事項

- 無料プランでは **1分あたり最大8リクエスト** まで

この制限に対応するため、本アプリケーションでは以下を実施しています：

- **candles バッチによるデータの事前取得**
- **Redisキャッシュによるリクエスト数の最小化**

### GCP認証の設定（ロゴ検出・企業分析機能を使用する場合）

ロゴ検出・企業分析機能は Google Cloud の Vision API と Vertex AI（Gemini）を使用します。

1. [Google Cloud CLI](https://cloud.google.com/sdk/docs/install) をインストール
2. ADC（Application Default Credentials）で認証

```bash
gcloud auth application-default login
```

3. `docker/.env` に以下を設定

```env
# コンテナ内のパス（root実行時: /root/... 、非root実行時は適宜変更）
GOOGLE_APPLICATION_CREDENTIALS=/root/.config/gcloud/application_default_credentials.json
HOST_GOOGLE_ADC_PATH=$HOME/.config/gcloud/application_default_credentials.json
```

4. `docker/.env` に以下を追加

```env
GOOGLE_GENAI_USE_VERTEXAI=true
GOOGLE_CLOUD_PROJECT=<GCPプロジェクトID>
GOOGLE_CLOUD_LOCATION=global
```

### APIサーバーの起動

`backend` を起動すると、依存として `migrate`（マイグレーション適用）→ `seed`（初期データ投入）が
順に実行され、`swagger-ui`（http://localhost:8081 ）も並行起動します。

```bash
docker compose -f docker/docker-compose.yml -p stock up backend
```

`seed.sql` は冪等（`INSERT ... ON CONFLICT` による upsert のみ）なので、再起動のたびに
再実行されても既存の candles / watchlists 等は削除されません。

### バッチプロセスの起動（株式データ取り込み）

```bash
docker compose -f docker/docker-compose.yml -p stock run --rm --no-deps candles
```

### バッチプロセスの起動（ロゴURL取得）

```bash
docker compose -f docker/docker-compose.yml -p stock run --rm --no-deps logo
```

### バッチプロセスの起動（期限切れ認証セッション削除）

```bash
docker compose -f docker/docker-compose.yml -p stock run --rm --no-deps auth-session-cleanup
```

### ER 図・テーブル定義書の生成（tbls）

スキーマは [tbls](https://github.com/k1LoW/tbls) で稼働中の PostgreSQL から自動生成されます。
生成物は [docs/schema/](docs/schema/) 配下にコミットされており、GitHub 上で Mermaid ER 図としてレンダリングされます。

`db/migrations/` 配下のスキーマを変更したときは、以下の1コマンドで再生成します。

```bash
./scripts/regen-schema-docs.sh
```

前提: Docker（`docker` コマンドが使えること）とホストの Go（`go run ./cmd/migrate` を実行するため）。
`.env` や GCP ADC は不要です。

このスクリプトは使い捨てのPostgreSQLコンテナ（`postgres:18`）を一時起動し、
そこにマイグレーションを一括適用してから `tbls doc --force` → `tbls diff` を実行し、
最後に一時コンテナ・ネットワークを削除します。稼働中の開発用DB（`stock-postgres`）や
そのボリュームには一切触れません。

**なぜフレッシュDBが必要か**: tblsは制約（PRIMARY KEY / FOREIGN KEY）をインデックスのOID順で
取得するため、up/downを繰り返した使い回しの開発用DBではOIDの配置が異なり、論理的に同一の
スキーマでも制約の表示順序が変わってしまいます。その状態で生成すると、毎回フレッシュなDBで
検証している CI の `Schema Doc Drift` ジョブと意図せず食い違って失敗します。

スクリプト実行後は `git diff` で `docs/schema/` の差分を確認し、意図した変更であれば
そのままコミットしてください。CI の `Schema Doc Drift` ジョブでスキーマと `docs/schema/` の
乖離を検出するため、スキーマを変更した PR では必ず再生成してコミットしてください。

### 補足

- **APIサーバー**: <http://localhost:8080>
- **PostgreSQL**: `localhost:5432`
- **Redis**: `localhost:6379`
- **ログ確認**: `docker logs -f stock-backend`
- **バッチプロセス**: `candles`（株価取り込み）、`logo`（ロゴURL取り込み）、`auth-session-cleanup`（期限切れ認証セッション削除）
