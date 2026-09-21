//go:build unit

package provider

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

type rateLimit429AccountRepoStub struct {
	accountcore.HealthStore
	rateLimitCalls     int
	lastRateLimitID    int64
	lastRateLimitReset time.Time
}

func (r *rateLimit429AccountRepoStub) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitCalls++
	r.lastRateLimitID = id
	r.lastRateLimitReset = resetAt
	return nil
}

func TestHandle429_FallbackUsesDBSeconds(t *testing.T) {
	accountRepo := &rateLimit429AccountRepoStub{}
	settingRepo := newCooldownSettingsStore()
	data, err := json.Marshal(accountcore.RateLimit429CooldownSettings{Enabled: true, CooldownSeconds: 12})
	require.NoError(t, err)
	settingRepo.data[accountcore.SettingKeyRateLimit429CooldownSettings] = string(data)

	settingSvc := accountcore.NewRuntimeSettings(settingRepo, errCooldownSettingMissing)
	svc := &RateLimitObserver{Health: accountcore.NewHealthService(accountRepo, nil, accountcore.HealthOptions{RateLimit429Settings: settingSvc.GetRateLimit429CooldownSettings})}

	account := &accountcore.Record{ID: 42, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}
	before := time.Now()
	svc.Observe429(context.Background(), account, http.Header{}, []byte(`{"error":{"type":"rate_limit_error","message":"slow down"}}`))
	after := time.Now()

	require.Equal(t, 1, accountRepo.rateLimitCalls)
	require.Equal(t, int64(42), accountRepo.lastRateLimitID)
	require.True(t, !accountRepo.lastRateLimitReset.Before(before.Add(12*time.Second)) && !accountRepo.lastRateLimitReset.After(after.Add(12*time.Second)))
}

func TestHandle429_FallbackDisabledSkipsLocalMark(t *testing.T) {
	accountRepo := &rateLimit429AccountRepoStub{}
	settingRepo := newCooldownSettingsStore()
	data, err := json.Marshal(accountcore.RateLimit429CooldownSettings{Enabled: false, CooldownSeconds: 12})
	require.NoError(t, err)
	settingRepo.data[accountcore.SettingKeyRateLimit429CooldownSettings] = string(data)

	settingSvc := accountcore.NewRuntimeSettings(settingRepo, errCooldownSettingMissing)
	svc := &RateLimitObserver{Health: accountcore.NewHealthService(accountRepo, nil, accountcore.HealthOptions{RateLimit429Settings: settingSvc.GetRateLimit429CooldownSettings})}

	account := &accountcore.Record{ID: 43, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}
	svc.Observe429(context.Background(), account, http.Header{}, []byte(`{"error":{"type":"rate_limit_error","message":"slow down"}}`))

	require.Zero(t, accountRepo.rateLimitCalls)
}

// Anthropic 缺少 reset 头的 429 也应进入短期兜底冷却，避免持续消耗故障转移预算。
func TestHandle429_AnthropicNoResetTimeUsesFallbackCooldown(t *testing.T) {
	accountRepo := &rateLimit429AccountRepoStub{}
	settingRepo := newCooldownSettingsStore()
	data, err := json.Marshal(accountcore.RateLimit429CooldownSettings{Enabled: true, CooldownSeconds: 12})
	require.NoError(t, err)
	settingRepo.data[accountcore.SettingKeyRateLimit429CooldownSettings] = string(data)

	settingSvc := accountcore.NewRuntimeSettings(settingRepo, errCooldownSettingMissing)
	svc := &RateLimitObserver{Health: accountcore.NewHealthService(accountRepo, nil, accountcore.HealthOptions{RateLimit429Settings: settingSvc.GetRateLimit429CooldownSettings})}

	account := &accountcore.Record{ID: 45, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth}
	before := time.Now()
	svc.Observe429(context.Background(), account, http.Header{}, []byte(`{"error":{"type":"rate_limit_error","message":"Extra usage required"}}`))
	after := time.Now()

	require.Equal(t, 1, accountRepo.rateLimitCalls)
	require.Equal(t, int64(45), accountRepo.lastRateLimitID)
	require.True(t, !accountRepo.lastRateLimitReset.Before(before.Add(12*time.Second)) && !accountRepo.lastRateLimitReset.After(after.Add(12*time.Second)))
}

// 管理端关闭兜底冷却后，Anthropic 缺少 reset 头的 429 不应标记账号。
func TestHandle429_AnthropicNoResetTimeFallbackDisabledSkipsMark(t *testing.T) {
	accountRepo := &rateLimit429AccountRepoStub{}
	settingRepo := newCooldownSettingsStore()
	data, err := json.Marshal(accountcore.RateLimit429CooldownSettings{Enabled: false, CooldownSeconds: 12})
	require.NoError(t, err)
	settingRepo.data[accountcore.SettingKeyRateLimit429CooldownSettings] = string(data)

	settingSvc := accountcore.NewRuntimeSettings(settingRepo, errCooldownSettingMissing)
	svc := &RateLimitObserver{Health: accountcore.NewHealthService(accountRepo, nil, accountcore.HealthOptions{RateLimit429Settings: settingSvc.GetRateLimit429CooldownSettings})}

	account := &accountcore.Record{ID: 46, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth}
	svc.Observe429(context.Background(), account, http.Header{}, []byte(`{"error":{"type":"rate_limit_error","message":"Extra usage required"}}`))

	require.Zero(t, accountRepo.rateLimitCalls)
}

func TestHandle429_FallbackUsesDefaultSecondsWhenSettingServiceMissing(t *testing.T) {
	accountRepo := &rateLimit429AccountRepoStub{}
	svc := &RateLimitObserver{Health: accountcore.NewHealthService(accountRepo, nil, accountcore.HealthOptions{})}

	account := &accountcore.Record{ID: 44, Platform: capability.PlatformGemini, Type: capability.AccountTypeAPIKey}
	before := time.Now()
	svc.Observe429(context.Background(), account, http.Header{}, []byte(`{"error":{"message":"slow down"}}`))
	after := time.Now()

	require.Equal(t, 1, accountRepo.rateLimitCalls)
	require.Equal(t, int64(44), accountRepo.lastRateLimitID)
	require.True(t, !accountRepo.lastRateLimitReset.Before(before.Add(5*time.Second)) && !accountRepo.lastRateLimitReset.After(after.Add(5*time.Second)))
}
