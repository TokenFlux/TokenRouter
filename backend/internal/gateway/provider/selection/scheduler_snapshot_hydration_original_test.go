//go:build unit

package selection

import (
	"context"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerredis "github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"

	"github.com/TokenFlux/TokenRouter/internal/config"
)

// snapshotHydrationCache 提供原轻量/完整投影，读取继续经过真实 SnapshotService。
type snapshotHydrationCache struct {
	scheduler.SnapshotCache
	snapshot []*gatewayprovider.ExecutionAccount
	accounts map[int64]*gatewayprovider.ExecutionAccount
}

func (c *snapshotHydrationCache) GetSnapshot(context.Context, scheduler.SchedulerBucket) ([]scheduler.SnapshotAccount, bool, error) {
	out := make([]scheduler.SnapshotAccount, 0, len(c.snapshot))
	for _, v := range c.snapshot {
		out = append(out, codec.WrapRecord(gatewayprovider.ExecutionRecord(v)))
	}
	return out, true, nil
}
func (c *snapshotHydrationCache) GetAccount(_ context.Context, id int64) (scheduler.SnapshotAccount, error) {
	return codec.WrapRecord(gatewayprovider.ExecutionRecord(c.accounts[id])), nil
}

func TestOpenAISelectAccountWithLoadAwareness_HydratesSelectedAccountFromSchedulerSnapshot(t *testing.T) {
	cache := &snapshotHydrationCache{
		snapshot: []*gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.AccountTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    1,
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"gpt-4": "gpt-4",
					},
				}},
			},
		},
		accounts: map[int64]*gatewayprovider.ExecutionAccount{
			1: {Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
				Platform:    capability.PlatformOpenAI,
				Type:        capability.AccountTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    1,
				Credentials: map[string]any{
					"api_key":       "sk-live",
					"model_mapping": map[string]any{"gpt-4": "gpt-4"},
				}},
			},
		},
	}

	schedulerSnapshot := newHydrationSnapshotForTest(cache, nil)
	groupID := int64(2)
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Snapshot: schedulerredis.NewSnapshotReader(schedulerSnapshot)},
		Shared: Shared{Cache: &responseCacheFixture{}},
	}, nil)

	selection, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "gpt-4", nil)
	if err != nil {
		t.Fatalf("SelectAccountWithLoadAwareness error: %v", err)
	}
	if selection == nil || selection.Account == nil {
		t.Fatalf("expected selected account")
	}
	if got := selection.Account.View().GetOpenAIApiKey(); got != "sk-live" {
		t.Fatalf("expected hydrated api key, got %q", got)
	}
}

func TestOpenAINewAcquiredSelectionResult_ReleasesSlotWhenHydrationFails(t *testing.T) {
	cache := &snapshotHydrationCache{
		accounts: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	schedulerSnapshot := newHydrationSnapshotForTest(cache, selectionAccountFixture{})
	svc := newCompatibleSelectionForTest(CompatibleDependencies{
		Reads: Reads{
			Snapshot: schedulerredis.NewSnapshotReader(schedulerSnapshot)},
		Shared: Shared{},
	}, nil)

	releaseCalls := 0

	selection, err := svc.newAcquiredSelectionResult(context.Background(), &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1001}}, func() {
		releaseCalls++
	})

	if err == nil {
		t.Fatalf("expected hydration error")
	}
	if selection != nil {
		t.Fatalf("expected nil selection on hydration error")
	}
	if releaseCalls != 1 {
		t.Fatalf("expected release to be called once, got %d", releaseCalls)
	}
}

func TestGatewaySelectAccountWithLoadAwareness_HydratesSelectedAccountFromSchedulerSnapshot(t *testing.T) {
	cache := &snapshotHydrationCache{
		snapshot: []*gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9,
				Platform:    capability.PlatformAnthropic,
				Type:        capability.AccountTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    1},
			},
		},
		accounts: map[int64]*gatewayprovider.ExecutionAccount{
			9: {Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 9,
				Platform:    capability.PlatformAnthropic,
				Type:        capability.AccountTypeAPIKey,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    1,
				Credentials: map[string]any{
					"api_key": "anthropic-live-key",
				}},
			},
		},
	}

	schedulerSnapshot := newHydrationSnapshotForTest(cache, nil)
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads:  Reads{Snapshot: schedulerredis.NewSnapshotReader(schedulerSnapshot)},
		Shared: Shared{Cache: &mockGatewayCacheForPlatform{}},
	}, testConfig())

	result, err := svc.SelectAccountWithLoadAwareness(context.Background(), nil, "", "claude-3-5-sonnet-20241022", nil, "", 0)
	if err != nil {
		t.Fatalf("SelectAccountWithLoadAwareness error: %v", err)
	}
	if result == nil || result.Account == nil {
		t.Fatalf("expected selected account")
	}
	if got := result.Account.View().GetCredential("api_key"); got != "anthropic-live-key" {
		t.Fatalf("expected hydrated api key, got %q", got)
	}
}

func TestGatewaySelectAccountWithLoadAwareness_SkipsAntigravityGeminiFamilyRateLimitedSnapshot(t *testing.T) {
	resetAt := time.Now().Add(10 * time.Minute).Format(time.RFC3339)
	cache := &snapshotHydrationCache{
		snapshot: []*gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
				Platform:    capability.PlatformAntigravity,
				Type:        capability.AccountTypeOAuth,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    1,
				AccountGroups: []accountcore.GroupMembership{
					{AccountID: 1, GroupID: 22},
				},
				GroupIDs: []int64{22},
				Extra: map[string]any{
					"mixed_scheduling": true, "model_rate_limits": map[string]any{"antigravity:gemini": map[string]any{
						"rate_limit_reset_at": resetAt,
					},
					},
				}},
			},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2,
				Platform:    capability.PlatformAntigravity,
				Type:        capability.AccountTypeOAuth,
				Status:      billing.StatusActive,
				Schedulable: true,
				Concurrency: 1,
				Priority:    2,
				AccountGroups: []accountcore.GroupMembership{
					{AccountID: 2, GroupID: 22},
				},
				GroupIDs: []int64{22},
				Extra: map[string]any{
					"mixed_scheduling": true,
				}},
			},
		},
		accounts: map[int64]*gatewayprovider.ExecutionAccount{
			1: {Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAntigravity, Type: capability.AccountTypeOAuth}},
			2: {Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAntigravity, Type: capability.AccountTypeOAuth}},
		},
	}
	groupID := int64(22)
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{
			Groups: &mockGroupRepoForGateway{groups: map[int64]*routing.Group{groupID: {ID: groupID,
				Platform: capability.PlatformGemini, Status: billing.StatusActive, Hydrated: true}}},
			Snapshot: schedulerredis.NewSnapshotReader(newHydrationSnapshotForTest(cache, nil)),
		},
		Shared: Shared{Concurrency: scheduler.NewConcurrencyService(&mockConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event})},
	}, &config.Config{Gateway: config.GatewayConfig{Scheduling: config.GatewaySchedulingConfig{LoadBatchEnabled: true, StickySessionMaxWaiting: 3, StickySessionWaitTimeout: time.Second,
		FallbackWaitTimeout: time.Second, FallbackMaxWaiting: 10}}})

	result, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "gemini-3-flash-preview", nil, "", 0)
	if err != nil {
		t.Fatalf("SelectAccountWithLoadAwareness error: %v", err)
	}
	if result == nil || result.Account == nil {
		t.Fatalf("expected selected account")
	}
	if result.Account.Record.ID != 2 {
		t.Fatalf("expected scheduler to skip Gemini-family limited antigravity account 1, got %d", result.Account.Record.ID)
	}
}

// 已取得账号槽后读取完整账号失败，错误返回前必须归还一次。
func TestGatewayNewSelectionResultReleasesSlotWhenHydrationFails(t *testing.T) {
	cache := &snapshotHydrationCache{accounts: map[int64]*gatewayprovider.ExecutionAccount{}}
	snapshot := newHydrationSnapshotForTest(cache, selectionAccountFixture{})
	gateway := newGenericSelectionForTest(GenericDependencies{
		Reads:  Reads{Snapshot: schedulerredis.NewSnapshotReader(snapshot)},
		Shared: Shared{},
	}, nil)

	calls := 0
	result, err := gateway.newSelectionResult(context.Background(), &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1001}}, true, func() { calls++ }, nil)
	if err == nil || result != nil {
		t.Fatal("补全失败必须返回原错误而非选择结果")
	}
	if calls != 1 {
		t.Fatalf("释放次数=%d，期望 1", calls)
	}
}

func newHydrationSnapshotForTest(cache *snapshotHydrationCache, source Accounts) *scheduler.SnapshotService {
	var read scheduler.SnapshotAccountSource
	if source != nil {
		read = hydrationAccountSource{source: source}
	}
	return scheduler.NewSnapshotService(cache, nil, read, nil, nil, scheduler.SnapshotBindings{AccountNotFound: accountcore.ErrAccountNotFound, GroupNotFound: routing.ErrGroupNotFound, Diagnostics: scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}})
}
