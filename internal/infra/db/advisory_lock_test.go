package db_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	infradb "github.com/UCHIDAnobuhiro/stock-backend/internal/infra/db"
)

func TestTryAdvisoryLock_NilDB(t *testing.T) {
	t.Parallel()

	lock, acquired, err := infradb.TryAdvisoryLock(t.Context(), nil, 100, 1)

	require.Error(t, err)
	assert.False(t, acquired)
	assert.Nil(t, lock)
}
