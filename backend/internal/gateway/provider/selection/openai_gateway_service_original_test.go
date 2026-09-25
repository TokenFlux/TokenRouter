package selection

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/stretchr/testify/require"
)

// 编译期接口断言

// tempUnschedulableOpenAIAccountRepo 记录临时不可调度规则写入的模型范围。

// 复现 #4386：gpt-image-2 /v1/images/edits 的 usage 携带 input_tokens_details.image_tokens，
// 提取器须将图片输入 token 单独填入 ImageInputTokens（此前被丢弃并入 InputTokens 按文本价计费）。

// prompt_tokens_details 回退路径（部分上游用 prompt_tokens 口径）。

// 纯文本请求：无 image_tokens 时 ImageInputTokens 为 0，行为不变。

func TestOpenAISelectAccountWithLoadAwareness_FiltersUnschedulable(t *testing.T) {
	now := time.Now()
	resetAt := now.Add(10 * time.Minute)
	groupID := int64(1)

	rateLimited := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
		Platform:         capability.PlatformOpenAI,
		Type:             capability.AccountTypeAPIKey,
		Status:           billing.StatusActive,
		Schedulable:      true,
		Concurrency:      1,
		Priority:         0,
		RateLimitResetAt: &resetAt},
	}
	available := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    1},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Accounts: selectionAccountFixture{accounts: []gatewayprovider.ExecutionAccount{
				rateLimited,
				available}}},
		Shared: Shared{Concurrency: schedulercore.NewConcurrencyService(selectionConcurrencyFixture{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event})},
	}, nil)

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "gpt-5.2", nil)
	if err != nil {
		t.Fatalf("SelectAccountWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.Account == nil {
		t.Fatalf("expected selection with account")
	}
	if selection.Account.Record.ID != available.Record.ID {
		t.Fatalf("expected account %d, got %d", available.Record.ID, selection.Account.Record.ID)
	}
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAISelectAccountWithLoadAwareness_ImageRateLimitSkipsOnlyImageRequests(t *testing.T) {
	future := time.Now().Add(10 * time.Minute).Format(time.RFC3339)
	groupID := int64(1)

	imageLimited := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    0,
		Extra: map[string]any{"model_rate_limits": map[string]any{accountcore.OpenAIImageGenerationRateLimitKey: map[string]any{
			"rate_limit_reset_at": future,
		},
		},
		}},
	}
	available := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    1},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Accounts: selectionAccountFixture{accounts: []gatewayprovider.ExecutionAccount{
				imageLimited,
				available}}},
		Shared: Shared{Concurrency: schedulercore.NewConcurrencyService(selectionConcurrencyFixture{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event})},
	}, nil)

	imageSelection, err := svc.SelectAccountWithLoadAwareness(requeststate.WithOpenAIImageGenerationIntent(context.Background()), &groupID, "", "gpt-5.4", nil)
	require.NoError(t, err)
	require.NotNil(t, imageSelection)
	require.Equal(t, available.Record.ID, imageSelection.Account.Record.ID)
	if imageSelection.ReleaseFunc != nil {
		imageSelection.ReleaseFunc()
	}

	textSelection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "gpt-5.4", nil)
	require.NoError(t, err)
	require.NotNil(t, textSelection)
	require.Equal(t, imageLimited.Record.ID, textSelection.Account.Record.ID)
	if textSelection.ReleaseFunc != nil {
		textSelection.ReleaseFunc()
	}
}

func TestOpenAISelectAccountWithLoadAwareness_FiltersUnschedulableWhenNoConcurrencyService(t *testing.T) {
	now := time.Now()
	resetAt := now.Add(10 * time.Minute)
	groupID := int64(1)

	rateLimited := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
		Platform:         capability.PlatformOpenAI,
		Type:             capability.AccountTypeAPIKey,
		Status:           billing.StatusActive,
		Schedulable:      true,
		Concurrency:      1,
		Priority:         0,
		RateLimitResetAt: &resetAt},
	}
	available := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Priority:    1},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Accounts: selectionAccountFixture{accounts: []gatewayprovider.ExecutionAccount{
				rateLimited,
				available}}},
		Shared: Shared{},
	}, nil)

	// concurrencyService is nil, forcing the non-load-batch selection path.

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "gpt-5.2", nil)
	if err != nil {
		t.Fatalf("SelectAccountWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.Account == nil {
		t.Fatalf("expected selection with account")
	}
	if selection.Account.Record.ID != available.Record.ID {
		t.Fatalf("expected account %d, got %d", available.Record.ID, selection.Account.Record.ID)
	}
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAISelectAccountForModelWithExclusions_StickyUnschedulableClearsSession(t *testing.T) {
	sessionHash := "session-1"
	repo := selectionAccountFixture{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusDisabled, Schedulable: true, Concurrency: 1}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: 1},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{
		Accounts: repo,
	}, Shared: Shared{Cache: cache}}, nil,
	)

	acc, err := svc.SelectAccountForModelWithExclusions(context.Background(), nil, sessionHash, "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectAccountForModelWithExclusions error: %v", err)
	}
	if acc == nil || acc.Record.ID != 2 {
		t.Fatalf("expected account 2, got %+v", acc)
	}
	if cache.deletedSessions["openai:"+sessionHash] != 1 {
		t.Fatalf("expected sticky session to be deleted")
	}
	if cache.sessionBindings["openai:"+sessionHash] != 2 {
		t.Fatalf("expected sticky session to bind to account 2")
	}
}

func TestOpenAISelectAccountForModelWithExclusions_StickyOutsideGroupClearsSession(t *testing.T) {
	sessionHash := "session-outside-group"
	groupID := int64(1001)
	repo := groupAwareStubOpenAIAccountRepo{
		selectionAccountFixture{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, AccountGroups: []accountcore.GroupMembership{{GroupID: groupID}}}},
			},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: 1},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{
		Accounts: repo,
	}, Shared: Shared{Cache: cache}}, nil,
	)

	acc, err := svc.SelectAccountForModelWithExclusions(context.Background(), &groupID, sessionHash, "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectAccountForModelWithExclusions error: %v", err)
	}
	if acc == nil || acc.Record.ID != 2 {
		t.Fatalf("expected account 2, got %+v", acc)
	}
	if cache.deletedSessions["openai:"+sessionHash] != 1 {
		t.Fatalf("expected sticky session to be deleted")
	}
	if cache.sessionBindings["openai:"+sessionHash] != 2 {
		t.Fatalf("expected sticky session to bind to account 2")
	}
}

func TestOpenAISelectAccountWithLoadAwareness_StickyUnschedulableClearsSession(t *testing.T) {
	sessionHash := "session-2"
	groupID := int64(1)
	repo := selectionAccountFixture{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusDisabled, Schedulable: true, Concurrency: 1}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: 1},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Accounts: repo,
		},
		Shared: Shared{
			Concurrency: schedulercore.NewConcurrencyService(
				selectionConcurrencyFixture{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf,
					Event: logging.Event}),
			Cache: cache,
		},
	}, nil,
	)

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, sessionHash, "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectAccountWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.Account == nil || selection.Account.Record.ID != 2 {
		t.Fatalf("expected account 2, got %+v", selection)
	}
	if cache.deletedSessions["openai:"+sessionHash] != 1 {
		t.Fatalf("expected sticky session to be deleted")
	}
	if cache.sessionBindings["openai:"+sessionHash] != 2 {
		t.Fatalf("expected sticky session to bind to account 2")
	}
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAISelectAccountWithLoadAwareness_StickyOutsideGroupClearsSession(t *testing.T) {
	sessionHash := "session-load-outside-group"
	groupID := int64(1002)
	repo := groupAwareStubOpenAIAccountRepo{
		selectionAccountFixture{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, AccountGroups: []accountcore.GroupMembership{{GroupID: groupID}}}},
			},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: 1},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Accounts: repo,
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(selectionConcurrencyFixture{}, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, nil,
	)

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, sessionHash, "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectAccountWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.Account == nil || selection.Account.Record.ID != 2 {
		t.Fatalf("expected account 2, got %+v", selection)
	}
	if cache.deletedSessions["openai:"+sessionHash] != 1 {
		t.Fatalf("expected sticky session to be deleted")
	}
	if cache.sessionBindings["openai:"+sessionHash] != 2 {
		t.Fatalf("expected sticky session to bind to account 2")
	}
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAISelectAccountForModelWithExclusions_NoModelSupport(t *testing.T) {
	repo := selectionAccountFixture{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
				Platform:    capability.PlatformOpenAI,
				Status:      billing.StatusActive,
				Schedulable: true,
				Credentials: map[string]any{"model_mapping": map[string]any{"gpt-3.5-turbo": "gpt-3.5-turbo"}}},
			},
		},
	}
	cache := &schedulerTestGatewayCache{}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{
		Accounts: repo,
	}, Shared: Shared{Cache: cache}}, nil,
	)

	acc, err := svc.SelectAccountForModelWithExclusions(context.Background(), nil, "", "gpt-4", nil)
	if err == nil {
		t.Fatalf("expected error for unsupported model")
	}
	if acc != nil {
		t.Fatalf("expected nil account for unsupported model")
	}
	if !strings.Contains(err.Error(), "does not support the requested model") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenAISelectAccountWithScheduler_GroupModelUnsupportedError(t *testing.T) {
	groupID := int64(1)
	repo := selectionAccountFixture{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
				Platform:    capability.PlatformOpenAI,
				Status:      billing.StatusActive,
				Schedulable: true,
				Credentials: map[string]any{
					"model_whitelist": []any{"gpt-5.4", "gpt-5.4-mini"},
				}},
			},
		},
	}
	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{
		Accounts: repo,
	}, Shared: Shared{}}, nil)

	selection, _, err := svc.SelectAccountWithSchedulerForCapability(
		context.Background(),
		&groupID,
		"",
		"",
		"o1-preview",
		nil, egress.OpenAIUpstreamTransportAny, accountcore.OpenAIEndpointCapabilityTextGeneration,
		false,
		false,
	)
	if err == nil {
		t.Fatalf("expected group model unsupported error")
	}
	if selection != nil {
		t.Fatalf("expected nil selection")
	}
	var modelErr *routing.GroupModelUnsupportedError
	if !errors.As(err, &modelErr) {
		t.Fatalf("expected GroupModelUnsupportedError, got %T: %v", err, err)
	}
	require.Equal(t, "o1-preview", modelErr.RequestedModel)
	require.Equal(t, []string{"gpt-5.4", "gpt-5.4-mini"}, modelErr.AvailableModels)
	require.Contains(t, err.Error(), `The current group does not support the requested model "o1-preview"`)
	require.Contains(t, err.Error(), "Available models: gpt-5.4, gpt-5.4-mini")
}

func TestOpenAISelectAccountWithLoadAwareness_LoadBatchErrorFallback(t *testing.T) {
	groupID := int64(1)
	repo := selectionAccountFixture{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 2}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{}
	concurrencyCache := selectionConcurrencyFixture{
		loadBatchErr: errors.New("load batch failed"),
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Accounts: repo,
		},
		Shared: Shared{
			Concurrency: schedulercore.NewConcurrencyService(
				concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf,
					Event: logging.Event}),
			Cache: cache,
		},
	}, nil)

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "fallback", "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectAccountWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.Account == nil {
		t.Fatalf("expected selection")
	}
	if selection.Account.Record.ID != 2 {
		t.Fatalf("expected account 2, got %d", selection.Account.Record.ID)
	}
	if cache.sessionBindings["openai:fallback"] != 2 {
		t.Fatalf("expected sticky session updated")
	}
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAISelectAccountWithLoadAwareness_NoSlotFallbackWait(t *testing.T) {
	groupID := int64(1)
	repo := selectionAccountFixture{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{}
	concurrencyCache := selectionConcurrencyFixture{
		acquireResults: map[int64]bool{1: false},
		loadMap: map[int64]*schedulercore.AccountLoadInfo{
			1: {AccountID: 1, LoadRate: 10},
		},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Accounts: repo,
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, nil)

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectAccountWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.WaitPlan == nil {
		t.Fatalf("expected wait plan fallback")
	}
	if selection.Account == nil || selection.Account.Record.ID != 1 {
		t.Fatalf("expected account 1")
	}
}

func TestOpenAISelectAccountForModelWithExclusions_SetsStickyBinding(t *testing.T) {
	sessionHash := "bind"
	repo := selectionAccountFixture{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{
		Accounts: repo,
	}, Shared: Shared{Cache: cache}}, nil,
	)

	acc, err := svc.SelectAccountForModelWithExclusions(context.Background(), nil, sessionHash, "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectAccountForModelWithExclusions error: %v", err)
	}
	if acc == nil || acc.Record.ID != 1 {
		t.Fatalf("expected account 1")
	}
	if cache.sessionBindings["openai:"+sessionHash] != 1 {
		t.Fatalf("expected sticky session binding")
	}
}

func TestOpenAISelectAccountWithLoadAwareness_StickyWaitPlan(t *testing.T) {
	sessionHash := "sticky-wait"
	groupID := int64(1)
	repo := selectionAccountFixture{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: 1},
	}
	concurrencyCache := selectionConcurrencyFixture{
		acquireResults: map[int64]bool{1: false},
		waitCounts:     map[int64]int{1: 0},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Accounts: repo,
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, nil)

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, sessionHash, "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectAccountWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.WaitPlan == nil {
		t.Fatalf("expected sticky wait plan")
	}
	if selection.Account == nil || selection.Account.Record.ID != 1 {
		t.Fatalf("expected account 1")
	}
}

func TestOpenAISelectAccountWithLoadAwareness_StickyCapacitySpilloverKeepsBinding(t *testing.T) {
	sessionHash := "sticky-spillover"
	groupID := int64(1)
	repo := selectionAccountFixture{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 6, Priority: 1, GroupIDs: []int64{groupID}}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 6, Priority: 1, GroupIDs: []int64{groupID}}},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: 1},
	}
	concurrencyCache := selectionConcurrencyFixture{
		acquireResults: map[int64]bool{1: false, 2: true},
		waitCounts:     map[int64]int{1: 1},
		loadMap: map[int64]*schedulercore.AccountLoadInfo{
			1: {AccountID: 1, LoadRate: 100},
			2: {AccountID: 2, LoadRate: 10},
		},
	}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	cfg.Gateway.Scheduling.StickySessionMaxWaiting = 1

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Accounts: repo,
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, cfg)

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, sessionHash, "gpt-4", nil)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(2), selection.Account.Record.ID, "capacity spillover should use the other account for this request")
	require.True(t, selection.Acquired)
	require.Equal(t, int64(1), cache.sessionBindings["openai:"+sessionHash], "capacity spillover must not migrate the durable sticky binding")
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAISelectAccountWithLoadAwareness_PrefersLowerLoad(t *testing.T) {
	groupID := int64(1)
	repo := selectionAccountFixture{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{}
	concurrencyCache := selectionConcurrencyFixture{
		loadMap: map[int64]*schedulercore.AccountLoadInfo{
			1: {AccountID: 1, LoadRate: 80},
			2: {AccountID: 2, LoadRate: 10},
		},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Accounts: repo,
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, nil)

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "load", "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectAccountWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.Account == nil || selection.Account.Record.ID != 2 {
		t.Fatalf("expected account 2")
	}
	if cache.sessionBindings["openai:load"] != 2 {
		t.Fatalf("expected sticky session updated")
	}
}

func TestOpenAISelectAccountForModelWithExclusions_StickyExcludedFallback(t *testing.T) {
	sessionHash := "excluded"
	repo := selectionAccountFixture{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 2}},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: 1},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{
		Accounts: repo,
	}, Shared: Shared{Cache: cache}}, nil,
	)

	excluded := map[int64]struct{}{1: {}}
	acc, err := svc.SelectAccountForModelWithExclusions(context.Background(), nil, sessionHash, "gpt-4", excluded)
	if err != nil {
		t.Fatalf("SelectAccountForModelWithExclusions error: %v", err)
	}
	if acc == nil || acc.Record.ID != 2 {
		t.Fatalf("expected account 2")
	}
}

func TestOpenAISelectAccountForModelWithExclusions_StickyNonOpenAI(t *testing.T) {
	sessionHash := "non-openai"
	repo := selectionAccountFixture{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 2}},
		},
	}
	cache := &schedulerTestGatewayCache{
		sessionBindings: map[string]int64{"openai:" + sessionHash: 1},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{
		Accounts: repo,
	}, Shared: Shared{Cache: cache}}, nil,
	)

	acc, err := svc.SelectAccountForModelWithExclusions(context.Background(), nil, sessionHash, "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectAccountForModelWithExclusions error: %v", err)
	}
	if acc == nil || acc.Record.ID != 2 {
		t.Fatalf("expected account 2")
	}
}

func TestOpenAISelectAccountForModelWithExclusions_NoAccounts(t *testing.T) {
	repo := selectionAccountFixture{accounts: []gatewayprovider.ExecutionAccount{}}
	cache := &schedulerTestGatewayCache{}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{
		Accounts: repo,
	}, Shared: Shared{Cache: cache}}, nil,
	)

	acc, err := svc.SelectAccountForModelWithExclusions(context.Background(), nil, "", "", nil)
	if err == nil {
		t.Fatalf("expected error for no accounts")
	}
	if acc != nil {
		t.Fatalf("expected nil account")
	}
	if !strings.Contains(err.Error(), "no available OpenAI accounts") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenAISelectAccountWithLoadAwareness_NoCandidates(t *testing.T) {
	groupID := int64(1)
	resetAt := time.Now().Add(1 * time.Hour)
	repo := selectionAccountFixture{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, RateLimitResetAt: &resetAt}},
		},
	}
	cache := &schedulerTestGatewayCache{}
	concurrencyCache := selectionConcurrencyFixture{}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Accounts: repo,
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, nil)

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "gpt-4", nil)
	if err == nil {
		t.Fatalf("expected error for no candidates")
	}
	if selection != nil {
		t.Fatalf("expected nil selection")
	}
}

func TestOpenAISelectAccountWithLoadAwareness_AllFullWaitPlan(t *testing.T) {
	groupID := int64(1)
	repo := selectionAccountFixture{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{}
	concurrencyCache := selectionConcurrencyFixture{
		loadMap: map[int64]*schedulercore.AccountLoadInfo{
			1: {AccountID: 1, LoadRate: 100},
		},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Accounts: repo,
		},
		Shared: Shared{
			Concurrency: schedulercore.NewConcurrencyService(
				concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf,
					Event: logging.Event}),
			Cache: cache,
		},
	}, nil)

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectAccountWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.WaitPlan == nil {
		t.Fatalf("expected wait plan")
	}
}

func TestOpenAISelectAccountWithLoadAwareness_LoadBatchErrorNoAcquire(t *testing.T) {
	groupID := int64(1)
	repo := selectionAccountFixture{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{}
	concurrencyCache := selectionConcurrencyFixture{
		loadBatchErr:   errors.New("load batch failed"),
		acquireResults: map[int64]bool{1: false},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Accounts: repo,
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, nil)

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectAccountWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.WaitPlan == nil {
		t.Fatalf("expected wait plan")
	}
}

func TestOpenAISelectAccountWithLoadAwareness_MissingLoadInfo(t *testing.T) {
	groupID := int64(1)
	repo := selectionAccountFixture{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{}
	concurrencyCache := selectionConcurrencyFixture{
		loadMap: map[int64]*schedulercore.AccountLoadInfo{
			1: {AccountID: 1, LoadRate: 50},
		},
		skipDefaultLoad: true,
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Accounts: repo,
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, nil)

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectAccountWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.Account == nil || selection.Account.Record.ID != 2 {
		t.Fatalf("expected account 2")
	}
}

func TestOpenAISelectAccountForModelWithExclusions_LeastRecentlyUsed(t *testing.T) {
	oldTime := time.Now().Add(-2 * time.Hour)
	newTime := time.Now().Add(-1 * time.Hour)
	repo := selectionAccountFixture{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Priority: 1, LastUsedAt: &newTime}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Priority: 1, LastUsedAt: &oldTime}},
		},
	}
	cache := &schedulerTestGatewayCache{}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{Reads: Reads{
		Accounts: repo,
	}, Shared: Shared{Cache: cache}}, nil,
	)

	acc, err := svc.SelectAccountForModelWithExclusions(context.Background(), nil, "", "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectAccountForModelWithExclusions error: %v", err)
	}
	if acc == nil || acc.Record.ID != 2 {
		t.Fatalf("expected account 2")
	}
}

func TestOpenAISelectAccountWithLoadAwareness_PreferNeverUsed(t *testing.T) {
	groupID := int64(1)
	lastUsed := time.Now().Add(-1 * time.Hour)
	repo := selectionAccountFixture{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, LastUsedAt: &lastUsed}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformOpenAI, Status: billing.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1}},
		},
	}
	cache := &schedulerTestGatewayCache{}
	concurrencyCache := selectionConcurrencyFixture{
		loadMap: map[int64]*schedulercore.AccountLoadInfo{
			1: {AccountID: 1, LoadRate: 10},
			2: {AccountID: 2, LoadRate: 10},
		},
	}

	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Accounts: repo,
		},
		Shared: Shared{
			Cache:       cache,
			Concurrency: schedulercore.NewConcurrencyService(concurrencyCache, schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
		},
	}, nil)

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectAccountWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.Account == nil || selection.Account.Record.ID != 2 {
		t.Fatalf("expected account 2")
	}
}

// 本用例需要把两次循环视为独立故障，关闭生产环境的并发断流折叠窗口。

// 池模式的瞬态容量错误即使未显式配置 502，也应在同一账号上受限重试。

// 流内 rate limit 进入 OAuth 同账号重试窗口，但不立即写账号级限流/封禁状态：
// HTTP 200 流的 x-codex-* 头不能让窗口内的账号提前失去调度资格。

// 命中透传规则也应记录 ops 上游错误事件（对齐 CC/Messages 与 antigravity 先例）。

// 本用例需要把两次循环视为独立故障，关闭生产环境的并发断流折叠窗口。

// 命中透传规则也应记录 ops 上游错误事件（对齐 CC/Messages 与 antigravity 先例）。

// 写入超过 MaxLineSize 的单行数据，触发 ErrTooLong

// 上游要求 originator 与最终 User-Agent 首段配套（issue #3901）：
// originator 一律由最终 UA 推导；推导不出官方身份时整体回退默认 Codex TUI 身份。

// ==================== P1-08 修复：model 替换性能优化测试 ====================

// ==================== P1-08 修复：model 替换性能优化测试 =============

// 非终态事件中的显式 usage 作为兼容 fallback，非零字段会被合并。

// completed 事件，应提取 usage

// done 事件同样可能携带最终 usage

// failed 事件在部分上游路径也会携带已消耗 usage，应与 WS/passthrough 保持一致

// xAI 可能在可见 output_tokens 外单独返回 reasoning_tokens；仅算术一致的
// 形态视为独立字段，OpenAI 标准形态已将推理 token 纳入 completion/output。

// Header 可能由上游 Content-Type 透传；关键是 body 已转换为最终 JSON 响应。

// Compact JSON 输出文本可以包含 data:/event: 普通字样，但不能因此被误判为 SSE。

// 响应必须保持原始 JSON，不能经 SSE 路径改写或丢失用量。
