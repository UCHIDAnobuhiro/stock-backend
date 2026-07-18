# ADR-0005: TwelveData タイムスタンプを市場ローカル時刻として解釈・集計する

| 項目       | 内容       |
| ---------- | ---------- |
| ステータス | Accepted   |
| 日付       | 2026-04-28 |

---

## コンテキスト

`internal/feature/candles/adapters/twelvedata/repository.go` では TwelveData API レスポンスの `datetime` を `time.Parse("2006-01-02 15:04:05", v.Datetime)` でパースしていた。Go の `time.Parse` は TZ 指定がない場合 UTC として解釈するが、TwelveData は実際には取引所ローカル時刻（米国株は ET、日本株は JST 等）でタイムスタンプを返す。

これにより以下の不整合が発生していた:

- DB 上の `time` 値が「市場ローカル日時を UTC とラベル付けした値」となり、市場ローカルの暦日と DB 上の暦日がずれる
- `internal/feature/candles/usecase/candle_aggregation.go` の `weekStart` / `monthStart` および `internal/feature/candles/usecase/candle_ingest_usecase.go` の境界判定（`Weekday()`, `Day()`）が `time.UTC` 前提で動作しており、月初・年末・夏時間切替日付近で週足・月足の集計が市場時間基準と一致しない

`symbols` テーブルには市場識別子（`market`）はあるが、IANA タイムゾーンを直接保持するカラムがなかった。

## 決定

`symbols` テーブルに `timezone`（IANA TZ 文字列）カラムを必須カラムとして追加し、ingest 時には銘柄ごとの `timezone` を `time.LoadLocation` で解決してから:

1. TwelveData レスポンスを `time.ParseInLocation` で取引所ローカル時刻として解釈する
2. 週足・月足集計の境界判定（曜日・日付・週開始・月開始）も同じロケーションで行う

を行う。`MarketRepository.GetTimeSeries` インターフェースは `loc *time.Location` を引数に取る形へ拡張し、`SymbolRepository` は `[]ActiveSymbol{Code, Timezone}` を返す形に変更する。

## 理由

- **銘柄単位で正しい市場時間軸を保てる**: 米国株・日本株が共存しても、各銘柄のローカル時刻で月足・週足が切られる
- **境界バグの根本解消**: 月初・年末・夏時間切替日に発生するバケット混入が消える
- **将来の市場拡張に強い**: 香港・ロンドン等を追加する際は seed の `timezone` を埋めるだけで対応可能
- **副作用の局所化**: `loc` を `usecase` 層から `adapters` 層まで明示的に貫通させることで、暗黙の UTC 仮定を排除した

## 代替案

| 代替案                                | 不採用の理由                                                                                             |
| ------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| 案B: 全銘柄を `Asia/Tokyo` 固定       | 既存シードに NASDAQ/NYSE 銘柄が多数含まれており、米国株を扱えなくなる割り切りは事業要件と合致しない         |
| 案C: TwelveData `/meta` から動的解決 | ingest ごとに銘柄数 × 1 回の追加 API 呼び出しが必要となり、無料枠 8 req/min のレート制限を圧迫する         |

## 影響

### ポジティブな影響

- 市場ローカル基準で集計が正しくなる
- DB 上の `time` 値（UTC 格納）も市場ローカル時刻に対応する正しい瞬間を指すようになる（`AT TIME ZONE 'America/New_York'` で参照可能）
- 銘柄ごとの TZ をデータで持つため、コードに市場仮定をハードコードせずに済む

### ネガティブな影響・トレードオフ

- `MarketRepository.GetTimeSeries` のシグネチャ変更により、既存呼び出し元（テスト含む）の修正が必要
- `symbols.timezone` を将来追加する銘柄でも必ず正しい IANA 文字列で埋める運用責任が発生する
- 既存 `candles` データは UTC 解釈で保存されているため、再 ingest による上書きで一掃する必要がある（dev 環境では `db/seed/seed.sql` の `TRUNCATE TABLE symbols CASCADE` が candles も連動クリアするため、自然に解消される）

## 既存データのマイグレーション

dev 環境専用のシードでは `TRUNCATE TABLE symbols CASCADE` で `candles` も同時にクリアされるため、再 ingest により正しい TZ で再構築する。本番・staging 運用が始まる場合は別途 `candles` を空にしてから再 ingest する手順を運用ランブックとして整備する必要がある。

## 関連ADR

- [ADR-0002](0002-フィーチャーベースのクリーンアーキテクチャ採用.md): リポジトリインターフェースを利用者（usecase）側で定義する原則に従い、`SymbolRepository` / `MarketRepository` は candles/usecase で再定義した
