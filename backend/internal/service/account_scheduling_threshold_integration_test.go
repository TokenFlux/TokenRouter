//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	account "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

type thresholdSelectionAccountRepoStub struct {
	rateLimitAccountRepoStub
	accounts []gatewayprovider.ExecutionAccount
}

func (r *thresholdSelectionAccountRepoStub) ListSchedulableByPlatform(_ context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	filtered := make([]gatewayprovider.ExecutionAccount, 0, len(r.accounts))
	for _, account := range r.accounts {
		if account.Record.Platform == platform {
			filtered = append(filtered, account)
		}
	}
	return filtered, nil
}

func (r *thresholdSelectionAccountRepoStub) ListSchedulableByGroupIDAndPlatform(ctx context.Context, _ int64, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func (r *thresholdSelectionAccountRepoStub) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func TestGatewayService_ListSchedulableAccounts_DoesNotFilterUnsupportedThresholdPlatforms(t *testing.T) {

	settingsRepo := newMockSettingRepo()
	settingsRepo.data[account.SettingKeyAccountSchedulingThresholds] = `{"openai":90}`

	accountRepo := &thresholdSelectionAccountRepoStub{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: account.Record{LoadLocation: time.LoadLocation, ID: 3101,
				Platform:    "kiro",
				Status:      billing.StatusActive,
				Schedulable: true,
				Credentials: map[string]any{
					"account_scheduling_threshold": 1,
				},
				Extra: map[string]any{
					"kiro_sched_utilization": 95.0,
					"kiro_sched_reset_at":    time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
				}},
			},
			{Record: account.Record{LoadLocation: time.LoadLocation, ID: 3102,
				Platform:    "kiro",
				Status:      billing.StatusActive,
				Schedulable: true,
				Extra: map[string]any{
					"kiro_sched_utilization": 42.0,
					"kiro_sched_reset_at":    time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339),
				}},
			},
		},
	}

	rateLimitService := NewRateLimitService(accountRepo, nil, &config.Config{}, nil)
	rateLimitService.SetSettingService(newExecutionReadersFixture(settingsRepo, &config.Config{}))
	svc := withSchedulerParametersForTest(&GatewayService{
		accountRepo:      accountRepo,
		cfg:              &config.Config{},
		rateLimitService: rateLimitService,
	})

	accounts, useMixed, err := svc.listSchedulableAccounts(context.Background(), nil, "kiro", false)

	require.NoError(t, err)
	require.False(t, useMixed)
	require.Len(t, accounts, 2)
	require.Equal(t, int64(3101), accounts[0].Record.ID)
	require.Equal(t, int64(3102), accounts[1].Record.ID)
	require.Equal(t, 0, accountRepo.tempCalls)
}

func TestOpenAIGatewayService_ListSchedulableAccounts_FiltersThresholdBlockedAccounts(t *testing.T) {

	settingsRepo := newMockSettingRepo()
	settingsRepo.data[account.SettingKeyAccountSchedulingThresholds] = `{"openai":85}`

	accountRepo := &thresholdSelectionAccountRepoStub{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: account.Record{LoadLocation: time.LoadLocation, ID: 4101,
				Platform:    capability.PlatformOpenAI,
				Status:      billing.StatusActive,
				Schedulable: true,
				Extra: map[string]any{
					"codex_7d_used_percent": 91.0,
					"codex_7d_reset_at":     time.Now().UTC().Add(12 * time.Hour).Format(time.RFC3339),
				}},
			},
			{Record: account.Record{LoadLocation: time.LoadLocation, ID: 4102,
				Platform:    capability.PlatformOpenAI,
				Status:      billing.StatusActive,
				Schedulable: true,
				Extra: map[string]any{
					"codex_7d_used_percent": 40.0,
					"codex_7d_reset_at":     time.Now().UTC().Add(12 * time.Hour).Format(time.RFC3339),
				}},
			},
		},
	}

	rateLimitService := NewRateLimitService(accountRepo, nil, &config.Config{}, nil)
	rateLimitService.SetSettingService(newExecutionReadersFixture(settingsRepo, &config.Config{}))
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo:      accountRepo,
		cfg:              &config.Config{},
		rateLimitService: rateLimitService,
	}))

	accounts, err := svc.listSchedulableAccounts(context.Background(), nil, capability.PlatformOpenAI)

	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, int64(4102), accounts[0].Record.ID)
	require.Equal(t, 1, accountRepo.tempCalls)
}
