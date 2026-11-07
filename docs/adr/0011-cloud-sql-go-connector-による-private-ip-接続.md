# ADR-0011: Cloud SQL Go Connector による private IP 接続

| 項目       | 内容       |
| ---------- | ---------- |
| ステータス | Proposed   |
| 日付       | 2026-11-07 |

---

## コンテキスト

本番の Cloud SQL には public IP があり、Cloud Run 組み込みの Cloud SQL 接続（`/cloudsql/<instance>` の Unix ソケット）で接続していた（[ADR-0003](0003-postgresql-の採用.md)）。

新しい GCP プロジェクトへ基盤を作り直すのに合わせて、インフラ側（stock-infra）では Cloud SQL を public IP なしの private IP 専用にし、Private Service Access で専用 VPC へ接続する構成に変える。Cloud Run の API・batch・migrate はすでに Direct VPC egress で専用 VPC を使っている。private IP 専用にすると public IP 前提の組み込み接続は前提から外れるため、backend 側の接続方法を決め直す必要がある。

## 決定

`INSTANCE_CONNECTION_NAME` が設定されている場合、backend は **Cloud SQL Go Connector**（`cloud.google.com/go/cloudsqlconn`）の dialer を pgx の `DialFunc` に設定して接続する。接続先の IP 種別は `DB_IP_TYPE`（`PUBLIC` / `PRIVATE`。未指定は `PUBLIC`）で選び、本番では `PRIVATE` を使う。ローカル開発は従来どおり `DB_HOST` / `DB_PORT` への TCP 接続とする。

## 理由

**暗号化と認可を Connector に任せられる**
Connector は Cloud SQL Admin API から取得した一時証明書を使い、サーバー側プロキシ（3307）へ mTLS で接続する。サーバー証明書の検証も自動で行われ、CA 証明書の配布やローテーションを管理しなくてよい。接続には `roles/cloudsql.client` による IAM 認可も必要になるため、ネットワーク到達性と DB パスワードだけに頼らない。

**変更範囲が小さい**
DB 接続は `internal/infra/db.OpenSQL` に集約されており、API・batch・migrate（goose）はいずれも `*sql.DB` を受け取るだけである。そのため、opener を差し替えるだけで全エントリーポイントに反映でき、`OpenSQL` のシグネチャも変わらない。

**将来の IAM DB 認証へ移行しやすい**
`cloudsqlconn.WithIAMAuthN()` を追加すれば、DB パスワードを使わない IAM DB 認証へ移行できる（stock-infra issue #39 で別途検討）。

## 代替案

| 代替案 | 不採用の理由 |
| ------ | ------------ |
| private IP へ TCP で直接接続（`DB_SSLMODE=require` + Cloud SQL 側 `ENCRYPTED_ONLY`） | backend の変更が不要で最も手軽。ただし `require` はサーバー証明書を検証せず、`verify-ca` には CA 証明書の配布が必要になる。認可もネットワークとパスワードだけになる |
| Cloud SQL Auth Proxy を sidecar で動かす | アプリの変更はほぼ不要だが、全 Cloud Run Service / Job にコンテナが1つ増え、リソースと設定の管理が増える |
| Cloud Run 組み込みの Cloud SQL 接続（Unix ソケット）を維持する | public IP を前提としており、Cloud SQL を private IP 専用にするという目的に合わない |

## 影響

### ポジティブな影響

- Cloud SQL の public IP をなくせる
- TLS の設定漏れによる平文接続を構造的に防げる
- IAM DB 認証へオプション追加で移行できる

### ネガティブな影響・トレードオフ

- `cloud.google.com/go/cloudsqlconn` とその推移的依存が増える
- 接続時に Cloud SQL Admin API を呼ぶため、ランタイム SA に `roles/cloudsql.client` が必要で、Admin API の可用性とクォータに依存する
- Cloud Run は CPU idle 時にバックグラウンドの証明書更新が止まるため、`WithLazyRefresh` で接続時に更新する。証明書の期限が切れた直後の接続は、更新を待つ分だけ遅くなる
- Connector の組み込みメトリクス（BETA）は、ランタイム SA に `roles/monitoring.metricWriter` が必要になるため無効にしている
- 本番で public IP 前提の Unix ソケット接続を使うイメージには戻せない

## 関連ADR

- [ADR-0003](0003-postgresql-の採用.md): 本番の Unix ソケット接続を、提案中の本 ADR で置き換える
