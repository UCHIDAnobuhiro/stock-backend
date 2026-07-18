# ADR-0007: Web フレームワークを Gin から net/http + chi へ移行

| 項目       | 内容       |
| ---------- | ---------- |
| ステータス | Accepted   |
| 日付       | 2026-06-09 |

---

## コンテキスト

本 API サーバーは当初 [Gin](https://github.com/gin-gonic/gin) を Web フレームワークとして採用していた。
一方で本プロジェクトは「Go 標準ライブラリ寄りの構成」を方針としており（フィーチャーごとの
垂直スライス、DTO を置かず `internal/api` 生成型を直接利用、リポジトリインターフェースは利用者側で定義など）、
Web 層だけが Gin という独自の `*gin.Context` 抽象に依存している点が方針との不整合になっていた。

Gin 依存により以下の課題があった。

- ハンドラー/ミドルウェアが `*gin.Context` に結合し、標準の `http.Handler` 互換でない
- `c.Set` / `c.MustGet` による文字列キーの型なしコンテキスト共有
- `SetSameSite` を Cookie ごとに呼び直す必要があるなど Gin 固有の癖
- アクセスログにおける `c.Errors`（実際には未使用のデッドコード）への依存
- フレームワーク本体と `gin-contrib/cors` という追加の外部依存

Go 1.22 以降、標準 `net/http` の `ServeMux` がパスパラメータとメソッドルーティングに対応したが、
ルートグループ・ミドルウェアチェーン・サブルーターといった機能は薄い。これらを補いつつ
標準 `http.Handler` 互換を保てるルーターとして [chi](https://github.com/go-chi/chi) を評価した。

## 決定

Web フレームワークを Gin から **Go 標準ライブラリ `net/http` + ルーターに `chi`（`go-chi/chi/v5`）** へ移行する。
CORS は `go-chi/cors` を使用する。

- ハンドラーは `func(http.ResponseWriter, *http.Request)`、ミドルウェアは `func(http.Handler) http.Handler` 形式に統一する
- 認証情報（ユーザーID・認証方式）は文字列キーではなく型付き `context.Context` キーで受け渡す
- リクエストボディの検証は `go-playground/validator` を直接利用し、既存の `binding` タグを流用する
- 共通処理（JSON 応答・ボディ検証・ClientIP）は `internal/transport/httpx` に集約する

## 理由

- **標準ライブラリへの収束**: プロジェクト方針（標準寄り構成）と整合し、Web 層も `http.Handler` という
  Go の標準抽象に統一できる。テストやミドルウェアの相互運用性が上がる。
- **移行リスクの局所化**: クリーンアーキテクチャにより影響範囲は `transport/` と `*http/` 層に限定され、
  フィーチャーコア・usecase・repository・sqlc・`internal/api` 型には波及しない。
- **chi の機能適合性**: chi は `http.Handler` 互換のまま、ルートグループ・パスパラメータ・サブルーター・
  `middleware.WrapResponseWriter`（アクセスログのステータス/サイズ取得）を提供し、Gin からの移行が小さい。
- **依存の見通し改善**: `SetSameSite` の都度呼び出しといった Gin 固有の癖が `http.SetCookie` で解消され、
  Cookie 設定が素直になる。デッドコードだった `c.Errors` 連携も除去できる。
- **挙動の互換性**: 既存のハンドラーテスト（テーブル駆動）を回帰テストとして維持し、
  ステータスコード・レスポンスボディ・Cookie 属性が移行前後で変わらないことを担保した。

## 代替案

| 代替案                                   | 不採用の理由                                                                                     |
| ---------------------------------------- | ------------------------------------------------------------------------------------------------ |
| Gin を継続利用する                       | プロジェクト方針（標準寄り）との不整合が残り、`*gin.Context` への結合と追加依存が解消されない     |
| 生の `net/http`（`ServeMux`）のみ        | ルートグループ・ミドルウェアチェーン・ResponseWriter ラッパーを自前実装する必要があり工数が増える |
| echo / fiber 等の他フレームワークへ移行  | 別フレームワーク固有の抽象に再び結合するだけで、標準ライブラリへ寄せる目的を達成できない          |

## 影響

### ポジティブな影響

- Web 層が標準 `http.Handler` 互換になり、ミドルウェア・テストの相互運用性が向上
- 認証情報の受け渡しが型付き context キーになり、文字列キーの取り違えリスクが低減
- Cookie 設定・アクセスログのコードが簡素化（Gin 固有の癖とデッドコードを除去）
- `gin-gonic/gin` と `gin-contrib/cors` を依存から削除
- 併せて `http.Server` ベースの起動に変更し、SIGINT/SIGTERM でのグレースフルシャットダウンを追加（Cloud Run 向け）

### ネガティブな影響・トレードオフ

- 入力バリデーションが `ShouldBindJSON` 同梱から `validator` の明示利用へ変わるため、
  検証の呼び出し漏れがないことの担保が必要（共通ヘルパー `httpx.DecodeAndValidate` に集約して対応）
- `c.ClientIP()` 相当の挙動（`RemoteAddr` のみ信頼）を自前ヘルパーで再現する必要がある。
  chi の `middleware.RealIP` は X-Forwarded-For を信頼するため**使用しない**
- Gin から chi へという別の外部ルーター依存は残る（標準ライブラリのみへの完全移行ではない）

## 関連ADR

- [ADR-0002](0002-フィーチャーベースのクリーンアーキテクチャ採用.md): 影響範囲を Web 層に限定できた前提となる構成
- [ADR-0006](0006-db操作をgormからsqlcとgooseへ移行.md): 同様に標準ライブラリ寄りへ依存を見直した先行事例
