---
name: schema-doc-sync
description: db/migrations/ の変更後に scripts/regen-schema-docs.sh を実行して docs/schema/（tbls生成のER図・テーブル定義書）を再生成し、差分を確認してコミットに含める。「migrationを追加したのでスキーマdocを更新して」「スキーマドキュメントを再生成して」と依頼された際、またはdb/migrations配下を変更するタスクの一部として使用。
---

db/migrations/ の変更を docs/schema/ に反映してください。

## トリガー条件

以下のいずれかに該当する場合に実行する：

1. **明示的な依頼**: 「スキーマdocを更新して」「ER図を再生成して」等
2. **自動トリガー**: 自分自身が `db/migrations/*.sql` を新規追加・変更するタスク（マイグレーションを伴う機能実装、`/issue` の実装フェーズ等）を行った場合、明示的な依頼がなくてもコミット前に本スキルを自動的に呼び出す

まず `git status` / `git diff --stat -- db/migrations/` で `db/migrations/` 配下に変更があるか確認する。なければ本スキルは不要である旨を伝えて終了する。

## 手順

### Step 1: 前提確認

1. `docker info` でDockerデーモンが起動しているか確認する
2. 未起動なら「失敗時のハンドリング」に従いユーザーに起動を促して中断する
3. リポジトリルートで実行する（スクリプト自身が `git rev-parse --show-toplevel` で移動するため、サブディレクトリからの実行でも問題ない）

### Step 2: スクリプト実行

```bash
./scripts/regen-schema-docs.sh
```

- 使い捨てのPostgreSQLコンテナ（`stock-tbls-regen-db`）を起動し、`go run ./cmd/migrate up` でマイグレーションを一括適用してから `tbls doc --force` で `docs/schema/` を再生成する
- 続けて `tbls diff` で生成結果とDBスキーマの乖離ゼロを検証する（CIの `schema-doc-check` ジョブと同じ検証）
- 開発用DB（`stock-postgres` / `stock_postgres_data` ボリューム）には一切触れない
- 終了時（正常・異常問わず）に `trap cleanup EXIT` でコンテナ・ネットワークが自動削除される

### Step 3: 生成差分の確認

```bash
git diff --stat docs/schema/
git diff docs/schema/
```

- 差分が空なら「スキーマdocsに変更はありませんでした」と報告して終了する
- 差分があればStep 4へ

### Step 4: 意図しない変更でないかの確認

- 変更されたテーブル・カラムが、今回のマイグレーションで意図した対象と一致しているか確認する
- 制約（PK/FK）の並び順だけが変わっている等、意図しない箇所に差分が出た場合は `TBLS_VERSION` が以下3箇所で揃っているか確認する（ズレていた場合は原因調査を優先し、勝手に値を書き換えない）：
  - `scripts/regen-schema-docs.sh` の `TBLS_VERSION`
  - `.github/workflows/ci.yaml`（`schema-doc-check` ジョブの `k1LoW/setup-tbls`）
  - `docker/docker-compose.yml`（`tbls` サービスの `ghcr.io/k1low/tbls` イメージタグ）
- 今回のマイグレーションと無関係な既存テーブルの記述が変わっている場合はユーザーに報告し、コミットに含めてよいか確認する

### Step 5: 通常のコミットフローへ

問題がなければ、マイグレーションファイルと `docs/schema/` の差分を**同じコミット**に含める。`/commit` スキルの手順・メッセージルールに従う。

```bash
git add db/migrations/<新規ファイル> docs/schema/
```

## 失敗時のハンドリング

- **Docker未起動**（`docker info` がエラー）: 自動起動を試みず、「Docker Desktopを起動してから再実行してください」と伝えて中断する
- **ポート衝突**（デフォルト55432が使用中）: `REGEN_DB_PORT=<空きポート> ./scripts/regen-schema-docs.sh` で再実行する
- **`go run ./cmd/migrate up` 失敗**: マイグレーションSQL自体の構文エラーの可能性が高い。該当ファイルを確認・修正してから再実行する（スクリプトの問題ではない）
- **`tbls diff` が非ゼロ終了**: 再生成直後にもかかわらず乖離が検出された場合はtbls自体の非決定動作の疑いがある。握りつぶさずユーザーに報告する
- 上記いずれの失敗でも `trap cleanup EXIT` によりコンテナ・ネットワークは自動削除されるため、手動クリーンアップは基本不要

## 派生パターン

- **「スクリプトだけ実行して、コミットはしないで」**: Step 2〜4のみ実行し、Step 5はスキップする
- **「複数のmigrationをまとめて追加した後にまとめて実行して」**: マイグレーションファイルごとに毎回実行する必要はない。一連の変更が揃った時点で1回実行すればよい

## 注意事項

- 起動中の開発用DB（`stock-postgres`）に対して `tbls doc` を**直接実行しない**。制約の表示順序が非決定になり、CIの `Schema Doc Drift` チェックが意図せず失敗する原因になる（本スクリプトが使い捨てDBを使うのはこれを避けるため）
- 本スキルはコミットまでは行うが、PR作成は行わない（別途 `/pull-request` スキルに委ねる）
