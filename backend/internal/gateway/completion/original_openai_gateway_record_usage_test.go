package completion_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	completiontestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"

	usagecore "github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/stretchr/testify/require"
)

func TestOpenAIGatewayServiceRecordUsage_RejectsNilInput(t *testing.T) {
	svc := completion.NewRecorder(completion.Dependencies{}, completion.RecorderOptions{DefaultMultiplier: 1})

	require.Error(t, svc.Record(context.Background(), gatewaycapture.CaptureOpenAI(context.Background(), nil), true))
	require.Error(t, svc.Record(context.Background(), gatewaycapture.CaptureOpenAI(context.Background(), &gatewaycapture.OpenAICapture{}), true))
}

func i64p(v int64) *int64 {
	return &v
}

func requireOpenAIRecordUsageBillingRepoStub(t *testing.T, svc *completiontestkit.Recording) *completiontestkit.SettlementStore {
	t.Helper()

	billingRepo, ok := svc.Dependencies.Funds.(*completiontestkit.SettlementStore)
	require.True(t, ok)
	return billingRepo
}

func expectedOpenAICost(t *testing.T, svc *completiontestkit.Recording, model string, usage openai.ForwardUsage, multiplier float64) *pricing.CostBreakdown {
	t.Helper()

	cost, err := svc.Dependencies.Calculator.CalculateCost(model, pricing.UsageTokens{
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
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	quotaSvc := &completiontestkit.KeyQuotaUpdater{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_zero_usage",
			Usage:     openai.ForwardUsage{},
			Model:     "gpt-5.1",
			Duration:  time.Second,
		},
		APIKey:        &apikey.APIKey{ID: 1000, Quota: 100, Group: &routing.Group{RateMultiplier: 1}},
		User:          &identity.User{ID: 2000},
		Account:       &accountcore.Record{ID: 3000, Type: capability.AccountTypeAPIKey},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, billingRepo.Calls)
	require.Equal(t, 1, usageRepo.Calls)
	require.Equal(t, 0, userRepo.DeductCalls)
	require.Equal(t, 0, subRepo.IncrementCalls)
	require.Equal(t, 0, quotaSvc.QuotaCalls)
	require.Equal(t, 0, quotaSvc.RateLimitCalls)

	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "resp_zero_usage", usageRepo.LastLog.RequestID)
	require.Zero(t, usageRepo.LastLog.InputTokens)
	require.Zero(t, usageRepo.LastLog.OutputTokens)
	require.Zero(t, usageRepo.LastLog.CacheCreationTokens)
	require.Zero(t, usageRepo.LastLog.CacheReadTokens)
	require.Zero(t, usageRepo.LastLog.ImageOutputTokens)
	require.Zero(t, usageRepo.LastLog.ImageCount)
	require.Zero(t, usageRepo.LastLog.InputCost)
	require.Zero(t, usageRepo.LastLog.OutputCost)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.ActualCost)

	require.NotNil(t, billingRepo.LastCmd)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
	require.Zero(t, billingRepo.LastCmd.APIKeyQuotaCost)
	require.Zero(t, billingRepo.LastCmd.APIKeyRateLimitCost)
	require.Zero(t, billingRepo.LastCmd.AccountQuotaCost)
}

func TestOpenAIGatewayServiceRecordUsage_MissingPricingRecordsZeroCostUsageLog(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	quotaSvc := &completiontestkit.KeyQuotaUpdater{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account:       &accountcore.Record{ID: 3002, Type: capability.AccountTypeAPIKey},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, billingRepo.Calls)
	require.Equal(t, 1, usageRepo.Calls)
	require.Equal(t, 0, userRepo.DeductCalls)
	require.Equal(t, 0, subRepo.IncrementCalls)
	require.Equal(t, 0, quotaSvc.QuotaCalls)
	require.Equal(t, 0, quotaSvc.RateLimitCalls)

	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "resp_missing_pricing", usageRepo.LastLog.RequestID)
	require.Equal(t, "gpt-unknown-model", usageRepo.LastLog.Model)
	require.Equal(t, "gpt-unknown-model", usageRepo.LastLog.RequestedModel)
	require.Equal(t, 1200, usageRepo.LastLog.InputTokens)
	require.Equal(t, 300, usageRepo.LastLog.OutputTokens)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeToken), *usageRepo.LastLog.BillingMode)

	require.NotNil(t, billingRepo.LastCmd)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
	require.Zero(t, billingRepo.LastCmd.APIKeyQuotaCost)
	require.Zero(t, billingRepo.LastCmd.APIKeyRateLimitCost)
	require.Zero(t, billingRepo.LastCmd.AccountQuotaCost)
}

func TestOpenAIGatewayServiceRecordUsage_UsesQuotaPlatformForPlatformQuota(t *testing.T) {
	dailyLimit := 100.0
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{
		Result: &billing.UsageBillingApplyResult{
			Applied:          true,
			BalanceAmountUSD: 0.25,
		},
	}
	quotaCache := &completiontestkit.QuotaCache{
		Entry: &billing.UserPlatformQuotaCacheEntry{DailyLimitUSD: &dailyLimit},
	}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(
		usageRepo,
		billingRepo,
		&completiontestkit.UserStore{},
		&completiontestkit.SubscriptionStore{},
		nil,
	)
	svc.Effects.Funds.Cache = newBillingEligibilityForCompletionTest(quotaCache)
	svc.Effects.Funds.Quotas = &completiontestkit.PlatformQuotaStore{}

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account:       &accountcore.Record{ID: 3100, Type: capability.AccountTypeAPIKey},
		QuotaPlatform: capability.PlatformAntigravity,
	})

	require.NoError(t, err)
	// ForcePlatform 由 handler 预先拍进 QuotaPlatform，后扣不能再回退到 API key 的 openai 分组。
	require.Equal(t, []completiontestkit.QuotaCacheGet{
		{UserID: 2100, Platform: capability.PlatformAntigravity},
	}, quotaCache.GetCalls)
	require.Equal(t, []completiontestkit.QuotaCacheIncrement{
		{UserID: 2100, Platform: capability.PlatformAntigravity, Cost: 0.25},
	}, quotaCache.IncrCalls)
}

func TestOpenAIGatewayServiceRecordUsage_UsesUserSpecificGroupRate(t *testing.T) {
	groupID := int64(11)
	groupRate := 1.4
	userRate := 1.8
	usage := openai.ForwardUsage{InputTokens: 15, OutputTokens: 4, CacheReadInputTokens: 3}

	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	rateRepo := &completiontestkit.GroupRateStore{Rate: &userRate}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, rateRepo)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 3001},
	})

	require.NoError(t, err)
	require.Equal(t, 1, rateRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, userRate, usageRepo.LastLog.RateMultiplier)
	require.Equal(t, 12, usageRepo.LastLog.InputTokens)
	require.Equal(t, 3, usageRepo.LastLog.CacheReadTokens)

	expected := expectedOpenAICost(t, svc, "gpt-5.1", usage, userRate)
	require.InDelta(t, expected.ActualCost, usageRepo.LastLog.ActualCost, 1e-12)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expected.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_PeakRateAffectsTokenModeImageOutputTokens(t *testing.T) {
	groupID := int64(14)
	groupRate := 1.0
	usage := openai.ForwardUsage{
		InputTokens:       1000,
		OutputTokens:      600,
		ImageOutputTokens: 100,
	}

	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	// 固定在峰值窗口内，避免测试在每天 23:59 的右开边界偶发失败。
	svc.Options.Now = func() time.Time {
		return time.Date(2026, 7, 1, 12, 0, 0, 0, time.Local)
	}
	svc.Dependencies.Prices = newOpenAITokenImageConfigPricingResolverForTest(t, groupID, "gpt-5.1")

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 3004},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 3.0, usageRepo.LastLog.RateMultiplier)
	require.Equal(t, usage.ImageOutputTokens, usageRepo.LastLog.ImageOutputTokens)

	expected, err := svc.Dependencies.Calculator.CalculateCostUnified(billing.CostInput{
		Ctx:     context.Background(),
		Model:   "gpt-5.1",
		GroupID: i64p(groupID),
		Tokens: pricing.UsageTokens{
			InputTokens:       usage.InputTokens,
			OutputTokens:      usage.OutputTokens,
			ImageOutputTokens: usage.ImageOutputTokens,
		},
		RateMultiplier: 1.0,
		Resolver:       svc.Dependencies.Prices,
	})
	require.NoError(t, err)
	expectedActual := expected.TotalCost * 3.0

	require.InDelta(t, expected.TotalCost, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, expected.ImageOutputCost, usageRepo.LastLog.ImageOutputCost, 1e-12)
	require.InDelta(t, expectedActual, usageRepo.LastLog.ActualCost, 1e-12)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expectedActual, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_IncludesEndpointMetadata(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	rateRepo := &completiontestkit.GroupRateStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, rateRepo)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account:          &accountcore.Record{ID: 3002},
		InboundEndpoint:  " /v1/chat/completions ",
		UpstreamEndpoint: " /v1/responses ",
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.InboundEndpoint)
	require.Equal(t, "/v1/chat/completions", *usageRepo.LastLog.InboundEndpoint)
	require.NotNil(t, usageRepo.LastLog.UpstreamEndpoint)
	require.Equal(t, "/v1/responses", *usageRepo.LastLog.UpstreamEndpoint)
}

func TestOpenAIGatewayServiceRecordUsage_FallsBackToGroupDefaultRateOnResolverError(t *testing.T) {
	groupID := int64(12)
	groupRate := 1.6
	usage := openai.ForwardUsage{InputTokens: 10, OutputTokens: 5, CacheReadInputTokens: 2}

	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	rateRepo := &completiontestkit.GroupRateStore{Err: errors.New("db unavailable")}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, rateRepo)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 3002},
	})

	require.NoError(t, err)
	require.Equal(t, 1, rateRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, groupRate, usageRepo.LastLog.RateMultiplier)

	expected := expectedOpenAICost(t, svc, "gpt-5.1", usage, groupRate)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expected.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_FallsBackToGroupDefaultRateWhenResolverMissing(t *testing.T) {
	groupID := int64(13)
	groupRate := 1.25
	usage := openai.ForwardUsage{InputTokens: 9, OutputTokens: 4, CacheReadInputTokens: 1}

	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	svc.Dependencies.Rates = nil

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 3003},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, groupRate, usageRepo.LastLog.RateMultiplier)
}

func TestOpenAIGatewayServiceRecordUsage_DuplicateUsageLogSkipsBilling(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: false}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: false}}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 3004},
	})

	require.NoError(t, err)
	require.Equal(t, 1, billingRepo.Calls)
	require.Equal(t, 1, usageRepo.Calls)
	require.Equal(t, 0, userRepo.DeductCalls)
	require.Equal(t, 0, subRepo.IncrementCalls)
}

func TestOpenAIGatewayServiceRecordUsage_DuplicateBillingKeySkipsBillingWithRepo(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: false}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: false}}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	quotaSvc := &completiontestkit.KeyQuotaUpdater{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account:       &accountcore.Record{ID: 30045},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, billingRepo.Calls)
	require.Equal(t, 1, usageRepo.Calls)
	require.Equal(t, 0, userRepo.DeductCalls)
	require.Equal(t, 0, subRepo.IncrementCalls)
	require.Equal(t, 0, quotaSvc.QuotaCalls)
}

func TestOpenAIGatewayServiceRecordUsage_BillsWhenUsageLogCreateReturnsError(t *testing.T) {
	usage := openai.ForwardUsage{InputTokens: 8, OutputTokens: 4}
	usageRepo := &completiontestkit.UsageLogStore{Inserted: false, Err: errors.New("usage log batch state uncertain")}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_usage_log_error",
			Usage:     usage,
			Model:     "gpt-5.1",
			Duration:  time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10041},
		User:    &identity.User{ID: 20041},
		Account: &accountcore.Record{ID: 30041},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, usageRepo.LastLog.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_UsageLogWriteErrorDoesNotSkipBilling(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: false, Err: usagecore.MarkUsageLogCreateNotPersisted(context.Canceled)}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	quotaSvc := &completiontestkit.KeyQuotaUpdater{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account:       &accountcore.Record{ID: 30043},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, billingRepo.LastCmd.BillableAmountUSD, billingRepo.LastCmd.APIKeyQuotaCost, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_BillingUsesDetachedContext(t *testing.T) {
	usage := openai.ForwardUsage{InputTokens: 10, OutputTokens: 6, CacheReadInputTokens: 2}
	usageRepo := &completiontestkit.UsageLogStore{Inserted: false, Err: context.DeadlineExceeded}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	quotaSvc := &completiontestkit.KeyQuotaUpdater{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.RecordOpenAI(reqCtx, &gatewaycapture.OpenAICapture{
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
		Account:       &accountcore.Record{ID: 30042},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NoError(t, billingRepo.LastCtxErr)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, billingRepo.LastCmd.BillableAmountUSD, billingRepo.LastCmd.APIKeyQuotaCost, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_BillingRepoUsesDetachedContext(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.RecordOpenAI(reqCtx, &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 30046},
	})

	require.NoError(t, err)
	require.Equal(t, 1, billingRepo.Calls)
	require.NoError(t, billingRepo.LastCtxErr)
	require.Equal(t, 1, usageRepo.Calls)
	require.NoError(t, usageRepo.LastCtxErr)
}

func TestOpenAIGatewayServiceRecordUsage_BillingFingerprintIncludesRequestPayloadHash(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{}, nil)

	payloadHash := billing.HashUsageRequestPayload([]byte(`{"model":"gpt-5","input":"hello"}`))
	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account:            &accountcore.Record{ID: 701},
		RequestPayloadHash: payloadHash,
	})
	require.NoError(t, err)
	require.NotNil(t, billingRepo.LastCmd)
	require.Equal(t, payloadHash, billingRepo.LastCmd.RequestPayloadHash)
}

func TestOpenAIGatewayServiceRecordUsage_UsesFallbackRequestIDForBillingAndUsageLog(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	ctx := context.WithValue(context.Background(), telemetry.RequestID, "req-local-fallback")
	err := svc.RecordOpenAI(ctx, &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 30047},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.LastCmd)
	require.Equal(t, "local:req-local-fallback", billingRepo.LastCmd.RequestID)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "local:req-local-fallback", usageRepo.LastLog.RequestID)
}

func TestOpenAIGatewayServiceRecordUsage_PrefersClientRequestIDOverUpstreamRequestID(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	ctx := context.WithValue(context.Background(), telemetry.ClientRequestID, "openai-client-stable-123")
	err := svc.RecordOpenAI(ctx, &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 30049},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.LastCmd)
	require.Equal(t, "client:openai-client-stable-123", billingRepo.LastCmd.RequestID)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "client:openai-client-stable-123", usageRepo.LastLog.RequestID)
}

func TestOpenAIGatewayServiceRecordUsage_WSModePrefersUpstreamRequestIDOverClientRequestID(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	ctx := context.WithValue(context.Background(), telemetry.ClientRequestID, "openai-ws-connection-123")
	err := svc.RecordOpenAI(ctx, &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 30050},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.LastCmd)
	require.Equal(t, "resp_openai_ws_turn_456", billingRepo.LastCmd.RequestID)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "resp_openai_ws_turn_456", usageRepo.LastLog.RequestID)
}

func TestOpenAIGatewayServiceRecordUsage_GeneratesRequestIDWhenAllSourcesMissing(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 30050},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.LastCmd)
	require.True(t, strings.HasPrefix(billingRepo.LastCmd.RequestID, "generated:"))
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, billingRepo.LastCmd.RequestID, usageRepo.LastLog.RequestID)
}

func TestOpenAIGatewayServiceRecordUsage_BillingErrorWritesUnsettledUsageLog(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{}
	billingErr := errors.New("billing tx failed")
	billingRepo := &completiontestkit.SettlementStore{Err: billingErr}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 30048},
	})

	require.ErrorIs(t, err, billingErr)
	require.Equal(t, 1, billingRepo.Calls)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 8, usageRepo.LastLog.InputTokens)
	require.Equal(t, 4, usageRepo.LastLog.OutputTokens)
	require.Greater(t, usageRepo.LastLog.InputCost, 0.0)
	require.Greater(t, usageRepo.LastLog.OutputCost, 0.0)
	require.Greater(t, usageRepo.LastLog.TotalCost, 0.0)
	require.Zero(t, usageRepo.LastLog.ActualCost)
}

func TestOpenAIGatewayServiceRecordUsage_UpdatesAPIKeyQuotaWhenConfigured(t *testing.T) {
	usage := openai.ForwardUsage{InputTokens: 10, OutputTokens: 6, CacheReadInputTokens: 2}
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	quotaSvc := &completiontestkit.KeyQuotaUpdater{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account:       &accountcore.Record{ID: 3005},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	expected := expectedOpenAICost(t, svc, "gpt-5.1", usage, 1.1)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expected.ActualCost, billingRepo.LastCmd.APIKeyQuotaCost, 1e-12)
	require.InDelta(t, 0.0, billingRepo.LastCmd.APIKeyRateLimitCost, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_ClampsActualInputTokensToZero(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 3006},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 0, usageRepo.LastLog.InputTokens)
}

func TestOpenAIGatewayServiceRecordUsage_GPT56SeparatesCacheWriteForBillingAndStats(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	svc.Dependencies.Calculator = billingtestkit.Calculator(svc.Options.DefaultMultiplier, newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*pricing.LiteLLMModelPricing{
		"gpt-5.6-sol": {
			InputCostPerToken:       5e-6,
			OutputCostPerToken:      30e-6,
			CacheReadInputTokenCost: 0.5e-6,
		},
	}}), nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 3056},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 700, usageRepo.LastLog.InputTokens)
	require.Equal(t, 200, usageRepo.LastLog.CacheCreationTokens)
	require.Equal(t, 100, usageRepo.LastLog.CacheReadTokens)
	require.Equal(t, 1050, usageRepo.LastLog.TotalTokens())
	require.InDelta(t, 700*5e-6, usageRepo.LastLog.InputCost, 1e-12)
	require.InDelta(t, 200*6.25e-6, usageRepo.LastLog.CacheCreationCost, 1e-12)
	require.InDelta(t, 100*0.5e-6, usageRepo.LastLog.CacheReadCost, 1e-12)
	require.InDelta(t, 50*30e-6, usageRepo.LastLog.OutputCost, 1e-12)
	require.InDelta(t, usageRepo.LastLog.TotalCost*1.1, usageRepo.LastLog.ActualCost, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_LongContextBillingIgnoresLegacyAccountExtra(t *testing.T) {
	parentID := int64(4016)
	tests := []struct {
		name    string
		account *accountcore.Record
	}{
		{name: "missing legacy value", account: &accountcore.Record{ID: 3014, Platform: capability.PlatformOpenAI}},
		{name: "legacy false", account: &accountcore.Record{ID: 3015, Platform: capability.PlatformOpenAI, Extra: map[string]any{"openai_long_context_billing_enabled": false}}},
		{name: "legacy true", account: &accountcore.Record{ID: 3016, Platform: capability.PlatformOpenAI, Extra: map[string]any{"openai_long_context_billing_enabled": true}}},
		{name: "spark shadow", account: &accountcore.Record{
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
			usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
			accountRepo := &completiontestkit.AccountLookup{Account: &accountcore.Record{ID: parentID, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
			svc := newOpenAIRecordUsageServiceForTest(
				usageRepo,
				&completiontestkit.UserStore{},
				&completiontestkit.SubscriptionStore{},
				nil,
			)
			swapInOpenAILadderCatalog(t, svc)
			svc.AccountLookup = func(ctx context.Context, id int64) (*accountcore.Record, error) {
				value,

					err := accountRepo.
					GetByID(ctx, id)
				return value, err
			}

			err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
			require.NotNil(t, usageRepo.LastLog)
			expectedInput := 300000 * 2.5e-6 * 2.0
			expectedOutput := 2000 * 15e-6 * 1.5
			require.InDelta(t, expectedInput, usageRepo.LastLog.InputCost, 1e-10)
			require.InDelta(t, expectedOutput, usageRepo.LastLog.OutputCost, 1e-10)
			require.InDelta(t, expectedInput+expectedOutput, usageRepo.LastLog.TotalCost, 1e-10)
			require.InDelta(t, (expectedInput+expectedOutput)*1.1, usageRepo.LastLog.ActualCost, 1e-10)
			require.True(t, usageRepo.LastLog.LongContextBillingApplied)
			billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
			require.Equal(t, 1, billingRepo.Calls)
			require.InDelta(t, usageRepo.LastLog.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-10)
			// 只有影子结算需要读取母账号解析凭据；该读取不再用于长上下文开关判断。
			wantAccountRepoCalls := 0
			if tt.account.IsShadow() {
				wantAccountRepoCalls = 1
			}
			require.Equal(t, wantAccountRepoCalls, accountRepo.Calls)
		})
	}
}

// swapInOpenAILadderCatalog 给测试服务换上带 above_272k 阶梯字段的目录；
// 静态兜底价已不再携带 OpenAI 长上下文规则。
func swapInOpenAILadderCatalog(t *testing.T, svc *completiontestkit.Recording) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1.1
	svc.Dependencies.Calculator = NewBillingService(cfg, newStubPricingServiceFromJSON(t, openAILadderCatalogJSON))
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
			usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
			svc := newOpenAIRecordUsageServiceForTest(
				usageRepo,
				&completiontestkit.UserStore{},
				&completiontestkit.SubscriptionStore{},
				nil,
			)
			swapInOpenAILadderCatalog(t, svc)
			svc.Dependencies.Prices = billingtestkit.PriceResolver(nil, svc.Dependencies.Calculator)
			groupID := int64(1)
			err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
				Account: &accountcore.Record{
					ID:       int64(3020 + i),
					Platform: capability.PlatformOpenAI,
					Extra:    map[string]any{"openai_long_context_billing_enabled": !tt.groupEnable},
				},
			})

			require.NoError(t, err)
			require.NotNil(t, usageRepo.LastLog)
			require.Equal(t, tt.wantApplied, usageRepo.LastLog.LongContextBillingApplied)
			inputMultiplier := 1.0
			outputMultiplier := 1.0
			if tt.wantApplied {
				inputMultiplier = 2.0
				outputMultiplier = 1.5
			}
			require.InDelta(t, 300000*2.5e-6*inputMultiplier, usageRepo.LastLog.InputCost, 1e-10)
			require.InDelta(t, 2000*15e-6*outputMultiplier, usageRepo.LastLog.OutputCost, 1e-10)
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
			usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
			svc := newOpenAIRecordUsageServiceForTest(
				usageRepo,
				&completiontestkit.UserStore{},
				&completiontestkit.SubscriptionStore{},
				nil,
			)
			svc.Dependencies.Prices = billingtestkit.PriceResolver(nil, svc.Dependencies.Calculator)
			groupID := int64(10 + i)

			err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
				Account: &accountcore.Record{ID: int64(3030 + i), Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth},
			})

			require.NoError(t, err)
			require.NotNil(t, usageRepo.LastLog)
			require.Equal(t, tt.wantApplied, usageRepo.LastLog.LongContextBillingApplied)
			multiplier := 1.0
			if tt.wantApplied {
				multiplier = 2
			}
			require.InDelta(t, baseInput*multiplier, usageRepo.LastLog.InputCost, 1e-10)
			require.InDelta(t, baseOutput*multiplier, usageRepo.LastLog.OutputCost, 1e-10)
		})
	}
}

func TestOpenAIGatewayServiceRecordUsage_ServiceTierPriorityUsesFastPricing(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	serviceTier := "priority"
	usage := openai.ForwardUsage{InputTokens: 100, OutputTokens: 50}

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID:   "resp_service_tier_priority",
			ServiceTier: &serviceTier,
			Usage:       usage,
			Model:       "gpt-5.4",
			Duration:    time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 1015},
		User:    &identity.User{ID: 2015},
		Account: &accountcore.Record{ID: 3015},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.ServiceTier)
	require.Equal(t, serviceTier, *usageRepo.LastLog.ServiceTier)

	baseCost, calcErr := svc.Dependencies.Calculator.CalculateCost("gpt-5.4", pricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1.0)
	require.NoError(t, calcErr)
	require.InDelta(t, baseCost.TotalCost*2, usageRepo.LastLog.TotalCost, 1e-10)
}

func TestOpenAIGatewayServiceRecordUsage_ServiceTierFlexHalvesCost(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	serviceTier := "flex"
	usage := openai.ForwardUsage{InputTokens: 100, OutputTokens: 50, CacheReadInputTokens: 20}

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID:   "resp_service_tier_flex",
			ServiceTier: &serviceTier,
			Usage:       usage,
			Model:       "gpt-5.4",
			Duration:    time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 1016},
		User:    &identity.User{ID: 2016},
		Account: &accountcore.Record{ID: 3016},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)

	baseCost, calcErr := svc.Dependencies.Calculator.CalculateCost("gpt-5.4", pricing.UsageTokens{InputTokens: 80, OutputTokens: 50, CacheReadTokens: 20}, 1.0)
	require.NoError(t, calcErr)
	require.InDelta(t, baseCost.TotalCost*0.5, usageRepo.LastLog.TotalCost, 1e-10)
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
	require.Equal(t, "priority", *requeststate.ExtractOpenAIServiceTier(map[string]any{"service_tier": "fast"}))
	require.Equal(t, "flex", *requeststate.ExtractOpenAIServiceTier(map[string]any{"service_tier": "flex"}))
	require.Equal(t, "auto", *requeststate.ExtractOpenAIServiceTier(map[string]any{"service_tier": "auto"}))
	require.Equal(t, "default", *requeststate.ExtractOpenAIServiceTier(map[string]any{"service_tier": "default"}))
	require.Equal(t, "scale", *requeststate.ExtractOpenAIServiceTier(map[string]any{"service_tier": "scale"}))
	require.Equal(t, "ultrafast", *requeststate.ExtractOpenAIServiceTier(map[string]any{"service_tier": "ultrafast"}))
	require.Nil(t, requeststate.ExtractOpenAIServiceTier(map[string]any{"service_tier": 1}))
	require.Nil(t, requeststate.ExtractOpenAIServiceTier(nil))
}

func TestExtractOpenAIServiceTierFromBody(t *testing.T) {
	require.Equal(t, "priority", *requeststate.ExtractOpenAIServiceTierFromBody([]byte(`{"service_tier":"fast"}`)))
	require.Equal(t, "flex", *requeststate.ExtractOpenAIServiceTierFromBody([]byte(`{"service_tier":"flex"}`)))
	require.Equal(t, "auto", *requeststate.ExtractOpenAIServiceTierFromBody([]byte(`{"service_tier":"auto"}`)))
	require.Equal(t, "default", *requeststate.ExtractOpenAIServiceTierFromBody([]byte(`{"service_tier":"default"}`)))
	require.Equal(t, "scale", *requeststate.ExtractOpenAIServiceTierFromBody([]byte(`{"service_tier":"scale"}`)))
	require.Equal(t, "ultrafast", *requeststate.ExtractOpenAIServiceTierFromBody([]byte(`{"service_tier":"ultrafast"}`)))
	require.Nil(t, requeststate.ExtractOpenAIServiceTierFromBody([]byte(`{"service_tier":"turbo"}`)))
	require.Nil(t, requeststate.ExtractOpenAIServiceTierFromBody(nil))
}

func TestOpenAIGatewayServiceRecordUsage_UsesRequestedModelAndUpstreamModelMetadataFields(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	serviceTier := "priority"
	reasoning := "high"

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account:   &accountcore.Record{ID: 30},
		UserAgent: "codex-cli/1.0",
		IPAddress: "127.0.0.1",
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "gpt-5.1", usageRepo.LastLog.Model)
	require.Equal(t, "gpt-5.1", usageRepo.LastLog.RequestedModel)
	require.NotNil(t, usageRepo.LastLog.UpstreamModel)
	require.Equal(t, "gpt-5.1-codex", *usageRepo.LastLog.UpstreamModel)
	require.NotNil(t, usageRepo.LastLog.ServiceTier)
	require.Equal(t, serviceTier, *usageRepo.LastLog.ServiceTier)
	require.NotNil(t, usageRepo.LastLog.ReasoningEffort)
	require.Equal(t, reasoning, *usageRepo.LastLog.ReasoningEffort)
	require.NotNil(t, usageRepo.LastLog.RequestedReasoningEffort)
	require.Equal(t, reasoning, *usageRepo.LastLog.RequestedReasoningEffort)
	require.NotNil(t, usageRepo.LastLog.UserAgent)
	require.Equal(t, "codex-cli/1.0", *usageRepo.LastLog.UserAgent)
	require.NotNil(t, usageRepo.LastLog.IPAddress)
	require.Equal(t, "127.0.0.1", *usageRepo.LastLog.IPAddress)
	require.NotNil(t, usageRepo.LastLog.GroupID)
	require.Equal(t, int64(11), *usageRepo.LastLog.GroupID)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, usageRepo.LastLog.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

// TestOpenAIGatewayServiceRecordUsage_PersistsRequestedReasoningEffort 验证显式请求档位与实际档位分开落库。
func TestOpenAIGatewayServiceRecordUsage_PersistsRequestedReasoningEffort(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	requested := "max"
	forwarded := "xhigh"

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 30},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.RequestedReasoningEffort)
	require.Equal(t, requested, *usageRepo.LastLog.RequestedReasoningEffort)
	require.Equal(t, forwarded, *usageRepo.LastLog.ReasoningEffort)
}

func TestOpenAIGatewayServiceRecordUsage_PreservesGroupMappedUpstreamModel(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 30},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:    "gpt-5.6-sol",
			GroupMappedModel: "gpt-5.6-terra",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "gpt-5.6-sol", usageRepo.LastLog.RequestedModel)
	require.Equal(t, "gpt-5.6-terra", usageRepo.LastLog.Model)
	require.NotNil(t, usageRepo.LastLog.UpstreamModel)
	require.Equal(t, "gpt-5.6-terra", *usageRepo.LastLog.UpstreamModel)
}

func TestOpenAIGatewayServiceRecordUsage_PreservesLoopedPricingConfigAndAccountUpstreamModel(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID:     "openai_looped_mapping_models",
			Model:         "gpt-5.6-terra",
			UpstreamModel: "gpt-5.6-sol",
			Usage:         openai.ForwardUsage{InputTokens: 20, OutputTokens: 10},
			Duration:      time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10},
		User:    &identity.User{ID: 20},
		Account: &accountcore.Record{ID: 30},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:    "gpt-5.6-sol",
			GroupMappedModel: "gpt-5.6-terra",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "gpt-5.6-sol", usageRepo.LastLog.RequestedModel)
	require.Equal(t, "gpt-5.6-terra", usageRepo.LastLog.Model)
	require.NotNil(t, usageRepo.LastLog.UpstreamModel)
	require.Equal(t, "gpt-5.6-sol", *usageRepo.LastLog.UpstreamModel)
}

func TestOpenAIGatewayServiceRecordUsage_BillsMappedRequestsUsingRequestedModel(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}

	// Billing should use the requested model ("gpt-5.1"), not the upstream mapped model ("gpt-5.1-codex").
	// This ensures pricing is always based on the model the user requested.
	expectedCost, err := svc.Dependencies.Calculator.CalculateCost("gpt-5.1", pricing.UsageTokens{
		InputTokens:  20,
		OutputTokens: 10,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID:     "resp_upstream_model_billing_fallback",
			Model:         "gpt-5.1",
			UpstreamModel: "gpt-5.1-codex",
			Usage:         usage,
			Duration:      time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10},
		User:    &identity.User{ID: 20},
		Account: &accountcore.Record{ID: 30},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "gpt-5.1", usageRepo.LastLog.Model)
	require.Equal(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost)
	require.Equal(t, expectedCost.TotalCost, usageRepo.LastLog.TotalCost)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.Equal(t, expectedCost.ActualCost, billingRepo.LastCmd.BillableAmountUSD)
}

func TestOpenAIGatewayServiceRecordUsage_GroupMappedDoesNotOverrideBillingModelWhenUnmapped(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}

	// 分组未发生模型映射时，应使用 result.BillingModel 中记录的实际上游计费模型，
	// 而不是未映射的原始请求模型。
	expectedCost, err := svc.Dependencies.Calculator.CalculateCost("gpt-5.1", pricing.UsageTokens{
		InputTokens:  20,
		OutputTokens: 10,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 30},
		PricingUsageFields: routing.PricingUsageFields{
			PricingConfigID:    1,
			OriginalModel:      "glm",
			GroupMappedModel:   "glm", // channel did NOT map
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost)
	require.True(t, usageRepo.LastLog.ActualCost > 0, "cost must not be zero")
}

func TestOpenAIGatewayServiceRecordUsage_GroupMappedOverridesBillingModelWhenMapped(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}

	// When channel DID map the model (GroupMappedModel != OriginalModel),
	// billing should use the channel-mapped model, honoring admin intent.
	expectedCost, err := svc.Dependencies.Calculator.CalculateCost("gpt-5.1", pricing.UsageTokens{
		InputTokens:  20,
		OutputTokens: 10,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID:     "resp_group_mapped_billing",
			Model:         "glm",
			BillingModel:  "gpt-5.1-codex",
			UpstreamModel: "gpt-5.1-codex",
			Usage:         usage,
			Duration:      time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10},
		User:    &identity.User{ID: 20},
		Account: &accountcore.Record{ID: 30},
		PricingUsageFields: routing.PricingUsageFields{
			PricingConfigID:    1,
			OriginalModel:      "glm",
			GroupMappedModel:   "gpt-5.1", // channel mapped glm → gpt-5.1
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost)
	require.True(t, usageRepo.LastLog.ActualCost > 0, "cost must not be zero")
}

func TestOpenAIGatewayServiceRecordUsage_UpstreamBillingSourceOverridesRequestedModel(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}

	expectedCost, err := svc.Dependencies.Calculator.CalculateCost("gpt-5.4-mini", pricing.UsageTokens{
		InputTokens:  20,
		OutputTokens: 10,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 30},
		PricingUsageFields: routing.PricingUsageFields{
			PricingConfigID:    1,
			OriginalModel:      "gpt-5.4",
			GroupMappedModel:   "gpt-5.4",
			BillingModelSource: routing.BillingModelSourceUpstream,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost)
	require.True(t, usageRepo.LastLog.ActualCost > 0, "cost must not be zero")
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
			usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
			userRepo := &completiontestkit.UserStore{}
			subRepo := &completiontestkit.SubscriptionStore{}
			svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

			expectedCost, err := svc.Dependencies.Calculator.CalculateCost(tt.wantBillingModel, tokens, 1.1)
			require.NoError(t, err)

			err = svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
				Account: &accountcore.Record{ID: 30},
				PricingUsageFields: routing.PricingUsageFields{
					OriginalModel:      "gpt-5.4",
					GroupMappedModel:   "gpt-5.4",
					BillingModelSource: tt.billingModelSource,
				},
			})

			require.NoError(t, err)
			require.NotNil(t, usageRepo.LastLog)
			require.Equal(t, "gpt-5.4", usageRepo.LastLog.Model)
			require.InDelta(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost, 1e-12)
			billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
			require.NotNil(t, billingRepo.LastCmd)
			require.InDelta(t, expectedCost.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
			require.Zero(t, userRepo.DeductCalls)
			require.True(t, usageRepo.LastLog.ActualCost > 0, "cost must not be zero")
		})
	}
}

// TestOpenAIUsageBillingModelPreservesImagePricingModel 验证图片轮次不会被文本上游模型覆盖计价。
func TestOpenAIUsageBillingModelPreservesImagePricingModel(t *testing.T) {
	tests := []struct {
		name   string
		result forwardcore.OpenAIResult
		fields routing.PricingUsageFields
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
			fields: routing.PricingUsageFields{BillingModelSource: routing.BillingModelSourceUpstream},
			want:   "gpt-image-2",
		},
		{
			name: "普通上游计费使用最终模型",
			result: forwardcore.OpenAIResult{
				Model:         "public-alias",
				UpstreamModel: "gpt-5.6-sol",
				BillingModel:  "group-model",
			},
			fields: routing.PricingUsageFields{BillingModelSource: routing.BillingModelSourceUpstream},
			want:   "gpt-5.6-sol",
		},
		{
			name: "分组未映射时的计费保留图片模型",
			result: forwardcore.OpenAIResult{
				Model:         "gpt-5.6-sol",
				UpstreamModel: "gpt-5.6-sol",
				BillingModel:  "gpt-image-2",
				ImageCount:    1,
			},
			fields: routing.PricingUsageFields{
				BillingModelSource: routing.BillingModelSourceGroupMapped,
				OriginalModel:      "gpt-5.6-sol",
				GroupMappedModel:   "gpt-5.6-sol",
			},
			want: "gpt-image-2",
		},
		{
			name: "请求模型来源覆盖图片模型",
			result: forwardcore.OpenAIResult{
				BillingModel: "gpt-image-2",
				ImageCount:   1,
			},
			fields: routing.PricingUsageFields{
				BillingModelSource: routing.BillingModelSourceRequested,
				OriginalModel:      "public-image-alias",
			},
			want: "public-image-alias",
		},
		{
			name: "分组映射计费来源覆盖图片模型",
			result: forwardcore.OpenAIResult{
				BillingModel: "gpt-image-2",
				ImageCount:   1,
			},
			fields: routing.PricingUsageFields{
				BillingModelSource: routing.BillingModelSourceGroupMapped,
				OriginalModel:      "public-image-alias",
				GroupMappedModel:   "priced-group-model",
			},
			want: "priced-group-model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, completion.OpenAIUsageBillingModel(gatewaycapture.ProjectOpenAICompletionResult(&tt.result, nil), tt.fields))
		})
	}
}

func TestOpenAIGatewayServiceRecordUsage_BillsCompactOpenAIModelAlias(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}

	expectedCost, err := svc.Dependencies.Calculator.CalculateCost("gpt-5.5", pricing.UsageTokens{
		InputTokens:  20,
		OutputTokens: 10,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID:     "resp_compact_openai_alias",
			Model:         "gpt5.5",
			UpstreamModel: "gpt-5.4",
			Usage:         usage,
			Duration:      time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10},
		User:    &identity.User{ID: 20},
		Account: &accountcore.Record{ID: 30},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "gpt5.5", usageRepo.LastLog.Model)
	require.NotNil(t, usageRepo.LastLog.UpstreamModel)
	require.Equal(t, "gpt-5.4", *usageRepo.LastLog.UpstreamModel)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost, 1e-12)
	require.True(t, usageRepo.LastLog.ActualCost > 0, "cost must not be zero")
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.InDelta(t, expectedCost.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_FallsBackToUpstreamModelWhenPrimaryUnpriceable(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}

	expectedCost, err := svc.Dependencies.Calculator.CalculateCost("gpt-5.4", pricing.UsageTokens{
		InputTokens:  20,
		OutputTokens: 10,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 30},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost, 1e-12)
	require.True(t, usageRepo.LastLog.ActualCost > 0, "cost must not be zero")
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.InDelta(t, expectedCost.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_UnpricedTokenModelFallsBackToZeroCostUsageLog(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_unpriceable_without_upstream",
			Model:     "not-priceable-alias",
			Usage:     openai.ForwardUsage{InputTokens: 20, OutputTokens: 10},
			Duration:  time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 10},
		User:    &identity.User{ID: 20},
		Account: &accountcore.Record{ID: 30},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "not-priceable-alias", usageRepo.LastLog.Model)
	require.Equal(t, 20, usageRepo.LastLog.InputTokens)
	require.Equal(t, 10, usageRepo.LastLog.OutputTokens)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.Equal(t, 0, userRepo.DeductCalls)
	require.Equal(t, 0, subRepo.IncrementCalls)
}

func TestOpenAIGatewayServiceRecordUsage_SubscriptionBillingSetsSubscriptionFields(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	subscription := &billing.UserSubscription{ID: 99}
	planID := int64(199)
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{
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

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_subscription_billing",
			Usage:     openai.ForwardUsage{InputTokens: 10, OutputTokens: 5},
			Model:     "gpt-5.1",
			Duration:  time.Second,
		},
		APIKey:       &apikey.APIKey{ID: 100, GroupID: i64p(88), Group: &routing.Group{ID: 88, RateMultiplier: 1.0}},
		User:         &identity.User{ID: 200},
		Account:      &accountcore.Record{ID: 300},
		Subscription: subscription,
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, usagecore.BillingTypeSubscription, usageRepo.LastLog.BillingType)
	require.NotNil(t, usageRepo.LastLog.SubscriptionID)
	require.Equal(t, subscription.ID, *usageRepo.LastLog.SubscriptionID)
	require.Equal(t, 1, billingRepo.Calls)
	require.Equal(t, 0, userRepo.DeductCalls)
}

func TestOpenAIGatewayServiceRecordUsage_SubscriptionBillingUsesPlanGroupRateOverUserGroupRate(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	userGroupRate := 0.17
	rateRepo := &completiontestkit.GroupRateStore{Rate: &userGroupRate}
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
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{
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

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account:      &accountcore.Record{ID: 300},
		Subscription: subscription,
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, usagecore.BillingTypeSubscription, usageRepo.LastLog.BillingType)
	require.NotNil(t, usageRepo.LastLog.SubscriptionID)
	require.Equal(t, subscription.ID, *usageRepo.LastLog.SubscriptionID)
	require.InDelta(t, 0.5, usageRepo.LastLog.RateMultiplier, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
	require.Equal(t, 0, rateRepo.Calls)
}

func TestOpenAIGatewayServiceRecordUsage_InferredSubscriptionUsesPlanGroupRate(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
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
	subRepo := &completiontestkit.SubscriptionStore{}
	userGroupRate := 0.17
	rateRepo := &completiontestkit.GroupRateStore{Rate: &userGroupRate}
	planID := subscription.PlanID
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{
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
	billingRepo.ResolveSub = &subscription
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, rateRepo)

	usage := openai.ForwardUsage{InputTokens: 10, OutputTokens: 5}
	expectedCost := expectedOpenAICost(t, svc, "gpt-5.1", usage, 0.5)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 300},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, usagecore.BillingTypeSubscription, usageRepo.LastLog.BillingType)
	require.NotNil(t, usageRepo.LastLog.SubscriptionID)
	require.Equal(t, subscription.ID, *usageRepo.LastLog.SubscriptionID)
	require.InDelta(t, 0.5, usageRepo.LastLog.RateMultiplier, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
	require.Equal(t, 0, rateRepo.Calls)
	require.Equal(t, 1, billingRepo.ResolveCalls)
}

func TestOpenAIGatewayServiceRecordUsage_SimpleModeSkipsBillingAfterPersist(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	svc.Options.Simple = true

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_simple_mode",
			Usage:     openai.ForwardUsage{InputTokens: 10, OutputTokens: 5},
			Model:     "gpt-5.1",
			Duration:  time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 1000},
		User:    &identity.User{ID: 2000},
		Account: &accountcore.Record{ID: 3000},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.Equal(t, 0, userRepo.DeductCalls)
	require.Equal(t, 0, subRepo.IncrementCalls)
}

func TestOpenAIGatewayServiceRecordUsage_ImageOnlyUsageStillPersists(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID:  "resp_image_only_usage",
			Model:      "gpt-image-2",
			ImageCount: 2,
			ImageSize:  "1K",
			Duration:   time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 1007},
		User:    &identity.User{ID: 2007},
		Account: &accountcore.Record{ID: 3007},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 2, usageRepo.LastLog.ImageCount)
	require.NotNil(t, usageRepo.LastLog.ImageSize)
	require.Equal(t, "1K", *usageRepo.LastLog.ImageSize)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.LastLog.BillingMode)
}

func TestOpenAIGatewayServiceRecordUsage_EmptyImageSizeDefaultsBeforeBillingAndPersistence(t *testing.T) {
	imagePrice2K := 0.31
	groupID := int64(1201)
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{}, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 31201},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 2, usageRepo.LastLog.ImageCount)
	require.NotNil(t, usageRepo.LastLog.ImageSize)
	require.Equal(t, pricing.ImageBillingSize2K, *usageRepo.LastLog.ImageSize)
	require.NotNil(t, usageRepo.LastLog.ImageSizeSource)
	require.Equal(t, pricing.ImageSizeSourceDefault, *usageRepo.LastLog.ImageSizeSource)
	require.Nil(t, usageRepo.LastLog.ImageInputSize)
	require.Nil(t, usageRepo.LastLog.ImageOutputSize)
	require.InDelta(t, 0.62, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.62, usageRepo.LastLog.ActualCost, 1e-12)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.LastLog.BillingMode)
}

func TestOpenAIGatewayServiceRecordUsage_OutputImageSizeWinsBeforeBillingAndPersistence(t *testing.T) {
	imagePrice1K := 0.11
	imagePrice4K := 0.44
	groupID := int64(1202)
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{}, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 31202},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.ImageSize)
	require.Equal(t, pricing.ImageBillingSize4K, *usageRepo.LastLog.ImageSize)
	require.NotNil(t, usageRepo.LastLog.ImageInputSize)
	require.Equal(t, "1024x1024", *usageRepo.LastLog.ImageInputSize)
	require.NotNil(t, usageRepo.LastLog.ImageOutputSize)
	require.Equal(t, "3840x2160", *usageRepo.LastLog.ImageOutputSize)
	require.NotNil(t, usageRepo.LastLog.ImageSizeSource)
	require.Equal(t, pricing.ImageSizeSourceOutput, *usageRepo.LastLog.ImageSizeSource)
	require.Equal(t, map[string]int{pricing.ImageBillingSize4K: 1}, usageRepo.LastLog.ImageSizeBreakdown)
	require.InDelta(t, 0.44, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.44, usageRepo.LastLog.ActualCost, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_ImageUsesPerImageBillingEvenWithUsageTokens(t *testing.T) {
	imagePrice := 0.02
	groupID := int64(12)

	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 3008},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.LastLog.BillingMode)
	require.Equal(t, 2, usageRepo.LastLog.ImageCount)
	require.InDelta(t, 0.04, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.04, usageRepo.LastLog.ActualCost, 1e-12)
	require.InDelta(t, 0.0, usageRepo.LastLog.InputCost, 1e-12)
	require.InDelta(t, 0.0, usageRepo.LastLog.OutputCost, 1e-12)
	require.InDelta(t, 0.0, usageRepo.LastLog.ImageOutputCost, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_ImageSharedMultiplierPreservesExistingBehavior(t *testing.T) {
	imagePrice := 0.2
	groupID := int64(121)

	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{}, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 30121},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.InDelta(t, 0.2, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.03, usageRepo.LastLog.ActualCost, 1e-12)
	require.InDelta(t, 0.15, usageRepo.LastLog.RateMultiplier, 1e-12)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.LastLog.BillingMode)
}

func TestOpenAIGatewayServiceRecordUsage_ImageSharedMultiplierUsesUserGroupOverride(t *testing.T) {
	imagePrice := 0.5
	userRate := 0.2
	groupID := int64(125)

	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(
		usageRepo,
		&completiontestkit.UserStore{},
		&completiontestkit.SubscriptionStore{},
		&completiontestkit.GroupRateStore{Rate: &userRate},
	)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 30125},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.InDelta(t, 0.5, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.1, usageRepo.LastLog.ActualCost, 1e-12)
	require.InDelta(t, 0.2, usageRepo.LastLog.RateMultiplier, 1e-12)
}

func TestOpenAIGatewayServiceRecordUsage_GrokVideoUsesDefaultRateCard(t *testing.T) {
	groupID := int64(1261)
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{}, nil)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 301261, Platform: capability.PlatformGrok},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Nil(t, usageRepo.LastLog.ImageSize)
	// 结果未携带 duration 时按上游默认 8 秒计费：0.14 USD/s × 8s。
	require.InDelta(t, 0.14*8, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.14*8, usageRepo.LastLog.ActualCost, 1e-12)
	require.Equal(t, 0, usageRepo.LastLog.ImageCount)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeVideo), *usageRepo.LastLog.BillingMode)
	require.Equal(t, 1, usageRepo.LastLog.VideoCount)
	require.NotNil(t, usageRepo.LastLog.VideoDurationSeconds)
	require.Equal(t, pricing.VideoBillingDefaultDurationSeconds, *usageRepo.LastLog.VideoDurationSeconds)
}

func TestOpenAIGatewayServiceRecordUsage_GroupImagePriceOverridesPricingConfigImagePrice(t *testing.T) {
	groupID := int64(127)
	pricingConfigPrice := 0.201
	groupImagePrice2K := 0.021
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{}, nil)
	svc.Dependencies.Prices = newOpenAIImageConfigPricingResolverForTest(t, groupID, "grok-imagine-image-quality", pricingConfigPrice)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 30127, Platform: capability.PlatformGrok},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 1, usageRepo.LastLog.ImageCount)
	require.Equal(t, pricing.ImageBillingSize2K, *usageRepo.LastLog.ImageSize)
	require.InDelta(t, 0.021, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.021, usageRepo.LastLog.ActualCost, 1e-12)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.LastLog.BillingMode)
}

func TestOpenAIGatewayServiceRecordUsage_GroupVideoPriceOverridesPricingConfigImagePrice(t *testing.T) {
	groupID := int64(128)
	pricingConfigPrice := 0.201
	groupVideoPrice720P := 0.037
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{}, nil)
	svc.Dependencies.Prices = newOpenAIImageConfigPricingResolverForTest(t, groupID, "grok-imagine-video", pricingConfigPrice)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 30128, Platform: capability.PlatformGrok},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 0, usageRepo.LastLog.ImageCount)
	require.Nil(t, usageRepo.LastLog.ImageSize)
	require.InDelta(t, 0.037, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.037, usageRepo.LastLog.ActualCost, 1e-12)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeVideo), *usageRepo.LastLog.BillingMode)
}

// 视频请求命中共享价格配置 token 计费时走 token 路径；此时行是 billing_mode='token'、image_count=1、
// image_size=NULL，必须携带 video_count>0 才能通过 usage_logs 的 image_size check 约束
// （迁移 194），否则整个计费事务会因约束违反而丢失。
func TestOpenAIGatewayServiceRecordUsage_GrokVideoWithTokenConfigPricingKeepsVideoMetadata(t *testing.T) {
	groupID := int64(132)
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{}, nil)
	svc.Dependencies.Prices = newOpenAITokenImageConfigPricingResolverForTest(t, groupID, "grok-imagine-video")

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 30132, Platform: capability.PlatformGrok},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeToken), *usageRepo.LastLog.BillingMode)
	require.Nil(t, usageRepo.LastLog.ImageSize)
	require.Equal(t, 0, usageRepo.LastLog.ImageCount)
	require.Equal(t, 1, usageRepo.LastLog.VideoCount)
	require.NotNil(t, usageRepo.LastLog.VideoResolution)
	require.Equal(t, pricing.VideoBillingResolution720P, *usageRepo.LastLog.VideoResolution)
	require.NotNil(t, usageRepo.LastLog.VideoDurationSeconds)
	require.Equal(t, 5, *usageRepo.LastLog.VideoDurationSeconds)
}

func TestOpenAIGatewayServiceRecordUsage_PricingConfigImageBillingUsesImageCountAndSharedMultiplier(t *testing.T) {
	groupID := int64(123)
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{}, nil)
	svc.Dependencies.Prices = newOpenAIImageConfigPricingResolverForTest(t, groupID, "gpt-image-2", 0.25)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 30123},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.InDelta(t, 0.75, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.1125, usageRepo.LastLog.ActualCost, 1e-12)
	require.InDelta(t, 0.15, usageRepo.LastLog.RateMultiplier, 1e-12)
	require.Equal(t, 3, usageRepo.LastLog.ImageCount)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.LastLog.BillingMode)
}

func newOpenAIImageConfigPricingResolverForTest(t *testing.T, groupID int64, model string, price float64) *billing.PriceResolver {
	t.Helper()
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Model: model}] = &routing.ModelPricingEntry{
		BillingMode:     routing.BillingModeImage,
		PerRequestPrice: &price,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = ""
	cache.LoadedAt = time.Now()

	cs := routingtestkit.ModelConfigFromData(cache)
	return billingtestkit.PriceResolver(cs, NewBillingService(&config.Config{}, nil))
}

func newOpenAITokenImageConfigPricingResolverForTest(t *testing.T, groupID int64, model string) *billing.PriceResolver {
	t.Helper()
	inputPrice := 3e-6
	outputPrice := 15e-6
	imageOutputPrice := 15e-6
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Model: model}] = &routing.ModelPricingEntry{
		BillingMode:      routing.BillingModeToken,
		InputPrice:       &inputPrice,
		OutputPrice:      &outputPrice,
		ImageOutputPrice: &imageOutputPrice,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = ""
	cache.LoadedAt = time.Now()

	cs := routingtestkit.ModelConfigFromData(cache)
	return billingtestkit.PriceResolver(cs, NewBillingService(&config.Config{}, nil))
}

func TestGatewayServiceCalculateRecordUsageCost_PricingConfigImageBillingUsesImageCount(t *testing.T) {
	groupID := int64(126)
	billingService := NewBillingService(&config.Config{}, nil)
	svc := completion.NewRecorder(completion.Dependencies{
		Calculator: billingService,
		Prices:     newOpenAIImageConfigPricingResolverForTest(t, groupID, "gemini-image", 0.25),
	}, completion.RecorderOptions{DefaultMultiplier: 1})

	cost := svc.CalculateRecordUsageCost(
		context.Background(), gatewaycapture.ProjectMessagesCompletionResult(&forwardcore.MessagesResult{Model: "gemini-image", ImageCount: 2, ImageSize: "1K"},

			nil), gatewaycapture.ProjectCompletionKey(&apikey.APIKey{GroupID: i64p(groupID), Group: &routing.Group{ID: groupID}}), gatewaycapture.ProjectCompletionAccount(nil), "gemini-image",
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

func TestGatewayServiceCalculateRecordUsageCost_PricingConfigImageBillingUsesSizeTier(t *testing.T) {
	groupID := int64(127)
	defaultPrice := 0.10
	price4K := 0.40
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Model: "gemini-image"}] = &routing.ModelPricingEntry{
		BillingMode:     routing.BillingModeImage,
		PerRequestPrice: &defaultPrice,
		Intervals: []routing.PricingInterval{{
			TierLabel:       "4K",
			PerRequestPrice: &price4K,
		}},
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	svc := completion.NewRecorder(completion.Dependencies{
		Calculator: NewBillingService(&config.Config{}, nil),
		Prices:     billingtestkit.PriceResolver(pricingConfigService, NewBillingService(&config.Config{}, nil)),
	}, completion.RecorderOptions{DefaultMultiplier: 1})

	cost := svc.CalculateRecordUsageCost(
		context.Background(), gatewaycapture.ProjectMessagesCompletionResult(&forwardcore.MessagesResult{Model: "gemini-image", ImageCount: 2, ImageSize: "4K"},

			nil), gatewaycapture.ProjectCompletionKey(&apikey.APIKey{GroupID: i64p(groupID), Group: &routing.Group{ID: groupID}}), gatewaycapture.ProjectCompletionAccount(nil), "gemini-image",
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

func TestGatewayServiceCalculateRecordUsageCost_GroupImagePriceOverridesPricingConfigImagePrice(t *testing.T) {
	groupID := int64(129)
	pricingConfigPrice := 0.25
	groupImagePrice2K := 0.021

	svc := completion.NewRecorder(completion.Dependencies{
		Calculator: NewBillingService(&config.Config{}, nil),
		Prices:     newOpenAIImageConfigPricingResolverForTest(t, groupID, "gemini-image", pricingConfigPrice),
	}, completion.RecorderOptions{DefaultMultiplier: 1})

	cost := svc.CalculateRecordUsageCost(
		context.Background(), gatewaycapture.ProjectMessagesCompletionResult(&forwardcore.MessagesResult{Model: "gemini-image", ImageCount: 2, ImageSize: pricing.ImageBillingSize2K},

			nil), gatewaycapture.ProjectCompletionKey(&apikey.APIKey{
			GroupID: i64p(groupID),
			Group: &routing.Group{
				ID:           groupID,
				ModelPricing: testImageModelPricing(map[string]*float64{"2K": &groupImagePrice2K}),
			},
		}), gatewaycapture.ProjectCompletionAccount(nil), "gemini-image",
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

func TestGatewayServiceCalculateRecordUsageCost_PricingConfigImageBillingNormalizesMissingSizeTier(t *testing.T) {
	groupID := int64(128)
	defaultPrice := 0.10
	price2K := 0.22
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Model: "gemini-image"}] = &routing.ModelPricingEntry{
		BillingMode:     routing.BillingModeImage,
		PerRequestPrice: &defaultPrice,
		Intervals: []routing.PricingInterval{{
			TierLabel:       "2K",
			PerRequestPrice: &price2K,
		}},
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	svc := completion.NewRecorder(completion.Dependencies{
		Calculator: NewBillingService(&config.Config{}, nil),
		Prices:     billingtestkit.PriceResolver(pricingConfigService, NewBillingService(&config.Config{}, nil)),
	}, completion.RecorderOptions{DefaultMultiplier: 1})

	cost := svc.CalculateRecordUsageCost(
		context.Background(), gatewaycapture.ProjectMessagesCompletionResult(&forwardcore.MessagesResult{Model: "gemini-image", ImageCount: 2, ImageSize: ""},

			nil), gatewaycapture.ProjectCompletionKey(&apikey.APIKey{GroupID: i64p(groupID), Group: &routing.Group{ID: groupID}}), gatewaycapture.ProjectCompletionAccount(nil), "gemini-image",
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
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	serviceTier := "priority"
	usage := openai.ForwardUsage{InputTokens: 100, OutputTokens: 50}

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 3017, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.ServiceTier)
	require.Equal(t, "default", *usageRepo.LastLog.ServiceTier, "usage log must record the tier actually billed")

	baseCost, calcErr := svc.Dependencies.Calculator.CalculateCost("gpt-5.4", pricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1.0)
	require.NoError(t, calcErr)
	require.InDelta(t, baseCost.TotalCost, usageRepo.LastLog.TotalCost, 1e-10, "a request served at default must not pay the priority price")
}

func TestOpenAIGatewayServiceRecordUsage_CodexDefaultEchoKeepsFastBilling(t *testing.T) {
	for _, accountType := range []string{capability.AccountTypeOAuth, capability.AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
			svc := newOpenAIRecordUsageServiceForTest(
				usageRepo,
				&completiontestkit.UserStore{},
				&completiontestkit.SubscriptionStore{},
				nil,
			)
			serviceTier := "priority"
			tokens := pricing.UsageTokens{InputTokens: 100, OutputTokens: 50}

			err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
				Account: &accountcore.Record{ID: 3019, Platform: capability.PlatformOpenAI, Type: accountType},
			})

			require.NoError(t, err)
			require.NotNil(t, usageRepo.LastLog)
			require.NotNil(t, usageRepo.LastLog.ServiceTier)
			require.Equal(t, "priority", *usageRepo.LastLog.ServiceTier)

			fastCost, calcErr := svc.Dependencies.Calculator.CalculateCostWithServiceTier("gpt-5.6-sol", tokens, 1.0, "priority")
			require.NoError(t, calcErr)
			require.InDelta(t, fastCost.TotalCost, usageRepo.LastLog.TotalCost, 1e-10)
		})
	}
}

func TestOpenAIGatewayServiceRecordUsage_ShadowUsesParentCredentialTierContract(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(
		usageRepo,
		&completiontestkit.UserStore{},
		&completiontestkit.SubscriptionStore{},
		nil,
	)
	parentID := int64(9001)
	accountRepo := &completiontestkit.AccountLookup{Account: &accountcore.Record{
		ID: parentID, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth,
	}}
	svc.AccountLookup = func(ctx context.Context, id int64) (*accountcore.Record, error) {
		value,

			err := accountRepo.
			GetByID(ctx, id)
		return value, err
	}

	serviceTier := "priority"
	tokens := pricing.UsageTokens{InputTokens: 100, OutputTokens: 50}

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{
			ID: 3020, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
			ParentAccountID: &parentID,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, accountRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.ServiceTier)
	require.Equal(t, "priority", *usageRepo.LastLog.ServiceTier)

	fastCost, calcErr := svc.Dependencies.Calculator.CalculateCostWithServiceTier("gpt-5.6-sol", tokens, 1.0, "priority")
	require.NoError(t, calcErr)
	require.InDelta(t, fastCost.TotalCost, usageRepo.LastLog.TotalCost, 1e-10)
}

// TestOpenAIGatewayServiceRecordUsage_FreeOpenAIFastChargesStandard 验证免费 Fast
// 只替换用户侧实际费用，Usage Log 仍保留 priority 的基础成本。
func TestOpenAIGatewayServiceRecordUsage_FreeOpenAIFastChargesStandard(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(
		usageRepo,
		&completiontestkit.UserStore{},
		&completiontestkit.SubscriptionStore{},
		nil,
	)
	svc.Dependencies.Prices = billingtestkit.PriceResolver(nil, svc.Dependencies.Calculator)
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
			ModelPricing: []routing.ModelPricingEntry{{
				Models:         []string{"gpt-5.6-sol"},
				BillingMode:    routing.BillingModeToken,
				InputPrice:     &inputPrice,
				OutputPrice:    &outputPrice,
				FastMultiplier: &fastMultiplier,
			}},
		},
	}

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
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
		Account: &accountcore.Record{ID: 3020, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.ServiceTier)
	require.Equal(t, "priority", *usageRepo.LastLog.ServiceTier)
	require.InDelta(t, 0.5, usageRepo.LastLog.RateMultiplier, 1e-12)

	standardTotal := float64(tokens.InputTokens)*inputPrice + float64(tokens.OutputTokens)*outputPrice
	require.InDelta(t, standardTotal*fastMultiplier, usageRepo.LastLog.TotalCost, 1e-10)
	require.InDelta(t, standardTotal*0.5, usageRepo.LastLog.ActualCost, 1e-10)

	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.NotNil(t, billingRepo.LastCmd)
	// 统一结算必须以 Standard 基础价分配订阅/余额，不能把 Fast 基础价当成用户欠费。
	require.InDelta(t, standardTotal, billingRepo.LastCmd.BaseAmountUSD, 1e-10)
	require.InDelta(t, standardTotal*0.5, billingRepo.LastCmd.BillableAmountUSD, 1e-10)
}

// TestGroupBillsOpenAIFastAtStandardRequiresOpenAIAccount 锁定平台、账号和档位三重边界。
func TestGroupBillsOpenAIFastAtStandardRequiresOpenAIAccount(t *testing.T) {
	apiKey := &apikey.APIKey{Group: &routing.Group{Platform: capability.PlatformOpenAI, FreeOpenAIFast: true}}

	require.True(t, completion.GroupBillsOpenAIFastAtStandard(gatewaycapture.ProjectCompletionKey(apiKey), gatewaycapture.ProjectCompletionAccount(&accountcore.Record{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}), "priority"))
	require.True(t, completion.GroupBillsOpenAIFastAtStandard(gatewaycapture.ProjectCompletionKey(apiKey), gatewaycapture.ProjectCompletionAccount(&accountcore.Record{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}), " FAST "))
	require.False(t, completion.GroupBillsOpenAIFastAtStandard(gatewaycapture.ProjectCompletionKey(apiKey), gatewaycapture.ProjectCompletionAccount(&accountcore.Record{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}), "standard"))
	require.False(t, completion.GroupBillsOpenAIFastAtStandard(gatewaycapture.ProjectCompletionKey(apiKey), gatewaycapture.ProjectCompletionAccount(&accountcore.Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey}), "priority"))
}

func TestOpenAIGatewayServiceRecordUsage_ServiceTierNeverRaisedByUpstreamResponse(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 100, OutputTokens: 50}

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID:                   "resp_service_tier_not_raised",
			UpstreamResponseServiceTier: "priority",
			Usage:                       usage,
			Model:                       "gpt-5.4",
			Duration:                    time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 1018},
		User:    &identity.User{ID: 2018},
		Account: &accountcore.Record{ID: 3018, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Nil(t, usageRepo.LastLog.ServiceTier)

	baseCost, calcErr := svc.Dependencies.Calculator.CalculateCost("gpt-5.4", pricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1.0)
	require.NoError(t, calcErr)
	require.InDelta(t, baseCost.TotalCost, usageRepo.LastLog.TotalCost, 1e-10)
}

// 记录测试保留原存储替身与缓存作用域，核心直接使用 completion.Recorder。
func newOpenAIRecordUsageServiceForTest(logs usagecore.UsageLogRepository, _ identity.UserRepository, _ billing.UserSubscriptionRepository, rates billing.UserGroupRateRepository) *completiontestkit.Recording {
	return completiontestkit.NewRecording(logs, &completiontestkit.SettlementStore{}, rates, true)
}

func newOpenAIRecordUsageServiceWithBillingRepoForTest(logs usagecore.UsageLogRepository, funds completion.Store, _ identity.UserRepository, _ billing.UserSubscriptionRepository, rates billing.UserGroupRateRepository) *completiontestkit.Recording {
	return completiontestkit.NewRecording(logs, funds, rates, false)
}
