package db_test

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	infradb "github.com/UCHIDAnobuhiro/stock-backend/internal/infra/db"
	"github.com/UCHIDAnobuhiro/stock-backend/internal/infra/db/dbtest"
)

func TestMain(m *testing.M) {
	code, err := dbtest.RunMainWithPostgres(m)
	if err != nil {
		log.Printf("dbtest setup failed: %v", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func TestTryAdvisoryLock(t *testing.T) {
	t.Parallel()

	sqlDB := dbtest.OpenIsolatedDB(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	first, acquired, err := infradb.TryAdvisoryLock(ctx, sqlDB, 100, 1)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, first)

	duplicate, acquired, err := infradb.TryAdvisoryLock(ctx, sqlDB, 100, 1)
	require.NoError(t, err)
	assert.False(t, acquired)
	assert.Nil(t, duplicate)

	different, acquired, err := infradb.TryAdvisoryLock(ctx, sqlDB, 100, 2)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, different)
	require.NoError(t, different.Unlock(ctx))

	require.NoError(t, first.Unlock(ctx))
	require.NoError(t, first.Unlock(ctx))

	reacquired, acquired, err := infradb.TryAdvisoryLock(ctx, sqlDB, 100, 1)
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, reacquired)
	require.NoError(t, reacquired.Unlock(ctx))
}

func TestTryAdvisoryLock_NilDB(t *testing.T) {
	t.Parallel()

	lock, acquired, err := infradb.TryAdvisoryLock(t.Context(), nil, 100, 1)

	require.Error(t, err)
	assert.False(t, acquired)
	assert.Nil(t, lock)
}
