# Architecture Decision Records (ADR)

このディレクトリは、stock_backend プロジェクトにおける重要なアーキテクチャ上の決定を記録します。

## ADRとは

ADR（アーキテクチャ決定記録）は、ソフトウェアアーキテクチャ上の重要な決定と、その背景・理由・影響を記録したドキュメントです。チームが「なぜそう作ったか」を将来にわたって追跡できるようにするために使用します。

## ファイル命名規則

```text
NNNN-kebab-case-title.md
```

- `NNNN`: ゼロ埋め4桁の連番（例: `0001`, `0042`）
- タイトル部分: 日本語または英語のケバブケース（例: `go-言語の採用`, `database-selection`）

## ステータス定義

| ステータス             | 意味                                      |
| ---------------------- | ----------------------------------------- |
| Proposed               | 提案中。まだ承認されていない               |
| Accepted               | 承認済み。現在有効な決定                   |
| Deprecated             | 廃止。理由はあるが後継ADRなし              |
| Superseded by ADR-XXXX | 後継ADRに置き換えられた                    |

## 新規ADR作成

Claude Code の `/adr` スキルを使用してください：

```bash
/adr <決定トピック>
```

例:

```bash
/adr データベースにPostgreSQLを採用する理由
/adr RedisをキャッシュレイヤーとしてCI環境でモックする方針
```

スキルが自動的に番号付けを行い、テンプレートに従ったADRを生成します。

## 運用ルール

- ADR は一度 `Accepted` になったら内容を書き換えない（不変性の原則）
- 決定を変更する場合は新しいADRを作成し、元のADRのステータスを `Superseded by ADR-XXXX` に更新する
- ステータスは基本的に `Proposed` で作成し、レビュー後に `Accepted` へ変更する

## 一覧

| 番号                                                                                   | タイトル                                 | ステータス |
| -------------------------------------------------------------------------------------- | ---------------------------------------- | ---------- |
| [ADR-0001](0001-go-言語の採用.md)                                                     | Go言語の採用                             | Accepted   |
| [ADR-0002](0002-フィーチャーベースのクリーンアーキテクチャ採用.md)                    | フィーチャーベースのクリーンアーキテクチャ採用 | Accepted   |
| [ADR-0003](0003-postgresql-の採用.md)                                                 | PostgreSQLの採用                         | Accepted   |
| [ADR-0004](0004-tbls-による-er-図自動生成.md)                                         | tbls による ER 図自動生成                | Accepted   |
| [ADR-0005](0005-twelvedata-タイムスタンプを市場ローカル時刻として保存する.md)         | TwelveData タイムスタンプを市場ローカル時刻として解釈・集計する | Accepted   |
| [ADR-0006](0006-db操作をgormからsqlcとgooseへ移行.md)                                 | DB 操作を GORM から sqlc と goose へ移行 | Accepted   |
| [ADR-0007](0007-webフレームワークをginからnet-httpとchiへ移行.md)                     | Web フレームワークを Gin から net/http + chi へ移行 | Accepted   |
| [ADR-0008](0008-レートリミット障害時のfail-open-fail-closed方針.md)                   | レートリミット障害時の fail-open / fail-closed 方針 | Accepted   |
