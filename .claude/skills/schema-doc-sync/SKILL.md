---
name: schema-doc-sync
description: db/migrations/ の変更時やスキーマ文書の再生成依頼時に scripts/regen-schema-docs.sh で docs/schema/ を更新・検証する。commitは依頼に含まれる場合だけ行う。
---

migration と `docs/schema/`（tbls生成のER図・テーブル定義書）を同期する。

## 実行条件

- 明示的な再生成依頼では、migrationの未コミット差分がなくても実行する。
- 自分が `db/migrations/` を追加・変更・削除した場合は、一連の変更が揃った時点で実行する。ファイルごとの実行は不要。
- 調査・レビューだけでは再生成しない。差分確認にはステージ済み・未ステージ・未追跡を含める。

## 手順

1. `git status --short -- db/migrations docs/schema` とステージ済み・未ステージの差分を確認する。既存の手動編集があれば内容を把握し、生成によって失われる変更を保存・比較できるようにする。安全に保持できない場合だけ扱いを確認する。
2. `scripts/regen-schema-docs.sh` を読み、`docker info` でDockerの利用可否を確認する。固定名 `stock-tbls-regen-db` / `stock-tbls-regen-net` を使うため、同スクリプトの別実行と並行させない。既存コンテナがあれば使用中か確認し、実行中の別作業を削除しない。
3. リポジトリルートから `./scripts/regen-schema-docs.sh` を実行する。スクリプトは使い捨てPostgreSQLへ全migrationを適用し、`tbls doc --force` と `tbls diff` を実行する。開発用DBへ `tbls doc` を直接実行しない。
4. 終了コードと、`git status --short -- docs/schema`、`git diff -- docs/schema`、`git diff --cached -- docs/schema` を確認する。新規生成ファイルも読み、今回の生成による差分と元からあった変更を区別する。
5. 変更対象がmigrationと一致することを確認する。差分が空でも、再生成・検証が成功したことを報告する。失敗した生成を「変更なし」と扱わない。

## 予期しない差分・失敗

- PK/FKの順序などの差分は、スクリプトの `TBLS_VERSION`、`.github/workflows/ci.yaml` の `schema-doc-check`、`docker/docker-compose.yml` のtblsタグを照合する。理由なくバージョンを変更しない。
- 無関係に見える差分も原因を調査する。同じ生成手順による妥当な更新は理由を説明し、別作業や意味の変化が混ざる場合だけ扱いを確認する。
- Docker未起動なら起動が必要と報告する。ポート衝突は `REGEN_DB_PORT=<空きポート>` で再実行できるが、固定コンテナ名の衝突はポート変更では解消しない。
- migration適用や `tbls diff` が失敗したらログから原因を確認する。SQL不良、接続、ツール実行環境、生成差分を区別し、SQLやtblsの非決定性と即断しない。
- 終了時のtrapでコンテナ・ネットワークを片付けるが、強制終了やDocker障害では残る可能性がある。手動削除前に今回の実行の残骸か確認する。

## 完了とcommit

再生成結果、検証結果、変更ファイルを報告する。再生成だけの依頼ではstage・commit・pushしない。
commitまで依頼されている場合は、利用可能なら `commit` スキルに従い、対象のmigrationと生成差分を同じ論理単位に含める。ディレクトリ全体を一括stageせず、既存の別作業を除外する。
