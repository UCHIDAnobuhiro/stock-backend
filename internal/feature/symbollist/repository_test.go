package symbollist

import (
	"context"
	"database/sql"
	"log"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/db/dbtest"
)

func TestMain(m *testing.M) {
	code, err := dbtest.RunMainWithPostgres(m)
	if err != nil {
		log.Fatalf("dbtest setup: %v", err)
	}
	os.Exit(code)
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return dbtest.OpenIsolatedDB(t)
}

// seedSymbol はテスト用の銘柄データをデータベースに作成して返します。
func seedSymbol(t *testing.T, db *sql.DB, code, name, market string, isActive bool) *Symbol {
	t.Helper()
	row := db.QueryRowContext(context.Background(),
		`INSERT INTO symbols (code, name, market, timezone, is_active)
		 VALUES ($1, $2, $3, 'America/New_York', $4)
		 RETURNING created_at, updated_at`,
		code, name, market, isActive,
	)
	s := &Symbol{
		Code:     code,
		Name:     name,
		Market:   market,
		Timezone: "America/New_York",
		IsActive: isActive,
	}
	require.NoError(t, row.Scan(&s.CreatedAt, &s.UpdatedAt), "failed to seed symbol")
	return s
}

// seedSymbolFull はロゴ情報付きで銘柄をシードします。
func seedSymbolFull(t *testing.T, db *sql.DB, s *Symbol) {
	t.Helper()
	var logoURL sql.NullString
	if s.LogoURL != nil {
		logoURL = sql.NullString{String: *s.LogoURL, Valid: true}
	}
	var logoAt sql.NullTime
	if s.LogoUpdatedAt != nil {
		logoAt = sql.NullTime{Time: *s.LogoUpdatedAt, Valid: true}
	}
	row := db.QueryRowContext(context.Background(),
		`INSERT INTO symbols (code, name, market, timezone, logo_url, logo_updated_at, is_active)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING created_at, updated_at`,
		s.Code, s.Name, s.Market, s.Timezone, logoURL, logoAt, s.IsActive,
	)
	require.NoError(t, row.Scan(&s.CreatedAt, &s.UpdatedAt))
}

// updateSymbolActive は銘柄の is_active フィールドを更新します。
func updateSymbolActive(t *testing.T, db *sql.DB, symbol *Symbol, isActive bool) {
	t.Helper()
	_, err := db.ExecContext(context.Background(),
		`UPDATE symbols SET is_active = $1 WHERE code = $2`, isActive, symbol.Code)
	require.NoError(t, err, "failed to update symbol active status")
}

func TestSymbolRepository_ListActive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		setupFunc     func(t *testing.T, db *sql.DB)
		expectedCodes []string
	}{
		{
			name: "success: returns active symbols sorted by code",
			setupFunc: func(t *testing.T, db *sql.DB) {
				seedSymbol(t, db, "NVDA", "NVIDIA Corporation", "NASDAQ", true)
				seedSymbol(t, db, "AAPL", "Apple Inc.", "NASDAQ", true)
				seedSymbol(t, db, "MSFT", "Microsoft Corporation", "NASDAQ", true)
			},
			expectedCodes: []string{"AAPL", "MSFT", "NVDA"},
		},
		{
			name: "success: excludes inactive symbols",
			setupFunc: func(t *testing.T, db *sql.DB) {
				seedSymbol(t, db, "MSFT", "Microsoft Corporation", "NASDAQ", true)
				inactive := seedSymbol(t, db, "AAPL", "Apple Inc.", "NASDAQ", true)
				updateSymbolActive(t, db, inactive, false)
				seedSymbol(t, db, "NVDA", "NVIDIA Corporation", "NASDAQ", true)
			},
			expectedCodes: []string{"MSFT", "NVDA"},
		},
		{
			name:          "success: returns empty list when no symbols",
			setupFunc:     func(t *testing.T, db *sql.DB) {},
			expectedCodes: []string{},
		},
		{
			name: "success: returns empty list when all symbols are inactive",
			setupFunc: func(t *testing.T, db *sql.DB) {
				s1 := seedSymbol(t, db, "MSFT", "Microsoft Corporation", "NASDAQ", true)
				updateSymbolActive(t, db, s1, false)
				s2 := seedSymbol(t, db, "AAPL", "Apple Inc.", "NASDAQ", true)
				updateSymbolActive(t, db, s2, false)
			},
			expectedCodes: []string{},
		},
		{
			name: "success: returns single active symbol",
			setupFunc: func(t *testing.T, db *sql.DB) {
				seedSymbol(t, db, "MSFT", "Microsoft Corporation", "NASDAQ", true)
			},
			expectedCodes: []string{"MSFT"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := setupTestDB(t)
			repo := NewRepository(db)
			if tt.setupFunc != nil {
				tt.setupFunc(t, db)
			}
			symbols, err := repo.ListActive(context.Background())
			require.NoError(t, err)
			gotCodes := make([]string, 0, len(symbols))
			for _, s := range symbols {
				gotCodes = append(gotCodes, s.Code)
			}
			assert.Equal(t, tt.expectedCodes, gotCodes)
		})
	}
}

func TestSymbolRepository_ListActive_FieldValues(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewRepository(db)

	seedSymbol(t, db, "MSFT", "Microsoft Corporation", "NASDAQ", true)
	symbols, err := repo.ListActive(context.Background())
	require.NoError(t, err)
	require.Len(t, symbols, 1)

	got := symbols[0]
	assert.Equal(t, "MSFT", got.Code)
	assert.Equal(t, "Microsoft Corporation", got.Name)
	assert.Equal(t, "NASDAQ", got.Market)
	assert.Equal(t, "America/New_York", got.Timezone)
	assert.Nil(t, got.LogoURL)
	assert.Nil(t, got.LogoUpdatedAt)
	assert.True(t, got.IsActive)
	assert.False(t, got.UpdatedAt.IsZero())
}

func TestSymbolRepository_ListActive_LogoURL(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewRepository(db)

	logoURL := "https://api.twelvedata.com/logo/apple.com"
	logoUpdatedAt := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	s := &Symbol{
		Code:          "AAPL",
		Name:          "Apple Inc.",
		Market:        "NASDAQ",
		Timezone:      "America/New_York",
		LogoURL:       &logoURL,
		LogoUpdatedAt: &logoUpdatedAt,
		IsActive:      true,
	}
	seedSymbolFull(t, db, s)

	symbols, err := repo.ListActive(context.Background())
	require.NoError(t, err)
	require.Len(t, symbols, 1)
	require.NotNil(t, symbols[0].LogoURL)
	require.NotNil(t, symbols[0].LogoUpdatedAt)
	assert.Equal(t, logoURL, *symbols[0].LogoURL)
	assert.True(t, symbols[0].LogoUpdatedAt.Equal(logoUpdatedAt))
}

func TestSymbolRepository_UpdateLogoURL(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewRepository(db)
	seedSymbol(t, db, "AAPL", "Apple Inc.", "NASDAQ", true)

	newLogoURL := "https://api.twelvedata.com/logo/apple.com"
	newLogoUpdatedAt := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	require.NoError(t, repo.UpdateLogoURL(context.Background(), "AAPL", newLogoURL, newLogoUpdatedAt))

	symbols, err := repo.ListActive(context.Background())
	require.NoError(t, err)
	require.Len(t, symbols, 1)
	require.NotNil(t, symbols[0].LogoURL)
	require.NotNil(t, symbols[0].LogoUpdatedAt)
	assert.Equal(t, newLogoURL, *symbols[0].LogoURL)
	assert.True(t, symbols[0].LogoUpdatedAt.Equal(newLogoUpdatedAt))
}

func TestSymbolRepository_UpdateLogoURL_NoMatchingSymbol(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewRepository(db)
	err := repo.UpdateLogoURL(context.Background(), "MISSING", "https://api.twelvedata.com/logo/missing.com", time.Now())
	assert.NoError(t, err)
}

func TestSymbolRepository_Exists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setupFunc  func(t *testing.T, db *sql.DB)
		code       string
		wantExists bool
	}{
		{
			name: "success: returns true for existing symbol",
			setupFunc: func(t *testing.T, db *sql.DB) {
				seedSymbol(t, db, "AAPL", "Apple Inc.", "NASDAQ", true)
			},
			code:       "AAPL",
			wantExists: true,
		},
		{
			name: "success: returns true for inactive symbol",
			setupFunc: func(t *testing.T, db *sql.DB) {
				seedSymbol(t, db, "AAPL", "Apple Inc.", "NASDAQ", false)
			},
			code:       "AAPL",
			wantExists: true,
		},
		{
			name:       "success: returns false for non-existent symbol",
			setupFunc:  func(t *testing.T, db *sql.DB) {},
			code:       "INVALID",
			wantExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := setupTestDB(t)
			repo := NewRepository(db)
			if tt.setupFunc != nil {
				tt.setupFunc(t, db)
			}
			exists, err := repo.Exists(context.Background(), tt.code)
			require.NoError(t, err)
			assert.Equal(t, tt.wantExists, exists)
		})
	}
}

func TestSymbolRepository_ContextCancellation(t *testing.T) {
	t.Parallel()
	db := setupTestDB(t)
	repo := NewRepository(db)
	seedSymbol(t, db, "MSFT", "Microsoft Corporation", "NASDAQ", true)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := repo.ListActive(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
