package candles

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/feature/candles/sqlc"
)

// dbRepository は Repository / WriteRepository の sqlc + 生 SQL 実装です。
// Find は sqlc 生成クエリを使用し、UpsertBatch は単発で大量の INSERT ... ON CONFLICT を
// 1 ステートメントにまとめて発行するため raw SQL を組み立てます（sqlc では多値 VALUES の
// ON CONFLICT を 1 クエリで表現しにくいため）。
type dbRepository struct {
	db *sql.DB
	q  *candlessqlc.Queries
}

var _ Repository = (*dbRepository)(nil)

// NewRepository は指定された *sql.DB で dbRepository の新しいインスタンスを生成します。
func NewRepository(db *sql.DB) *dbRepository {
	return &dbRepository{db: db, q: candlessqlc.New(db)}
}

const upsertCandleConflict = `
ON CONFLICT (symbol_code, "interval", "time") DO UPDATE
SET open = EXCLUDED.open,
    high = EXCLUDED.high,
    low = EXCLUDED.low,
    close = EXCLUDED.close,
    volume = EXCLUDED.volume`

// UpsertBatch はローソク足データをバッチで挿入または更新します。
// (symbol_code, interval, time) の複合 UNIQUE をキーに ON CONFLICT DO UPDATE で
// OHLCV を上書きします。1 ステートメントで全件処理するため round-trip は 1 回です。
func (r *dbRepository) UpsertBatch(ctx context.Context, candles []Candle) error {
	if len(candles) == 0 {
		return nil
	}

	var sb strings.Builder
	sb.WriteString(`INSERT INTO candles (symbol_code, "interval", "time", open, high, low, close, volume) VALUES `)
	args := make([]any, 0, len(candles)*8)
	for i, c := range candles {
		if i > 0 {
			sb.WriteString(", ")
		}
		off := i * 8
		fmt.Fprintf(&sb, "($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
			off+1, off+2, off+3, off+4, off+5, off+6, off+7, off+8)
		args = append(args,
			c.SymbolCode, c.Interval, c.Time,
			c.Open, c.High, c.Low, c.Close, c.Volume,
		)
	}
	sb.WriteString(upsertCandleConflict)

	if _, err := r.db.ExecContext(ctx, sb.String(), args...); err != nil {
		return fmt.Errorf("upsert candles: %w", err)
	}
	return nil
}

// Find は指定された銘柄とインターバルのローソク足データを取得します。
// 結果は時間の降順でソートされ、outputsize 件で制限されます。
func (r *dbRepository) Find(ctx context.Context, symbol, interval string, outputsize int) ([]Candle, error) {
	if outputsize <= 0 || outputsize > MaxOutputSize {
		return nil, fmt.Errorf("find candles: %w", ErrInvalidOutputSize)
	}
	rows, err := r.q.FindCandlesLimit(ctx, candlessqlc.FindCandlesLimitParams{
		SymbolCode: symbol,
		Interval:   interval,
		Limit:      int32(outputsize),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Candle, 0, len(rows))
	for _, row := range rows {
		out = append(out, Candle{
			SymbolCode: row.SymbolCode,
			Interval:   row.Interval,
			Time:       row.Time,
			Open:       row.Open,
			High:       row.High,
			Low:        row.Low,
			Close:      row.Close,
			Volume:     row.Volume,
		})
	}
	return out, nil
}
