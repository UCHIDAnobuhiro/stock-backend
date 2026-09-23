//go:build !integration

package symbollist

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockLogoProvider struct {
	getLogoURLFunc func(ctx context.Context, symbol string) (string, error)
}

func (m *mockLogoProvider) GetLogoURL(ctx context.Context, symbol string) (string, error) {
	if m.getLogoURLFunc != nil {
		return m.getLogoURLFunc(ctx, symbol)
	}
	return "", nil
}

type mockLogoSymbolRepository struct {
	symbols           []Symbol
	listActiveErr     error
	updateLogoURLFunc func(ctx context.Context, code, logoURL string, updatedAt time.Time) error
	updates           []logoUpdate
}

type logoUpdate struct {
	code      string
	logoURL   string
	updatedAt time.Time
}

func (m *mockLogoSymbolRepository) ListActive(ctx context.Context) ([]Symbol, error) {
	if m.listActiveErr != nil {
		return nil, m.listActiveErr
	}
	return m.symbols, nil
}

func (m *mockLogoSymbolRepository) UpdateLogoURL(ctx context.Context, code, logoURL string, updatedAt time.Time) error {
	if m.updateLogoURLFunc != nil {
		if err := m.updateLogoURLFunc(ctx, code, logoURL, updatedAt); err != nil {
			return err
		}
	}
	m.updates = append(m.updates, logoUpdate{code: code, logoURL: logoURL, updatedAt: updatedAt})
	return nil
}

type mockRateLimiter struct {
	waitFunc func(ctx context.Context) error
	calls    int
}

func (m *mockRateLimiter) WaitIfNeeded(ctx context.Context) error {
	m.calls++
	if m.waitFunc != nil {
		return m.waitFunc(ctx)
	}
	return nil
}

func TestLogoIngestUsecase_IngestAll_Success(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	repo := &mockLogoSymbolRepository{
		symbols: []Symbol{
			{Code: "AAPL", IsActive: true},
			{Code: "MSFT", IsActive: true},
		},
	}
	provider := &mockLogoProvider{
		getLogoURLFunc: func(ctx context.Context, symbol string) (string, error) {
			return "https://api.twelvedata.com/logo/" + symbol + ".com", nil
		},
	}
	limiter := &mockRateLimiter{}
	uc := NewLogoIngestUsecase(provider, repo, limiter)
	uc.now = func() time.Time { return now }

	result, err := uc.IngestAll(context.Background())

	require.NoError(t, err)
	assert.Equal(t, LogoIngestResult{Total: 2, Succeeded: 2}, result)
	assert.Equal(t, 2, limiter.calls)
	require.Len(t, repo.updates, 2)
	assert.Equal(t, "AAPL", repo.updates[0].code)
	assert.Equal(t, "https://api.twelvedata.com/logo/AAPL.com", repo.updates[0].logoURL)
	assert.Equal(t, now, repo.updates[0].updatedAt)
	assert.Equal(t, "MSFT", repo.updates[1].code)
}

func TestLogoIngestUsecase_IngestAll_ContinuesOnFetchFailure(t *testing.T) {
	t.Parallel()

	repo := &mockLogoSymbolRepository{
		symbols: []Symbol{
			{Code: "AAPL", IsActive: true},
			{Code: "MSFT", IsActive: true},
		},
	}
	provider := &mockLogoProvider{
		getLogoURLFunc: func(ctx context.Context, symbol string) (string, error) {
			if symbol == "AAPL" {
				return "", errors.New("temporary error")
			}
			return "https://api.twelvedata.com/logo/microsoft.com", nil
		},
	}
	uc := NewLogoIngestUsecase(provider, repo, &mockRateLimiter{})

	result, err := uc.IngestAll(context.Background())

	require.NoError(t, err)
	assert.Equal(t, LogoIngestResult{Total: 2, Succeeded: 1, Failed: 1}, result)
	require.Len(t, repo.updates, 1)
	assert.Equal(t, "MSFT", repo.updates[0].code)
}

func TestLogoIngestUsecase_IngestAll_UpdateFailureDoesNotStopBatch(t *testing.T) {
	t.Parallel()

	repo := &mockLogoSymbolRepository{
		symbols: []Symbol{
			{Code: "AAPL", IsActive: true},
			{Code: "MSFT", IsActive: true},
		},
		updateLogoURLFunc: func(ctx context.Context, code, logoURL string, updatedAt time.Time) error {
			if code == "AAPL" {
				return errors.New("db error")
			}
			return nil
		},
	}
	provider := &mockLogoProvider{
		getLogoURLFunc: func(ctx context.Context, symbol string) (string, error) {
			return "https://api.twelvedata.com/logo/" + symbol + ".com", nil
		},
	}
	uc := NewLogoIngestUsecase(provider, repo, &mockRateLimiter{})

	result, err := uc.IngestAll(context.Background())

	require.NoError(t, err)
	assert.Equal(t, LogoIngestResult{Total: 2, Succeeded: 1, Failed: 1}, result)
	require.Len(t, repo.updates, 1)
	assert.Equal(t, "MSFT", repo.updates[0].code)
}

func TestLogoIngestUsecase_IngestAll_ListActiveFatalError(t *testing.T) {
	t.Parallel()

	uc := NewLogoIngestUsecase(&mockLogoProvider{}, &mockLogoSymbolRepository{listActiveErr: errors.New("db down")}, &mockRateLimiter{})

	result, err := uc.IngestAll(context.Background())

	assert.Error(t, err)
	assert.Equal(t, LogoIngestResult{}, result)
}

func TestLogoIngestUsecase_IngestAll_ContextCancelled(t *testing.T) {
	t.Parallel()

	repo := &mockLogoSymbolRepository{
		symbols: []Symbol{
			{Code: "AAPL", IsActive: true},
			{Code: "MSFT", IsActive: true},
		},
	}
	provider := &mockLogoProvider{
		getLogoURLFunc: func(ctx context.Context, symbol string) (string, error) {
			return "https://api.twelvedata.com/logo/" + symbol + ".com", nil
		},
	}
	limiter := &mockRateLimiter{}
	uc := NewLogoIngestUsecase(provider, repo, limiter)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result, err := uc.IngestAll(ctx)

	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, LogoIngestResult{Total: 2, Succeeded: 0, Failed: 0}, result)
	assert.Equal(t, 0, limiter.calls)
	assert.Empty(t, repo.updates)
}

func TestLogoIngestResult_FailureRate(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 0.0, LogoIngestResult{}.FailureRate())
	assert.Equal(t, 0.25, LogoIngestResult{Total: 4, Failed: 1}.FailureRate())
}
