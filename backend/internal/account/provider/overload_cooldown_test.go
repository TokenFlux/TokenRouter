//go:build unit

package provider

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

type overloadAccountRepoStub struct {
	accountcore.HealthStore
	overloadCalls   int
	errorCalls      int
	lastOverloadID  int64
	lastOverloadEnd time.Time
}

func (r *overloadAccountRepoStub) SetError(_ context.Context, _ int64, _ string) error {
	r.errorCalls++
	return nil
}

func (r *overloadAccountRepoStub) SetOverloaded(_ context.Context, id int64, until time.Time) error {
	r.overloadCalls++
	r.lastOverloadID = id
	r.lastOverloadEnd = until
	return nil
}

func TestHandle529_EnabledFromDB_PausesAccount(t *testing.T) {
	accountRepo := &overloadAccountRepoStub{}
	settingRepo := newCooldownSettingsStore()
	data, err := json.Marshal(accountcore.OverloadCooldownSettings{Enabled: true, CooldownMinutes: 15})
	require.NoError(t, err)
	settingRepo.data[accountcore.SettingKeyOverloadCooldownSettings] = string(data)

	settingSvc := accountcore.NewRuntimeSettings(settingRepo, errCooldownSettingMissing)
	svc := newOverloadObserver(accountRepo, accountcore.HealthOptions{OverloadSettings: settingSvc.GetOverloadCooldownSettings})

	account := &accountcore.Record{ID: 42, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth}
	before := time.Now()
	svc.Core.ApplyOverload(context.Background(), account)

	require.Equal(t, 1, accountRepo.overloadCalls)
	require.Equal(t, int64(42), accountRepo.lastOverloadID)
	require.WithinDuration(t, before.Add(15*time.Minute), accountRepo.lastOverloadEnd, 2*time.Second)
}

func TestHandle529_DisabledFromDB_SkipsAccount(t *testing.T) {
	accountRepo := &overloadAccountRepoStub{}
	settingRepo := newCooldownSettingsStore()
	data, err := json.Marshal(accountcore.OverloadCooldownSettings{Enabled: false, CooldownMinutes: 15})
	require.NoError(t, err)
	settingRepo.data[accountcore.SettingKeyOverloadCooldownSettings] = string(data)

	settingSvc := accountcore.NewRuntimeSettings(settingRepo, errCooldownSettingMissing)
	svc := newOverloadObserver(accountRepo, accountcore.HealthOptions{OverloadSettings: settingSvc.GetOverloadCooldownSettings})

	account := &accountcore.Record{ID: 42, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth}
	svc.Core.ApplyOverload(context.Background(), account)

	require.Equal(t, 0, accountRepo.overloadCalls, "should NOT pause when disabled")
}

func TestHandle529_NilSettingService_FallsBackToConfig(t *testing.T) {
	accountRepo := &overloadAccountRepoStub{}
	minutes := 20
	svc := newOverloadObserver(accountRepo, accountcore.HealthOptions{OverloadMinutes: minutes})
	// NOT calling SetSettingService — remains nil

	account := &accountcore.Record{ID: 77, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth}
	before := time.Now()
	svc.Core.ApplyOverload(context.Background(), account)

	require.Equal(t, 1, accountRepo.overloadCalls)
	require.WithinDuration(t, before.Add(20*time.Minute), accountRepo.lastOverloadEnd, 2*time.Second)
}

func TestHandle529_NilSettingService_ZeroConfig_DefaultsTen(t *testing.T) {
	accountRepo := &overloadAccountRepoStub{}
	svc := newOverloadObserver(accountRepo, accountcore.HealthOptions{})

	account := &accountcore.Record{ID: 88, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth}
	before := time.Now()
	svc.Core.ApplyOverload(context.Background(), account)

	require.Equal(t, 1, accountRepo.overloadCalls)
	require.WithinDuration(t, before.Add(10*time.Minute), accountRepo.lastOverloadEnd, 2*time.Second)
}

func TestHandle529_DBReadError_FallsBackToConfig(t *testing.T) {
	accountRepo := &overloadAccountRepoStub{}
	errRepo := &errSettingRepo{readErr: context.DeadlineExceeded}
	errRepo.data = make(map[string]string)

	minutes := 7
	settingSvc := accountcore.NewRuntimeSettings(errRepo, errCooldownSettingMissing)
	svc := newOverloadObserver(accountRepo, accountcore.HealthOptions{OverloadMinutes: minutes, OverloadSettings: settingSvc.GetOverloadCooldownSettings})

	account := &accountcore.Record{ID: 99, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth}
	before := time.Now()
	svc.Core.ApplyOverload(context.Background(), account)

	require.Equal(t, 1, accountRepo.overloadCalls)
	require.WithinDuration(t, before.Add(7*time.Minute), accountRepo.lastOverloadEnd, 2*time.Second)
}

func TestHandleUpstreamError_529RespectsAccountPolicies(t *testing.T) {
	tests := []struct {
		name        string
		credentials map[string]any
	}{
		{
			name:        "pool mode",
			credentials: map[string]any{"pool_mode": true},
		},
		{
			name: "custom code filter excludes 529",
			credentials: map[string]any{
				"custom_error_codes_enabled": true,
				"custom_error_codes":         []any{float64(429)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &overloadAccountRepoStub{}
			svc := newOverloadObserver(repo, accountcore.HealthOptions{})
			account := &accountcore.Record{
				ID:          101,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.AccountTypeAPIKey,
				Credentials: tt.credentials,
			}

			shouldDisable := svc.ApplyUpstreamError(context.Background(), account, healthTestObservation(context.Background(), 529, nil, []byte(`{"error":{"message":"overloaded"}}`))).StopScheduling

			require.False(t, shouldDisable)
			require.Zero(t, repo.overloadCalls)
			require.Zero(t, repo.errorCalls)
		})
	}
}

func TestHandleUpstreamError_529CustomCodeDisablesInsteadOfOverloadCooldown(t *testing.T) {
	repo := &overloadAccountRepoStub{}
	svc := newOverloadObserver(repo, accountcore.HealthOptions{})
	account := &accountcore.Record{
		ID:       102,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"custom_error_codes_enabled": true,
			"custom_error_codes":         []any{float64(529)},
		},
	}

	shouldDisable := svc.ApplyUpstreamError(context.Background(), account, healthTestObservation(context.Background(), 529, nil, []byte(`{"error":{"message":"overloaded"}}`))).StopScheduling

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.errorCalls)
	require.Zero(t, repo.overloadCalls)
}

// 只绑定原生健康实现；供应商窗口和模型端口未被这些过载用例调用。
func newOverloadObserver(repo accountcore.HealthStore, options accountcore.HealthOptions) *UpstreamHealth {
	return &UpstreamHealth{Core: accountcore.NewHealthService(repo, nil, options)}
}

type errSettingRepo struct {
	cooldownSettingsStore
	readErr error
}

func (s *errSettingRepo) GetValue(context.Context, string) (string, error) { return "", s.readErr }
