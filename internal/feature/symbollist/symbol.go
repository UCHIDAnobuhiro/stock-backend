package symbollist

import "time"

// Symbol はシステム内の株式銘柄コードを表します。
// 銘柄コード、企業名、市場などの取引証券に関する情報を保持します。
type Symbol struct {
	Code          string     // 銘柄コード（例: "AAPL", "7203.T"）、主キー
	Name          string     // 企業名
	Market        string     // 市場識別子（例: "NASDAQ", "TSE"）
	Timezone      string     // 取引所の IANA タイムゾーン（例: "America/New_York", "Asia/Tokyo"）
	LogoURL       *string    // Twelve DataのロゴURL（未取得時はNULL）
	LogoUpdatedAt *time.Time // ロゴURLを最後に取得・更新した日時
	IsActive      bool       // トラッキング対象かどうか
	CreatedAt     time.Time  // 登録日時
	UpdatedAt     time.Time  // 最終更新日時
}
