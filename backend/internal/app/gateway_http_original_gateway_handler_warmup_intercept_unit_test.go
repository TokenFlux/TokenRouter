//go:build unit

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 目标：严格验证“antigravity 账号通过 /v1/messages 提供 Claude 服务时”，
// 当账号 credentials.intercept_warmup_requests=true 且请求为 Warmup 时，
// 后端会在转发上游前直接拦截并返回 mock 响应（不依赖上游）。

type fakeSchedulerCache struct {
	accounts []*gatewayprovider.ExecutionAccount
}

func (f *fakeSchedulerCache) GetSnapshot(_ context.Context, _ scheduler.SchedulerBucket) ([]scheduler.SnapshotAccount, bool, error) {
	return service.LegacySnapshotWrapPointers(f.accounts), true, nil
}
func (f *fakeSchedulerCache) CaptureBucketWriteToken(_ context.Context, bucket scheduler.SchedulerBucket) (scheduler.SchedulerBucketWriteToken, error) {
	return scheduler.SchedulerBucketWriteToken{Bucket: bucket, Epoch: 1}, nil
}
func (f *fakeSchedulerCache) SetSnapshot(_ context.Context, _ scheduler.SchedulerBucket, _ scheduler.SchedulerBucketWriteToken, _ []scheduler.SnapshotAccount) error {
	return nil
}
func (f *fakeSchedulerCache) RetireBucket(_ context.Context, _ scheduler.SchedulerBucket) error {
	return nil
}
func (f *fakeSchedulerCache) ReopenBucket(_ context.Context, bucket scheduler.SchedulerBucket) (scheduler.SchedulerBucketWriteToken, error) {
	return scheduler.SchedulerBucketWriteToken{Bucket: bucket, Epoch: 1}, nil
}
func (f *fakeSchedulerCache) TryAcquireGroupLifecycleLease(_ context.Context, _ int64, _ time.Duration) (scheduler.SchedulerGroupLifecycleLease, bool, error) {
	return scheduler.SchedulerGroupLifecycleLease{}, false, nil
}
func (f *fakeSchedulerCache) ReleaseGroupLifecycleLease(_ context.Context, _ scheduler.SchedulerGroupLifecycleLease) error {
	return nil
}
func (f *fakeSchedulerCache) GetAccount(_ context.Context, id int64) (scheduler.SnapshotAccount, error) {
	for _, account := range f.accounts {
		if account != nil && account.Record.ID == id {
			return service.LegacySnapshotWrap(account), nil
		}
	}
	return nil, nil
}
func (f *fakeSchedulerCache) SetAccount(_ context.Context, _ scheduler.SnapshotAccount) error {
	return nil
}
func (f *fakeSchedulerCache) DeleteAccount(_ context.Context, _ int64) error { return nil }
func (f *fakeSchedulerCache) UpdateLastUsed(_ context.Context, _ map[int64]time.Time) error {
	return nil
}
func (f *fakeSchedulerCache) TryLockBucket(_ context.Context, _ scheduler.SchedulerBucket, _ time.Duration) (bool, error) {
	return true, nil
}
func (f *fakeSchedulerCache) UnlockBucket(_ context.Context, _ scheduler.SchedulerBucket) error {
	return nil
}
func (f *fakeSchedulerCache) ListBuckets(_ context.Context) ([]scheduler.SchedulerBucket, error) {
	return nil, nil
}
func (f *fakeSchedulerCache) GetOutboxWatermark(_ context.Context) (int64, error) { return 0, nil }
func (f *fakeSchedulerCache) SetOutboxWatermark(_ context.Context, _ int64) error { return nil }

type fakeGroupRepo struct {
	group *routing.Group
}

func (f *fakeGroupRepo) Create(context.Context, *routing.Group) error { return nil }
func (f *fakeGroupRepo) GetByID(context.Context, int64) (*routing.Group, error) {
	return f.group, nil
}
func (f *fakeGroupRepo) GetByIDLite(context.Context, int64) (*routing.Group, error) {
	return f.group, nil
}
func (f *fakeGroupRepo) Update(context.Context, *routing.Group) error          { return nil }
func (f *fakeGroupRepo) Delete(context.Context, int64) error                   { return nil }
func (f *fakeGroupRepo) DeleteCascade(context.Context, int64) ([]int64, error) { return nil, nil }
func (f *fakeGroupRepo) List(context.Context, pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (f *fakeGroupRepo) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string, *bool) ([]routing.Group, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (f *fakeGroupRepo) ListActive(context.Context) ([]routing.Group, error) { return nil, nil }
func (f *fakeGroupRepo) ListActiveByPlatform(context.Context, string) ([]routing.Group, error) {
	return nil, nil
}
func (f *fakeGroupRepo) ListActiveByPlatformLite(ctx context.Context, platform string) ([]routing.Group, error) {
	return f.ListActiveByPlatform(ctx, platform)
}
func (f *fakeGroupRepo) ExistsByName(context.Context, string) (bool, error) { return false, nil }
func (f *fakeGroupRepo) GetAccountCount(context.Context, int64) (int64, int64, error) {
	return 0, 0, nil
}
func (f *fakeGroupRepo) DeleteAccountGroupsByGroupID(context.Context, int64) (int64, error) {
	return 0, nil
}
func (f *fakeGroupRepo) GetAccountIDsByGroupIDs(context.Context, []int64) ([]int64, error) {
	return nil, nil
}
func (f *fakeGroupRepo) BindAccountsToGroup(context.Context, int64, []int64) error { return nil }
func (f *fakeGroupRepo) UpdateSortOrders(context.Context, []routing.GroupSortOrderUpdate) error {
	return nil
}

type fakeConcurrencyCache struct{}

func (f *fakeConcurrencyCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}
func (f *fakeConcurrencyCache) ReleaseAccountSlot(context.Context, int64, string) error { return nil }
func (f *fakeConcurrencyCache) GetAccountConcurrency(context.Context, int64) (int, error) {
	return 0, nil
}
func (f *fakeConcurrencyCache) IncrementAccountWaitCount(context.Context, int64, int) (bool, error) {
	return true, nil
}
func (f *fakeConcurrencyCache) DecrementAccountWaitCount(context.Context, int64) error { return nil }
func (f *fakeConcurrencyCache) GetAccountWaitingCount(context.Context, int64) (int, error) {
	return 0, nil
}
func (f *fakeConcurrencyCache) AcquireUserSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}
func (f *fakeConcurrencyCache) ReleaseUserSlot(context.Context, int64, string) error   { return nil }
func (f *fakeConcurrencyCache) GetUserConcurrency(context.Context, int64) (int, error) { return 0, nil }
func (f *fakeConcurrencyCache) IncrementWaitCount(context.Context, int64, int) (bool, error) {
	return true, nil
}
func (f *fakeConcurrencyCache) DecrementWaitCount(context.Context, int64) error { return nil }
func (f *fakeConcurrencyCache) GetAccountsLoadBatch(context.Context, []scheduler.AccountWithConcurrency) (map[int64]*scheduler.AccountLoadInfo, error) {
	return map[int64]*scheduler.AccountLoadInfo{}, nil
}
func (f *fakeConcurrencyCache) GetUsersLoadBatch(context.Context, []scheduler.UserWithConcurrency) (map[int64]*scheduler.UserLoadInfo, error) {
	return map[int64]*scheduler.UserLoadInfo{}, nil
}
func (f *fakeConcurrencyCache) GetAccountConcurrencyBatch(_ context.Context, accountIDs []int64) (map[int64]int, error) {
	result := make(map[int64]int, len(accountIDs))
	for _, id := range accountIDs {
		result[id] = 0
	}
	return result, nil
}
func (f *fakeConcurrencyCache) CleanupExpiredAccountSlots(context.Context, int64) error { return nil }
func (f *fakeConcurrencyCache) CleanupExpiredAccountSlotKeys(context.Context) error     { return nil }
func (f *fakeConcurrencyCache) CleanupStaleProcessSlots(context.Context, string) error  { return nil }

func newTestGatewayHandler(t *testing.T, group *routing.Group, accounts []*gatewayprovider.ExecutionAccount) (*messageEndpointsFixture, func()) {
	t.Helper()

	schedulerCache := &fakeSchedulerCache{accounts: accounts}
	schedulerSnapshot := scheduler.NewSnapshotService(schedulerCache, nil, nil, nil, nil, scheduler.SnapshotBindings{})

	gwSvc := service.NewGatewayService(
		nil,                               // accountRepo (not used: scheduler snapshot hit)
		&fakeGroupRepo{group: group}, nil, // usageLogRepo
		// usageBillingRepo
		// userRepo
		// userSubRepo
		// userGroupRateRepo
		nil, // cache (disable sticky)
		nil, // cfg
		schedulerSnapshot,
		nil, // concurrencyService (disable load-aware; tryAcquire always acquired)
		// billingService
		nil, // rateLimitService
		// billingCacheService
		nil,      // identityService
		nil, nil, // httpUpstream
		// deferredService
		nil,      // claudeTokenProvider
		nil, nil, // sessionLimitCache
		nil, // rpmCache
		nil, // digestStore
		nil, // settingService
		nil, // tlsFPProfileService
		nil, // channelService
		nil, // resolver
		// balanceNotifyService
		responseHeaderFilterForTest(nil),

		// userPlatformQuotaRepo
	)
	gwSvc.

		// RunModeSimple：跳过计费检查，避免引入 repo/cache 依赖。
		BindCompletionRecorder(newHTTPCompletionFixture(nil, nil,

			nil,

			nil,

			nil,

			nil, nil, false))

	cfg := &config.Config{RunMode: config.RunModeSimple}
	billingCacheSvc := newBillingEligibilityFixture(cfg)
	billingCacheSvc.Start()

	concurrencySvc := scheduler.NewConcurrencyService(&fakeConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,
		Event: logging.Event,
	},
	)
	concurrencyHelper := gatewayhttp.NewConcurrencyHelper(concurrencySvc, gatewayhttp.SSEPingFormatClaude, 0)

	h := newMessageEndpointsFixture(gwSvc, newFundingAdmissionFixture(billingCacheSvc, cfg), concurrencyHelper, gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: openAITextOptions(nil).MaxBodyBytes, MaxSwitches: 1, MaxGeminiSwitches: 1})

	cleanup := func() {
		billingCacheSvc.Stop()
	}
	return h, cleanup
}

func TestGatewayHandlerMessages_InterceptWarmup_AntigravityAccount_MixedSchedulingV1(t *testing.T) {

	groupID := int64(2001)
	accountID := int64(1001)

	group := &routing.Group{
		ID:       groupID,
		Hydrated: true,
		Platform: capability.PlatformAnthropic, // /v1/messages（Claude兼容）入口
		Status:   billing.StatusActive,
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: accountID,
		Name:     "ag-1",
		Platform: capability.PlatformAntigravity,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":              "tok_xxx",
			"intercept_warmup_requests": true,
		},
		Extra: map[string]any{
			"mixed_scheduling": true, // 关键：允许被 anthropic 分组混合调度选中
		},
		Concurrency:   1,
		Priority:      1,
		Status:        billing.StatusActive,
		Schedulable:   true,
		AccountGroups: []accountcore.GroupMembership{{AccountID: accountID, GroupID: groupID}}},
	}

	h, cleanup := newTestGatewayHandler(t, group, []*gatewayprovider.ExecutionAccount{account})
	defer cleanup()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	body := []byte(`{
		"model": "claude-sonnet-4-5",
		"max_tokens": 256,
		"messages": [{"role":"user","content":[{"type":"text","text":"Warmup"}]}]
	}`)
	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(requeststate.WithGroup(req.Context(), group))
	c.Request = req

	apiKey := &apikey.APIKey{
		ID:      3001,
		UserID:  4001,
		GroupID: &groupID,
		Status:  billing.StatusActive,
		User: &identity.User{
			ID:          4001,
			Concurrency: 10,
			Balance:     100,
		},
		Group: group,
	}

	c.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})

	h.Messages(c)

	require.Equal(t, 200, rec.Code)

	// 断言：确实选中了 antigravity 账号（不是纯函数测试，而是从 Handler 里验证调度结果）
	selected, ok := c.Get(gatewayhttp.OpsAccountIDKey)
	require.True(t, ok)
	require.Equal(t, accountID, selected)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Regexp(t, `^msg_01[0-9A-Za-z]{22}$`, resp["id"])
	require.Equal(t, "claude-sonnet-4-5", resp["model"])

	content, ok := resp["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	first, ok := content[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "New Conversation", first["text"])
}

func TestGatewayHandlerMessages_InterceptWarmup_AntigravityAccount_ForcePlatform(t *testing.T) {

	groupID := int64(2002)
	accountID := int64(1002)

	group := &routing.Group{
		ID:       groupID,
		Hydrated: true,
		Platform: capability.PlatformAntigravity,
		Status:   billing.StatusActive,
	}

	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: accountID,
		Name:     "ag-2",
		Platform: capability.PlatformAntigravity,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":              "tok_xxx",
			"intercept_warmup_requests": true,
		},
		Concurrency:   1,
		Priority:      1,
		Status:        billing.StatusActive,
		Schedulable:   true,
		AccountGroups: []accountcore.GroupMembership{{AccountID: accountID, GroupID: groupID}}},
	}

	h, cleanup := newTestGatewayHandler(t, group, []*gatewayprovider.ExecutionAccount{account})
	defer cleanup()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	body := []byte(`{
		"model": "claude-sonnet-4-5",
		"max_tokens": 256,
		"messages": [{"role":"user","content":[{"type":"text","text":"Warmup"}]}]
	}`)
	req := httptest.NewRequest("POST", "/antigravity/v1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// 模拟 routes/gateway.go 里的 ForcePlatform 中间件效果：
	// - 写入 request.Context（Service读取）
	// - 写入 gin.Context（Handler快速读取）
	ctx := requeststate.WithGroup(req.Context(), group)
	ctx = apikey.WithForcePlatform(ctx, capability.PlatformAntigravity)
	req = req.WithContext(ctx)
	c.Request = req
	c.Set(string(keyhttp.ContextKeyForcePlatform), capability.PlatformAntigravity)

	apiKey := &apikey.APIKey{
		ID:      3002,
		UserID:  4002,
		GroupID: &groupID,
		Status:  billing.StatusActive,
		User: &identity.User{
			ID:          4002,
			Concurrency: 10,
			Balance:     100,
		},
		Group: group,
	}

	c.Set(string(keyhttp.ContextKeyAPIKey), apiKey)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: apiKey.UserID, Concurrency: 10})

	h.Messages(c)

	require.Equal(t, 200, rec.Code)

	selected, ok := c.Get(gatewayhttp.OpsAccountIDKey)
	require.True(t, ok)
	require.Equal(t, accountID, selected)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Regexp(t, `^msg_01[0-9A-Za-z]{22}$`, resp["id"])
	require.Equal(t, "claude-sonnet-4-5", resp["model"])
}

// 夹具适配本次持有者句柄，继续沿用原锁失败/等待控制和断言。
func (f *fakeSchedulerCache) AcquireBucketLease(ctx context.Context, bucket scheduler.SchedulerBucket, ttl time.Duration) (*scheduler.BucketLease, bool, error) {
	ok, err := f.TryLockBucket(ctx, bucket, ttl)
	if err != nil || !ok {
		return nil, ok, err
	}
	return scheduler.NewBucketLease(func(cleanup context.Context) error { return f.UnlockBucket(cleanup, bucket) }), true, nil
}
