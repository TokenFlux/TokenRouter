package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	usagecore "github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/stretchr/testify/require"
)

type openAIRecordUsageLogRepoStub struct {
	usagecore.UsageLogRepository

	inserted   bool
	err        error
	calls      int
	lastLog    *usagecore.UsageLog
	lastCtxErr error
}

func (s *openAIRecordUsageLogRepoStub) Create(ctx context.Context, log *usagecore.UsageLog) (bool, error) {
	s.calls++
	s.lastLog = log
	s.lastCtxErr = ctx.Err()
	return s.inserted, s.err
}

type openAIRecordUsageBillingRepoStub struct {
	completion.Store

	result       *billing.UsageBillingApplyResult
	err          error
	calls        int
	lastCmd      *billing.UsageBillingCommand
	lastCtxErr   error
	resolveSub   *billing.UserSubscription
	resolveCalls int
}

type openAIRecordUsageAccountRepoStub struct {
	AccountRepository
	account *Account
	calls   int
}

func (s *openAIRecordUsageAccountRepoStub) GetByID(_ context.Context, _ int64) (*Account, error) {
	s.calls++
	return s.account, nil
}

func (s *openAIRecordUsageBillingRepoStub) Apply(ctx context.Context, cmd *billing.UsageBillingCommand) (*billing.UsageBillingApplyResult, error) {
	s.calls++
	s.lastCmd = cmd
	s.lastCtxErr = ctx.Err()
	if s.err != nil {
		return nil, s.err
	}
	if s.result != nil {
		return s.result, nil
	}
	result := &billing.UsageBillingApplyResult{Applied: true}
	if cmd != nil {
		switch cmd.BillingType {
		case usagecore.BillingTypeSubscription:
			result.SubscriptionAmountUSD = cmd.BillableAmountUSD
		default:
			result.BalanceAmountUSD = cmd.BillableAmountUSD
		}
	}
	return result, nil
}

func (s *openAIRecordUsageBillingRepoStub) ResolveUsableSubscriptionForGroup(ctx context.Context, userID, groupID int64) (*billing.UserSubscription, error) {
	s.resolveCalls++
	return s.resolveSub, nil
}

type openAIRecordUsageQuotaCacheStub struct {
	entry     *billing.UserPlatformQuotaCacheEntry
	getCalls  []openAIRecordUsageQuotaCacheGetCall
	incrCalls []openAIRecordUsageQuotaCacheIncrCall
}

type openAIRecordUsageQuotaCacheGetCall struct {
	userID   int64
	platform string
}

type openAIRecordUsageQuotaCacheIncrCall struct {
	userID   int64
	platform string
	cost     float64
}

func (s *openAIRecordUsageQuotaCacheStub) GetUserBalance(ctx context.Context, userID int64) (float64, error) {
	return 0, nil
}

func (s *openAIRecordUsageQuotaCacheStub) SetUserBalance(ctx context.Context, userID int64, balance float64) error {
	return nil
}

func (s *openAIRecordUsageQuotaCacheStub) DeductUserBalance(ctx context.Context, userID int64, amount float64) error {
	return nil
}

func (s *openAIRecordUsageQuotaCacheStub) InvalidateUserBalance(ctx context.Context, userID int64) error {
	return nil
}

func (s *openAIRecordUsageQuotaCacheStub) GetAPIKeyRateLimit(ctx context.Context, keyID int64) (*billing.APIKeyRateLimitCacheData, error) {
	return nil, nil
}

func (s *openAIRecordUsageQuotaCacheStub) SetAPIKeyRateLimit(ctx context.Context, keyID int64, data *billing.APIKeyRateLimitCacheData) error {
	return nil
}

func (s *openAIRecordUsageQuotaCacheStub) UpdateAPIKeyRateLimitUsage(ctx context.Context, keyID int64, cost float64) error {
	return nil
}

func (s *openAIRecordUsageQuotaCacheStub) InvalidateAPIKeyRateLimit(ctx context.Context, keyID int64) error {
	return nil
}

func (s *openAIRecordUsageQuotaCacheStub) GetUserPlatformQuotaCache(ctx context.Context, userID int64, platform string) (*billing.UserPlatformQuotaCacheEntry, bool, error) {
	s.getCalls = append(s.getCalls, openAIRecordUsageQuotaCacheGetCall{userID: userID, platform: platform})
	if s.entry == nil {
		return nil, false, nil
	}
	return s.entry, true, nil
}

func (s *openAIRecordUsageQuotaCacheStub) SetUserPlatformQuotaCache(ctx context.Context, userID int64, platform string, entry *billing.UserPlatformQuotaCacheEntry, ttl time.Duration) error {
	s.entry = entry
	return nil
}

func (s *openAIRecordUsageQuotaCacheStub) DeleteUserPlatformQuotaCache(ctx context.Context, userID int64, platform string) error {
	s.entry = nil
	return nil
}

func (s *openAIRecordUsageQuotaCacheStub) IncrUserPlatformQuotaUsageCache(ctx context.Context, userID int64, platform string, cost float64, ttl time.Duration, markDirty bool) error {
	s.incrCalls = append(s.incrCalls, openAIRecordUsageQuotaCacheIncrCall{userID: userID, platform: platform, cost: cost})
	return nil
}

func (s *openAIRecordUsageQuotaCacheStub) PopDirtyUserPlatformQuotaKeys(ctx context.Context, n int) ([]billing.UserPlatformQuotaKey, error) {
	return nil, nil
}

func (s *openAIRecordUsageQuotaCacheStub) ReaddDirtyUserPlatformQuotaKeys(ctx context.Context, keys []billing.UserPlatformQuotaKey) error {
	return nil
}

func (s *openAIRecordUsageQuotaCacheStub) BatchGetUserPlatformQuotaCache(ctx context.Context, keys []billing.UserPlatformQuotaKey) ([]*billing.UserPlatformQuotaCacheEntry, error) {
	return make([]*billing.UserPlatformQuotaCacheEntry, len(keys)), nil
}

type openAIRecordUsagePlatformQuotaRepoStub struct{}

func (s *openAIRecordUsagePlatformQuotaRepoStub) GetByUserPlatform(ctx context.Context, userID int64, platform string) (*billing.UserPlatformQuotaRecord, error) {
	return nil, nil
}

func (s *openAIRecordUsagePlatformQuotaRepoStub) BulkInsertInitial(ctx context.Context, records []billing.UserPlatformQuotaRecord) error {
	return nil
}

func (s *openAIRecordUsagePlatformQuotaRepoStub) IncrementUsageWithReset(ctx context.Context, userID int64, platform string, cost float64, now time.Time) error {
	return nil
}

func (s *openAIRecordUsagePlatformQuotaRepoStub) ListByUser(ctx context.Context, userID int64) ([]billing.UserPlatformQuotaRecord, error) {
	return nil, nil
}

func (s *openAIRecordUsagePlatformQuotaRepoStub) UpsertForUser(ctx context.Context, userID int64, records []billing.UserPlatformQuotaRecord) error {
	return nil
}

func (s *openAIRecordUsagePlatformQuotaRepoStub) ResetExpiredWindow(ctx context.Context, userID int64, platform string, window string, newStart time.Time) error {
	return nil
}

func (s *openAIRecordUsagePlatformQuotaRepoStub) BatchSnapshotUsage(ctx context.Context, snapshots []billing.UserPlatformQuotaSnapshot, now time.Time) error {
	return nil
}

func TestOpenAIGatewayServiceRecordUsage_RejectsNilInput(t *testing.T) {
	svc := &OpenAIGatewayService{}
	require.Error(t, svc.RecordUsage(context.Background(), nil))
	require.Error(t, svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{}))
}

type openAIRecordUsageUserRepoStub struct {
	identity.UserRepository

	deductCalls int
	deductErr   error
	lastAmount  float64
	lastCtxErr  error
}

func (s *openAIRecordUsageUserRepoStub) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	s.deductCalls++
	s.lastAmount = amount
	s.lastCtxErr = ctx.Err()
	if s.deductErr != nil {
		return 0, s.deductErr
	}
	return amount, nil
}

func (s *openAIRecordUsageUserRepoStub) AdjustBalance(ctx context.Context, id int64, delta float64) (identity.BalanceChange, error) {
	panic("unexpected AdjustBalance call")
}

func (s *openAIRecordUsageUserRepoStub) SetBalance(ctx context.Context, id int64, value float64) (identity.BalanceChange, error) {
	panic("unexpected SetBalance call")
}

type openAIRecordUsageSubRepoStub struct {
	billing.UserSubscriptionRepository

	incrementCalls int
	incrementErr   error
	lastCtxErr     error
}

func (s *openAIRecordUsageSubRepoStub) IncrementUsage(ctx context.Context, id int64, costUSD float64) error {
	s.incrementCalls++
	s.lastCtxErr = ctx.Err()
	return s.incrementErr
}

type openAIRecordUsageAPIKeyQuotaStub struct {
	quotaCalls          int
	rateLimitCalls      int
	err                 error
	lastAmount          float64
	lastQuotaCtxErr     error
	lastRateLimitCtxErr error
}

func (s *openAIRecordUsageAPIKeyQuotaStub) UpdateQuotaUsed(ctx context.Context, apiKeyID int64, cost float64) error {
	s.quotaCalls++
	s.lastAmount = cost
	s.lastQuotaCtxErr = ctx.Err()
	return s.err
}

func (s *openAIRecordUsageAPIKeyQuotaStub) UpdateRateLimitUsage(ctx context.Context, apiKeyID int64, cost float64) error {
	s.rateLimitCalls++
	s.lastAmount = cost
	s.lastRateLimitCtxErr = ctx.Err()
	return s.err
}

type openAIUserGroupRateRepoStub struct {
	billing.UserGroupRateRepository

	rate  *float64
	err   error
	calls int
}

func (s *openAIUserGroupRateRepoStub) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.rate, nil
}

func i64p(v int64) *int64 {
	return &v
}

func requireOpenAIRecordUsageBillingRepoStub(t *testing.T, svc *OpenAIGatewayService) *openAIRecordUsageBillingRepoStub {
	t.Helper()

	billingRepo, ok := svc.usageBillingRepo.(*openAIRecordUsageBillingRepoStub)
	require.True(t, ok)
	return billingRepo
}

func newOpenAIRecordUsageServiceForTest(usageRepo usagecore.UsageLogRepository, userRepo identity.UserRepository, subRepo billing.UserSubscriptionRepository, rateRepo billing.UserGroupRateRepository) *OpenAIGatewayService {
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1.1
	billingRepo := &openAIRecordUsageBillingRepoStub{}
	svc := NewOpenAIGatewayService(
		nil,
		usageRepo,
		billingRepo,
		userRepo,
		subRepo,
		rateRepo,
		nil,
		cfg,
		nil,
		nil,
		NewBillingService(cfg, nil),
		nil,
		nil,
		nil,
		nil,
		&accountcore.DeferredService{},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil, // 用户平台配额仓库
	)
	svc.userGroupRateResolver = billing.NewGroupRateResolver(
		rateRepo,
		nil,
		resolveUserGroupRateCacheTTL(cfg),
		nil,
		"service.openai_gateway.test", logging.LegacyPrintf,
	)
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)

	return svc
}

func newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo usagecore.UsageLogRepository, billingRepo completion.Store, userRepo identity.UserRepository, subRepo billing.UserSubscriptionRepository, rateRepo billing.UserGroupRateRepository) *OpenAIGatewayService {
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1.1
	svc := NewOpenAIGatewayService(
		nil,
		usageRepo,
		billingRepo,
		userRepo,
		subRepo,
		rateRepo,
		nil,
		cfg,
		nil,
		nil,
		NewBillingService(cfg, nil),
		nil,
		nil,
		nil,
		nil,
		&accountcore.DeferredService{},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
	svc.userGroupRateResolver = billing.NewGroupRateResolver(
		rateRepo,
		nil,
		resolveUserGroupRateCacheTTL(cfg),
		nil,
		"service.openai_gateway.test", logging.LegacyPrintf,
	)
	return svc
}

func expectedOpenAICost(t *testing.T, svc *OpenAIGatewayService, model string, usage openai.ForwardUsage, multiplier float64) *pricing.CostBreakdown {
	t.Helper()

	cost, err := svc.billingService.CalculateCost(model, pricing.UsageTokens{
		InputTokens:         max(usage.InputTokens-usage.CacheReadInputTokens-usage.CacheCreationInputTokens, 0),
		OutputTokens:        usage.OutputTokens,
		CacheCreationTokens: usage.CacheCreationInputTokens,
		CacheReadTokens:     usage.CacheReadInputTokens,
	}, multiplier)
	require.NoError(t, err)
	return cost
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func TestOpenAIGatewayServiceRecordUsage_ZeroUsageStillWritesUsageLog(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &billing.UsageBillingApplyResult{Applied: true}}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	quotaSvc := &openAIRecordUsageAPIKeyQuotaStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_zero_usage",
			Usage:     openai.ForwardUsage{},
			Model:     "gpt-5.1",
			Duration:  time.Second,
		},
		APIKey:        &apikey.APIKey{ID: 1000, Quota: 100, Group: &routing.Group{RateMultiplier: 1}},
		User:          &identity.User{ID: 2000},
		Account:       &Account{ID: 3000, Type: capability.AccountTypeAPIKey},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, billingRepo.calls)
	require.Equal(t, 1, usageRepo.calls)
	require.Equal(t, 0, userRepo.deductCalls)
	require.Equal(t, 0, subRepo.incrementCalls)
	require.Equal(t, 0, quotaSvc.quotaCalls)
	require.Equal(t, 0, quotaSvc.rateLimitCalls)

	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "resp_zero_usage", usageRepo.lastLog.RequestID)
	require.Zero(t, usageRepo.lastLog.InputTokens)
	require.Zero(t, usageRepo.lastLog.OutputTokens)
	require.Zero(t, usageRepo.lastLog.CacheCreationTokens)
	require.Zero(t, usageRepo.lastLog.CacheReadTokens)
	require.Zero(t, usageRepo.lastLog.ImageOutputTokens)
	require.Zero(t, usageRepo.lastLog.ImageCount)
	require.Zero(t, usageRepo.lastLog.InputCost)
	require.Zero(t, usageRepo.lastLog.OutputCost)
	require.Zero(t, usageRepo.lastLog.TotalCost)
	require.Zero(t, usageRepo.lastLog.ActualCost)

	require.NotNil(t, billingRepo.lastCmd)
	require.Zero(t, billingRepo.lastCmd.BillableAmountUSD)
	require.Zero(t, billingRepo.lastCmd.APIKeyQuotaCost)
	require.Zero(t, billingRepo.lastCmd.APIKeyRateLimitCost)
	require.Zero(t, billingRepo.lastCmd.AccountQuotaCost)
}

func TestOpenAIGatewayServiceRecordUsage_MissingPricingRecordsZeroCostUsageLog(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &billing.UsageBillingApplyResult{Applied: true}}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	quotaSvc := &openAIRecordUsageAPIKeyQuotaStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_missing_pricing",
			Usage: openai.ForwardUsage{
				InputTokens:  1200,
				OutputTokens: 300,
			},
			Model:    "gpt-unknown-model",
			Duration: time.Second,
		},
		APIKey:        &apikey.APIKey{ID: 1002, Quota: 100, Group: &routing.Group{RateMultiplier: 1}},
		User:          &identity.User{ID: 2002},
		Account:       &Account{ID: 3002, Type: capability.AccountTypeAPIKey},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, billingRepo.calls)
	require.Equal(t, 1, usageRepo.calls)
	require.Equal(t, 0, userRepo.deductCalls)
	require.Equal(t, 0, subRepo.incrementCalls)
	require.Equal(t, 0, quotaSvc.quotaCalls)
	require.Equal(t, 0, quotaSvc.rateLimitCalls)

	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "resp_missing_pricing", usageRepo.lastLog.RequestID)
	require.Equal(t, "gpt-unknown-model", usageRepo.lastLog.Model)
	require.Equal(t, "gpt-unknown-model", usageRepo.lastLog.RequestedModel)
	require.Equal(t, 1200, usageRepo.lastLog.InputTokens)
	require.Equal(t, 300, usageRepo.lastLog.OutputTokens)
	require.Zero(t, usageRepo.lastLog.TotalCost)
	require.Zero(t, usageRepo.lastLog.ActualCost)
	require.NotNil(t, usageRepo.lastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeToken), *usageRepo.lastLog.BillingMode)

	require.NotNil(t, billingRepo.lastCmd)
	require.Zero(t, billingRepo.lastCmd.BillableAmountUSD)
	require.Zero(t, billingRepo.lastCmd.APIKeyQuotaCost)
	require.Zero(t, billingRepo.lastCmd.APIKeyRateLimitCost)
	require.Zero(t, billingRepo.lastCmd.AccountQuotaCost)
}

func TestOpenAIGatewayServiceRecordUsage_UsesQuotaPlatformForPlatformQuota(t *testing.T) {
	dailyLimit := 100.0
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{
		result: &billing.UsageBillingApplyResult{
			Applied:          true,
			BalanceAmountUSD: 0.25,
		},
	}
	quotaCache := &openAIRecordUsageQuotaCacheStub{
		entry: &billing.UserPlatformQuotaCacheEntry{DailyLimitUSD: &dailyLimit},
	}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(
		usageRepo,
		billingRepo,
		&openAIRecordUsageUserRepoStub{},
		&openAIRecordUsageSubRepoStub{},
		nil,
	)
	svc.billingCacheService = newBillingEligibilityForCompletionTest(quotaCache)
	svc.userPlatformQuotaRepo = &openAIRecordUsagePlatformQuotaRepoStub{}

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_quota_platform",
			Usage: openai.ForwardUsage{
				InputTokens:  1000,
				OutputTokens: 1000,
			},
			Model:    "gpt-5.1",
			Duration: time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:    1100,
			Quota: 100,
			Group: &routing.Group{Platform: capability.PlatformOpenAI, RateMultiplier: 1},
		},
		User:          &identity.User{ID: 2100, Balance: 10},
		Account:       &Account{ID: 3100, Type: capability.AccountTypeAPIKey},
		QuotaPlatform: capability.PlatformAntigravity,
	})

	require.NoError(t, err)
	// ForcePlatform 由 handler 预先拍进 QuotaPlatform，后扣不能再回退到 API key 的 openai 分组。
	require.Equal(t, []openAIRecordUsageQuotaCacheGetCall{
		{userID: 2100, platform: capability.PlatformAntigravity},
	}, quotaCache.getCalls)
	require.Equal(t, []openAIRecordUsageQuotaCacheIncrCall{
		{userID: 2100, platform: capability.PlatformAntigravity, cost: 0.25},
	}, quotaCache.incrCalls)
}

func TestOpenAIGatewayServiceRecordUsage_UsesUserSpecificGroupRate(t *testing.T) {
	groupID := int64(11)
	groupRate := 1.4
	userRate := 1.8
	usage := openai.ForwardUsage{InputTokens: 15, OutputTokens: 4, CacheReadInputTokens: 3}

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	rateRepo := &openAIUserGroupRateRepoStub{rate: &userRate}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, rateRepo)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_user_group_rate",
			Usage:     usage,
			Model:     "gpt-5.1",
			Duration:  time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      1001,
			GroupID: i64p(groupID),
			Group: &routing.Group{
				ID:             groupID,
				RateMultiplier: groupRate,
			},
		},
		User:    &identity.User{ID: 2001},
		Account: &Account{ID: 3001},
	})

	require.NoError(t, err)
	require.Equal(t, 1, rateRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, userRate, usageRepo.lastLog.RateMultiplier)
	require.Equal(t, 12, usageRepo.lastLog.InputTokens)
	require.Equal(t, 3, usageRepo.lastLog.CacheReadTokens)

	expected := expectedOpenAICost(t, svc, "gpt-5.1", usage, userRate)
	require.InDelta(t, expected.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, expected.ActualCost, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_PeakRateAffectsTokenModeImageOutputTokens(t *testing.T) {
	groupID := int64(14)
	groupRate := 1.0
	usage := openai.ForwardUsage{
		InputTokens:       1000,
		OutputTokens:      600,
		ImageOutputTokens: 100,
	}

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	// 固定在峰值窗口内，避免测试在每天 23:59 的右开边界偶发失败。
	svc.usageBillingNow = func() time.Time {
		return time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	}
	svc.resolver = newOpenAITokenImageChannelPricingResolverForTest(t, groupID, "gpt-5.1")

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:  "resp_peak_image_tokens",
			Usage:      usage,
			Model:      "gpt-5.1",
			Duration:   time.Second,
			ImageCount: 1,
		},
		APIKey: &apikey.APIKey{
			ID:      1004,
			GroupID: i64p(groupID),
			Group: &routing.Group{
				ID:                 groupID,
				RateMultiplier:     groupRate,
				PeakRateEnabled:    true,
				PeakStart:          "11:59",
				PeakEnd:            "12:01",
				PeakRateMultiplier: 3.0,
			},
		},
		User:    &identity.User{ID: 2004},
		Account: &Account{ID: 3004},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, 3.0, usageRepo.lastLog.RateMultiplier)
	require.Equal(t, usage.ImageOutputTokens, usageRepo.lastLog.ImageOutputTokens)

	expected, err := svc.billingService.CalculateCostUnified(billing.CostInput{
		Ctx:     context.Background(),
		Model:   "gpt-5.1",
		GroupID: i64p(groupID),
		Tokens: pricing.UsageTokens{
			InputTokens:       usage.InputTokens,
			OutputTokens:      usage.OutputTokens,
			ImageOutputTokens: usage.ImageOutputTokens,
		},
		RateMultiplier: 1.0,
		Resolver:       svc.resolver,
	})
	require.NoError(t, err)
	expectedActual := expected.TotalCost * 3.0

	require.InDelta(t, expected.TotalCost, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, expected.ImageOutputCost, usageRepo.lastLog.ImageOutputCost, 1e-12)
	require.InDelta(t, expectedActual, usageRepo.lastLog.ActualCost, 1e-12)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, expectedActual, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_IncludesEndpointMetadata(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	rateRepo := &openAIUserGroupRateRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, rateRepo)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_endpoint_metadata",
			Usage: openai.ForwardUsage{
				InputTokens:  8,
				OutputTokens: 2,
			},
			Model:    "gpt-5.1",
			Duration: time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:    1002,
			Group: &routing.Group{RateMultiplier: 1},
		},
		User:             &identity.User{ID: 2002},
		Account:          &Account{ID: 3002},
		InboundEndpoint:  " /v1/chat/completions ",
		UpstreamEndpoint: " /v1/responses ",
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.InboundEndpoint)
	require.Equal(t, "/v1/chat/completions", *usageRepo.lastLog.InboundEndpoint)
	require.NotNil(t, usageRepo.lastLog.UpstreamEndpoint)
	require.Equal(t, "/v1/responses", *usageRepo.lastLog.UpstreamEndpoint)
}

func TestOpenAIGatewayServiceRecordUsage_FallsBackToGroupDefaultRateOnResolverError(t *testing.T) {
	groupID := int64(12)
	groupRate := 1.6
	usage := openai.ForwardUsage{InputTokens: 10, OutputTokens: 5, CacheReadInputTokens: 2}

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	rateRepo := &openAIUserGroupRateRepoStub{err: errors.New("db unavailable")}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, rateRepo)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_group_default_on_error",
			Usage:     usage,
			Model:     "gpt-5.1",
			Duration:  time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      1002,
			GroupID: i64p(groupID),
			Group: &routing.Group{
				ID:             groupID,
				RateMultiplier: groupRate,
			},
		},
		User:    &identity.User{ID: 2002},
		Account: &Account{ID: 3002},
	})

	require.NoError(t, err)
	require.Equal(t, 1, rateRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, groupRate, usageRepo.lastLog.RateMultiplier)

	expected := expectedOpenAICost(t, svc, "gpt-5.1", usage, groupRate)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, expected.ActualCost, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_FallsBackToGroupDefaultRateWhenResolverMissing(t *testing.T) {
	groupID := int64(13)
	groupRate := 1.25
	usage := openai.ForwardUsage{InputTokens: 9, OutputTokens: 4, CacheReadInputTokens: 1}

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	svc.userGroupRateResolver = nil

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_group_default_nil_resolver",
			Usage:     usage,
			Model:     "gpt-5.1",
			Duration:  time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      1003,
			GroupID: i64p(groupID),
			Group: &routing.Group{
				ID:             groupID,
				RateMultiplier: groupRate,
			},
		},
		User:    &identity.User{ID: 2003},
		Account: &Account{ID: 3003},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, groupRate, usageRepo.lastLog.RateMultiplier)
}

func TestOpenAIGatewayServiceRecordUsage_DuplicateUsageLogSkipsBilling(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: false}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &billing.UsageBillingApplyResult{Applied: false}}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_duplicate",
			Usage: openai.ForwardUsage{
				InputTokens:  8,
				OutputTokens: 4,
			},
			Model:    "gpt-5.1",
			Duration: time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 1004},
		User:    &identity.User{ID: 2004},
		Account: &Account{ID: 3004},
	})

	require.NoError(t, err)
	require.Equal(t, 1, billingRepo.calls)
	require.Equal(t, 1, usageRepo.calls)
	require.Equal(t, 0, userRepo.deductCalls)
	require.Equal(t, 0, subRepo.incrementCalls)
}

func TestOpenAIGatewayServiceRecordUsage_DuplicateBillingKeySkipsBillingWithRepo(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: false}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &billing.UsageBillingApplyResult{Applied: false}}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	quotaSvc := &openAIRecordUsageAPIKeyQuotaStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_duplicate_billing_key",
			Usage: openai.ForwardUsage{
				InputTokens:  8,
				OutputTokens: 4,
			},
			Model:    "gpt-5.1",
			Duration: time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:    10045,
			Quota: 100,
		},
		User:          &identity.User{ID: 20045},
		Account:       &Account{ID: 30045},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, billingRepo.calls)
	require.Equal(t, 1, usageRepo.calls)
	require.Equal(t, 0, userRepo.deductCalls)
	require.Equal(t, 0, subRepo.incrementCalls)
	require.Equal(t, 0, quotaSvc.quotaCalls)
}

func TestOpenAIGatewayServiceRecordUsage_BillsWhenUsageLogCreateReturnsError(t *testing.T) {
	usage := openai.ForwardUsage{InputTokens: 8, OutputTokens: 4}
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: false, err: errors.New("usage log batch state uncertain")}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_usage_log_error",
			Usage:     usage,
			Model:     "gpt-5.1",
			Duration:  time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10041},
		User:    &identity.User{ID: 20041},
		Account: &Account{ID: 30041},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, usageRepo.lastLog.ActualCost, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_UsageLogWriteErrorDoesNotSkipBilling(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: false, err: usagecore.MarkUsageLogCreateNotPersisted(context.Canceled)}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	quotaSvc := &openAIRecordUsageAPIKeyQuotaStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_not_persisted",
			Usage: openai.ForwardUsage{
				InputTokens:  8,
				OutputTokens: 4,
			},
			Model:    "gpt-5.1",
			Duration: time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:    10043,
			Quota: 100,
		},
		User:          &identity.User{ID: 20043},
		Account:       &Account{ID: 30043},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, billingRepo.lastCmd.BillableAmountUSD, billingRepo.lastCmd.APIKeyQuotaCost, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_BillingUsesDetachedContext(t *testing.T) {
	usage := openai.ForwardUsage{InputTokens: 10, OutputTokens: 6, CacheReadInputTokens: 2}
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: false, err: context.DeadlineExceeded}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	quotaSvc := &openAIRecordUsageAPIKeyQuotaStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.RecordUsage(reqCtx, &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_detached_billing_ctx",
			Usage:     usage,
			Model:     "gpt-5.1",
			Duration:  time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:    10042,
			Quota: 100,
		},
		User:          &identity.User{ID: 20042},
		Account:       &Account{ID: 30042},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.calls)
	require.NoError(t, billingRepo.lastCtxErr)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, billingRepo.lastCmd.BillableAmountUSD, billingRepo.lastCmd.APIKeyQuotaCost, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_BillingRepoUsesDetachedContext(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &billing.UsageBillingApplyResult{Applied: true}}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.RecordUsage(reqCtx, &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_detached_billing_repo_ctx",
			Usage: openai.ForwardUsage{
				InputTokens:  8,
				OutputTokens: 4,
			},
			Model:    "gpt-5.1",
			Duration: time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10046},
		User:    &identity.User{ID: 20046},
		Account: &Account{ID: 30046},
	})

	require.NoError(t, err)
	require.Equal(t, 1, billingRepo.calls)
	require.NoError(t, billingRepo.lastCtxErr)
	require.Equal(t, 1, usageRepo.calls)
	require.NoError(t, usageRepo.lastCtxErr)
}

func TestOpenAIGatewayServiceRecordUsage_BillingFingerprintIncludesRequestPayloadHash(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &billing.UsageBillingApplyResult{Applied: true}}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)

	payloadHash := billing.HashUsageRequestPayload([]byte(`{"model":"gpt-5","input":"hello"}`))
	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "openai_payload_hash",
			Usage: openai.ForwardUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "gpt-5",
			Duration: time.Second,
		},
		APIKey:             &apikey.APIKey{ID: 501, Quota: 100},
		User:               &identity.User{ID: 601},
		Account:            &Account{ID: 701},
		RequestPayloadHash: payloadHash,
	})
	require.NoError(t, err)
	require.NotNil(t, billingRepo.lastCmd)
	require.Equal(t, payloadHash, billingRepo.lastCmd.RequestPayloadHash)
}

func TestOpenAIGatewayServiceRecordUsage_UsesFallbackRequestIDForBillingAndUsageLog(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &billing.UsageBillingApplyResult{Applied: true}}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	ctx := context.WithValue(context.Background(), telemetry.RequestID, "req-local-fallback")
	err := svc.RecordUsage(ctx, &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "",
			Usage: openai.ForwardUsage{
				InputTokens:  8,
				OutputTokens: 4,
			},
			Model:    "gpt-5.1",
			Duration: time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10047},
		User:    &identity.User{ID: 20047},
		Account: &Account{ID: 30047},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.lastCmd)
	require.Equal(t, "local:req-local-fallback", billingRepo.lastCmd.RequestID)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "local:req-local-fallback", usageRepo.lastLog.RequestID)
}

func TestOpenAIGatewayServiceRecordUsage_PrefersClientRequestIDOverUpstreamRequestID(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &billing.UsageBillingApplyResult{Applied: true}}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	ctx := context.WithValue(context.Background(), telemetry.ClientRequestID, "openai-client-stable-123")
	err := svc.RecordUsage(ctx, &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "upstream-openai-volatile-456",
			Usage: openai.ForwardUsage{
				InputTokens:  8,
				OutputTokens: 4,
			},
			Model:    "gpt-5.1",
			Duration: time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10049},
		User:    &identity.User{ID: 20049},
		Account: &Account{ID: 30049},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.lastCmd)
	require.Equal(t, "client:openai-client-stable-123", billingRepo.lastCmd.RequestID)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "client:openai-client-stable-123", usageRepo.lastLog.RequestID)
}

func TestOpenAIGatewayServiceRecordUsage_WSModePrefersUpstreamRequestIDOverClientRequestID(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &billing.UsageBillingApplyResult{Applied: true}}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	ctx := context.WithValue(context.Background(), telemetry.ClientRequestID, "openai-ws-connection-123")
	err := svc.RecordUsage(ctx, &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:    "resp_openai_ws_turn_456",
			OpenAIWSMode: true,
			Usage: openai.ForwardUsage{
				InputTokens:  8,
				OutputTokens: 4,
			},
			Model:    "gpt-5.1",
			Duration: time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10050},
		User:    &identity.User{ID: 20050},
		Account: &Account{ID: 30050},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.lastCmd)
	require.Equal(t, "resp_openai_ws_turn_456", billingRepo.lastCmd.RequestID)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "resp_openai_ws_turn_456", usageRepo.lastLog.RequestID)
}

func TestOpenAIGatewayServiceRecordUsage_GeneratesRequestIDWhenAllSourcesMissing(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &billing.UsageBillingApplyResult{Applied: true}}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "",
			Usage: openai.ForwardUsage{
				InputTokens:  8,
				OutputTokens: 4,
			},
			Model:    "gpt-5.1",
			Duration: time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10050},
		User:    &identity.User{ID: 20050},
		Account: &Account{ID: 30050},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.lastCmd)
	require.True(t, strings.HasPrefix(billingRepo.lastCmd.RequestID, "generated:"))
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, billingRepo.lastCmd.RequestID, usageRepo.lastLog.RequestID)
}

func TestOpenAIGatewayServiceRecordUsage_BillingErrorWritesUnsettledUsageLog(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	billingErr := errors.New("billing tx failed")
	billingRepo := &openAIRecordUsageBillingRepoStub{err: billingErr}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_billing_fail",
			Usage: openai.ForwardUsage{
				InputTokens:  8,
				OutputTokens: 4,
			},
			Model:    "gpt-5.1",
			Duration: time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10048},
		User:    &identity.User{ID: 20048},
		Account: &Account{ID: 30048},
	})

	require.ErrorIs(t, err, billingErr)
	require.Equal(t, 1, billingRepo.calls)
	require.Equal(t, 1, usageRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, 8, usageRepo.lastLog.InputTokens)
	require.Equal(t, 4, usageRepo.lastLog.OutputTokens)
	require.Greater(t, usageRepo.lastLog.InputCost, 0.0)
	require.Greater(t, usageRepo.lastLog.OutputCost, 0.0)
	require.Greater(t, usageRepo.lastLog.TotalCost, 0.0)
	require.Zero(t, usageRepo.lastLog.ActualCost)
}

func TestOpenAIGatewayServiceRecordUsage_UpdatesAPIKeyQuotaWhenConfigured(t *testing.T) {
	usage := openai.ForwardUsage{InputTokens: 10, OutputTokens: 6, CacheReadInputTokens: 2}
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	quotaSvc := &openAIRecordUsageAPIKeyQuotaStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_quota_update",
			Usage:     usage,
			Model:     "gpt-5.1",
			Duration:  time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:    1005,
			Quota: 100,
		},
		User:          &identity.User{ID: 2005},
		Account:       &Account{ID: 3005},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	expected := expectedOpenAICost(t, svc, "gpt-5.1", usage, 1.1)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, expected.ActualCost, billingRepo.lastCmd.APIKeyQuotaCost, 1e-12)
	require.InDelta(t, 0.0, billingRepo.lastCmd.APIKeyRateLimitCost, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_ClampsActualInputTokensToZero(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_clamp_actual_input",
			Usage: openai.ForwardUsage{
				InputTokens:          2,
				OutputTokens:         1,
				CacheReadInputTokens: 5,
			},
			Model:    "gpt-5.1",
			Duration: time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 1006},
		User:    &identity.User{ID: 2006},
		Account: &Account{ID: 3006},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, 0, usageRepo.lastLog.InputTokens)
}

func TestOpenAIGatewayServiceRecordUsage_GPT56SeparatesCacheWriteForBillingAndStats(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	svc.billingService = NewBillingService(svc.cfg, newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*pricing.LiteLLMModelPricing{
		"gpt-5.6-sol": {
			InputCostPerToken:       5e-6,
			OutputCostPerToken:      30e-6,
			CacheReadInputTokenCost: 0.5e-6,
		},
	}}))

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_gpt56_cache_write",
			Usage: openai.ForwardUsage{
				InputTokens:              1000,
				OutputTokens:             50,
				CacheCreationInputTokens: 200,
				CacheReadInputTokens:     100,
			},
			Model:    "gpt-5.6-sol",
			Duration: time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 1056},
		User:    &identity.User{ID: 2056},
		Account: &Account{ID: 3056},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, 700, usageRepo.lastLog.InputTokens)
	require.Equal(t, 200, usageRepo.lastLog.CacheCreationTokens)
	require.Equal(t, 100, usageRepo.lastLog.CacheReadTokens)
	require.Equal(t, 1050, usageRepo.lastLog.TotalTokens())
	require.InDelta(t, 700*5e-6, usageRepo.lastLog.InputCost, 1e-12)
	require.InDelta(t, 200*6.25e-6, usageRepo.lastLog.CacheCreationCost, 1e-12)
	require.InDelta(t, 100*0.5e-6, usageRepo.lastLog.CacheReadCost, 1e-12)
	require.InDelta(t, 50*30e-6, usageRepo.lastLog.OutputCost, 1e-12)
	require.InDelta(t, usageRepo.lastLog.TotalCost*1.1, usageRepo.lastLog.ActualCost, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_LongContextBillingIgnoresLegacyAccountExtra(t *testing.T) {
	parentID := int64(4016)
	tests := []struct {
		name    string
		account *Account
	}{
		{name: "missing legacy value", account: &Account{ID: 3014, Platform: capability.PlatformOpenAI}},
		{name: "legacy false", account: &Account{ID: 3015, Platform: capability.PlatformOpenAI, Extra: map[string]any{"openai_long_context_billing_enabled": false}}},
		{name: "legacy true", account: &Account{ID: 3016, Platform: capability.PlatformOpenAI, Extra: map[string]any{"openai_long_context_billing_enabled": true}}},
		{name: "spark shadow", account: &Account{
			ID:              3017,
			Platform:        capability.PlatformOpenAI,
			Type:            capability.AccountTypeOAuth,
			ParentAccountID: &parentID,
			QuotaDimension:  accountcore.QuotaDimensionSpark,
			Extra:           map[string]any{"openai_long_context_billing_enabled": false},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			accountRepo := &openAIRecordUsageAccountRepoStub{account: &Account{ID: parentID, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
			svc := newOpenAIRecordUsageServiceForTest(
				usageRepo,
				&openAIRecordUsageUserRepoStub{},
				&openAIRecordUsageSubRepoStub{},
				nil,
			)
			swapInOpenAILadderCatalog(t, svc)
			svc.accountRepo = accountRepo

			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &forwardcore.OpenAIResult{
					RequestID: "resp_gpt54_long_context_" + tt.name,
					Usage:     openai.ForwardUsage{InputTokens: 300000, OutputTokens: 2000},
					Model:     "gpt-5.4-2026-03-05",
					Duration:  time.Second,
				},
				APIKey:  &apikey.APIKey{ID: 1014},
				User:    &identity.User{ID: 2014},
				Account: tt.account,
			})

			require.NoError(t, err)
			require.NotNil(t, usageRepo.lastLog)
			expectedInput := 300000 * 2.5e-6 * 2.0
			expectedOutput := 2000 * 15e-6 * 1.5
			require.InDelta(t, expectedInput, usageRepo.lastLog.InputCost, 1e-10)
			require.InDelta(t, expectedOutput, usageRepo.lastLog.OutputCost, 1e-10)
			require.InDelta(t, expectedInput+expectedOutput, usageRepo.lastLog.TotalCost, 1e-10)
			require.InDelta(t, (expectedInput+expectedOutput)*1.1, usageRepo.lastLog.ActualCost, 1e-10)
			require.True(t, usageRepo.lastLog.LongContextBillingApplied)
			billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
			require.Equal(t, 1, billingRepo.calls)
			require.InDelta(t, usageRepo.lastLog.ActualCost, billingRepo.lastCmd.BillableAmountUSD, 1e-10)
			// 只有影子结算需要读取母账号解析凭据；该读取不再用于长上下文开关判断。
			wantAccountRepoCalls := 0
			if tt.account.IsShadow() {
				wantAccountRepoCalls = 1
			}
			require.Equal(t, wantAccountRepoCalls, accountRepo.calls)
		})
	}
}

// swapInOpenAILadderCatalog 给测试服务换上带 above_272k 阶梯字段的目录；
// 静态兜底价已不再携带 OpenAI 长上下文规则。
func swapInOpenAILadderCatalog(t *testing.T, svc *OpenAIGatewayService) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1.1
	svc.billingService = NewBillingService(cfg, newStubPricingServiceFromJSON(t, openAILadderCatalogJSON))
}

func TestOpenAIGatewayServiceRecordUsage_GroupControlsLongContextBilling(t *testing.T) {
	// 分组开关是唯一外部策略来源；账户 Extra 中的历史字段不再参与判断。
	tests := []struct {
		name        string
		groupEnable bool
		wantApplied bool
	}{
		{name: "group enabled", groupEnable: true, wantApplied: true},
		{name: "group disabled", groupEnable: false, wantApplied: false},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			svc := newOpenAIRecordUsageServiceForTest(
				usageRepo,
				&openAIRecordUsageUserRepoStub{},
				&openAIRecordUsageSubRepoStub{},
				nil,
			)
			swapInOpenAILadderCatalog(t, svc)
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			groupID := int64(1)
			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &forwardcore.OpenAIResult{
					RequestID: "resp_group_long_context_" + tt.name,
					Usage:     openai.ForwardUsage{InputTokens: 300000, OutputTokens: 2000},
					Model:     "gpt-5.4-2026-03-05",
					Duration:  time.Second,
				},
				APIKey: &apikey.APIKey{
					ID:      int64(1020 + i),
					GroupID: &groupID,
					Group: &routing.Group{
						ID:                        groupID,
						RateMultiplier:            1,
						LongContextPricingEnabled: tt.groupEnable,
					},
				},
				User: &identity.User{ID: int64(2020 + i)},
				Account: &Account{
					ID:       int64(3020 + i),
					Platform: capability.PlatformOpenAI,
					Extra:    map[string]any{"openai_long_context_billing_enabled": !tt.groupEnable},
				},
			})

			require.NoError(t, err)
			require.NotNil(t, usageRepo.lastLog)
			require.Equal(t, tt.wantApplied, usageRepo.lastLog.LongContextBillingApplied)
			inputMultiplier := 1.0
			outputMultiplier := 1.0
			if tt.wantApplied {
				inputMultiplier = 2.0
				outputMultiplier = 1.5
			}
			require.InDelta(t, 300000*2.5e-6*inputMultiplier, usageRepo.lastLog.InputCost, 1e-10)
			require.InDelta(t, 2000*15e-6*outputMultiplier, usageRepo.lastLog.OutputCost, 1e-10)
		})
	}
}

// Grok 没有账号级长上下文开关，官方阶梯只能由分组策略控制。
func TestOpenAIGatewayServiceRecordUsage_GrokLongContextFollowsGroupToggle(t *testing.T) {
	baseInput := 250000 * 2e-6
	baseOutput := 1000 * 6e-6

	for i, tt := range []struct {
		name        string
		groupEnable bool
		wantApplied bool
	}{
		{name: "group enabled", groupEnable: true, wantApplied: true},
		{name: "group disabled", groupEnable: false, wantApplied: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			svc := newOpenAIRecordUsageServiceForTest(
				usageRepo,
				&openAIRecordUsageUserRepoStub{},
				&openAIRecordUsageSubRepoStub{},
				nil,
			)
			svc.resolver = NewModelPricingResolver(nil, svc.billingService)
			groupID := int64(10 + i)

			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &forwardcore.OpenAIResult{
					RequestID: "resp_grok_long_context_" + tt.name,
					Usage:     openai.ForwardUsage{InputTokens: 250000, OutputTokens: 1000},
					Model:     "grok-4.5",
					Duration:  time.Second,
				},
				APIKey: &apikey.APIKey{
					ID:      int64(1030 + i),
					GroupID: &groupID,
					Group: &routing.Group{
						ID:                        groupID,
						Platform:                  capability.PlatformGrok,
						RateMultiplier:            1,
						LongContextPricingEnabled: tt.groupEnable,
					},
				},
				User:    &identity.User{ID: int64(2030 + i)},
				Account: &Account{ID: int64(3030 + i), Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth},
			})

			require.NoError(t, err)
			require.NotNil(t, usageRepo.lastLog)
			require.Equal(t, tt.wantApplied, usageRepo.lastLog.LongContextBillingApplied)
			multiplier := 1.0
			if tt.wantApplied {
				multiplier = 2
			}
			require.InDelta(t, baseInput*multiplier, usageRepo.lastLog.InputCost, 1e-10)
			require.InDelta(t, baseOutput*multiplier, usageRepo.lastLog.OutputCost, 1e-10)
		})
	}
}

func TestOpenAIGatewayServiceRecordUsage_ServiceTierPriorityUsesFastPricing(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	serviceTier := "priority"
	usage := openai.ForwardUsage{InputTokens: 100, OutputTokens: 50}

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:   "resp_service_tier_priority",
			ServiceTier: &serviceTier,
			Usage:       usage,
			Model:       "gpt-5.4",
			Duration:    time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 1015},
		User:    &identity.User{ID: 2015},
		Account: &Account{ID: 3015},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.ServiceTier)
	require.Equal(t, serviceTier, *usageRepo.lastLog.ServiceTier)

	baseCost, calcErr := svc.billingService.CalculateCost("gpt-5.4", pricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1.0)
	require.NoError(t, calcErr)
	require.InDelta(t, baseCost.TotalCost*2, usageRepo.lastLog.TotalCost, 1e-10)
}

func TestOpenAIGatewayServiceRecordUsage_ServiceTierFlexHalvesCost(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	serviceTier := "flex"
	usage := openai.ForwardUsage{InputTokens: 100, OutputTokens: 50, CacheReadInputTokens: 20}

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:   "resp_service_tier_flex",
			ServiceTier: &serviceTier,
			Usage:       usage,
			Model:       "gpt-5.4",
			Duration:    time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 1016},
		User:    &identity.User{ID: 2016},
		Account: &Account{ID: 3016},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)

	baseCost, calcErr := svc.billingService.CalculateCost("gpt-5.4", pricing.UsageTokens{InputTokens: 80, OutputTokens: 50, CacheReadTokens: 20}, 1.0)
	require.NoError(t, calcErr)
	require.InDelta(t, baseCost.TotalCost*0.5, usageRepo.lastLog.TotalCost, 1e-10)
}

func TestNormalizeOpenAIServiceTier(t *testing.T) {
	t.Run("fast maps to priority", func(t *testing.T) {
		got := openai.NormalizeServiceTier(" fast ")
		require.NotNil(t, got)
		require.Equal(t, "priority", *got)
	})

	t.Run("openai official tiers preserved", func(t *testing.T) {
		// OpenAI 官方文档定义的合法 tier 值都应被透传保留，避免因白名单过窄
		// 静默剥离客户端显式发送的合法字段。Codex 会发 priority/flex/ultrafast。
		for _, tier := range []string{"priority", "flex", "auto", "default", "scale", "ultrafast"} {
			got := openai.NormalizeServiceTier(tier)
			require.NotNil(t, got, "tier %q should not be normalized to nil", tier)
			require.Equal(t, tier, *got)
		}
	})

	t.Run("invalid ignored", func(t *testing.T) {
		require.Nil(t, openai.NormalizeServiceTier("turbo"))
		require.Nil(t, openai.NormalizeServiceTier("xxx"))
	})
}

func TestExtractOpenAIServiceTier(t *testing.T) {
	require.Equal(t, "priority", *extractOpenAIServiceTier(map[string]any{"service_tier": "fast"}))
	require.Equal(t, "flex", *extractOpenAIServiceTier(map[string]any{"service_tier": "flex"}))
	require.Equal(t, "auto", *extractOpenAIServiceTier(map[string]any{"service_tier": "auto"}))
	require.Equal(t, "default", *extractOpenAIServiceTier(map[string]any{"service_tier": "default"}))
	require.Equal(t, "scale", *extractOpenAIServiceTier(map[string]any{"service_tier": "scale"}))
	require.Equal(t, "ultrafast", *extractOpenAIServiceTier(map[string]any{"service_tier": "ultrafast"}))
	require.Nil(t, extractOpenAIServiceTier(map[string]any{"service_tier": 1}))
	require.Nil(t, extractOpenAIServiceTier(nil))
}

func TestExtractOpenAIServiceTierFromBody(t *testing.T) {
	require.Equal(t, "priority", *extractOpenAIServiceTierFromBody([]byte(`{"service_tier":"fast"}`)))
	require.Equal(t, "flex", *extractOpenAIServiceTierFromBody([]byte(`{"service_tier":"flex"}`)))
	require.Equal(t, "auto", *extractOpenAIServiceTierFromBody([]byte(`{"service_tier":"auto"}`)))
	require.Equal(t, "default", *extractOpenAIServiceTierFromBody([]byte(`{"service_tier":"default"}`)))
	require.Equal(t, "scale", *extractOpenAIServiceTierFromBody([]byte(`{"service_tier":"scale"}`)))
	require.Equal(t, "ultrafast", *extractOpenAIServiceTierFromBody([]byte(`{"service_tier":"ultrafast"}`)))
	require.Nil(t, extractOpenAIServiceTierFromBody([]byte(`{"service_tier":"turbo"}`)))
	require.Nil(t, extractOpenAIServiceTierFromBody(nil))
}

func TestOpenAIGatewayServiceRecordUsage_UsesRequestedModelAndUpstreamModelMetadataFields(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	serviceTier := "priority"
	reasoning := "high"

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:                "resp_billing_model_override",
			BillingModel:             "gpt-5.1-codex",
			Model:                    "gpt-5.1",
			UpstreamModel:            "gpt-5.1-codex",
			ServiceTier:              &serviceTier,
			ReasoningEffort:          &reasoning,
			RequestedReasoningEffort: &reasoning,
			Usage: openai.ForwardUsage{
				InputTokens:  20,
				OutputTokens: 10,
			},
			Duration:     2 * time.Second,
			FirstTokenMs: func() *int { v := 120; return &v }(),
		},
		APIKey:    &apikey.APIKey{ID: 10, GroupID: i64p(11), Group: &routing.Group{ID: 11, RateMultiplier: 1.2}},
		User:      &identity.User{ID: 20},
		Account:   &Account{ID: 30},
		UserAgent: "codex-cli/1.0",
		IPAddress: "127.0.0.1",
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "gpt-5.1", usageRepo.lastLog.Model)
	require.Equal(t, "gpt-5.1", usageRepo.lastLog.RequestedModel)
	require.NotNil(t, usageRepo.lastLog.UpstreamModel)
	require.Equal(t, "gpt-5.1-codex", *usageRepo.lastLog.UpstreamModel)
	require.NotNil(t, usageRepo.lastLog.ServiceTier)
	require.Equal(t, serviceTier, *usageRepo.lastLog.ServiceTier)
	require.NotNil(t, usageRepo.lastLog.ReasoningEffort)
	require.Equal(t, reasoning, *usageRepo.lastLog.ReasoningEffort)
	require.NotNil(t, usageRepo.lastLog.RequestedReasoningEffort)
	require.Equal(t, reasoning, *usageRepo.lastLog.RequestedReasoningEffort)
	require.NotNil(t, usageRepo.lastLog.UserAgent)
	require.Equal(t, "codex-cli/1.0", *usageRepo.lastLog.UserAgent)
	require.NotNil(t, usageRepo.lastLog.IPAddress)
	require.Equal(t, "127.0.0.1", *usageRepo.lastLog.IPAddress)
	require.NotNil(t, usageRepo.lastLog.GroupID)
	require.Equal(t, int64(11), *usageRepo.lastLog.GroupID)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, usageRepo.lastLog.ActualCost, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
}

// TestOpenAIGatewayServiceRecordUsage_PersistsRequestedReasoningEffort 验证显式请求档位与实际档位分开落库。
func TestOpenAIGatewayServiceRecordUsage_PersistsRequestedReasoningEffort(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	requested := "max"
	forwarded := "xhigh"

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:                "resp_requested_effort",
			Model:                    "gpt-5.4",
			ReasoningEffort:          &forwarded,
			RequestedReasoningEffort: &requested,
			Usage: openai.ForwardUsage{
				InputTokens:  20,
				OutputTokens: 10,
			},
			Duration: time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10},
		User:    &identity.User{ID: 20},
		Account: &Account{ID: 30},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.RequestedReasoningEffort)
	require.Equal(t, requested, *usageRepo.lastLog.RequestedReasoningEffort)
	require.Equal(t, forwarded, *usageRepo.lastLog.ReasoningEffort)
}

func TestOpenAIGatewayServiceRecordUsage_PreservesChannelMappedUpstreamModel(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:     "openai_channel_mapping_models",
			Model:         "gpt-5.6-terra",
			UpstreamModel: "gpt-5.6-terra",
			Usage: openai.ForwardUsage{
				InputTokens:  20,
				OutputTokens: 10,
			},
			Duration: time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10},
		User:    &identity.User{ID: 20},
		Account: &Account{ID: 30},
		ChannelUsageFields: routing.ChannelUsageFields{
			OriginalModel:      "gpt-5.6-sol",
			ChannelMappedModel: "gpt-5.6-terra",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "gpt-5.6-sol", usageRepo.lastLog.RequestedModel)
	require.Equal(t, "gpt-5.6-terra", usageRepo.lastLog.Model)
	require.NotNil(t, usageRepo.lastLog.UpstreamModel)
	require.Equal(t, "gpt-5.6-terra", *usageRepo.lastLog.UpstreamModel)
}

func TestOpenAIGatewayServiceRecordUsage_PreservesLoopedChannelAndAccountUpstreamModel(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:     "openai_looped_mapping_models",
			Model:         "gpt-5.6-terra",
			UpstreamModel: "gpt-5.6-sol",
			Usage:         openai.ForwardUsage{InputTokens: 20, OutputTokens: 10},
			Duration:      time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10},
		User:    &identity.User{ID: 20},
		Account: &Account{ID: 30},
		ChannelUsageFields: routing.ChannelUsageFields{
			OriginalModel:      "gpt-5.6-sol",
			ChannelMappedModel: "gpt-5.6-terra",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "gpt-5.6-sol", usageRepo.lastLog.RequestedModel)
	require.Equal(t, "gpt-5.6-terra", usageRepo.lastLog.Model)
	require.NotNil(t, usageRepo.lastLog.UpstreamModel)
	require.Equal(t, "gpt-5.6-sol", *usageRepo.lastLog.UpstreamModel)
}

func TestOpenAIGatewayServiceRecordUsage_BillsMappedRequestsUsingRequestedModel(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}

	// Billing should use the requested model ("gpt-5.1"), not the upstream mapped model ("gpt-5.1-codex").
	// This ensures pricing is always based on the model the user requested.
	expectedCost, err := svc.billingService.CalculateCost("gpt-5.1", pricing.UsageTokens{
		InputTokens:  20,
		OutputTokens: 10,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:     "resp_upstream_model_billing_fallback",
			Model:         "gpt-5.1",
			UpstreamModel: "gpt-5.1-codex",
			Usage:         usage,
			Duration:      time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10},
		User:    &identity.User{ID: 20},
		Account: &Account{ID: 30},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "gpt-5.1", usageRepo.lastLog.Model)
	require.Equal(t, expectedCost.ActualCost, usageRepo.lastLog.ActualCost)
	require.Equal(t, expectedCost.TotalCost, usageRepo.lastLog.TotalCost)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.Equal(t, expectedCost.ActualCost, billingRepo.lastCmd.BillableAmountUSD)
}

func TestOpenAIGatewayServiceRecordUsage_ChannelMappedDoesNotOverrideBillingModelWhenUnmapped(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}

	// 渠道未发生模型映射时，应使用 result.BillingModel 中记录的实际上游计费模型，
	// 而不是未映射的原始请求模型。
	expectedCost, err := svc.billingService.CalculateCost("gpt-5.1", pricing.UsageTokens{
		InputTokens:  20,
		OutputTokens: 10,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:     "resp_channel_unmapped_billing",
			Model:         "glm",
			BillingModel:  "gpt-5.1",
			UpstreamModel: "gpt-5.1",
			Usage:         usage,
			Duration:      time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10},
		User:    &identity.User{ID: 20},
		Account: &Account{ID: 30},
		ChannelUsageFields: routing.ChannelUsageFields{
			ChannelID:          1,
			OriginalModel:      "glm",
			ChannelMappedModel: "glm", // channel did NOT map
			BillingModelSource: routing.BillingModelSourceChannelMapped,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, expectedCost.ActualCost, usageRepo.lastLog.ActualCost)
	require.True(t, usageRepo.lastLog.ActualCost > 0, "cost must not be zero")
}

func TestOpenAIGatewayServiceRecordUsage_ChannelMappedOverridesBillingModelWhenMapped(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}

	// When channel DID map the model (ChannelMappedModel != OriginalModel),
	// billing should use the channel-mapped model, honoring admin intent.
	expectedCost, err := svc.billingService.CalculateCost("gpt-5.1", pricing.UsageTokens{
		InputTokens:  20,
		OutputTokens: 10,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:     "resp_channel_mapped_billing",
			Model:         "glm",
			BillingModel:  "gpt-5.1-codex",
			UpstreamModel: "gpt-5.1-codex",
			Usage:         usage,
			Duration:      time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10},
		User:    &identity.User{ID: 20},
		Account: &Account{ID: 30},
		ChannelUsageFields: routing.ChannelUsageFields{
			ChannelID:          1,
			OriginalModel:      "glm",
			ChannelMappedModel: "gpt-5.1", // channel mapped glm → gpt-5.1
			BillingModelSource: routing.BillingModelSourceChannelMapped,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, expectedCost.ActualCost, usageRepo.lastLog.ActualCost)
	require.True(t, usageRepo.lastLog.ActualCost > 0, "cost must not be zero")
}

func TestOpenAIGatewayServiceRecordUsage_UpstreamBillingSourceOverridesRequestedModel(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}

	expectedCost, err := svc.billingService.CalculateCost("gpt-5.4-mini", pricing.UsageTokens{
		InputTokens:  20,
		OutputTokens: 10,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:     "resp_upstream_billing",
			Model:         "gpt-5.4",
			BillingModel:  "gpt-5.4",
			UpstreamModel: "gpt-5.4-mini",
			Usage:         usage,
			Duration:      time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10},
		User:    &identity.User{ID: 20},
		Account: &Account{ID: 30},
		ChannelUsageFields: routing.ChannelUsageFields{
			ChannelID:          1,
			OriginalModel:      "gpt-5.4",
			ChannelMappedModel: "gpt-5.4",
			BillingModelSource: routing.BillingModelSourceUpstream,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, expectedCost.ActualCost, usageRepo.lastLog.ActualCost)
	require.True(t, usageRepo.lastLog.ActualCost > 0, "cost must not be zero")
}

func TestOpenAIGatewayServiceRecordUsage_ResponsesMappedBillingModelHonorsBillingModelSource(t *testing.T) {
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}
	tokens := pricing.UsageTokens{InputTokens: 20, OutputTokens: 10}

	tests := []struct {
		name               string
		billingModelSource string
		wantBillingModel   string
	}{
		{
			name:               "upstream uses mapped billing model",
			billingModelSource: routing.BillingModelSourceUpstream,
			wantBillingModel:   "gpt-5.5",
		},
		{
			name:               "requested overrides mapped billing model",
			billingModelSource: routing.BillingModelSourceRequested,
			wantBillingModel:   "gpt-5.4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			userRepo := &openAIRecordUsageUserRepoStub{}
			subRepo := &openAIRecordUsageSubRepoStub{}
			svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

			expectedCost, err := svc.billingService.CalculateCost(tt.wantBillingModel, tokens, 1.1)
			require.NoError(t, err)

			err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &forwardcore.OpenAIResult{
					RequestID:     "resp_mapped_billing_model_source",
					Model:         "gpt-5.4",
					BillingModel:  "gpt-5.5",
					UpstreamModel: "gpt-5.5",
					Usage:         usage,
					Duration:      time.Second,
				},
				APIKey:  &apikey.APIKey{ID: 10},
				User:    &identity.User{ID: 20},
				Account: &Account{ID: 30},
				ChannelUsageFields: routing.ChannelUsageFields{
					OriginalModel:      "gpt-5.4",
					ChannelMappedModel: "gpt-5.4",
					BillingModelSource: tt.billingModelSource,
				},
			})

			require.NoError(t, err)
			require.NotNil(t, usageRepo.lastLog)
			require.Equal(t, "gpt-5.4", usageRepo.lastLog.Model)
			require.InDelta(t, expectedCost.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
			billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
			require.NotNil(t, billingRepo.lastCmd)
			require.InDelta(t, expectedCost.ActualCost, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
			require.Zero(t, userRepo.deductCalls)
			require.True(t, usageRepo.lastLog.ActualCost > 0, "cost must not be zero")
		})
	}
}

// TestOpenAIUsageBillingModelPreservesImagePricingModel 验证图片轮次不会被文本上游模型覆盖计价。
func TestOpenAIUsageBillingModelPreservesImagePricingModel(t *testing.T) {
	tests := []struct {
		name   string
		result forwardcore.OpenAIResult
		fields routing.ChannelUsageFields
		want   string
	}{
		{
			name: "上游计费保留图片模型",
			result: forwardcore.OpenAIResult{
				Model:         "gpt-5.6-sol",
				UpstreamModel: "gpt-5.6-sol",
				BillingModel:  "gpt-image-2",
				ImageCount:    1,
			},
			fields: routing.ChannelUsageFields{BillingModelSource: routing.BillingModelSourceUpstream},
			want:   "gpt-image-2",
		},
		{
			name: "普通上游计费使用最终模型",
			result: forwardcore.OpenAIResult{
				Model:         "public-alias",
				UpstreamModel: "gpt-5.6-sol",
				BillingModel:  "channel-model",
			},
			fields: routing.ChannelUsageFields{BillingModelSource: routing.BillingModelSourceUpstream},
			want:   "gpt-5.6-sol",
		},
		{
			name: "未映射渠道计费保留图片模型",
			result: forwardcore.OpenAIResult{
				Model:         "gpt-5.6-sol",
				UpstreamModel: "gpt-5.6-sol",
				BillingModel:  "gpt-image-2",
				ImageCount:    1,
			},
			fields: routing.ChannelUsageFields{
				BillingModelSource: routing.BillingModelSourceChannelMapped,
				OriginalModel:      "gpt-5.6-sol",
				ChannelMappedModel: "gpt-5.6-sol",
			},
			want: "gpt-image-2",
		},
		{
			name: "请求模型来源覆盖图片模型",
			result: forwardcore.OpenAIResult{
				BillingModel: "gpt-image-2",
				ImageCount:   1,
			},
			fields: routing.ChannelUsageFields{
				BillingModelSource: routing.BillingModelSourceRequested,
				OriginalModel:      "public-image-alias",
			},
			want: "public-image-alias",
		},
		{
			name: "映射渠道来源覆盖图片模型",
			result: forwardcore.OpenAIResult{
				BillingModel: "gpt-image-2",
				ImageCount:   1,
			},
			fields: routing.ChannelUsageFields{
				BillingModelSource: routing.BillingModelSourceChannelMapped,
				OriginalModel:      "public-image-alias",
				ChannelMappedModel: "priced-channel-model",
			},
			want: "priced-channel-model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, openAIUsageBillingModel(&tt.result, tt.fields))
		})
	}
}

func TestOpenAIGatewayServiceRecordUsage_BillsCompactOpenAIModelAlias(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}

	expectedCost, err := svc.billingService.CalculateCost("gpt-5.5", pricing.UsageTokens{
		InputTokens:  20,
		OutputTokens: 10,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:     "resp_compact_openai_alias",
			Model:         "gpt5.5",
			UpstreamModel: "gpt-5.4",
			Usage:         usage,
			Duration:      time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10},
		User:    &identity.User{ID: 20},
		Account: &Account{ID: 30},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "gpt5.5", usageRepo.lastLog.Model)
	require.NotNil(t, usageRepo.lastLog.UpstreamModel)
	require.Equal(t, "gpt-5.4", *usageRepo.lastLog.UpstreamModel)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
	require.True(t, usageRepo.lastLog.ActualCost > 0, "cost must not be zero")
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.InDelta(t, expectedCost.ActualCost, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_FallsBackToUpstreamModelWhenPrimaryUnpriceable(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}

	expectedCost, err := svc.billingService.CalculateCost("gpt-5.4", pricing.UsageTokens{
		InputTokens:  20,
		OutputTokens: 10,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:     "resp_unpriceable_primary_upstream_fallback",
			Model:         "not-priceable-alias",
			BillingModel:  "not-priceable-alias",
			UpstreamModel: "gpt-5.4",
			Usage:         usage,
			Duration:      time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10},
		User:    &identity.User{ID: 20},
		Account: &Account{ID: 30},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
	require.True(t, usageRepo.lastLog.ActualCost > 0, "cost must not be zero")
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.InDelta(t, expectedCost.ActualCost, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_UnpricedTokenModelFallsBackToZeroCostUsageLog(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_unpriceable_without_upstream",
			Model:     "not-priceable-alias",
			Usage:     openai.ForwardUsage{InputTokens: 20, OutputTokens: 10},
			Duration:  time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10},
		User:    &identity.User{ID: 20},
		Account: &Account{ID: 30},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "not-priceable-alias", usageRepo.lastLog.Model)
	require.Equal(t, 20, usageRepo.lastLog.InputTokens)
	require.Equal(t, 10, usageRepo.lastLog.OutputTokens)
	require.Zero(t, usageRepo.lastLog.TotalCost)
	require.Zero(t, usageRepo.lastLog.ActualCost)
	require.Equal(t, 0, userRepo.deductCalls)
	require.Equal(t, 0, subRepo.incrementCalls)
}

func TestOpenAIGatewayServiceRecordUsage_SubscriptionBillingSetsSubscriptionFields(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	subscription := &billing.UserSubscription{ID: 99}
	planID := int64(199)
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &billing.UsageBillingApplyResult{
		Applied:               true,
		SubscriptionAmountUSD: 12.5,
		BillingAllocations: []billing.BillingAllocation{
			{
				Type:           billing.BillingAllocationTypeSubscription,
				AmountUSD:      12.5,
				SubscriptionID: &subscription.ID,
				PlanID:         &planID,
			},
		},
	}}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_subscription_billing",
			Usage:     openai.ForwardUsage{InputTokens: 10, OutputTokens: 5},
			Model:     "gpt-5.1",
			Duration:  time.Second,
		},
		APIKey:       &apikey.APIKey{ID: 100, GroupID: i64p(88), Group: &routing.Group{ID: 88, RateMultiplier: 1.0}},
		User:         &identity.User{ID: 200},
		Account:      &Account{ID: 300},
		Subscription: subscription,
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, usagecore.BillingTypeSubscription, usageRepo.lastLog.BillingType)
	require.NotNil(t, usageRepo.lastLog.SubscriptionID)
	require.Equal(t, subscription.ID, *usageRepo.lastLog.SubscriptionID)
	require.Equal(t, 1, billingRepo.calls)
	require.Equal(t, 0, userRepo.deductCalls)
}

func TestOpenAIGatewayServiceRecordUsage_SubscriptionBillingUsesPlanGroupRateOverUserGroupRate(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	userGroupRate := 0.17
	rateRepo := &openAIUserGroupRateRepoStub{rate: &userGroupRate}
	planID := int64(199)
	subscription := &billing.UserSubscription{
		ID: 99,
		Plan: &billing.SubscriptionPlan{
			ID:       planID,
			GroupIDs: []int64{88},
			GroupRateMultipliers: map[int64]float64{
				88: 0.5,
			},
		},
	}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &billing.UsageBillingApplyResult{
		Applied:               true,
		SubscriptionAmountUSD: 1,
		BillingAllocations: []billing.BillingAllocation{
			{
				Type:           billing.BillingAllocationTypeSubscription,
				AmountUSD:      1,
				SubscriptionID: &subscription.ID,
				PlanID:         &planID,
			},
		},
	}}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, rateRepo)

	usage := openai.ForwardUsage{InputTokens: 10, OutputTokens: 5}
	expectedCost := expectedOpenAICost(t, svc, "gpt-5.1", usage, 0.5)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_subscription_group_rate",
			Usage:     usage,
			Model:     "gpt-5.1",
			Duration:  time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      100,
			GroupID: i64p(88),
			Group: &routing.Group{
				ID:             88,
				RateMultiplier: 1.0,
			},
		},
		User:         &identity.User{ID: 200},
		Account:      &Account{ID: 300},
		Subscription: subscription,
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, usagecore.BillingTypeSubscription, usageRepo.lastLog.BillingType)
	require.NotNil(t, usageRepo.lastLog.SubscriptionID)
	require.Equal(t, subscription.ID, *usageRepo.lastLog.SubscriptionID)
	require.InDelta(t, 0.5, usageRepo.lastLog.RateMultiplier, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
	require.Equal(t, 0, rateRepo.calls)
}

func TestOpenAIGatewayServiceRecordUsage_InferredSubscriptionUsesPlanGroupRate(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	now := time.Now()
	subscription := billing.UserSubscription{
		ID:        99,
		UserID:    200,
		PlanID:    199,
		StartsAt:  now.Add(-time.Hour),
		ExpiresAt: now.Add(time.Hour),
		Status:    billing.SubscriptionStatusActive,
		Plan: &billing.SubscriptionPlan{
			ID:       199,
			GroupIDs: []int64{88},
			GroupRateMultipliers: map[int64]float64{
				88: 0.5,
			},
		},
	}
	subRepo := &openAIRecordUsageSubRepoStub{}
	userGroupRate := 0.17
	rateRepo := &openAIUserGroupRateRepoStub{rate: &userGroupRate}
	planID := subscription.PlanID
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &billing.UsageBillingApplyResult{
		Applied:               true,
		SubscriptionAmountUSD: 1,
		BillingAllocations: []billing.BillingAllocation{
			{
				Type:           billing.BillingAllocationTypeSubscription,
				AmountUSD:      1,
				SubscriptionID: &subscription.ID,
				PlanID:         &planID,
			},
		},
	}}
	billingRepo.resolveSub = &subscription
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, rateRepo)

	usage := openai.ForwardUsage{InputTokens: 10, OutputTokens: 5}
	expectedCost := expectedOpenAICost(t, svc, "gpt-5.1", usage, 0.5)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_inferred_subscription_group_rate",
			Usage:     usage,
			Model:     "gpt-5.1",
			Duration:  time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      100,
			GroupID: i64p(88),
			Group: &routing.Group{
				ID:             88,
				RateMultiplier: 1.0,
			},
		},
		User:    &identity.User{ID: 200},
		Account: &Account{ID: 300},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, usagecore.BillingTypeSubscription, usageRepo.lastLog.BillingType)
	require.NotNil(t, usageRepo.lastLog.SubscriptionID)
	require.Equal(t, subscription.ID, *usageRepo.lastLog.SubscriptionID)
	require.InDelta(t, 0.5, usageRepo.lastLog.RateMultiplier, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
	require.Equal(t, 0, rateRepo.calls)
	require.Equal(t, 1, billingRepo.resolveCalls)
}

func TestOpenAIGatewayServiceRecordUsage_SimpleModeSkipsBillingAfterPersist(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	svc.cfg.RunMode = config.RunModeSimple

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_simple_mode",
			Usage:     openai.ForwardUsage{InputTokens: 10, OutputTokens: 5},
			Model:     "gpt-5.1",
			Duration:  time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 1000},
		User:    &identity.User{ID: 2000},
		Account: &Account{ID: 3000},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	require.Equal(t, 0, userRepo.deductCalls)
	require.Equal(t, 0, subRepo.incrementCalls)
}

func TestOpenAIGatewayServiceRecordUsage_ImageOnlyUsageStillPersists(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:  "resp_image_only_usage",
			Model:      "gpt-image-2",
			ImageCount: 2,
			ImageSize:  "1K",
			Duration:   time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 1007},
		User:    &identity.User{ID: 2007},
		Account: &Account{ID: 3007},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, 2, usageRepo.lastLog.ImageCount)
	require.NotNil(t, usageRepo.lastLog.ImageSize)
	require.Equal(t, "1K", *usageRepo.lastLog.ImageSize)
	require.NotNil(t, usageRepo.lastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.lastLog.BillingMode)
}

func TestOpenAIGatewayServiceRecordUsage_EmptyImageSizeDefaultsBeforeBillingAndPersistence(t *testing.T) {
	imagePrice2K := 0.31
	groupID := int64(1201)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:  "resp_image_default_size",
			Model:      "gpt-image-2",
			ImageCount: 2,
			ImageSize:  "",
			Duration:   time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      11201,
			GroupID: i64p(groupID),
			Group: &routing.Group{
				ID:             groupID,
				RateMultiplier: 1.0,
				ModelPricing:   testImageModelPricing(map[string]*float64{"2K": &imagePrice2K}),
			},
		},
		User:    &identity.User{ID: 21201},
		Account: &Account{ID: 31201},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, 2, usageRepo.lastLog.ImageCount)
	require.NotNil(t, usageRepo.lastLog.ImageSize)
	require.Equal(t, pricing.ImageBillingSize2K, *usageRepo.lastLog.ImageSize)
	require.NotNil(t, usageRepo.lastLog.ImageSizeSource)
	require.Equal(t, pricing.ImageSizeSourceDefault, *usageRepo.lastLog.ImageSizeSource)
	require.Nil(t, usageRepo.lastLog.ImageInputSize)
	require.Nil(t, usageRepo.lastLog.ImageOutputSize)
	require.InDelta(t, 0.62, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.62, usageRepo.lastLog.ActualCost, 1e-12)
	require.NotNil(t, usageRepo.lastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.lastLog.BillingMode)
}

func TestOpenAIGatewayServiceRecordUsage_OutputImageSizeWinsBeforeBillingAndPersistence(t *testing.T) {
	imagePrice1K := 0.11
	imagePrice4K := 0.44
	groupID := int64(1202)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:        "resp_image_output_size",
			Model:            "gpt-image-2",
			ImageCount:       1,
			ImageInputSize:   "1024x1024",
			ImageOutputSizes: []string{"3840x2160"},
			Duration:         time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      11202,
			GroupID: i64p(groupID),
			Group: &routing.Group{
				ID:             groupID,
				RateMultiplier: 1.0,
				ModelPricing:   testImageModelPricing(map[string]*float64{"1K": &imagePrice1K, "4K": &imagePrice4K}),
			},
		},
		User:    &identity.User{ID: 21202},
		Account: &Account{ID: 31202},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.ImageSize)
	require.Equal(t, pricing.ImageBillingSize4K, *usageRepo.lastLog.ImageSize)
	require.NotNil(t, usageRepo.lastLog.ImageInputSize)
	require.Equal(t, "1024x1024", *usageRepo.lastLog.ImageInputSize)
	require.NotNil(t, usageRepo.lastLog.ImageOutputSize)
	require.Equal(t, "3840x2160", *usageRepo.lastLog.ImageOutputSize)
	require.NotNil(t, usageRepo.lastLog.ImageSizeSource)
	require.Equal(t, pricing.ImageSizeSourceOutput, *usageRepo.lastLog.ImageSizeSource)
	require.Equal(t, map[string]int{pricing.ImageBillingSize4K: 1}, usageRepo.lastLog.ImageSizeBreakdown)
	require.InDelta(t, 0.44, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.44, usageRepo.lastLog.ActualCost, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_ImageUsesPerImageBillingEvenWithUsageTokens(t *testing.T) {
	imagePrice := 0.02
	groupID := int64(12)

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_image_per_request",
			Model:     "gpt-image-2",
			Usage: openai.ForwardUsage{
				InputTokens:       1110,
				OutputTokens:      1756,
				ImageOutputTokens: 1756,
			},
			ImageCount: 2,
			ImageSize:  "1K",
			Duration:   time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      1008,
			GroupID: i64p(groupID),
			Group: &routing.Group{
				ID:             groupID,
				RateMultiplier: 1.0,
				ModelPricing:   testImageModelPricing(map[string]*float64{"1K": &imagePrice}),
			},
		},
		User:    &identity.User{ID: 2008},
		Account: &Account{ID: 3008},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.lastLog.BillingMode)
	require.Equal(t, 2, usageRepo.lastLog.ImageCount)
	require.InDelta(t, 0.04, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.04, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, 0.0, usageRepo.lastLog.InputCost, 1e-12)
	require.InDelta(t, 0.0, usageRepo.lastLog.OutputCost, 1e-12)
	require.InDelta(t, 0.0, usageRepo.lastLog.ImageOutputCost, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_ImageSharedMultiplierPreservesExistingBehavior(t *testing.T) {
	imagePrice := 0.2
	groupID := int64(121)

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:  "resp_image_shared_multiplier",
			Model:      "gpt-image-2",
			ImageCount: 1,
			ImageSize:  "1K",
			Duration:   time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      10121,
			GroupID: i64p(groupID),
			Group: &routing.Group{
				ID:             groupID,
				RateMultiplier: 0.15,
				ModelPricing:   testImageModelPricing(map[string]*float64{"1K": &imagePrice}),
			},
		},
		User:    &identity.User{ID: 20121},
		Account: &Account{ID: 30121},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, 0.2, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.03, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, 0.15, usageRepo.lastLog.RateMultiplier, 1e-12)
	require.NotNil(t, usageRepo.lastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.lastLog.BillingMode)
}

func TestOpenAIGatewayServiceRecordUsage_ImageSharedMultiplierUsesUserGroupOverride(t *testing.T) {
	imagePrice := 0.5
	userRate := 0.2
	groupID := int64(125)

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(
		usageRepo,
		&openAIRecordUsageUserRepoStub{},
		&openAIRecordUsageSubRepoStub{},
		&openAIUserGroupRateRepoStub{rate: &userRate},
	)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:  "resp_image_user_group_override",
			Model:      "gpt-image-2",
			ImageCount: 1,
			ImageSize:  "1K",
			Duration:   time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      10125,
			GroupID: i64p(groupID),
			Group: &routing.Group{
				ID:             groupID,
				RateMultiplier: 0.15,
				ModelPricing:   testImageModelPricing(map[string]*float64{"1K": &imagePrice}),
			},
		},
		User:    &identity.User{ID: 20125},
		Account: &Account{ID: 30125},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, 0.5, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.1, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, 0.2, usageRepo.lastLog.RateMultiplier, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_GrokVideoUsesDefaultRateCard(t *testing.T) {
	groupID := int64(1261)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:       "video-default-rate-card",
			ResponseID:      "video-default-rate-card",
			Model:           "grok-imagine-video-1.5",
			BillingModel:    "grok-imagine-video-1.5",
			ImageCount:      0,
			VideoCount:      1,
			VideoResolution: pricing.VideoBillingResolution720P,
			Duration:        time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      101261,
			GroupID: i64p(groupID),
			Group: &routing.Group{
				ID:             groupID,
				Platform:       capability.PlatformGrok,
				RateMultiplier: 1,
			},
		},
		User:    &identity.User{ID: 201261},
		Account: &Account{ID: 301261, Platform: capability.PlatformGrok},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Nil(t, usageRepo.lastLog.ImageSize)
	// 结果未携带 duration 时按上游默认 8 秒计费：0.14 USD/s × 8s。
	require.InDelta(t, 0.14*8, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.14*8, usageRepo.lastLog.ActualCost, 1e-12)
	require.Equal(t, 0, usageRepo.lastLog.ImageCount)
	require.NotNil(t, usageRepo.lastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeVideo), *usageRepo.lastLog.BillingMode)
	require.Equal(t, 1, usageRepo.lastLog.VideoCount)
	require.NotNil(t, usageRepo.lastLog.VideoDurationSeconds)
	require.Equal(t, pricing.VideoBillingDefaultDurationSeconds, *usageRepo.lastLog.VideoDurationSeconds)
}

func TestOpenAIGatewayServiceRecordUsage_GroupImagePriceOverridesChannelImagePrice(t *testing.T) {
	groupID := int64(127)
	channelPrice := 0.201
	groupImagePrice2K := 0.021
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	svc.resolver = newOpenAIImageChannelPricingResolverForTest(t, groupID, "grok-imagine-image-quality", channelPrice)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:    "resp_grok_image_group_price",
			Model:        "grok-imagine-image-quality",
			BillingModel: "grok-imagine-image-quality",
			ImageCount:   1,
			ImageSize:    pricing.ImageBillingSize2K,
			Duration:     time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      10127,
			GroupID: i64p(groupID),
			Group: &routing.Group{
				ID:             groupID,
				Platform:       capability.PlatformGrok,
				RateMultiplier: 1,
				ModelPricing:   testImageModelPricing(map[string]*float64{"2K": &groupImagePrice2K}),
			},
		},
		User:    &identity.User{ID: 20127},
		Account: &Account{ID: 30127, Platform: capability.PlatformGrok},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, 1, usageRepo.lastLog.ImageCount)
	require.Equal(t, pricing.ImageBillingSize2K, *usageRepo.lastLog.ImageSize)
	require.InDelta(t, 0.021, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.021, usageRepo.lastLog.ActualCost, 1e-12)
	require.NotNil(t, usageRepo.lastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.lastLog.BillingMode)
}

func TestOpenAIGatewayServiceRecordUsage_GroupVideoPriceOverridesChannelImagePrice(t *testing.T) {
	groupID := int64(128)
	channelPrice := 0.201
	groupVideoPrice720P := 0.037
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	svc.resolver = newOpenAIImageChannelPricingResolverForTest(t, groupID, "grok-imagine-video", channelPrice)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:            "resp_grok_video_group_price",
			Model:                "grok-imagine-video",
			BillingModel:         "grok-imagine-video",
			ImageCount:           0,
			VideoCount:           1,
			VideoResolution:      pricing.VideoBillingResolution720P,
			VideoDurationSeconds: 1,
			Duration:             time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      10128,
			GroupID: i64p(groupID),
			Group: &routing.Group{
				ID:             groupID,
				Platform:       capability.PlatformGrok,
				RateMultiplier: 1,
				ModelPricing:   testVideoModelPricing(map[string]*float64{"720p": &groupVideoPrice720P}),
			},
		},
		User:    &identity.User{ID: 20128},
		Account: &Account{ID: 30128, Platform: capability.PlatformGrok},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, 0, usageRepo.lastLog.ImageCount)
	require.Nil(t, usageRepo.lastLog.ImageSize)
	require.InDelta(t, 0.037, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.037, usageRepo.lastLog.ActualCost, 1e-12)
	require.NotNil(t, usageRepo.lastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeVideo), *usageRepo.lastLog.BillingMode)
}

// 视频请求命中渠道 token 计费时走 token 路径；此时行是 billing_mode='token'、image_count=1、
// image_size=NULL，必须携带 video_count>0 才能通过 usage_logs 的 image_size check 约束
// （迁移 194），否则整个计费事务会因约束违反而丢失。
func TestOpenAIGatewayServiceRecordUsage_GrokVideoWithTokenChannelPricingKeepsVideoMetadata(t *testing.T) {
	groupID := int64(132)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	svc.resolver = newOpenAITokenImageChannelPricingResolverForTest(t, groupID, "grok-imagine-video")

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:            "resp_grok_video_token_channel",
			Model:                "grok-imagine-video",
			BillingModel:         "grok-imagine-video",
			ImageCount:           0,
			VideoCount:           1,
			VideoResolution:      pricing.VideoBillingResolution720P,
			VideoDurationSeconds: 5,
			Usage:                openai.ForwardUsage{InputTokens: 100, OutputTokens: 200},
			Duration:             time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      10132,
			GroupID: i64p(groupID),
			Group: &routing.Group{
				ID:             groupID,
				Platform:       capability.PlatformGrok,
				RateMultiplier: 1,
			},
		},
		User:    &identity.User{ID: 20132},
		Account: &Account{ID: 30132, Platform: capability.PlatformGrok},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeToken), *usageRepo.lastLog.BillingMode)
	require.Nil(t, usageRepo.lastLog.ImageSize)
	require.Equal(t, 0, usageRepo.lastLog.ImageCount)
	require.Equal(t, 1, usageRepo.lastLog.VideoCount)
	require.NotNil(t, usageRepo.lastLog.VideoResolution)
	require.Equal(t, pricing.VideoBillingResolution720P, *usageRepo.lastLog.VideoResolution)
	require.NotNil(t, usageRepo.lastLog.VideoDurationSeconds)
	require.Equal(t, 5, *usageRepo.lastLog.VideoDurationSeconds)
}

func TestOpenAIGatewayServiceRecordUsage_ChannelImageBillingUsesImageCountAndSharedMultiplier(t *testing.T) {
	groupID := int64(123)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	svc.resolver = newOpenAIImageChannelPricingResolverForTest(t, groupID, "gpt-image-2", 0.25)

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:  "resp_image_channel_shared",
			Model:      "gpt-image-2",
			ImageCount: 3,
			ImageSize:  "1K",
			Duration:   time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      10123,
			GroupID: i64p(groupID),
			Group: &routing.Group{
				ID:             groupID,
				RateMultiplier: 0.15,
			},
		},
		User:    &identity.User{ID: 20123},
		Account: &Account{ID: 30123},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, 0.75, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.1125, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, 0.15, usageRepo.lastLog.RateMultiplier, 1e-12)
	require.Equal(t, 3, usageRepo.lastLog.ImageCount)
	require.NotNil(t, usageRepo.lastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.lastLog.BillingMode)
}

func newOpenAIImageChannelPricingResolverForTest(t *testing.T, groupID int64, model string, price float64) *billing.PriceResolver {
	t.Helper()
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, model: model}] = &routing.ChannelModelPricing{
		BillingMode:     routing.BillingModeImage,
		PerRequestPrice: &price,
	}
	cache.channelByGroupID[groupID] = &routing.Channel{ID: groupID, Status: billing.StatusActive}
	cache.groupPlatform[groupID] = ""
	cache.loadedAt = time.Now()

	cs := seedChannelFixture(cache)
	return NewModelPricingResolver(cs, NewBillingService(&config.Config{}, nil))
}

func newOpenAITokenImageChannelPricingResolverForTest(t *testing.T, groupID int64, model string) *billing.PriceResolver {
	t.Helper()
	inputPrice := 3e-6
	outputPrice := 15e-6
	imageOutputPrice := 15e-6
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, model: model}] = &routing.ChannelModelPricing{
		BillingMode:      routing.BillingModeToken,
		InputPrice:       &inputPrice,
		OutputPrice:      &outputPrice,
		ImageOutputPrice: &imageOutputPrice,
	}
	cache.channelByGroupID[groupID] = &routing.Channel{ID: groupID, Status: billing.StatusActive}
	cache.groupPlatform[groupID] = ""
	cache.loadedAt = time.Now()

	cs := seedChannelFixture(cache)
	return NewModelPricingResolver(cs, NewBillingService(&config.Config{}, nil))
}

func TestGatewayServiceCalculateRecordUsageCost_ChannelImageBillingUsesImageCount(t *testing.T) {
	groupID := int64(126)
	billingService := NewBillingService(&config.Config{}, nil)
	svc := &GatewayService{
		billingService: billingService,
		resolver:       newOpenAIImageChannelPricingResolverForTest(t, groupID, "gemini-image", 0.25),
	}

	cost := svc.calculateRecordUsageCost(
		context.Background(),
		&forwardcore.MessagesResult{Model: "gemini-image", ImageCount: 2, ImageSize: "1K"},
		&apikey.APIKey{GroupID: i64p(groupID), Group: &routing.Group{ID: groupID}},
		nil,
		"gemini-image",
		"gemini-image",
		"",
		"",
		0.15,
		1.0,
		nil,
	)

	require.NotNil(t, cost)
	require.Equal(t, string(routing.BillingModeImage), cost.BillingMode)
	require.InDelta(t, 0.5, cost.TotalCost, 1e-12)
	require.InDelta(t, 0.5, cost.ActualCost, 1e-12)
}

func TestGatewayServiceCalculateRecordUsageCost_ChannelImageBillingUsesSizeTier(t *testing.T) {
	groupID := int64(127)
	defaultPrice := 0.10
	price4K := 0.40
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, model: "gemini-image"}] = &routing.ChannelModelPricing{
		BillingMode:     routing.BillingModeImage,
		PerRequestPrice: &defaultPrice,
		Intervals: []routing.PricingInterval{{
			TierLabel:       "4K",
			PerRequestPrice: &price4K,
		}},
	}
	cache.channelByGroupID[groupID] = &routing.Channel{ID: groupID, Status: billing.StatusActive}
	cache.loadedAt = time.Now()

	channelService := seedChannelFixture(cache)

	svc := &GatewayService{
		billingService: NewBillingService(&config.Config{}, nil),
		resolver:       NewModelPricingResolver(channelService, NewBillingService(&config.Config{}, nil)),
	}

	cost := svc.calculateRecordUsageCost(
		context.Background(),
		&forwardcore.MessagesResult{Model: "gemini-image", ImageCount: 2, ImageSize: "4K"},
		&apikey.APIKey{GroupID: i64p(groupID), Group: &routing.Group{ID: groupID}},
		nil,
		"gemini-image",
		"gemini-image",
		"",
		"",
		1.0,
		1.0,
		nil,
	)

	require.NotNil(t, cost)
	require.Equal(t, string(routing.BillingModeImage), cost.BillingMode)
	require.InDelta(t, 0.80, cost.TotalCost, 1e-12)
	require.InDelta(t, 0.80, cost.ActualCost, 1e-12)
}

func TestGatewayServiceCalculateRecordUsageCost_GroupImagePriceOverridesChannelImagePrice(t *testing.T) {
	groupID := int64(129)
	channelPrice := 0.25
	groupImagePrice2K := 0.021

	svc := &GatewayService{
		billingService: NewBillingService(&config.Config{}, nil),
		resolver:       newOpenAIImageChannelPricingResolverForTest(t, groupID, "gemini-image", channelPrice),
	}

	cost := svc.calculateRecordUsageCost(
		context.Background(),
		&forwardcore.MessagesResult{Model: "gemini-image", ImageCount: 2, ImageSize: pricing.ImageBillingSize2K},
		&apikey.APIKey{
			GroupID: i64p(groupID),
			Group: &routing.Group{
				ID:           groupID,
				ModelPricing: testImageModelPricing(map[string]*float64{"2K": &groupImagePrice2K}),
			},
		},
		nil,
		"gemini-image",
		"gemini-image",
		"",
		"",
		1.0,
		1.0,
		nil,
	)

	require.NotNil(t, cost)
	require.Equal(t, string(routing.BillingModeImage), cost.BillingMode)
	require.InDelta(t, 0.042, cost.TotalCost, 1e-12)
	require.InDelta(t, 0.042, cost.ActualCost, 1e-12)
}

func TestGatewayServiceCalculateRecordUsageCost_ChannelImageBillingNormalizesMissingSizeTier(t *testing.T) {
	groupID := int64(128)
	defaultPrice := 0.10
	price2K := 0.22
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, model: "gemini-image"}] = &routing.ChannelModelPricing{
		BillingMode:     routing.BillingModeImage,
		PerRequestPrice: &defaultPrice,
		Intervals: []routing.PricingInterval{{
			TierLabel:       "2K",
			PerRequestPrice: &price2K,
		}},
	}
	cache.channelByGroupID[groupID] = &routing.Channel{ID: groupID, Status: billing.StatusActive}
	cache.loadedAt = time.Now()

	channelService := seedChannelFixture(cache)

	svc := &GatewayService{
		billingService: NewBillingService(&config.Config{}, nil),
		resolver:       NewModelPricingResolver(channelService, NewBillingService(&config.Config{}, nil)),
	}

	cost := svc.calculateRecordUsageCost(
		context.Background(),
		&forwardcore.MessagesResult{Model: "gemini-image", ImageCount: 2, ImageSize: ""},
		&apikey.APIKey{GroupID: i64p(groupID), Group: &routing.Group{ID: groupID}},
		nil,
		"gemini-image",
		"gemini-image",
		"",
		"",
		1.0,
		1.0,
		nil,
	)

	require.NotNil(t, cost)
	require.Equal(t, string(routing.BillingModeImage), cost.BillingMode)
	require.InDelta(t, 0.44, cost.TotalCost, 1e-12)
	require.InDelta(t, 0.44, cost.ActualCost, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_ServiceTierDowngradedByUpstreamResponse(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	serviceTier := "priority"
	usage := openai.ForwardUsage{InputTokens: 100, OutputTokens: 50}

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:                   "resp_service_tier_downgraded",
			ServiceTier:                 &serviceTier,
			UpstreamResponseServiceTier: "default",
			Usage:                       usage,
			Model:                       "gpt-5.4",
			Duration:                    time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 1017},
		User:    &identity.User{ID: 2017},
		Account: &Account{ID: 3017, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.ServiceTier)
	require.Equal(t, "default", *usageRepo.lastLog.ServiceTier, "usage log must record the tier actually billed")

	baseCost, calcErr := svc.billingService.CalculateCost("gpt-5.4", pricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1.0)
	require.NoError(t, calcErr)
	require.InDelta(t, baseCost.TotalCost, usageRepo.lastLog.TotalCost, 1e-10, "a request served at default must not pay the priority price")
}

func TestOpenAIGatewayServiceRecordUsage_CodexDefaultEchoKeepsFastBilling(t *testing.T) {
	for _, accountType := range []string{capability.AccountTypeOAuth, capability.AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			svc := newOpenAIRecordUsageServiceForTest(
				usageRepo,
				&openAIRecordUsageUserRepoStub{},
				&openAIRecordUsageSubRepoStub{},
				nil,
			)
			serviceTier := "priority"
			tokens := pricing.UsageTokens{InputTokens: 100, OutputTokens: 50}

			err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
				Result: &forwardcore.OpenAIResult{
					RequestID:                   "resp_codex_default_echo",
					ServiceTier:                 &serviceTier,
					UpstreamResponseServiceTier: "default",
					Usage:                       openai.ForwardUsage{InputTokens: tokens.InputTokens, OutputTokens: tokens.OutputTokens},
					Model:                       "gpt-5.6-sol",
					Duration:                    time.Second,
				},
				APIKey:  &apikey.APIKey{ID: 1019},
				User:    &identity.User{ID: 2019},
				Account: &Account{ID: 3019, Platform: capability.PlatformOpenAI, Type: accountType},
			})

			require.NoError(t, err)
			require.NotNil(t, usageRepo.lastLog)
			require.NotNil(t, usageRepo.lastLog.ServiceTier)
			require.Equal(t, "priority", *usageRepo.lastLog.ServiceTier)

			fastCost, calcErr := svc.billingService.CalculateCostWithServiceTier("gpt-5.6-sol", tokens, 1.0, "priority")
			require.NoError(t, calcErr)
			require.InDelta(t, fastCost.TotalCost, usageRepo.lastLog.TotalCost, 1e-10)
		})
	}
}

func TestOpenAIGatewayServiceRecordUsage_ShadowUsesParentCredentialTierContract(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(
		usageRepo,
		&openAIRecordUsageUserRepoStub{},
		&openAIRecordUsageSubRepoStub{},
		nil,
	)
	parentID := int64(9001)
	accountRepo := &openAIRecordUsageAccountRepoStub{account: &Account{
		ID: parentID, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
	}}
	svc.accountRepo = accountRepo
	serviceTier := "priority"
	tokens := pricing.UsageTokens{InputTokens: 100, OutputTokens: 50}

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:                   "resp_shadow_codex_default_echo",
			ServiceTier:                 &serviceTier,
			UpstreamResponseServiceTier: "default",
			Usage:                       openai.ForwardUsage{InputTokens: tokens.InputTokens, OutputTokens: tokens.OutputTokens},
			Model:                       "gpt-5.6-sol",
			Duration:                    time.Second,
		},
		APIKey: &apikey.APIKey{ID: 1020},
		User:   &identity.User{ID: 2020},
		Account: &Account{
			ID: 3020, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
			ParentAccountID: &parentID,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, accountRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.ServiceTier)
	require.Equal(t, "priority", *usageRepo.lastLog.ServiceTier)

	fastCost, calcErr := svc.billingService.CalculateCostWithServiceTier("gpt-5.6-sol", tokens, 1.0, "priority")
	require.NoError(t, calcErr)
	require.InDelta(t, fastCost.TotalCost, usageRepo.lastLog.TotalCost, 1e-10)
}

// TestOpenAIGatewayServiceRecordUsage_FreeOpenAIFastChargesStandard 验证免费 Fast
// 只替换用户侧实际费用，Usage Log 仍保留 priority 的基础成本。
func TestOpenAIGatewayServiceRecordUsage_FreeOpenAIFastChargesStandard(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(
		usageRepo,
		&openAIRecordUsageUserRepoStub{},
		&openAIRecordUsageSubRepoStub{},
		nil,
	)
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	groupID := int64(77)
	serviceTier := "priority"
	tokens := pricing.UsageTokens{InputTokens: 100, OutputTokens: 50}
	inputPrice := 0.001
	outputPrice := 0.002
	fastMultiplier := 3.0
	apiKey := &apikey.APIKey{
		ID:      1020,
		GroupID: &groupID,
		Group: &routing.Group{
			ID: groupID, Platform: capability.PlatformOpenAI, Status: billing.StatusActive,
			Hydrated: true, RateMultiplier: 0.5, FreeOpenAIFast: true,
			ModelPricing: []routing.ChannelModelPricing{{
				Models:         []string{"gpt-5.6-sol"},
				BillingMode:    routing.BillingModeToken,
				InputPrice:     &inputPrice,
				OutputPrice:    &outputPrice,
				FastMultiplier: &fastMultiplier,
			}},
		},
	}

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:                   "resp_free_fast",
			ServiceTier:                 &serviceTier,
			UpstreamResponseServiceTier: "default",
			Usage:                       openai.ForwardUsage{InputTokens: tokens.InputTokens, OutputTokens: tokens.OutputTokens},
			Model:                       "gpt-5.6-sol",
			Duration:                    time.Second,
		},
		APIKey:  apiKey,
		User:    &identity.User{ID: 2020},
		Account: &Account{ID: 3020, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.ServiceTier)
	require.Equal(t, "priority", *usageRepo.lastLog.ServiceTier)
	require.InDelta(t, 0.5, usageRepo.lastLog.RateMultiplier, 1e-12)

	standardTotal := float64(tokens.InputTokens)*inputPrice + float64(tokens.OutputTokens)*outputPrice
	require.InDelta(t, standardTotal*fastMultiplier, usageRepo.lastLog.TotalCost, 1e-10)
	require.InDelta(t, standardTotal*0.5, usageRepo.lastLog.ActualCost, 1e-10)

	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.NotNil(t, billingRepo.lastCmd)
	// 统一结算必须以 Standard 基础价分配订阅/余额，不能把 Fast 基础价当成用户欠费。
	require.InDelta(t, standardTotal, billingRepo.lastCmd.BaseAmountUSD, 1e-10)
	require.InDelta(t, standardTotal*0.5, billingRepo.lastCmd.BillableAmountUSD, 1e-10)
}

// TestGroupBillsOpenAIFastAtStandardRequiresOpenAIAccount 锁定平台、账号和档位三重边界。
func TestGroupBillsOpenAIFastAtStandardRequiresOpenAIAccount(t *testing.T) {
	apiKey := &apikey.APIKey{Group: &routing.Group{Platform: capability.PlatformOpenAI, FreeOpenAIFast: true}}

	require.True(t, groupBillsOpenAIFastAtStandard(
		apiKey,
		&Account{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth},
		"priority",
	))
	require.True(t, groupBillsOpenAIFastAtStandard(
		apiKey,
		&Account{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth},
		" FAST ",
	))
	require.False(t, groupBillsOpenAIFastAtStandard(
		apiKey,
		&Account{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth},
		"standard",
	))
	require.False(t, groupBillsOpenAIFastAtStandard(
		apiKey,
		&Account{Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey},
		"priority",
	))
}

func TestOpenAIGatewayServiceRecordUsage_ServiceTierNeverRaisedByUpstreamResponse(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 100, OutputTokens: 50}

	err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
		Result: &forwardcore.OpenAIResult{
			RequestID:                   "resp_service_tier_not_raised",
			UpstreamResponseServiceTier: "priority",
			Usage:                       usage,
			Model:                       "gpt-5.4",
			Duration:                    time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 1018},
		User:    &identity.User{ID: 2018},
		Account: &Account{ID: 3018, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Nil(t, usageRepo.lastLog.ServiceTier)

	baseCost, calcErr := svc.billingService.CalculateCost("gpt-5.4", pricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1.0)
	require.NoError(t, calcErr)
	require.InDelta(t, baseCost.TotalCost, usageRepo.lastLog.TotalCost, 1e-10)
}
