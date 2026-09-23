package service

import (
	"context"
	"testing"
	"time"

	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"

	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	scheduler "github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/stretchr/testify/require"
)

func TestOpenAIGatewayService_SelectAccountByPreviousResponseID_Hit(t *testing.T) {
	ctx := context.Background()
	groupID := int64(23)
	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 2,
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
		}},
	}
	cache := &stubGatewayCache{}
	store := session.NewOpenAIWSStateStore(cache, gatewayprovider.LogOpenAIWSModeInfo)
	cfg := newOpenAIWSV2TestConfig()

	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{account}},
		cache:       cache,
		cfg:         cfg,
		concurrencyService: scheduler.NewConcurrencyService(stubConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event},
		),
		openaiWSStateStore: store,
	}))

	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_prev_1", account.Record.ID, time.Hour))

	selection, err := svc.SelectAccountByPreviousResponseID(ctx, &groupID, "resp_prev_1", "gpt-5.1", nil, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, account.Record.ID, selection.Account.Record.ID)
	require.True(t, selection.Acquired)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAIGatewayService_SelectAccountByPreviousResponseID_QuotaAutoPausedMiss(t *testing.T) {
	ctx := context.Background()
	groupID := int64(23)
	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 77,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 2,
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
			"codex_5h_used_percent":                         96.0,
			"auto_pause_5h_threshold":                       0.95,
		}},
	}
	cache := &stubGatewayCache{}
	store := session.NewOpenAIWSStateStore(cache, gatewayprovider.LogOpenAIWSModeInfo)
	cfg := newOpenAIWSV2TestConfig()
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{account}},
		cache:       cache,
		cfg:         cfg,
		concurrencyService: scheduler.NewConcurrencyService(stubConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event},
		),
		openaiWSStateStore: store,
	}))

	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_prev_quota", account.Record.ID, time.Hour))

	selection, err := svc.SelectAccountByPreviousResponseID(ctx, &groupID, "resp_prev_quota", "gpt-5.1", nil, false)
	require.NoError(t, err)
	require.Nil(t, selection, "超过 5h 配额阈值的账号不应继续命中 previous_response_id 粘连")

	// Auto-pause is transient, so the binding is preserved: the chain can resume on the
	// same account once the quota window resets.
	boundAccountID, getErr := store.GetResponseAccount(ctx, groupID, "resp_prev_quota")
	require.NoError(t, getErr)
	require.Equal(t, account.Record.ID, boundAccountID)
}

func TestOpenAIGatewayService_SelectAccountByPreviousResponseID_RateLimitedMiss(t *testing.T) {
	ctx := context.Background()
	groupID := int64(23)
	rateLimitedUntil := time.Now().Add(30 * time.Minute)
	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12,
		Platform:         capability.PlatformOpenAI,
		Type:             capability.AccountTypeAPIKey,
		Status:           billing.StatusActive,
		Schedulable:      true,
		Concurrency:      1,
		RateLimitResetAt: &rateLimitedUntil,
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
		}},
	}
	cache := &stubGatewayCache{}
	store := session.NewOpenAIWSStateStore(cache, gatewayprovider.LogOpenAIWSModeInfo)
	cfg := newOpenAIWSV2TestConfig()
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{account}},
		cache:       cache,
		cfg:         cfg,
		concurrencyService: scheduler.NewConcurrencyService(stubConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event},
		),
		openaiWSStateStore: store,
	}))

	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_prev_rl", account.Record.ID, time.Hour))

	selection, err := svc.SelectAccountByPreviousResponseID(ctx, &groupID, "resp_prev_rl", "gpt-5.1", nil, false)
	require.NoError(t, err)
	require.Nil(t, selection, "限额中的账号不应继续命中 previous_response_id 粘连")
	boundAccountID, getErr := store.GetResponseAccount(ctx, groupID, "resp_prev_rl")
	require.NoError(t, getErr)
	require.Zero(t, boundAccountID)
}

func TestOpenAIGatewayService_SelectAccountByPreviousResponseID_DBRuntimeRecheckRateLimitedMiss(t *testing.T) {
	ctx := context.Background()
	groupID := int64(24)
	rateLimitedUntil := time.Now().Add(30 * time.Minute)
	staleAccount := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 13,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
		}},
	}
	dbAccount := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 13,
		Platform:         capability.PlatformOpenAI,
		Type:             capability.AccountTypeAPIKey,
		Status:           billing.StatusActive,
		Schedulable:      true,
		Concurrency:      1,
		RateLimitResetAt: &rateLimitedUntil,
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
		}},
	}
	cache := &stubGatewayCache{}
	store := session.NewOpenAIWSStateStore(cache, gatewayprovider.LogOpenAIWSModeInfo)
	cfg := newOpenAIWSV2TestConfig()
	snapshotCache := &openAISnapshotCacheStub{
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{dbAccount.Record.ID: staleAccount},
	}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{dbAccount}},
		cache:       cache,
		cfg:         cfg,
		concurrencyService: scheduler.NewConcurrencyService(stubConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event},
		),
		openaiWSStateStore: store,
		schedulerSnapshot:  NewSchedulerSnapshotService(snapshotCache, nil, nil, nil, nil),
	}))

	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_prev_db_rl", dbAccount.Record.ID, time.Hour))

	selection, err := svc.SelectAccountByPreviousResponseID(ctx, &groupID, "resp_prev_db_rl", "gpt-5.1", nil, false)
	require.NoError(t, err)
	require.Nil(t, selection, "DB 中已限流的账号不应继续命中 previous_response_id 粘连")
	boundAccountID, getErr := store.GetResponseAccount(ctx, groupID, "resp_prev_db_rl")
	require.NoError(t, getErr)
	require.Zero(t, boundAccountID)
}

func TestOpenAIGatewayService_SelectAccountByPreviousResponseID_Excluded(t *testing.T) {
	ctx := context.Background()
	groupID := int64(23)
	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 8,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
		}},
	}
	cache := &stubGatewayCache{}
	store := session.NewOpenAIWSStateStore(cache, gatewayprovider.LogOpenAIWSModeInfo)
	cfg := newOpenAIWSV2TestConfig()
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{account}},
		cache:       cache,
		cfg:         cfg,
		concurrencyService: scheduler.NewConcurrencyService(stubConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event},
		),
		openaiWSStateStore: store,
	}))

	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_prev_2", account.Record.ID, time.Hour))

	selection, err := svc.SelectAccountByPreviousResponseID(ctx, &groupID, "resp_prev_2", "gpt-5.1", map[int64]struct{}{account.Record.ID: {}}, false)
	require.NoError(t, err)
	require.Nil(t, selection)
}

func TestOpenAIGatewayService_SelectAccountByPreviousResponseID_APIKeyForceHTTPHit(t *testing.T) {
	ctx := context.Background()
	groupID := int64(23)
	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 11,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Extra: map[string]any{
			"openai_ws_force_http":            true,
			"responses_websockets_v2_enabled": true,
		}},
	}
	cache := &stubGatewayCache{}
	store := session.NewOpenAIWSStateStore(cache, gatewayprovider.LogOpenAIWSModeInfo)
	cfg := newOpenAIWSV2TestConfig()
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{account}},
		cache:       cache,
		cfg:         cfg,
		concurrencyService: scheduler.NewConcurrencyService(stubConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event},
		),
		openaiWSStateStore: store,
	}))

	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_prev_force_http", account.Record.ID, time.Hour))

	selection, err := svc.SelectAccountByPreviousResponseID(ctx, &groupID, "resp_prev_force_http", "gpt-5.1", nil, false)
	require.NoError(t, err)
	require.NotNil(t, selection, "API-key HTTP continuation must retain the key/project that created the response")
	require.NotNil(t, selection.Account)
	require.Equal(t, account.Record.ID, selection.Account.Record.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAIGatewayService_SelectAccountByPreviousResponseID_OAuthForceHTTPIgnored(t *testing.T) {
	ctx := context.Background()
	groupID := int64(23)
	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 12,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Extra: map[string]any{
			"openai_ws_force_http":            true,
			"responses_websockets_v2_enabled": true,
		}},
	}
	cache := &stubGatewayCache{}
	store := session.NewOpenAIWSStateStore(cache, gatewayprovider.LogOpenAIWSModeInfo)
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{account}},
		cache:       cache,
		cfg:         newOpenAIWSV2TestConfig(),
		concurrencyService: scheduler.NewConcurrencyService(stubConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event},
		),
		openaiWSStateStore: store,
	}))

	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_prev_oauth_force_http", account.Record.ID, time.Hour))

	selection, err := svc.SelectAccountByPreviousResponseID(ctx, &groupID, "resp_prev_oauth_force_http", "gpt-5.1", nil, false)
	require.NoError(t, err)
	require.Nil(t, selection, "OAuth HTTP fallback cannot preserve WSv2 continuation state")
}

func TestOpenAIGatewayService_SelectAccountByPreviousResponseID_BusyKeepsSticky(t *testing.T) {
	ctx := context.Background()
	groupID := int64(23)
	accounts := []gatewayprovider.ExecutionAccount{
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 21,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_enabled": true,
			}},
		},
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 22,
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeAPIKey,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    9,
			Extra: map[string]any{
				"openai_apikey_responses_websockets_v2_enabled": true,
			}},
		},
	}

	cache := &stubGatewayCache{}
	store := session.NewOpenAIWSStateStore(cache, gatewayprovider.LogOpenAIWSModeInfo)
	cfg := newOpenAIWSV2TestConfig()
	cfg.Gateway.Scheduling.StickySessionMaxWaiting = 2
	cfg.Gateway.Scheduling.StickySessionWaitTimeout = 30 * time.Second

	concurrencyCache := stubConcurrencyCache{
		acquireResults: map[int64]bool{
			21: false, // previous_response 命中的账号繁忙
			22: true,  // 次优账号可用（若回退会命中）
		},
		waitCounts: map[int64]int{
			21: 999,
		},
	}

	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: accounts},
		cache:       cache,
		cfg:         cfg,
		concurrencyService: scheduler.NewConcurrencyService(concurrencyCache, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event},
		),
		openaiWSStateStore: store,
	}))

	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_prev_busy", 21, time.Hour))

	selection, err := svc.SelectAccountByPreviousResponseID(ctx, &groupID, "resp_prev_busy", "gpt-5.1", nil, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(21), selection.Account.Record.ID, "busy previous_response sticky account should remain selected")
	require.False(t, selection.Acquired)
	require.NotNil(t, selection.WaitPlan)
	require.Equal(t, int64(21), selection.WaitPlan.AccountID)
}

func TestOpenAIGatewayService_SelectAccountByPreviousResponseID_CapabilityMismatchKeepsSticky(t *testing.T) {
	ctx := context.Background()
	groupID := int64(25)
	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 31,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"openai_workload_capabilities": []any{"text_generation"},
		},
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
		}},
	}
	cache := &stubGatewayCache{}
	store := session.NewOpenAIWSStateStore(cache, gatewayprovider.LogOpenAIWSModeInfo)
	cfg := newOpenAIWSV2TestConfig()
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo: stubOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{account}},
		cache:       cache,
		cfg:         cfg,
		concurrencyService: scheduler.NewConcurrencyService(stubConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event},
		),
		openaiWSStateStore: store,
	}))

	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_prev_capability", account.Record.ID, time.Hour))

	selection, err := svc.selectAccountByPreviousResponseIDForCapability(
		ctx,
		&groupID,
		"resp_prev_capability",
		"text-embedding-3-small",
		nil,
		accountcore.OpenAIEndpointCapabilityEmbeddings,
		false,
	)
	require.NoError(t, err)
	require.Nil(t, selection)
	boundAccountID, getErr := store.GetResponseAccount(ctx, groupID, "resp_prev_capability")
	require.NoError(t, getErr)
	require.Equal(t, account.Record.ID, boundAccountID)
}

// TestOpenAIGatewayService_SelectAccountByPreviousResponseIDUsesResolvedRoutingModel 验证响应链粘性检查不会把 D 重新解析成 C。
func TestOpenAIGatewayService_SelectAccountByPreviousResponseIDUsesResolvedRoutingModel(t *testing.T) {

	ctx := context.Background()
	groupID := int64(26)
	price := 0.01
	channel := routing.Channel{
		ID:                 78,
		Status:             billing.StatusActive,
		RestrictModels:     true,
		BillingModelSource: routing.BillingModelSourceUpstream,
		ModelMapping: map[string]map[string]string{
			capability.PlatformOpenAI: {"client-alias": "channel-model"},
		},
		ModelPricing: []routing.ChannelModelPricing{{
			Platform:   capability.PlatformOpenAI,
			Models:     []string{"allowed-upstream"},
			InputPrice: &price,
		}},
	}
	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 32,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"channel-model":  "blocked-upstream",
				"dispatch-model": "allowed-upstream",
			},
			"model_whitelist": []any{"blocked-upstream", "allowed-upstream"},
		},
		Extra: map[string]any{
			"openai_apikey_responses_websockets_v2_enabled": true,
		}},
	}
	cache := &stubGatewayCache{}
	store := session.NewOpenAIWSStateStore(cache, gatewayprovider.LogOpenAIWSModeInfo)
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo:    stubOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{account}},
		cache:          cache,
		cfg:            newOpenAIWSV2TestConfig(),
		channelService: routingtestkit.Channel(groupID, capability.PlatformOpenAI, channel),
		concurrencyService: scheduler.NewConcurrencyService(stubConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

			Event: logging.Event},
		),
		openaiWSStateStore: store,
		healthObserver:     newUpstreamHealthForTest(nil, nil, nil, accountcore.HealthOptions{}, nil),
		schedulerParameters: newAdvancedSchedulerParametersForTest(newOpenAIWSV2TestConfig(),

			"true"),
	}))

	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_dispatch_model", account.Record.ID, time.Hour))
	selection, _, err := svc.SelectAccountWithSchedulerForCapabilityAndRoutingModel(
		ctx,
		&groupID,
		"resp_dispatch_model",
		"",
		"client-alias",
		"dispatch-model",
		nil, egress.OpenAIUpstreamTransportResponsesWebsocketV2, accountcore.OpenAIEndpointCapabilityTextGeneration,
		false,
		false,
	)

	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, account.Record.ID, selection.Account.Record.ID)
	require.Equal(t, "allowed-upstream", gatewayprovider.ExecutionModelPolicy(selection.Account).OpenAIUpstream("dispatch-model", false, false))
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func newOpenAIWSV2TestConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.OAuthEnabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.StickyResponseIDTTLSeconds = 3600
	return cfg
}
