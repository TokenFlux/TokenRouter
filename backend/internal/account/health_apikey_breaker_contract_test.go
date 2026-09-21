package account_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

type openAIAPIKeyHealthSettingRepo struct {
	accountcore.RuntimeSettingsStore
	value    string
	getCalls int
}

func (r *openAIAPIKeyHealthSettingRepo) GetValue(context.Context, string) (string, error) {
	r.getCalls++
	return r.value, nil
}

type openAIAPIKeyHealthAccountRepo struct {
	accountcore.HealthStore
	setCalls int
	reason   string
}

func (r *openAIAPIKeyHealthAccountRepo) SetTempUnschedulable(_ context.Context, _ int64, _ time.Time, reason string) error {
	r.setCalls++
	r.reason = reason
	return nil
}

type openAIAPIKeyHealthCacheStub struct {
	accountcore.TempUnschedCache
	recordCalls int
	setCalls    int
	tripped     bool
}

func (c *openAIAPIKeyHealthCacheStub) RecordOpenAIAPIKeyHealthFailure(context.Context, int64, int, int) (int64, bool, error) {
	c.recordCalls++
	return 3, c.tripped, nil
}

func (c *openAIAPIKeyHealthCacheStub) SetTempUnsched(context.Context, int64, *accountcore.TempUnschedState) error {
	c.setCalls++
	return nil
}

type openAIAPIKeyHealthRuntimeBlocker struct{ calls int }

func (b *openAIAPIKeyHealthRuntimeBlocker) BlockAccountScheduling(*accountcore.Record, time.Time, string) {
	b.calls++
}
func (*openAIAPIKeyHealthRuntimeBlocker) ClearAccountSchedulingBlock(int64) {}

func openAIHealthPoolAccount() *accountcore.Record {
	return &accountcore.Record{
		ID:       42,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"pool_mode": true,
		},
	}
}

func TestOpenAIAPIKeyHealthBreakerDefaultDisabled(t *testing.T) {
	settings := accountcore.NewRuntimeSettings(&openAIAPIKeyHealthSettingRepo{}, nil)
	cache := &openAIAPIKeyHealthCacheStub{tripped: true}
	svc := accountcore.NewHealthService(&openAIAPIKeyHealthAccountRepo{}, cache, accountcore.HealthOptions{APIKeyHealthCounter: cache, APIKeyHealthSettings: settings.GetOpenAIAPIKeyHealthBreakerSettings})

	require.False(t, svc.ApplyAPIKeyHealthFailure(context.Background(), openAIHealthPoolAccount(), http.StatusBadGateway, nil, true))
	require.Zero(t, cache.recordCalls)
}

func TestOpenAIAPIKeyHealthBreakerTripsPersistedAndRuntimeState(t *testing.T) {
	encoded, err := json.Marshal(accountcore.OpenAIAPIKeyHealthBreakerSettings{Enabled: true, WindowMinutes: 1, FailureThreshold: 3, CooldownMinutes: 5})
	require.NoError(t, err)
	settings := accountcore.NewRuntimeSettings(&openAIAPIKeyHealthSettingRepo{value: string(encoded)}, nil)
	cache := &openAIAPIKeyHealthCacheStub{tripped: true}
	repo := &openAIAPIKeyHealthAccountRepo{}
	blocker := &openAIAPIKeyHealthRuntimeBlocker{}
	svc := accountcore.NewHealthService(repo, cache, accountcore.HealthOptions{APIKeyHealthCounter: cache, APIKeyHealthSettings: settings.GetOpenAIAPIKeyHealthBreakerSettings, Block: blocker.BlockAccountScheduling})
	account := openAIHealthPoolAccount()

	require.True(t, svc.ApplyAPIKeyHealthFailure(context.Background(), account, http.StatusBadGateway, []byte(`{"error":"upstream"}`), true))
	require.Equal(t, 1, cache.recordCalls)
	require.Equal(t, 1, cache.setCalls)
	require.Equal(t, 1, repo.setCalls)
	require.Equal(t, 1, blocker.calls)
	require.NotNil(t, account.TempUnschedulableUntil)
	require.Contains(t, repo.reason, accountcore.OpenAIAPIKeyHealthBreakerReason)
}

func TestOpenAIAPIKeyHealthSuccessDoesNotTouchSettingsOrCache(t *testing.T) {
	encoded, err := json.Marshal(accountcore.OpenAIAPIKeyHealthBreakerSettings{Enabled: true, WindowMinutes: 1, FailureThreshold: 3, CooldownMinutes: 5})
	require.NoError(t, err)
	settingRepo := &openAIAPIKeyHealthSettingRepo{value: string(encoded)}
	settings := accountcore.NewRuntimeSettings(settingRepo, nil)
	cache := &openAIAPIKeyHealthCacheStub{}
	svc := accountcore.NewHealthService(&openAIAPIKeyHealthAccountRepo{}, cache, accountcore.HealthOptions{APIKeyHealthCounter: cache, APIKeyHealthSettings: settings.GetOpenAIAPIKeyHealthBreakerSettings})

	svc.ObserveAPIKeyHealthSuccess(context.Background(), openAIHealthPoolAccount())
	svc.ObserveAPIKeyHealthSuccess(context.Background(), &accountcore.Record{ID: 43, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey})
	require.Zero(t, settingRepo.getCalls)
	require.Zero(t, cache.recordCalls)
}
