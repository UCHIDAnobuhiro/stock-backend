# Candles フィーチャー

## 概要

Candlesフィーチャーは、株式市場のローソク足（OHLCV）データ管理を提供します。REST APIによるリアルタイムデータ取得と、外部マーケットデータプロバイダーからのバッチデータ取り込みの両方を処理します。

### 主な機能

- **ローソク足データ取得**: 銘柄、インターバル、出力サイズによるOHLCVデータのクエリ
- **複数の時間間隔**: 日次、週次、月次のインターバルをサポート
- **バッチデータ取り込み**: レート制限付きのTwelveData APIからの自動データ取得
- **Redisキャッシュ**: 自動キャッシュ無効化を備えた透過的なキャッシュレイヤー
- **Upsert操作**: 複合ユニークキーを使用した効率的なバッチ挿入/更新

## シーケンス図

### ローソク足取得フロー（APIリクエスト）

```mermaid
sequenceDiagram
    participant Client
    participant Handler as Handler
    participant Usecase as Usecase
    participant Cache as CachingRepository
    participant Repository as Repository
    participant Redis
    participant DB as PostgreSQL

    Client->>Handler: GET /candles/:code?interval=1day&outputsize=200
    Handler->>Handler: Parse params (defaults: interval=1day, outputsize=200)
    Handler->>Usecase: GetCandles(symbol, interval, outputsize)
    Usecase->>Usecase: Apply defaults if needed
    Usecase->>Cache: Find(symbol, interval, outputsize)

    alt Redis Available
        Cache->>Redis: GET candles:AAPL:1day:200
        alt Cache HIT
            Redis-->>Cache: JSON data
            Cache->>Cache: Unmarshal JSON
            Cache-->>Usecase: []Candle
        else Cache MISS
            Redis-->>Cache: nil
            Cache->>Repository: Find(symbol, interval, outputsize)
            Repository->>DB: SELECT * FROM candles WHERE symbol_code=? AND interval=? ORDER BY time DESC LIMIT ?
            DB-->>Repository: Rows
            Repository-->>Cache: []Candle
            Cache->>Redis: SET candles:AAPL:1day:200 (TTL)
            Cache-->>Usecase: []Candle
        end
    else Redis Unavailable
        Cache->>Repository: Find(symbol, interval, outputsize)
        Repository->>DB: SELECT ...
        DB-->>Repository: Rows
        Repository-->>Cache: []Candle
        Cache-->>Usecase: []Candle
    end

    Usecase-->>Handler: []Candle
    Handler->>Handler: Convert to DTOs
    Handler-->>Client: 200 OK<br/>[{time, open, high, low, close, volume}, ...]
```

### バッチ取り込みフロー

外部APIへのリクエスト数を抑えるため、**日足のみを取得し、週足/月足はサーバー内で集計**します。集計ロジックは [aggregation.go](../../internal/feature/candles/aggregation.go) に分離されています。

```mermaid
sequenceDiagram
    participant Main as cmd/batch (candles)
    participant Usecase as IngestUsecase
    participant SymbolRepo as SymbolRepository
    participant RateLimiter
    participant Market as MarketRepository<br/>(TwelveData API)
    participant Aggregation as aggregation.go
    participant Cache as CachingRepository
    participant Repository as Repository
    participant Redis
    participant DB as PostgreSQL

    Main->>Usecase: IngestAll(ctx)
    Usecase->>SymbolRepo: ListActiveSymbols(ctx)
    SymbolRepo-->>Usecase: []ActiveSymbol{Code, Timezone}

    loop For each ActiveSymbol
        Usecase->>Usecase: Load IANA timezone (loc)
        Usecase->>RateLimiter: WaitIfNeeded()
        Usecase->>Market: GetTimeSeries(symbol, "1day", 5000, loc)
        Market-->>Usecase: []Candle (daily, 取引所ローカル時刻)

        Usecase->>Aggregation: aggregateWeekly(daily, loc)
        Aggregation-->>Usecase: []Candle (weekly, ISO週単位)
        Usecase->>Aggregation: trimIncompleteFirstBucket(weekly, daily, isWeekStart)
        Aggregation-->>Usecase: []Candle (先頭の不完全週を除外)

        Usecase->>Aggregation: aggregateMonthly(daily, loc)
        Aggregation-->>Usecase: []Candle (monthly)
        Usecase->>Aggregation: trimIncompleteFirstBucket(monthly, daily, isMonthStart)
        Aggregation-->>Usecase: []Candle (先頭の不完全月を除外)

        Usecase->>Usecase: Set symbol & interval on each batch
        Usecase->>Usecase: dedupCandles (重複排除)
        Usecase->>Cache: UpsertBatch(daily + weekly + monthly)
        Cache->>Repository: UpsertBatch(candles)
        Repository->>DB: INSERT ... ON CONFLICT DO UPDATE
        DB-->>Repository: Success

        alt Redis Available
            Cache->>Redis: DEL candles:{symbol}:{interval} ※全インターバル分
            Note over Cache,Redis: Cache invalidation
        end

        Cache-->>Usecase: nil

        alt Error Occurred
            Usecase->>Usecase: Log error, increment Failed, continue
        end
    end

    Usecase-->>Main: IngestResult{Total, Succeeded, Failed}
```

**集計ロジックのポイント**:
- **タイムゾーン考慮**: `ActiveSymbol.Timezone`（IANA タイムゾーン）を `*time.Location` にロードし、週/月の境界判定および代表タイムスタンプ生成に使用
- **週足の境界**: ISO 週（月曜起点）。バケットキー例 `2024-W03`、代表時刻はその週の月曜 00:00:00
- **月足の境界**: 暦月の 1 日 00:00:00
- **不完全バケット除外**: 取得データの先頭が週/月の途中から始まる場合、`trimIncompleteFirstBucket` で先頭バケットを除外し、既存の完全レコードを上書きしないようにする
- **重複排除**: `dedupCandles` で `(symbol_code, interval, time)` の重複を除去してから Upsert

## API仕様

### GET /candles/:code

指定された銘柄のローソク足データを取得します。JWT認証が必要です。

**認証方式**（優先順位順）:
1. `auth_token` Cookie（ブラウザクライアント）+ `X-CSRF-Token` ヘッダー（必須）
2. `Authorization: Bearer <token>` ヘッダー（APIクライアント・curl等）— この場合CSRFチェックをスキップ

**パスパラメータ**
| パラメータ | 説明 | 例 |
|-----------|------|-----|
| `code` | 銘柄コード | `7203.T`, `AAPL` |

**クエリパラメータ**
| パラメータ | デフォルト | 説明 |
|-----------|-----------|------|
| `interval` | `1day` | 時間間隔（`1day`, `1week`, `1month`）。未指定時のみデフォルト適用。空文字や未対応値は `400` |
| `outputsize` | `200` | 返却するデータポイント数（`1`〜`5000`）。範囲外・整数以外は `400` |

**リクエスト例（Cookieベース）**
```http
GET /v1/candles/7203.T?interval=1day&outputsize=100
Cookie: auth_token=eyJhbGc...
X-CSRF-Token: <csrf_token Cookieの値>
```

**リクエスト例（Bearerヘッダー）**
```http
GET /v1/candles/7203.T?interval=1day&outputsize=100
Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
```

**レスポンス**

- **200 OK** - 成功
  ```json
  [
    {
      "time": "2024-01-15",
      "open": 2500.0,
      "high": 2550.0,
      "low": 2480.0,
      "close": 2530.0,
      "volume": 1500000
    },
    {
      "time": "2024-01-14",
      "open": 2480.0,
      "high": 2510.0,
      "low": 2470.0,
      "close": 2500.0,
      "volume": 1200000
    }
  ]
  ```
  注: 結果は時間の降順（新しい順）でソートされます。

- **401 Unauthorized** - 認証トークンが未指定または無効
  ```json
  {
    "error": "missing authentication token"
  }
  ```

- **403 Forbidden** - CSRFトークンが不正（Cookieベース認証時）
  ```json
  {
    "error": "invalid csrf token"
  }
  ```

- **502 Bad Gateway** - データベースまたは上流サービスのエラー
  ```json
  {
    "error": "database connection failed"
  }
  ```

## 依存関係図

```mermaid
graph TB
    subgraph "Transport Layer"
        Handler[Handler<br/>candleshttp]
    end

    subgraph "API Types (Generated)"
        APITypes[CandleResponse<br/>internal/api/types.gen.go]
    end

    subgraph "Usecase Layer"
        CandlesUC[Usecase<br/>candles]
        IngestUC[IngestUsecase<br/>candles]
        ReadInterface[Repository Interface<br/>usecase.go]
        WriteInterface[WriteRepository Interface<br/>ingest.go]
        MarketInterface[MarketRepository Interface<br/>ingest.go]
        SymbolInterface[SymbolRepository Interface<br/>ingest.go]
    end

    subgraph "Domain Layer"
        Entity[Candle Entity<br/>candles]
    end

    subgraph "Adapters Layer"
        RepoImpl[Repository<br/>candles]
        Cache[CachingRepository<br/>candles]
        TwelveData[TwelveDataMarket<br/>twelvedata]
    end

    subgraph "Shared"
        RateLimiter[RateLimiter<br/>shared/clientratelimit]
    end

    subgraph "External Dependencies"
        DB[(PostgreSQL)]
        Redis[(Redis)]
        API[TwelveData API]
    end

    Handler -->|depends on| CandlesUC
    Handler -->|uses| APITypes
    CandlesUC -->|defines| ReadInterface
    CandlesUC -->|uses| Entity
    IngestUC -->|defines| WriteInterface
    IngestUC -->|defines| MarketInterface
    IngestUC -->|defines| SymbolInterface
    IngestUC -->|uses| RateLimiter
    Cache -.->|implements| ReadInterface
    Cache -.->|implements| WriteInterface
    Cache -->|decorates| RepoImpl
    Cache -->|uses| Redis
    RepoImpl -->|uses| Entity
    RepoImpl -->|accesses| DB
    TwelveData -.->|implements| MarketInterface
    TwelveData -->|calls| API

    style Handler fill:#e1f5ff
    style CandlesUC fill:#fff4e1
    style IngestUC fill:#fff4e1
    style Entity fill:#e8f5e9
    style ReadInterface fill:#fff4e1
    style WriteInterface fill:#fff4e1
    style MarketInterface fill:#fff4e1
    style SymbolInterface fill:#fff4e1
    style RepoImpl fill:#f3e5f5
    style Cache fill:#f3e5f5
    style TwelveData fill:#f3e5f5
    style DB fill:#ffebee
    style Redis fill:#ffebee
    style API fill:#ffebee
```

### 依存関係の説明

#### トランスポート層（[candleshttp/handler.go](../../internal/feature/candles/candleshttp/handler.go)）
- **Handler**: HTTPリクエストを処理し、Usecaseを呼び出す
- **API型**（`internal/api/types.gen.go`）: OpenAPI仕様から自動生成された `api.CandleResponse` を使用

#### ユースケース層
- **Usecase**（[usecase.go](../../internal/feature/candles/usecase.go)）: ローソク足データ取得
  - パラメータの妥当性検証は OpenAPI バリデーションミドルウェアと handler が担うため、usecase は受け取った値をそのままリポジトリへ渡す（丸めやデフォルト化はしない）
  - 未指定時のデフォルト（`interval=1day` / `outputsize=200`）は handler が補う
  - `Repository`インターフェース（読み取り専用）を定義（Goの「インターフェースは利用者が定義する」慣例に従う）
- **IngestUsecase**（[ingest.go](../../internal/feature/candles/ingest.go)）: 外部APIからのバッチデータ取り込み
  - アクティブな銘柄（コード + IANA タイムゾーン）を取得
  - **日足のみ外部APIから取得**し、サーバー内で週足/月足を集計（API リクエスト数の削減）
  - RateLimiterによるレート制限を遵守
  - `WriteRepository`インターフェース（書き込み専用）を定義
  - `MarketRepository`インターフェース（外部API抽象化）を定義
  - `SymbolRepository`インターフェース（`ListActiveSymbols(ctx) ([]ActiveSymbol, error)` を返す）を定義
  - 結果は `IngestResult{Total, Succeeded, Failed}` として返却（部分失敗時の集計）
- **集計ロジック**（[aggregation.go](../../internal/feature/candles/aggregation.go)）: 日足から週足/月足を生成
  - `aggregateWeekly` / `aggregateMonthly`: ISO 週・暦月単位で OHLCV を集計（タイムゾーン考慮）
  - `trimIncompleteFirstBucket`: 先頭の不完全バケットを除外し、既存レコードの上書きを防止
  - `aggregate`: 共通の集計エンジン（バケット化 + 出現順保持）

#### ドメイン層
- **Candle Entity**（[candle.go](../../internal/feature/candles/candle.go)）: OHLCVローソク足データモデル
  - `SymbolCode`: 銘柄コード（例: "AAPL", "7203.T"）。`symbols.code` への外部キー
  - `Interval`: 時間間隔（例: "1day", "1week", "1month"）
  - `Time`: ローソク足期間のタイムスタンプ
  - `Open`, `High`, `Low`, `Close`: 価格データ
  - `Volume`: 出来高

#### アダプター層（[repository.go](../../internal/feature/candles/repository.go)）
- **candleDBRepository**: Repository/WriteRepository のリポジトリ実装（sqlc + database/sql、UpsertBatch は raw 多値 INSERT ON CONFLICT）
  - `Find`: 時間の降順でローソク足を取得
  - `UpsertBatch`: `ON CONFLICT DO UPDATE`によるバッチ挿入/更新
  - （symbol_code, interval, time）の複合ユニークインデックス
  - `symbol_code` は `symbols.code` への FK（ON DELETE RESTRICT、`db/migrations` のスキーマで付与）

#### アダプター層（キャッシュ）
- **CachingRepository**（[caching_repository.go](../../internal/feature/candles/caching_repository.go)）: Redisキャッシュデコレータ
  - Repositoryをラップするデコレータパターンを実装
  - `Repository`（読み取り）と`WriteRepository`（書き込み）の両インターフェースを実装
  - キャッシュキー形式: `candles:{symbol}:{interval}:{outputsize}`
  - UpsertBatch時の自動キャッシュ無効化
  - Redis利用不可時のグレースフルデグレード
- **TwelveDataMarket**（[twelvedata/repository.go](../../internal/feature/candles/twelvedata/repository.go)）: TwelveData APIクライアント
  - `MarketRepository`インターフェースを実装
  - 外部APIからの時系列データ取得

### アーキテクチャの特徴

1. **クリーンアーキテクチャ**: ドメイン層がインフラストラクチャから独立
2. **依存性逆転**: Usecaseが読み取り用（Repository）・書き込み用（WriteRepository）の分離されたインターフェースを定義し、adaptersが実装
3. **デコレータパターン**: CachingRepositoryが透過的にキャッシュを追加（adapters層内で完結）
4. **インターフェース所有権**: インターフェースは利用される場所で定義（Goのベストプラクティス）
5. **グレースフルデグレード**: Redis利用不可時もシステムは継続して動作
6. **フィーチャー内完結**: TwelveData APIクライアントとキャッシュデコレータがcandles feature内のadaptersに配置され、関心の分離を実現

## ディレクトリ構成

```
candles/                               # package candles（コア: domain/usecase/adapters を統合）
├── README.md                          # 本ファイル
├── candle.go                          # Candleエンティティ（OHLCVデータ）
├── usecase.go                         # クエリロジック + Repositoryインターフェース
├── usecase_test.go                    # ユースケーステスト
├── ingest.go                          # バッチ取り込み + MarketRepository / WriteRepository / SymbolRepositoryインターフェース
├── ingest_test.go                     # 取り込みテスト
├── aggregation.go                     # 日足→週足/月足 集計ロジック
├── aggregation_test.go                # 集計テスト
├── repository.go                      # リポジトリ実装
├── repository_test.go                 # リポジトリテスト
├── caching_repository.go              # Redisキャッシュデコレータ
├── caching_repository_test.go
├── sqlc/                              # package candlessqlc（sqlc 生成コード、手動編集禁止）
│   ├── db.go
│   ├── models.go
│   ├── querier.go
│   ├── queries.sql
│   └── queries.sql.go
├── twelvedata/                        # package twelvedata（TwelveData APIクライアント）
│   ├── config.go                      # API設定
│   ├── logo.go                        # ロゴURL取得
│   ├── logo_test.go
│   ├── repository.go                  # MarketRepository実装
│   ├── repository_test.go
│   └── time_series_response.go        # APIレスポンス型
└── candleshttp/                         # package candleshttp
    ├── handler.go                     # HTTPハンドラー
    └── handler_test.go                # ハンドラーテスト
```

## テスト

Candlesフィーチャーの全テストは、一貫性と保守性のために**テーブル駆動テストパターン**に従います。

### テスト構造とパターン

#### 全テスト共通のパターン

1. **テーブル駆動テスト**: 全テスト関数は構造体フィールドを持つ`tests`スライスを使用:
   - `name`: テストケースの説明（例: `"success: all parameters specified"`, `"error: repository returns error"`）
   - `wantErr`: エラーが期待されるかどうかを示すブール値フラグ
   - テストタイプ固有の追加フィールド

2. **並列実行**: リポジトリテストとハンドラーテストは`t.Parallel()`を使用:
   ```go
   func TestCandleRepository_Find(t *testing.T) {
       t.Parallel()
       // ...
       for _, tt := range tests {
           t.Run(tt.name, func(t *testing.T) {
               t.Parallel()
               // ...
           })
       }
   }
   ```

3. **ヘルパー関数**: 各テストファイルにヘルパー関数を含む:
   - リポジトリ: `setupTestDB()`, `seedCandle()`
   - ハンドラー: HTTPテスト用に`httptest.NewRecorder()`を使用

#### ユースケーステスト（[usecase_test.go](../../internal/feature/candles/usecase_test.go)）

ビジネスロジックを分離してテストするために**モックリポジトリ**を使用します。

**テストケース構造:**
```go
tests := []struct {
    name               string
    inputSymbol        string
    inputInterval      string
    inputOutputsize    int
    mockFindFunc       func(...) ([]entity.Candle, error)
    expectedCandles    []entity.Candle
    expectedErr        error
    expectedInterval   string  // モックに渡されるべき値
    expectedOutputsize int     // モックに渡されるべき値
}{/* ... */}
```

**主な特徴:**
- カスタマイズ可能な動作を持つモック実装
- パラメータバリデーションテスト（デフォルト値、最大値制限）
- 呼び出し回数の検証

**実行コマンド:**
```bash
go test ./internal/feature/candles/... -v
```

#### ハンドラーテスト（[candleshttp/handler_test.go](../../internal/feature/candles/candleshttp/handler_test.go)）

HTTPリクエスト/レスポンス処理をテストするために**モックユースケース**を使用します。

**テストケース構造:**
```go
tests := []struct {
    name           string
    url            string
    mockGetCandles func(...) ([]entity.Candle, error)
    expectedStatus int
    expectedBody   string  // JSON文字列比較
}{/* ... */}
```

**主な特徴:**
- HTTPステータスコードの検証
- `assert.JSONEq`によるJSONレスポンスボディの照合
- クエリパラメータのパース検証
- デフォルト値の処理

**実行コマンド:**
```bash
go test ./internal/feature/candles/candleshttp/... -v
```

#### リポジトリテスト（[repository_test.go](../../internal/feature/candles/repository_test.go)）

統合テストに**インメモリSQLiteデータベース**を使用します。

**テストケース構造:**
```go
tests := []struct {
    name         string
    symbol       string
    interval     string
    outputsize   int
    wantErr      bool
    setupFunc    func(t *testing.T, db *sql.DB)
    validateFunc func(t *testing.T, candles []entity.Candle)
}{/* ... */}
```

**主な特徴:**
- 各テストは新しいインメモリSQLiteデータベースを使用
- `setupFunc`: テスト実行前にテストデータを準備
- `validateFunc`: 成功ケースのカスタムバリデーションロジック
- Upsert動作のテスト（挿入 vs 更新）
- ソート順とLIMIT機能のテスト

**実行コマンド:**
```bash
go test ./internal/feature/candles/... -v
```

### 全テスト実行

```bash
go test ./internal/feature/candles/... -v -race -cover
```

### テスト出力例

```
=== RUN   TestCandlesUsecase_GetCandles
=== RUN   TestCandlesUsecase_GetCandles/success:_all_parameters_specified
=== RUN   TestCandlesUsecase_GetCandles/success:_default_value_used_when_interval_is_empty
=== RUN   TestCandlesUsecase_GetCandles/success:_default_value_used_when_outputsize_is_0
=== RUN   TestCandlesUsecase_GetCandles/success:_default_value_used_when_outputsize_exceeds_max
=== RUN   TestCandlesUsecase_GetCandles/error:_repository_returns_error
--- PASS: TestCandlesUsecase_GetCandles (0.00s)
    --- PASS: TestCandlesUsecase_GetCandles/success:_all_parameters_specified (0.00s)
    --- PASS: TestCandlesUsecase_GetCandles/success:_default_value_used_when_interval_is_empty (0.00s)
    ...
```

## キャッシュ戦略

### キャッシュ設定

| 設定 | 値 | 説明 |
|------|-----|------|
| キー形式 | `candles:{symbol}:{interval}` | symbol+interval単位でキャッシュ（全データ最大5000件を保存） |
| 本番TTL | 7日 | `candles.DefaultCacheTTL`。ingest連続失敗時のセーフティネット、通常は日次ingestで上書き |
| デフォルトTTL | 5分 | コンストラクタにttl=0を渡した場合のフォールバック |
| 名前空間 | `candles` | 分離のためのキープレフィックス |

### キャッシュ動作

1. **読み取りパス（Find）**
   - Redisのキャッシュデータを確認
   - ヒット時: デシリアライズしたデータを返却
   - ミス時: PostgreSQLにクエリ、結果をキャッシュして返却
   - Redisエラー時: キャッシュをバイパスし、PostgreSQLに直接クエリ

2. **書き込みパス（UpsertBatch）**
   - まずPostgreSQLに書き込み
   - パターンマッチングで関連するキャッシュエントリを無効化
   - パターン: `candles:{symbol}:{interval}:*`

### グレースフルデグレード

キャッシュ層はグレースフルに障害を処理するよう設計されています:
- Redis利用不可時、リクエストはPostgreSQLから直接提供
- キャッシュ書き込みの失敗はログに記録されるがリクエストは失敗しない
- 破損したキャッシュエントリは自動的に削除

## 環境変数

| 変数 | 説明 | 必須 |
|------|------|------|
| `TWELVE_DATA_API_KEY` | TwelveDataマーケットデータのAPIキー | はい（取り込み用） |

**注:** RedisとPostgreSQLの接続設定は、このフィーチャー固有ではなくアプリケーションレベルで設定されます。

## 今後の拡張

- WebSocketによるリアルタイムデータストリーミング
- テクニカル指標の追加（SMA、EMA、RSIなど）
- ヒストリカルデータのバックフィル機能
- カスタムインターバルのサポート（5分、15分、1時間）
- CSV/Excel形式でのデータエクスポート
