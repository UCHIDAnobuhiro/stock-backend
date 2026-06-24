# Logodetection フィーチャー

## 概要

Logodetectionフィーチャーは、画像からのロゴ検出とAIによる企業分析を提供します。Google Cloud Vision APIを使用した画像認識と、Google Gemini APIを使用した企業分析レポート生成を統合し、ロゴ画像から企業情報を取得する一連のフローを実現します。

### 主な機能

- **ロゴ検出**: 画像アップロードによるロゴ検出（Google Cloud Vision API）
- **企業分析**: 企業名からAI生成の分析レポート作成（Google Gemini API）
- **マルチパートアップロード**: 最大10MBの画像ファイル対応
- **プロンプトテンプレート**: `go:embed`による外部Markdownファイルからのプロンプト管理
- **バリデーション**: 画像サイズ制限と企業名の文字パターン検証

## シーケンス図

### ロゴ検出フロー

```mermaid
sequenceDiagram
    participant Client
    participant Handler as Handler
    participant Usecase as usecase
    participant Vision as VisionLogoDetector
    participant API as Google Cloud Vision API

    Client->>Handler: POST /v1/logo/detect<br/>(multipart/form-data: image)
    Handler->>Handler: FormFile("image")で画像取得

    alt 画像フィールドなし
        Handler-->>Client: 400 Bad Request<br/>{"error":"image file is required"}
    end

    alt ファイルサイズ > 10MB
        Handler-->>Client: 413 Request Entity Too Large<br/>{"error":"image size exceeds the limit (10MB)"}
    end

    Handler->>Handler: io.ReadAll(io.LimitReader(f, 10MB+1))
    Handler->>Usecase: DetectLogos(ctx, imageData)
    Usecase->>Usecase: バリデーション（空チェック、サイズチェック）
    Usecase->>Vision: DetectLogos(ctx, imageData)
    Vision->>API: BatchAnnotateImages<br/>(LOGO_DETECTION)
    API-->>Vision: LogoAnnotations
    Vision-->>Usecase: []DetectedLogo
    Usecase-->>Handler: []DetectedLogo
    Handler->>Handler: []api.DetectedLogoResponseに変換
    Handler-->>Client: 200 OK<br/>[{name, confidence}, ...]

    alt Vision APIエラー
        API-->>Vision: Error
        Vision-->>Usecase: error
        Usecase-->>Handler: error
        Handler-->>Client: 502 Bad Gateway<br/>{"error":"logo detection failed"}
    end
```

### 企業分析フロー

```mermaid
sequenceDiagram
    participant Client
    participant Handler as Handler
    participant Usecase as usecase
    participant Gemini as GeminiAnalyzer
    participant API as Google Gemini API

    Client->>Handler: POST /v1/logo/analyze<br/>{"company_name":"任天堂"}
    Handler->>Handler: ShouldBindJSON(&req)

    alt バリデーションエラー
        Handler-->>Client: 400 Bad Request<br/>{"error":"invalid request"}
    end

    Handler->>Usecase: AnalyzeCompany(ctx, "任天堂")
    Usecase->>Usecase: バリデーション（空チェック、長さ、文字パターン）
    Usecase->>Usecase: プロンプト組み立て<br/>fmt.Sprintf(AnalysisPromptTemplate, companyName)
    Usecase->>Gemini: Analyze(ctx, prompt)
    Gemini->>API: GenerateContent<br/>(gemini-2.5-flash, prompt)
    API-->>Gemini: GenerateContentResponse
    Gemini-->>Usecase: summary string
    Usecase-->>Handler: *CompanyAnalysis
    Handler-->>Client: 200 OK<br/>{"company_name":"任天堂","summary":"..."}

    alt Gemini APIエラー
        API-->>Gemini: Error
        Gemini-->>Usecase: error
        Usecase-->>Handler: error
        Handler-->>Client: 502 Bad Gateway<br/>{"error":"company analysis failed"}
    end
```

## API仕様

### POST /v1/logo/detect

画像をアップロードしてロゴを検出します。JWT認証が必要です。

**認証方式**（優先順位順）:
1. `auth_token` Cookie（ブラウザクライアント）+ `X-CSRF-Token` ヘッダー（必須）
2. `Authorization: Bearer <token>` ヘッダー（APIクライアント・curl等）

**Content-Type**: `multipart/form-data`

**リクエストフィールド**

| フィールド | 型 | 必須 | 説明 |
|-----------|------|------|------|
| `image` | binary | はい | ロゴ検出対象の画像ファイル（最大10MB） |

**リクエスト例**
```http
POST /v1/logo/detect
Cookie: auth_token=eyJhbGc...
X-CSRF-Token: <csrf_token Cookieの値>
Content-Type: multipart/form-data; boundary=----FormBoundary

------FormBoundary
Content-Disposition: form-data; name="image"; filename="photo.jpg"
Content-Type: image/jpeg

<バイナリデータ>
------FormBoundary--
```

**レスポンス**

- **200 OK** - 成功
  ```json
  [
    {
      "name": "Apple",
      "confidence": 0.95
    },
    {
      "name": "Google",
      "confidence": 0.87
    }
  ]
  ```

- **400 Bad Request** - 画像フィールドなし
  ```json
  {
    "error": "image file is required"
  }
  ```

- **413 Request Entity Too Large** - 画像サイズ超過
  ```json
  {
    "error": "image size exceeds the limit (10MB)"
  }
  ```

- **502 Bad Gateway** - Vision APIエラー
  ```json
  {
    "error": "logo detection failed"
  }
  ```

### POST /v1/logo/analyze

企業名からAI分析サマリーを生成します。JWT認証が必要です。

**認証方式**（優先順位順）:
1. `auth_token` Cookie（ブラウザクライアント）+ `X-CSRF-Token` ヘッダー（必須）
2. `Authorization: Bearer <token>` ヘッダー（APIクライアント・curl等）

**Content-Type**: `application/json`

**リクエストボディ**

| フィールド | 型 | 必須 | 説明 |
|-----------|------|------|------|
| `company_name` | string | はい | 分析対象の企業名 |

**バリデーションルール**

| ルール | 詳細 |
|--------|------|
| 必須チェック | `company_name`は空文字不可 |
| 最大文字数 | 100文字（rune数） |
| 文字パターン | `[\p{L}\p{N} ・\-\.&,'']+`（英数字・日本語・スペース・中黒・ハイフン・ピリオド・アンパサンド・カンマ・アポストロフィ） |

**リクエスト例**
```http
POST /v1/logo/analyze
Cookie: auth_token=eyJhbGc...
X-CSRF-Token: <csrf_token Cookieの値>
Content-Type: application/json

{
  "company_name": "任天堂"
}
```

**レスポンス**

- **200 OK** - 成功
  ```json
  {
    "company_name": "任天堂",
    "summary": "# 任天堂 (7974)\n\n## 基本情報\n..."
  }
  ```

- **400 Bad Request** - 企業名なしまたはバリデーションエラー
  ```json
  {
    "error": "invalid request"
  }
  ```

- **502 Bad Gateway** - Gemini APIエラー
  ```json
  {
    "error": "company analysis failed"
  }
  ```

## 依存関係図

```mermaid
graph TB
    subgraph "Transport Layer"
        Handler[Handler<br/>logodetectionhttp]
    end

    subgraph "API Types (Generated)"
        APITypes[DetectedLogoResponse /<br/>CompanyAnalysisRequest /<br/>CompanyAnalysisResponse<br/>internal/api/types.gen.go]
    end

    subgraph "Usecase Layer"
        Usecase[usecase<br/>usecase.go]
        DetectorIF[LogoDetector Interface<br/>usecase.go]
        AnalyzerIF[CompanyAnalyzer Interface<br/>usecase.go]
    end

    subgraph "Domain Layer"
        Logo[DetectedLogo Entity<br/>logo.go]
        Analysis[CompanyAnalysis Entity<br/>analysis.go]
    end

    subgraph "Adapters Layer"
        Vision[VisionLogoDetector<br/>vision]
        Gemini[GeminiAnalyzer<br/>gemini]
    end

    subgraph "External Dependencies"
        VisionAPI[Google Cloud Vision API]
        GeminiAPI[Google Gemini API]
    end

    Handler -->|depends on| Usecase
    Handler -->|uses| APITypes
    Usecase -->|defines| DetectorIF
    Usecase -->|defines| AnalyzerIF
    Usecase -->|uses| Logo
    Usecase -->|uses| Analysis
    Vision -.->|implements| DetectorIF
    Gemini -.->|implements| AnalyzerIF
    Vision -->|calls| VisionAPI
    Gemini -->|calls| GeminiAPI

    style Handler fill:#e1f5ff
    style Usecase fill:#fff4e1
    style DetectorIF fill:#fff4e1
    style AnalyzerIF fill:#fff4e1
    style Logo fill:#e8f5e9
    style Analysis fill:#e8f5e9
    style Vision fill:#f3e5f5
    style Gemini fill:#f3e5f5
    style VisionAPI fill:#ffebee
    style GeminiAPI fill:#ffebee
```

### 依存関係の説明

#### トランスポート層（[logodetectionhttp/handler.go](../../internal/feature/logodetection/logodetectionhttp/handler.go)）
- **Handler**: HTTPリクエストを処理し、usecaseを呼び出す
- `Usecase`インターフェースを定義（Goの「インターフェースは利用者が定義する」慣例に従う）
- API型（`internal/api/types.gen.go`）: `api.DetectedLogoResponse`, `api.CompanyAnalysisRequest`, `api.CompanyAnalysisResponse`, `api.ErrorResponse`を使用

#### ユースケース層（[usecase.go](../../internal/feature/logodetection/usecase.go)）
- **usecase**: ロゴ検出と企業分析のビジネスロジックを実装
- `LogoDetector`インターフェース（画像 → 検出ロゴ一覧）を定義
- `CompanyAnalyzer`インターフェース（プロンプト → 分析テキスト）を定義
- バリデーション: 画像サイズ（最大10MB）、企業名（空チェック、最大100文字、正規表現パターン）
- 埋め込みMarkdownテンプレートからプロンプトを組み立て（`go:embed prompts/analysis.md`, `prompts/format.md`）
- 定数: `MaxImageSize`（10MB）、`MaxCompanyNameLength`（100）

#### ドメイン層
- **DetectedLogo**（[logo.go](../../internal/feature/logodetection/logo.go)）: `Name`（検出された企業名）、`Confidence`（信頼度スコア 0.0〜1.0）
- **CompanyAnalysis**（[analysis.go](../../internal/feature/logodetection/analysis.go)）: `CompanyName`（分析対象の企業名）、`Summary`（AI生成の分析サマリー）

#### アダプター層 - Vision（[vision/client.go](../../internal/feature/logodetection/vision/client.go)）
- **VisionLogoDetector**: `LogoDetector`インターフェースを実装
- Google Cloud Vision API v2（`cloud.google.com/go/vision/v2/apiv1`）を使用
- ADC（Application Default Credentials）認証
- `LOGO_DETECTION`フィーチャーによる`BatchAnnotateImagesRequest`
- コンパイル時インターフェース検証: `var _ usecase.LogoDetector = (*VisionLogoDetector)(nil)`

#### アダプター層 - Gemini（[gemini/client.go](../../internal/feature/logodetection/gemini/client.go)）
- **GeminiAnalyzer**: `CompanyAnalyzer`インターフェースを実装
- Google GenAIクライアント（`google.golang.org/genai`）を使用
- デフォルトモデル: `gemini-2.5-flash`
- Vertex AI経由の利用をサポート（環境変数で設定）
- コンパイル時インターフェース検証: `var _ usecase.CompanyAnalyzer = (*GeminiAnalyzer)(nil)`

### アーキテクチャの特徴

1. **クリーンアーキテクチャ**: ドメイン層がインフラストラクチャから独立
2. **依存性逆転**: Usecaseがインターフェース（LogoDetector, CompanyAnalyzer）を定義し、adaptersが実装
3. **インターフェース所有権**: インターフェースは利用される場所で定義（Goのベストプラクティス） — usecase層（LogoDetector, CompanyAnalyzer）とhandler層（Usecase）の両方で適用
4. **プロンプトテンプレート管理**: 外部Markdownファイルを`go:embed`でコンパイル時に埋め込み
5. **コンパイル時インターフェース検証**: `var _ Interface = (*Impl)(nil)`パターンで型安全性を保証
6. **データベース非依存**: 本フィーチャーは外部AI APIのみと通信し、データベースを使用しない

## ディレクトリ構成

```text
logodetection/
├── README.md            # 本ファイル
├── logo.go              # DetectedLogoエンティティ（ロゴ名、信頼度）
├── analysis.go          # CompanyAnalysisエンティティ（企業名、サマリー）
├── usecase.go           # ビジネスロジック + LogoDetector / CompanyAnalyzerインターフェース
├── usecase_test.go      # ユースケーステスト
├── prompts/
│   ├── analysis.md      # 企業分析プロンプト（go:embedで埋め込み）
│   └── format.md        # 出力フォーマットテンプレート（go:embedで埋め込み）
├── vision/
│   └── client.go        # Google Cloud Vision APIクライアント（LogoDetector実装）
├── gemini/
│   └── client.go        # Google Gemini APIクライアント（CompanyAnalyzer実装）
└── logodetectionhttp/
    ├── handler.go       # HTTPハンドラー + Usecaseインターフェース
    └── handler_test.go  # ハンドラーテスト
```

## テスト

Logodetectionフィーチャーのすべてのテストは、一貫性と保守性のために**テーブル駆動テストパターン**に従います。

### テスト構造とパターン

#### 全テスト共通のパターン

1. **テーブル駆動テスト**: 全テスト関数は構造体フィールドを持つ`testCases`/`tests`スライスを使用:
   - `name`: テストケースの説明（例: `"success: logos detected"`, `"error: empty image data"`）
   - テストタイプ固有の追加フィールド（モック関数、期待値など）

2. **モック実装**: 関数フィールドを持つモック構造体:
   ```go
   type mockLogoDetector struct {
       DetectLogosFunc  func(ctx context.Context, imageData []byte) ([]entity.DetectedLogo, error)
       DetectLogosCalls int
   }
   ```

3. **ヘルパー関数**: テストファイルごとにユーティリティ関数を定義:
   - ユースケーステスト: `contains()`, `containsSubstring()`（エラーメッセージの部分一致検証）
   - ハンドラーテスト: `createMultipartRequest()`（マルチパートリクエスト生成）

#### ユースケーステスト（[usecase_test.go](../../internal/feature/logodetection/usecase_test.go)）

ビジネスロジックを分離してテストするために**モックアダプター**を使用します。

**テストケース構造:**
```go
testCases := []struct {
    name          string
    imageData     []byte
    mockFunc      func(ctx context.Context, imageData []byte) ([]entity.DetectedLogo, error)
    expectedLogos []entity.DetectedLogo
    expectedErr   string
}{/* ... */}
```

**主な特徴:**
- `mockLogoDetector`と`mockCompanyAnalyzer`によるモック実装
- 呼び出し回数カウンター（`DetectLogosCalls`, `AnalyzeCalls`）
- バリデーションテスト（空画像、サイズ超過、空企業名）
- APIエラーの伝播テスト

**実行コマンド:**
```bash
go test ./internal/feature/logodetection/ -v
```

#### ハンドラーテスト（[logodetectionhttp/handler_test.go](../../internal/feature/logodetection/logodetectionhttp/handler_test.go)）

HTTPリクエスト/レスポンス処理をテストするために**モックユースケース**を使用します。

**テストケース構造:**
```go
tests := []struct {
    name           string
    setupRequest   func(t *testing.T) *http.Request
    mockFunc       func(ctx context.Context, imageData []byte) ([]entity.DetectedLogo, error)
    expectedStatus int
    expectedBody   string
}{/* ... */}
```

**主な特徴:**
- `httptest.NewRecorder()`によるHTTPレスポンス記録
- `assert.JSONEq`によるJSONレスポンスボディの照合
- マルチパートリクエストのヘルパー関数
- HTTPステータスコードの検証

**実行コマンド:**
```bash
go test ./internal/feature/logodetection/logodetectionhttp/... -v
```

**注:** 本フィーチャーはデータベースを使用しないため、リポジトリテスト（インメモリSQLite等）は存在しません。

### 全テスト実行

```bash
go test ./internal/feature/logodetection/... -v -race -cover
```

### テスト出力例

```text
=== RUN   TestLogoDetectionUsecase_DetectLogos
=== RUN   TestLogoDetectionUsecase_DetectLogos/success:_logos_detected
=== RUN   TestLogoDetectionUsecase_DetectLogos/error:_empty_image_data
=== RUN   TestLogoDetectionUsecase_DetectLogos/error:_image_too_large
=== RUN   TestLogoDetectionUsecase_DetectLogos/error:_api_returns_error
--- PASS: TestLogoDetectionUsecase_DetectLogos (0.00s)
=== RUN   TestLogoDetectionUsecase_AnalyzeCompany
=== RUN   TestLogoDetectionUsecase_AnalyzeCompany/success:_analysis_generated
=== RUN   TestLogoDetectionUsecase_AnalyzeCompany/error:_empty_company_name
=== RUN   TestLogoDetectionUsecase_AnalyzeCompany/error:_api_returns_error
--- PASS: TestLogoDetectionUsecase_AnalyzeCompany (0.00s)
=== RUN   TestLogoDetectionHandler_DetectLogos
=== RUN   TestLogoDetectionHandler_DetectLogos/success:_logos_detected
=== RUN   TestLogoDetectionHandler_DetectLogos/error:_no_image_field
=== RUN   TestLogoDetectionHandler_DetectLogos/error:_usecase_returns_error
--- PASS: TestLogoDetectionHandler_DetectLogos (0.00s)
=== RUN   TestLogoDetectionHandler_AnalyzeCompany
=== RUN   TestLogoDetectionHandler_AnalyzeCompany/success:_analysis_generated
=== RUN   TestLogoDetectionHandler_AnalyzeCompany/error:_empty_request_body
=== RUN   TestLogoDetectionHandler_AnalyzeCompany/error:_invalid_json
=== RUN   TestLogoDetectionHandler_AnalyzeCompany/error:_usecase_returns_error
--- PASS: TestLogoDetectionHandler_AnalyzeCompany (0.00s)
PASS
```

## プロンプトテンプレート

本フィーチャーでは、企業分析のプロンプトを`go:embed`ディレクティブを使用して外部Markdownファイルから管理しています。

### テンプレート構成

| ファイル | 説明 |
|--------|------|
| `prompts/analysis.md` | 分析指示文（`%s`プレースホルダーで企業名を挿入） |
| `prompts/format.md` | 出力フォーマット定義（Markdown構造） |

### プロンプト組み立て

```go
//go:embed prompts/analysis.md
var analysisPrompt string

//go:embed prompts/format.md
var analysisFormat string

var AnalysisPromptTemplate = analysisPrompt + "\n## 出力フォーマット\n" + analysisFormat
```

ランタイムでは`fmt.Sprintf(AnalysisPromptTemplate, companyName)`により企業名が挿入され、最終的なプロンプトが生成されます。

### 出力フォーマット

生成されるレポートは以下のセクションで構成されます:

1. **基本情報**: セクター・業種、本社所在地、設立年（テーブル形式）
2. **ビジネス理解**: 事業概要、主力商品・サービス、主要競合
3. **投資関連**: 時価総額、主なリスク要因

## 環境変数

| 変数 | 説明 | 必須 |
|------|------|------|
| `GOOGLE_GENAI_USE_VERTEXAI` | Vertex AI経由でGemini APIを使用するフラグ（`true`に設定） | はい（Vertex AI使用時） |
| `GOOGLE_CLOUD_PROJECT` | Google CloudプロジェクトID | はい（Vertex AI使用時） |
| `GOOGLE_CLOUD_LOCATION` | Google Cloudリージョン（例: `asia-northeast1`） | はい（Vertex AI使用時） |
| `GOOGLE_APPLICATION_CREDENTIALS` | サービスアカウントキーのパス（ADC） | はい（Docker環境） |

**注:** Vision APIもGemini APIもADC（Application Default Credentials）を使用して認証します。個別のAPIキーは不要です。

## 今後の拡張

- バッチ画像処理（複数画像の一括ロゴ検出）
- 検出ロゴの履歴保存（データベース永続化）
- ロゴ検出結果から企業分析への自動連携フロー
- 分析結果のキャッシュ（Redis）
- 対応画像フォーマットの明示的バリデーション（JPEG, PNG等）
- 分析プロンプトのバージョニングと管理
