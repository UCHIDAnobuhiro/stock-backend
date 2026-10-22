# ADR-0011: Cloud SQL private IP接続への段階移行

| 項目 | 内容 |
| --- | --- |
| ステータス | Proposed |
| 日付 | 2026-10-22 |

---

## コンテキスト

現行の本番API・batch・migrateはCloud Run組み込みCloud SQL接続のUnixソケットを使用し、Cloud SQLの公開IPが有効である。専用VPCからCloud SQLのprivate IPへ移行し、検証後に公開IPを停止したい。既存のRedis通信とDB接続予算、各runtimeの`*sql.DB`契約を維持する必要がある。

## 決定

Cloud SQL Private Service Accessでprivate IPを追加し、Go Cloud SQL Connectorでprivate IPを明示して接続する。Terraformでネットワーク追加、runtime切替、公開IP停止を独立した段階として扱い、API・batch・migrateすべての疎通を確認してから次段階へ進む。

## 理由

既存の公開IP/ソケット経路を移行中の復旧経路として維持できる。Go ConnectorはIAMによるインスタンス接続認証とTLSを提供し、既存のPostgreSQLユーザー/パスワードをそのまま使用できる。`*sql.DB`を保持し、DB.CloseでDialerも閉じる。SQL専用network tagにprivate IPのTCP 3307だけを許可し、migrateにはRedisの権限を付けない。

## 代替案

| 代替案 | 不採用の理由 |
| --- | --- |
| private IPへ直接TCP 5432 | コネクタのIAM認証と証明書管理を別途実装する必要がある |
| 初回から公開IPを停止 | すべてのruntimeとbatch lock専用プールを検証する前に復旧経路を失う |

## 影響

### ポジティブな影響

- runtimeのDB通信を専用VPCのprivate IPへ限定できる。
- 既存のsocketとローカルTCP接続は移行期間中も使える。

### ネガティブな影響・トレードオフ

- PSA予約アドレスとService Networking peeringが増える。private networkはCloud SQLに設定後に除去できない。
- private IP追加・公開IP停止はCloud SQL更新となり、一時的な接続断の可能性がある。公開IP再有効化は即時ロールバックとは限らない。
- 実環境の既存peeringとアドレス競合はローカルmock testでは証明できず、本番plan前に確認する。

## 関連ADR

- [ADR-0003](0003-postgresql-の採用.md): PostgreSQLの採用
- [ADR-0006](0006-db操作をgormからsqlcとgooseへ移行.md): DB操作の構成
