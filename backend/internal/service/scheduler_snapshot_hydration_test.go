//go:build unit

package service

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

	"github.com/TokenFlux/TokenRouter/internal/config"
)

type snapshotHydrationCache struct {
	snapshot []*gatewayprovider.ExecutionAccount
	accounts map[int64]*gatewayprovider.ExecutionAccount
}

func (c *snapshotHydrationCache) GetSnapshot(ctx context.Context, bucket scheduler.SchedulerBucket) ([]*gatewayprovider.ExecutionAccount, bool, error) {
	return c.snapshot, true, nil
}

func (c *snapshotHydrationCache) CaptureBucketWriteToken(ctx context.Context, bucket scheduler.SchedulerBucket) (scheduler.SchedulerBucketWriteToken, error) {
	return scheduler.SchedulerBucketWriteToken{Bucket: bucket, Epoch: 1}, nil
}

func (c *snapshotHydrationCache) SetSnapshot(ctx context.Context, bucket scheduler.SchedulerBucket, token scheduler.SchedulerBucketWriteToken, accounts []gatewayprovider.ExecutionAccount) error {
	return nil
}

func (c *snapshotHydrationCache) RetireBucket(ctx context.Context, bucket scheduler.SchedulerBucket) error {
	return nil
}

func (c *snapshotHydrationCache) ReopenBucket(ctx context.Context, bucket scheduler.SchedulerBucket) (scheduler.SchedulerBucketWriteToken, error) {
	return scheduler.SchedulerBucketWriteToken{Bucket: bucket, Epoch: 1}, nil
}

func (c *snapshotHydrationCache) TryAcquireGroupLifecycleLease(context.Context, int64, time.Duration) (scheduler.SchedulerGroupLifecycleLease, bool, error) {
	return scheduler.SchedulerGroupLifecycleLease{}, false, nil
}

func (c *snapshotHydrationCache) ReleaseGroupLifecycleLease(context.Context, scheduler.SchedulerGroupLifecycleLease) error {
	return nil
}

func (c *snapshotHydrationCache) GetAccount(ctx context.Context, accountID int64) (*gatewayprovider.ExecutionAccount, error) {
	if c.accounts == nil {
		return nil, nil
	}
	return c.accounts[accountID], nil
}

func (c *snapshotHydrationCache) SetAccount(ctx context.Context, account *gatewayprovider.ExecutionAccount) error {
	return nil
}

func (c *snapshotHydrationCache) DeleteAccount(ctx context.Context, accountID int64) error {
	return nil
}

func (c *snapshotHydrationCache) UpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error {
	return nil
}

func (c *snapshotHydrationCache) TryLockBucket(ctx context.Context, bucket scheduler.SchedulerBucket, ttl time.Duration) (bool, error) {
	return true, nil
}

func (c *snapshotHydrationCache) UnlockBucket(ctx context.Context, bucket scheduler.SchedulerBucket) error {
	return nil
}

func (c *snapshotHydrationCache) ListBuckets(ctx context.Context) ([]scheduler.SchedulerBucket, error) {
	return nil, nil
}

func (c *snapshotHydrationCache) GetOutboxWatermark(ctx context.Context) (int64, error) {
	return 0, nil
}

func (c *snapshotHydrationCache) SetOutboxWatermark(ctx context.Context, id int64) error {
	return nil
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

	schedulerSnapshot := NewSchedulerSnapshotService(cache, nil, nil, nil, nil)
	groupID := int64(2)
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		schedulerSnapshot: schedulerSnapshot,
		cache:             &stubGatewayCache{},
	})

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
	schedulerSnapshot := NewSchedulerSnapshotService(cache, nil, stubOpenAIAccountRepo{}, nil, nil)
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		schedulerSnapshot: schedulerSnapshot,
	})
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

	schedulerSnapshot := NewSchedulerSnapshotService(cache, nil, nil, nil, nil)
	svc := withSchedulerParametersForTest(&GatewayService{
		schedulerSnapshot: schedulerSnapshot,
		cache:             &mockGatewayCacheForPlatform{},
		cfg:               testConfig(),
	})

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
	svc := withSchedulerParametersForTest(&GatewayService{
		schedulerSnapshot: NewSchedulerSnapshotService(cache, nil, nil, nil, nil),
		groupRepo: &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:       groupID,
					Platform: capability.PlatformGemini,
					Status:   billing.StatusActive,
					Hydrated: true,
				},
			},
		},
		concurrencyService: scheduler.NewConcurrencyService(&mockConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,
			Event: logging.Event,
		},
		),
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				Scheduling: config.GatewaySchedulingConfig{
					LoadBatchEnabled:         true,
					StickySessionMaxWaiting:  3,
					StickySessionWaitTimeout: time.Second,
					FallbackWaitTimeout:      time.Second,
					FallbackMaxWaiting:       10,
				},
			},
		},
	})

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

// B03：已取得账号槽后读取完整账号失败，错误返回前必须归还一次。
func TestGatewayNewSelectionResultReleasesSlotWhenHydrationFails(t *testing.T) {
	cache := &snapshotHydrationCache{accounts: map[int64]*gatewayprovider.ExecutionAccount{}}
	snapshot := NewSchedulerSnapshotService(cache, nil, stubOpenAIAccountRepo{}, nil, nil)
	gateway := withSchedulerParametersForTest(&GatewayService{schedulerSnapshot: snapshot})
	calls := 0
	result, err := gateway.newSelectionResult(context.Background(), &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1001}}, true, func() { calls++ }, nil)
	if err == nil || result != nil {
		t.Fatal("补全失败必须返回原错误而非选择结果")
	}
	if calls != 1 {
		t.Fatalf("释放次数=%d，期望 1", calls)
	}
}

// 夹具适配本次持有者句柄，继续沿用原锁失败/等待控制和断言。
func (c *snapshotHydrationCache) AcquireBucketLease(ctx context.Context, bucket scheduler.SchedulerBucket, ttl time.Duration) (*scheduler.BucketLease, bool, error) {
	ok, err := c.TryLockBucket(ctx, bucket, ttl)
	if err != nil || !ok {
		return nil, ok, err
	}
	return scheduler.NewBucketLease(func(cleanup context.Context) error { return c.UnlockBucket(cleanup, bucket) }), true, nil
}
