package symbollist

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"
)

func TestLogoIngestUsecase_IngestAll_ContextEndsDuringSymbol(t *testing.T) {
	positions := []struct {
		name          string
		total, target int
	}{
		{"single", 1, 0}, {"last", 5, 4}, {"middle", 5, 2},
	}
	phases := []string{"fetch_error", "save_error", "fetch_nil", "save_nil"}
	endings := []struct {
		name     string
		deadline bool
		want     error
	}{
		{"canceled", false, context.Canceled}, {"deadline", true, context.DeadlineExceeded},
	}
	for _, position := range positions {
		for _, phase := range phases {
			for _, ending := range endings {
				t.Run(position.name+"/"+phase+"/"+ending.name, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						var ctx context.Context
						var cancel context.CancelFunc
						if ending.deadline {
							ctx, cancel = context.WithTimeout(context.Background(), time.Hour)
						} else {
							ctx, cancel = context.WithCancel(context.Background())
						}
						defer cancel()
						end := func() {
							if ending.deadline {
								<-ctx.Done()
							} else {
								cancel()
							}
						}
						codes := []string{"A", "B", "C", "D", "E"}[:position.total]
						target := codes[position.target]
						var fetched, saved []string
						provider := &mockLogoProvider{getLogoURLFunc: func(_ context.Context, symbol string) (string, error) {
							fetched = append(fetched, symbol)
							if symbol == target && (phase == "fetch_error" || phase == "fetch_nil") {
								end()
								if phase == "fetch_error" {
									return "", errors.New("fetch failed")
								}
							}
							return "https://example.com/" + symbol, nil
						}}
						repo := &mockLogoSymbolRepository{symbols: make([]Symbol, len(codes)), updateLogoURLFunc: func(_ context.Context, code, _ string, _ time.Time) error {
							saved = append(saved, code)
							if code == target && (phase == "save_error" || phase == "save_nil") {
								end()
								if phase == "save_error" {
									return errors.New("save failed")
								}
							}
							return nil
						}}
						for i, code := range codes {
							repo.symbols[i] = Symbol{Code: code, IsActive: true}
						}
						result, err := NewLogoIngestUsecase(provider, repo, &mockRateLimiter{}).IngestAll(ctx)
						if !errors.Is(err, ending.want) {
							t.Fatalf("err=%v, want %v", err, ending.want)
						}
						wantSucceeded, wantFailed := position.target, 0
						if phase == "save_nil" {
							wantSucceeded++
						} else {
							wantFailed++
						}
						if result != (LogoIngestResult{Total: position.total, Succeeded: wantSucceeded, Failed: wantFailed}) {
							t.Errorf("result=%+v, want total=%d succeeded=%d failed=%d", result, position.total, wantSucceeded, wantFailed)
						}
						if len(fetched) != position.target+1 {
							t.Errorf("fetched=%v; later symbols must not run", fetched)
						}
						wantSaved := position.target
						if phase == "save_error" || phase == "save_nil" {
							wantSaved++
						}
						if len(saved) != wantSaved {
							t.Errorf("saved=%v; want %d calls", saved, wantSaved)
						}
					})
				})
			}
		}
	}
}

func TestLogoIngestUsecase_IngestAll_ContinuesWhenParentContextActive(t *testing.T) {
	for _, phase := range []string{"fetch", "save"} {
		for _, operationError := range []error{errors.New("ordinary failure"), fmt.Errorf("external canceled: %w", context.Canceled), fmt.Errorf("external timeout: %w", context.DeadlineExceeded)} {
			t.Run(phase+"/"+operationError.Error(), func(t *testing.T) {
				var saved []string
				provider := &mockLogoProvider{getLogoURLFunc: func(_ context.Context, symbol string) (string, error) {
					if phase == "fetch" && symbol == "A" {
						return "", operationError
					}
					return "https://example.com/" + symbol, nil
				}}
				repo := &mockLogoSymbolRepository{symbols: []Symbol{{Code: "A"}, {Code: "B"}}, updateLogoURLFunc: func(_ context.Context, code, _ string, _ time.Time) error {
					saved = append(saved, code)
					if phase == "save" && code == "A" {
						return operationError
					}
					return nil
				}}
				result, err := NewLogoIngestUsecase(provider, repo, &mockRateLimiter{}).IngestAll(context.Background())
				if err != nil {
					t.Fatalf("err=%v, want nil", err)
				}
				if result != (LogoIngestResult{Total: 2, Succeeded: 1, Failed: 1}) {
					t.Errorf("result=%+v", result)
				}
				if len(saved) == 0 || saved[len(saved)-1] != "B" {
					t.Errorf("saved=%v; B must be processed", saved)
				}
			})
		}
	}
}

func TestLogoIngestUsecase_IngestAll_ContextEndsAfterRateWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := &mockLogoProvider{getLogoURLFunc: func(context.Context, string) (string, error) {
		t.Fatal("fetch must not start after parent cancellation")
		return "", nil
	}}
	repo := &mockLogoSymbolRepository{symbols: []Symbol{{Code: "A"}}, updateLogoURLFunc: func(context.Context, string, string, time.Time) error {
		t.Fatal("save must not start after parent cancellation")
		return nil
	}}
	limiter := &mockRateLimiter{waitFunc: func(context.Context) error { cancel(); return nil }}
	result, err := NewLogoIngestUsecase(provider, repo, limiter).IngestAll(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	if result != (LogoIngestResult{Total: 1}) {
		t.Errorf("result=%+v, want only total", result)
	}
}
