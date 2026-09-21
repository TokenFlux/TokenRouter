package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/stretchr/testify/require"
)

// 新旧调度器都必须复核数据库开关，缓存显示关闭也不能永久漏选已重新启用的账号。
func TestCompactSchedulingRechecksAdministratorSwitchFromDatabase(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("advanced=%v/enabled=%v", advanced, enabled), func(t *testing.T) {
				resetAdvancedSchedulerSettingCacheForTest()
				groupID := int64(91090)
				cachedMode, dbMode := "force_on", "force_off"
				if enabled {
					cachedMode, dbMode = dbMode, cachedMode
				}
				cached := &Account{ID: 71990, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive,
					Schedulable: true, Concurrency: 1, GroupIDs: []int64{groupID}, Extra: map[string]any{"openai_compact_mode": cachedMode}}
				fresh := *cached
				fresh.Extra = map[string]any{"openai_compact_mode": dbMode}
				svc := &OpenAIGatewayService{
					accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{fresh}},
					cache:       &schedulerTestGatewayCache{}, cfg: &config.Config{},
					rateLimitService: newAdvancedSchedulerRateLimitService(fmt.Sprint(advanced)),
					concurrencyService: scheduler.NewConcurrencyService(schedulerTestConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

						Event: logging.Event,
					},
					),
					schedulerSnapshot: NewSchedulerSnapshotService(&openAISnapshotCacheStub{
						snapshotAccounts: []*Account{cached}, accountsByID: map[int64]*Account{cached.ID: cached}}, nil, nil, nil, nil),
				}
				selected, _, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", "", "gpt-5.4", nil, egress.OpenAIUpstreamTransportAny, true)
				if enabled {
					require.NoError(t, err)
					require.NotNil(t, selected)
					require.Equal(t, cached.ID, selected.Account.ID)
				} else {
					require.ErrorIs(t, err, scheduler.ErrNoAvailableCompactAccounts)
					require.Nil(t, selected)
				}
			})
		}
	}
}

// TestOpenAIGatewayService_SelectAccountWithScheduler_CompactSelectsEnabledAccount
// 验证 Compact 调度只选择管理员启用的账号。
func TestOpenAIGatewayService_SelectAccountWithScheduler_CompactSelectsEnabledAccount(t *testing.T) {
	resetAdvancedSchedulerSettingCacheForTest()

	ctx := context.Background()
	groupID := int64(91001)
	accounts := []Account{
		{
			ID:          71001,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra:       map[string]any{"openai_compact_mode": "force_off"}, // 管理员禁用
		},
		{
			ID:          71002,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra:       map[string]any{"openai_compact_mode": "force_on"}, // 管理员启用
		},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:       &schedulerTestGatewayCache{},
		cfg:         cfg,
		concurrencyService: scheduler.NewConcurrencyService(schedulerTestConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event,
		},
		),
	}

	selection, _, err := svc.SelectAccountWithScheduler(
		ctx,
		&groupID,
		"",
		"",
		"gpt-5.4",
		nil, egress.OpenAIUpstreamTransportAny, true,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(71002), selection.Account.ID, "disabled account must be excluded")
}

// TestOpenAIGatewayService_SelectAccountWithScheduler_CompactRejectsDisabled
// 验证管理员关闭的账号不会被 Compact 请求选中。
func TestOpenAIGatewayService_SelectAccountWithScheduler_CompactRejectsDisabled(t *testing.T) {
	resetAdvancedSchedulerSettingCacheForTest()

	ctx := context.Background()
	groupID := int64(91002)
	accounts := []Account{
		{
			ID:          71010,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra:       map[string]any{"openai_compact_mode": accountcore.OpenAICompactModeForceOff},
		},
		{
			ID:          71011,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra:       map[string]any{"openai_compact_mode": "force_off"},
		},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:       &schedulerTestGatewayCache{},
		cfg:         cfg,
		concurrencyService: scheduler.NewConcurrencyService(schedulerTestConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event,
		},
		),
	}

	selection, _, err := svc.SelectAccountWithScheduler(
		ctx,
		&groupID,
		"",
		"",
		"gpt-5.4",
		nil, egress.OpenAIUpstreamTransportAny, true,
	)
	require.Error(t, err)
	require.True(t, errors.Is(err, scheduler.ErrNoAvailableCompactAccounts), "compact-only accounts should rejected explicitly unsupported and return compact error")
	require.Nil(t, selection)
}

// TestOpenAIGatewayService_SelectAccountWithScheduler_CompactUsesDefaultEnabledAccount
// 验证缺省压缩开关保持开启，显式关闭仍然排除。
func TestOpenAIGatewayService_SelectAccountWithScheduler_CompactUsesDefaultEnabledAccount(t *testing.T) {
	resetAdvancedSchedulerSettingCacheForTest()

	ctx := context.Background()
	groupID := int64(91003)
	accounts := []Account{
		{
			ID:          71020,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra:       map[string]any{"openai_compact_mode": "force_off"}, // 管理员关闭
		},
		{
			ID:          71021,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra:       map[string]any{}, // 缺省开启
		},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:       &schedulerTestGatewayCache{},
		cfg:         cfg,
		concurrencyService: scheduler.NewConcurrencyService(schedulerTestConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event,
		},
		),
	}

	selection, _, err := svc.SelectAccountWithScheduler(
		ctx,
		&groupID,
		"",
		"",
		"gpt-5.4",
		nil, egress.OpenAIUpstreamTransportAny, true,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(71021), selection.Account.ID, "default-enabled account should be selected")
}

// TestOpenAIGatewayService_SelectAccountWithScheduler_CompactAllowsGrok 验证 compact 调度允许 Grok 账号参与。
func TestOpenAIGatewayService_SelectAccountWithScheduler_CompactAllowsGrok(t *testing.T) {
	resetAdvancedSchedulerSettingCacheForTest()

	ctx := context.Background()
	groupID := int64(91004)
	accounts := []Account{
		{
			ID:          71030,
			Platform:    capability.PlatformGrok,
			Type:        capability.AccountTypeOAuth,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Credentials: map[string]any{
				"model_mapping": map[string]any{"grok-4.5": "grok-4.5"},
			},
		},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:       &schedulerTestGatewayCache{},
		cfg:         cfg,
		concurrencyService: scheduler.NewConcurrencyService(schedulerTestConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event,
		},
		),
	}

	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		ctx,
		&groupID,
		"",
		"",
		"grok-4.5",
		nil, egress.OpenAIUpstreamTransportAny, accountcore.OpenAIEndpointCapabilityTextGeneration,
		true,
		false,
		capability.PlatformGrok,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(71030), selection.Account.ID)
}

func TestOpenAIGatewayService_SelectAccountWithScheduler_NativeCompactionSeparatesLegacyCompactSupport(t *testing.T) {
	resetAdvancedSchedulerSettingCacheForTest()

	groupID := int64(91005)
	accounts := []Account{
		{
			ID:          71050,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    10,
			Extra: map[string]any{
				"openai_compact_mode":    "force_on",
				"openai_text_route_mode": "force_chat_completions",
			},
		},
		{
			ID:          71051,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Extra: map[string]any{
				"openai_compact_mode": accountcore.OpenAICompactModeForceOff,
			},
		},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:       &schedulerTestGatewayCache{},
		cfg:         cfg,
		concurrencyService: scheduler.NewConcurrencyService(schedulerTestConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event,
		},
		),
	}

	nativeSelection, _, err := svc.SelectAccountWithSchedulerForCapability(
		context.Background(),
		&groupID,
		"",
		"",
		"gpt-5.6-sol",
		nil, egress.OpenAIUpstreamTransportAny, accountcore.OpenAIEndpointCapabilityResponses,
		false,
		false,
	)
	require.NoError(t, err)
	require.NotNil(t, nativeSelection)
	require.Equal(t, int64(71051), nativeSelection.Account.ID)

	legacySelection, _, err := svc.SelectAccountWithSchedulerForCapability(
		context.Background(),
		&groupID,
		"",
		"",
		"gpt-5.6-sol",
		nil, egress.OpenAIUpstreamTransportAny, accountcore.OpenAIEndpointCapabilityResponses,
		true,
		false,
	)
	require.ErrorIs(t, err, scheduler.ErrNoAvailableCompactAccounts)
	require.Nil(t, legacySelection)
}

// TestOpenAIGatewayService_SelectAccountWithScheduler_NativeCompactionV2Mode
// 验证原生 V2 只读取自身管理员开关，忽略历史状态和旧版 Compact 开关。
func TestOpenAIGatewayService_SelectAccountWithScheduler_NativeCompactionV2Mode(t *testing.T) {
	resetAdvancedSchedulerSettingCacheForTest()

	groupID := int64(91006)
	accounts := []Account{
		{
			ID:          71060,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra: map[string]any{
				accountcore.OpenAINativeCompactionV2ModeExtraKey: accountcore.OpenAICompactModeForceOff,
				"openai_native_compaction_v2_supported":          true,
			},
		},
		{
			ID:          71061,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    1,
			Extra: map[string]any{
				"openai_native_compaction_v2_supported":          false,
				accountcore.OpenAINativeCompactionV2ModeExtraKey: accountcore.OpenAICompactModeForceOff,
			},
		},
		{
			ID:          71062,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    2,
			Extra: map[string]any{
				accountcore.OpenAINativeCompactionV2ModeExtraKey: accountcore.OpenAICompactModeForceOn,
				"openai_native_compaction_v2_supported":          false,
			},
		},
	}
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	svc := &OpenAIGatewayService{
		accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:       &schedulerTestGatewayCache{},
		cfg:         cfg,
		concurrencyService: scheduler.NewConcurrencyService(schedulerTestConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event,
		},
		),
	}

	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		context.Background(),
		&groupID,
		"",
		"",
		"gpt-5.6-sol",
		nil, egress.OpenAIUpstreamTransportAny, accountcore.OpenAIEndpointCapabilityRemoteCompactionV2,
		false,
		false,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(71062), selection.Account.ID)
}

// TestAllowsOpenAICompatibleCompact 验证压缩资格开关。
func TestAllowsOpenAICompatibleCompact(t *testing.T) {
	tests := []struct {
		name    string
		account *Account
		want    bool
	}{
		{name: "nil", account: nil, want: false},
		{name: "non openai", account: &Account{Platform: capability.PlatformAnthropic}, want: false},
		{name: "grok", account: &Account{Platform: capability.PlatformGrok}, want: true},
		{name: "openai default enabled", account: &Account{Platform: capability.PlatformOpenAI, Extra: map[string]any{}}, want: true},
		{name: "openai enabled", account: &Account{Platform: capability.PlatformOpenAI, Extra: map[string]any{"openai_compact_mode": "force_on"}}, want: true},
		{name: "openai disabled", account: &Account{Platform: capability.PlatformOpenAI, Extra: map[string]any{"openai_compact_mode": "force_off"}}, want: false},
		{name: "force on", account: &Account{Platform: capability.PlatformOpenAI, Extra: map[string]any{"openai_compact_mode": accountcore.OpenAICompactModeForceOn}}, want: true},
		{name: "force off overrides probe true", account: &Account{Platform: capability.PlatformOpenAI, Extra: map[string]any{"openai_compact_mode": accountcore.OpenAICompactModeForceOff, "openai_compact_supported": true}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := allowsOpenAICompatibleCompact(tt.account); got != tt.want {
				t.Fatalf("allowsOpenAICompatibleCompact(...) = %v, want %v", got, tt.want)
			}
		})
	}
}
