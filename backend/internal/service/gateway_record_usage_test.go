//go:build unit

package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func newGatewayRecordUsageServiceForTest(usageRepo UsageLogRepository, userRepo UserRepository, subRepo UserSubscriptionRepository) *GatewayService {
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1.1
	billingRepo := &openAIRecordUsageBillingRepoStub{}
	return NewGatewayService(
		nil,
		nil,
		usageRepo,
		billingRepo,
		userRepo,
		subRepo,
		nil,
		nil,
		cfg,
		nil,
		nil,
		NewBillingService(cfg, nil),
		nil,
		&BillingCacheService{},
		nil,
		nil,
		&DeferredService{},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil, // 用户平台配额仓库
		nil, // 数据共享服务
	)
}

func requireGatewayRecordUsageBillingRepoStub(t *testing.T, svc *GatewayService) *openAIRecordUsageBillingRepoStub {
	t.Helper()

	billingRepo, ok := svc.usageBillingRepo.(*openAIRecordUsageBillingRepoStub)
	require.True(t, ok)
	return billingRepo
}

func newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo UsageLogRepository, billingRepo UsageBillingRepository, userRepo UserRepository, subRepo UserSubscriptionRepository) *GatewayService {
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1.1
	return NewGatewayService(
		nil,
		nil,
		usageRepo,
		billingRepo,
		userRepo,
		subRepo,
		nil,
		nil,
		cfg,
		nil,
		nil,
		NewBillingService(cfg, nil),
		nil,
		&BillingCacheService{},
		nil,
		nil,
		&DeferredService{},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)
}

type openAIRecordUsageBestEffortLogRepoStub struct {
	UsageLogRepository

	bestEffortErr   error
	createErr       error
	bestEffortCalls int
	createCalls     int
	lastLog         *UsageLog
	lastCtxErr      error
}

func (s *openAIRecordUsageBestEffortLogRepoStub) CreateBestEffort(ctx context.Context, log *UsageLog) error {
	s.bestEffortCalls++
	s.lastLog = log
	s.lastCtxErr = ctx.Err()
	return s.bestEffortErr
}

func (s *openAIRecordUsageBestEffortLogRepoStub) Create(ctx context.Context, log *UsageLog) (bool, error) {
	s.createCalls++
	s.lastLog = log
	s.lastCtxErr = ctx.Err()
	return false, s.createErr
}

func TestGatewayServiceRecordUsage_BillingUsesDetachedContext(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: false, err: context.DeadlineExceeded}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	quotaSvc := &openAIRecordUsageAPIKeyQuotaStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)

	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.RecordUsage(reqCtx, &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_detached_ctx",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey: &APIKey{
			ID:    501,
			Quota: 100,
		},
		User:          &User{ID: 601},
		Account:       &Account{ID: 701},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	billingRepo := requireGatewayRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.calls)
	require.NoError(t, billingRepo.lastCtxErr)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, billingRepo.lastCmd.BillableAmountUSD, billingRepo.lastCmd.APIKeyQuotaCost, 1e-12)
}

func TestGatewayServiceRecordUsage_BillingFingerprintIncludesRequestPayloadHash(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	payloadHash := HashUsageRequestPayload([]byte(`{"messages":[{"role":"user","content":"hello"}]}`))
	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_payload_hash",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:             &APIKey{ID: 501, Quota: 100},
		User:               &User{ID: 601},
		Account:            &Account{ID: 701},
		RequestPayloadHash: payloadHash,
	})
	require.NoError(t, err)
	require.NotNil(t, billingRepo.lastCmd)
	require.Equal(t, payloadHash, billingRepo.lastCmd.RequestPayloadHash)
}

func TestGatewayServiceRecordUsage_BillingFingerprintFallsBackToContextRequestID(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	ctx := context.WithValue(context.Background(), ctxkey.RequestID, "req-local-123")
	err := svc.RecordUsage(ctx, &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_payload_fallback",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &APIKey{ID: 501, Quota: 100},
		User:    &User{ID: 601},
		Account: &Account{ID: 701},
	})
	require.NoError(t, err)
	require.NotNil(t, billingRepo.lastCmd)
	require.Equal(t, "local:req-local-123", billingRepo.lastCmd.RequestPayloadHash)
}

func TestGatewayServiceRecordUsage_PreservesRequestedAndUpstreamModels(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	mappedModel := "claude-sonnet-4-20250514"

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:     "gateway_models_split",
			Usage:         ClaudeUsage{InputTokens: 10, OutputTokens: 6},
			Model:         "claude-sonnet-4",
			UpstreamModel: mappedModel,
			Duration:      time.Second,
		},
		APIKey:  &APIKey{ID: 501, Quota: 100},
		User:    &User{ID: 601},
		Account: &Account{ID: 701},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "claude-sonnet-4", usageRepo.lastLog.Model)
	require.Equal(t, "claude-sonnet-4", usageRepo.lastLog.RequestedModel)
	require.NotNil(t, usageRepo.lastLog.UpstreamModel)
	require.Equal(t, mappedModel, *usageRepo.lastLog.UpstreamModel)
}

func TestGatewayServiceRecordUsage_QoderUsesStandardRequestedModelPricing(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	usage := ClaudeUsage{InputTokens: 1200, OutputTokens: 300}
	expectedCost, err := svc.billingService.CalculateCost("gpt-5.4", UsageTokens{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:     "qoder_standard_requested_model_pricing",
			Usage:         usage,
			Model:         "gpt-5.4",
			UpstreamModel: "ultimate",
			Duration:      time.Second,
		},
		APIKey: &APIKey{
			ID:    502,
			Quota: 100,
			Group: &Group{Platform: PlatformQoder, RateMultiplier: 1},
		},
		User:    &User{ID: 602},
		Account: &Account{ID: 702, Platform: PlatformQoder},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "gpt-5.4", usageRepo.lastLog.Model)
	require.Equal(t, 1200, usageRepo.lastLog.InputTokens)
	require.Equal(t, 300, usageRepo.lastLog.OutputTokens)
	require.InDelta(t, expectedCost.TotalCost, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
	require.True(t, usageRepo.lastLog.ActualCost > 0, "Qoder should reuse standard requested-model pricing instead of a Qoder-specific upstream/deferred branch")

	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, expectedCost.ActualCost, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
	require.Zero(t, billingRepo.lastCmd.APIKeyQuotaCost)
	require.Zero(t, billingRepo.lastCmd.APIKeyRateLimitCost)
	require.Zero(t, billingRepo.lastCmd.AccountQuotaCost)
}

func TestGatewayServiceRecordUsage_QoderDefaultAliasesUseOpus48Pricing(t *testing.T) {
	aliases := make([]string, 0, len(defaultQoderModelAliases))
	for alias := range defaultQoderModelAliases {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	for _, alias := range aliases {
		t.Run(alias, func(t *testing.T) {
			usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
			billingRepo := &openAIRecordUsageBillingRepoStub{}
			svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

			usage := ClaudeUsage{InputTokens: 1200, OutputTokens: 300, CacheCreationInputTokens: 50, CacheReadInputTokens: 25}
			expectedCost, err := svc.billingService.CalculateCost(qoderDefaultAliasFallbackBillingModel, UsageTokens{
				InputTokens:           usage.InputTokens,
				OutputTokens:          usage.OutputTokens,
				CacheCreationTokens:   usage.CacheCreationInputTokens,
				CacheReadTokens:       usage.CacheReadInputTokens,
				CacheCreation5mTokens: usage.CacheCreation5mTokens,
				CacheCreation1hTokens: usage.CacheCreation1hTokens,
			}, 1.1)
			require.NoError(t, err)

			err = svc.RecordUsage(context.Background(), &RecordUsageInput{
				Result: &ForwardResult{
					RequestID:     "qoder_alias_" + alias,
					Usage:         usage,
					Model:         alias,
					UpstreamModel: lookupQoderAliasKeyForTest(alias),
					Duration:      time.Second,
				},
				APIKey: &APIKey{
					ID:    502,
					Quota: 100,
					Group: &Group{Platform: PlatformQoder, RateMultiplier: 1},
				},
				User:    &User{ID: 602},
				Account: &Account{ID: 702, Platform: PlatformQoder},
			})

			require.NoError(t, err)
			require.Equal(t, 1, usageRepo.calls)
			require.NotNil(t, usageRepo.lastLog)
			require.Equal(t, alias, usageRepo.lastLog.Model)
			require.InDelta(t, expectedCost.TotalCost, usageRepo.lastLog.TotalCost, 1e-12)
			require.InDelta(t, expectedCost.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)

			require.Equal(t, 1, billingRepo.calls)
			require.NotNil(t, billingRepo.lastCmd)
			require.InDelta(t, expectedCost.ActualCost, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
		})
	}
}

func TestGatewayServiceRecordUsage_QoderChannelMappedRouteKeyUsesOpus48Pricing(t *testing.T) {
	groupID := int64(902)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	usage := ClaudeUsage{InputTokens: 1200, OutputTokens: 300, CacheCreationInputTokens: 50, CacheReadInputTokens: 25}
	expectedCost, err := svc.billingService.CalculateCost(qoderDefaultAliasFallbackBillingModel, UsageTokens{
		InputTokens:         usage.InputTokens,
		OutputTokens:        usage.OutputTokens,
		CacheCreationTokens: usage.CacheCreationInputTokens,
		CacheReadTokens:     usage.CacheReadInputTokens,
	}, 1)
	require.NoError(t, err)

	err = svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:     "qoder_channel_mapped_route_key",
			Usage:         usage,
			Model:         "qmodel",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1},
		},
		User:    &User{ID: 602},
		Account: &Account{ID: 702, Platform: PlatformQoder},
		ChannelUsageFields: ChannelUsageFields{
			OriginalModel:      "qwen3.7-plus",
			ChannelMappedModel: "qmodel",
			BillingModelSource: BillingModelSourceChannelMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, expectedCost.TotalCost, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, expectedCost.ActualCost, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
}

func TestGatewayServiceRecordUsage_QoderChannelMappedRouteKeyUsesOriginalAliasManualPricing(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	outputPrice := 0.02
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformQoder, model: "qwen3.7-plus"}] = &ChannelModelPricing{
		BillingMode: BillingModeToken,
		InputPrice:  &inputPrice,
		OutputPrice: &outputPrice,
	}
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	cache.groupPlatform[groupID] = PlatformQoder
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	svc.resolver = NewModelPricingResolver(channelService, svc.billingService)

	usage := ClaudeUsage{InputTokens: 1200, OutputTokens: 300}
	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:     "qoder_channel_mapped_route_key_original_alias_pricing",
			Usage:         usage,
			Model:         "qmodel",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1},
		},
		User:    &User{ID: 602},
		Account: &Account{ID: 702, Platform: PlatformQoder},
		ChannelUsageFields: ChannelUsageFields{
			OriginalModel:      "qwen3.7-plus",
			ChannelMappedModel: "qmodel",
			BillingModelSource: BillingModelSourceChannelMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, 18.0, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, 18.0, usageRepo.lastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, 18.0, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
}

func TestGatewayServiceRecordUsage_QoderChannelMappedCustomAliasPartialManualPricingUsesRouteKeyBaseWithoutMappingCache(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformQoder, model: "custom-qoder"}] = &ChannelModelPricing{
		BillingMode: BillingModeToken,
		InputPrice:  &inputPrice,
	}
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	cache.groupPlatform[groupID] = PlatformQoder
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	svc.resolver = NewModelPricingResolver(channelService, svc.billingService)

	usage := ClaudeUsage{InputTokens: 100, OutputTokens: 100000}
	defaultPricing, err := svc.billingService.GetModelPricing(qoderDefaultAliasFallbackBillingModel)
	require.NoError(t, err)
	expectedCost := float64(usage.InputTokens)*inputPrice + float64(usage.OutputTokens)*defaultPricing.OutputPricePerToken

	err = svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:     "qoder_channel_mapped_custom_alias_partial_pricing",
			Usage:         usage,
			Model:         "qmodel",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1},
		},
		User:    &User{ID: 602},
		Account: &Account{ID: 702, Platform: PlatformQoder},
		ChannelUsageFields: ChannelUsageFields{
			OriginalModel:      "custom-qoder",
			ChannelMappedModel: "qmodel",
			BillingModelSource: BillingModelSourceChannelMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, expectedCost, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, expectedCost, usageRepo.lastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, expectedCost, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
}

func TestGatewayServiceRecordUsage_QoderAccountMappedCustomAliasPartialManualPricingUsesUpstreamRouteKeyBase(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformQoder, model: "custom-qoder"}] = &ChannelModelPricing{
		BillingMode: BillingModeToken,
		InputPrice:  &inputPrice,
	}
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	cache.groupPlatform[groupID] = PlatformQoder
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	svc.resolver = NewModelPricingResolver(channelService, svc.billingService)

	usage := ClaudeUsage{InputTokens: 100, OutputTokens: 100000}
	defaultPricing, err := svc.billingService.GetModelPricing(qoderDefaultAliasFallbackBillingModel)
	require.NoError(t, err)
	expectedCost := float64(usage.InputTokens)*inputPrice + float64(usage.OutputTokens)*defaultPricing.OutputPricePerToken

	err = svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:     "qoder_account_mapped_custom_alias_partial_pricing",
			Usage:         usage,
			Model:         "custom-qoder",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1},
		},
		User:    &User{ID: 602},
		Account: &Account{ID: 702, Platform: PlatformQoder},
		ChannelUsageFields: ChannelUsageFields{
			OriginalModel:      "custom-qoder",
			ChannelMappedModel: "custom-qoder",
			BillingModelSource: BillingModelSourceChannelMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, expectedCost, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, expectedCost, usageRepo.lastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, expectedCost, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
}

func TestGatewayServiceRecordUsage_QoderCustomMappedRouteKeyFallsBackToOpus48Pricing(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	usage := ClaudeUsage{InputTokens: 1200, OutputTokens: 300, CacheCreationInputTokens: 50, CacheReadInputTokens: 25}
	expectedCost, err := svc.billingService.CalculateCost(qoderDefaultAliasFallbackBillingModel, UsageTokens{
		InputTokens:         usage.InputTokens,
		OutputTokens:        usage.OutputTokens,
		CacheCreationTokens: usage.CacheCreationInputTokens,
		CacheReadTokens:     usage.CacheReadInputTokens,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:     "qoder_custom_alias_route_key",
			Usage:         usage,
			Model:         "custom-qoder-model",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &APIKey{
			ID:    502,
			Quota: 100,
			Group: &Group{Platform: PlatformQoder, RateMultiplier: 1},
		},
		User:    &User{ID: 602},
		Account: &Account{ID: 702, Platform: PlatformQoder},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "custom-qoder-model", usageRepo.lastLog.Model)
	require.InDelta(t, expectedCost.TotalCost, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, expectedCost.ActualCost, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
}

func TestGatewayServiceRecordUsage_QoderUpstreamBillingSourceUsesRouteKeyPricing(t *testing.T) {
	groupID := int64(902)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	usage := ClaudeUsage{InputTokens: 1200, OutputTokens: 300}
	expectedCost, err := svc.billingService.CalculateCost(qoderDefaultAliasFallbackBillingModel, UsageTokens{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
	}, 1)
	require.NoError(t, err)

	err = svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:     "qoder_upstream_billing_source_route_key",
			Usage:         usage,
			Model:         "gpt-5.4-mini",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1},
		},
		User:    &User{ID: 602},
		Account: &Account{ID: 702, Platform: PlatformQoder},
		ChannelUsageFields: ChannelUsageFields{
			OriginalModel:      "gpt-5.4-mini",
			ChannelMappedModel: "gpt-5.4-mini",
			BillingModelSource: BillingModelSourceUpstream,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, expectedCost.TotalCost, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
}

func TestGatewayServiceRecordUsage_QoderChannelMappedAccountStatsUsesOriginalAliasRule(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	outputPrice := 0.02
	cache := newEmptyChannelCache()
	cache.channelByGroupID[groupID] = &Channel{
		ID:     groupID,
		Status: StatusActive,
		AccountStatsPricingRules: []AccountStatsPricingRule{
			{
				GroupIDs: []int64{groupID},
				Pricing: []ChannelModelPricing{
					{
						Models:      []string{"qwen3.7-plus"},
						InputPrice:  &inputPrice,
						OutputPrice: &outputPrice,
					},
				},
			},
		},
	}
	cache.groupPlatform[groupID] = PlatformQoder
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	svc.channelService = channelService

	usage := ClaudeUsage{InputTokens: 100, OutputTokens: 50}
	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:     "qoder_channel_mapped_account_stats_alias",
			Usage:         usage,
			Model:         "qmodel",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1},
		},
		User:    &User{ID: 602},
		Account: &Account{ID: 702, Platform: PlatformQoder},
		ChannelUsageFields: ChannelUsageFields{
			OriginalModel:      "qwen3.7-plus",
			ChannelMappedModel: "qmodel",
			BillingModelSource: BillingModelSourceChannelMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.AccountStatsCost)
	require.InDelta(t, 2.0, *usageRepo.lastLog.AccountStatsCost, 1e-12)
}

func TestGatewayServiceRecordUsage_QoderBlankChannelPricingKeepsOpus48Base(t *testing.T) {
	groupID := int64(902)
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformQoder, model: "auto"}] = &ChannelModelPricing{
		BillingMode: BillingModeToken,
	}
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	cache.groupPlatform[groupID] = PlatformQoder
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	svc.resolver = NewModelPricingResolver(channelService, svc.billingService)

	usage := ClaudeUsage{InputTokens: 1200, OutputTokens: 300, CacheCreationInputTokens: 50, CacheReadInputTokens: 25}
	expectedCost, err := svc.billingService.CalculateCost(qoderDefaultAliasFallbackBillingModel, UsageTokens{
		InputTokens:         usage.InputTokens,
		OutputTokens:        usage.OutputTokens,
		CacheCreationTokens: usage.CacheCreationInputTokens,
		CacheReadTokens:     usage.CacheReadInputTokens,
	}, 1)
	require.NoError(t, err)

	err = svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:     "qoder_alias_blank_channel_pricing",
			Usage:         usage,
			Model:         "auto",
			UpstreamModel: "auto",
			Duration:      time.Second,
		},
		APIKey: &APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1},
		},
		User:    &User{ID: 602},
		Account: &Account{ID: 702, Platform: PlatformQoder},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, expectedCost.TotalCost, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.lastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, expectedCost.ActualCost, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
}

func TestGatewayServiceRecordUsage_QoderManualChannelPricingOverridesDefaultAliasPricing(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	outputPrice := 0.02
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformQoder, model: "auto"}] = &ChannelModelPricing{
		BillingMode: BillingModeToken,
		InputPrice:  &inputPrice,
		OutputPrice: &outputPrice,
	}
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	cache.groupPlatform[groupID] = PlatformQoder
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	billingRepo := &openAIRecordUsageBillingRepoStub{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	svc.resolver = NewModelPricingResolver(channelService, svc.billingService)

	usage := ClaudeUsage{InputTokens: 1200, OutputTokens: 300}
	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:     "qoder_alias_manual_pricing",
			Usage:         usage,
			Model:         "auto",
			UpstreamModel: "auto",
			Duration:      time.Second,
		},
		APIKey: &APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1},
		},
		User:    &User{ID: 602},
		Account: &Account{ID: 702, Platform: PlatformQoder},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	require.NotNil(t, usageRepo.lastLog)
	require.InDelta(t, 18.0, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, 18.0, usageRepo.lastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, 18.0, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
}

func lookupQoderAliasKeyForTest(alias string) string {
	if info, ok := defaultQoderModelAliases[alias]; ok {
		return info.Key
	}
	return alias
}

func TestForwardResultBillingModelPrefersRequestedModel(t *testing.T) {
	require.Equal(t, "claude-opus-4-6", forwardResultBillingModel("claude-opus-4-6", "ultimate"))
	require.Equal(t, "ultimate", forwardResultBillingModel("", "ultimate"))
}

func TestGatewayServiceRecordUsage_ImageIndependentMultiplierUsesImageRate(t *testing.T) {
	imagePrice := 0.2
	groupID := int64(711)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:  "gateway_image_independent_multiplier",
			Model:      "gemini-image",
			ImageCount: 2,
			ImageSize:  "1K",
			Duration:   time.Second,
		},
		APIKey: &APIKey{
			ID:      511,
			GroupID: i64p(groupID),
			Group: &Group{
				ID:                   groupID,
				RateMultiplier:       0.15,
				ImageRateIndependent: true,
				ImageRateMultiplier:  0.5,
				ImagePrice1K:         &imagePrice,
			},
		},
		User:    &User{ID: 611},
		Account: &Account{ID: 711},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, 2, usageRepo.lastLog.ImageCount)
	require.InDelta(t, 0.4, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.2, usageRepo.lastLog.ActualCost, 1e-12)
	require.InDelta(t, 0.5, usageRepo.lastLog.RateMultiplier, 1e-12)
	require.NotNil(t, usageRepo.lastLog.BillingMode)
	require.Equal(t, string(BillingModeImage), *usageRepo.lastLog.BillingMode)
}

func TestGatewayServiceRecordUsage_EmptyImageSizeDefaultsBeforeBillingAndPersistence(t *testing.T) {
	imagePrice2K := 0.19
	groupID := int64(901)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:      "gateway_image_default_size",
			Model:          "gemini-image",
			ImageCount:     1,
			ImageInputSize: "auto",
			Duration:       time.Second,
		},
		APIKey: &APIKey{
			ID:      801,
			GroupID: i64p(groupID),
			Group: &Group{
				ID:             groupID,
				RateMultiplier: 1.0,
				ImagePrice2K:   &imagePrice2K,
			},
		},
		User:    &User{ID: 601},
		Account: &Account{ID: 701},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, 1, usageRepo.lastLog.ImageCount)
	require.NotNil(t, usageRepo.lastLog.ImageSize)
	require.Equal(t, ImageBillingSize2K, *usageRepo.lastLog.ImageSize)
	require.NotNil(t, usageRepo.lastLog.ImageInputSize)
	require.Equal(t, "auto", *usageRepo.lastLog.ImageInputSize)
	require.NotNil(t, usageRepo.lastLog.ImageSizeSource)
	require.Equal(t, ImageSizeSourceDefault, *usageRepo.lastLog.ImageSizeSource)
	require.InDelta(t, 0.19, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.19, usageRepo.lastLog.ActualCost, 1e-12)
}

func TestGatewayServiceRecordUsage_PeakRateAffectsTokenModeImageOutputTokens(t *testing.T) {
	groupID := int64(902)
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
	userRepo := &openAIRecordUsageUserRepoStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, &openAIRecordUsageSubRepoStub{})
	svc.resolver = newOpenAITokenImageChannelPricingResolverForTest(t, groupID, "gemini-image")

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID:  "gateway_peak_image_tokens",
			Model:      "gemini-image",
			ImageCount: 1,
			Usage: ClaudeUsage{
				InputTokens:       1000,
				OutputTokens:      600,
				ImageOutputTokens: 100,
			},
			Duration: time.Second,
		},
		APIKey: &APIKey{
			ID:      802,
			GroupID: i64p(groupID),
			Group: &Group{
				ID:                 groupID,
				RateMultiplier:     1.0,
				PeakRateEnabled:    true,
				PeakStart:          "00:00",
				PeakEnd:            "23:59",
				PeakRateMultiplier: 3.0,
			},
		},
		User:    &User{ID: 602},
		Account: &Account{ID: 702},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.BillingMode)
	require.Equal(t, string(BillingModeToken), *usageRepo.lastLog.BillingMode)
	require.Equal(t, 3.0, usageRepo.lastLog.RateMultiplier)

	textInput := 1000 * 3e-6
	textOutput := 500 * 15e-6
	imageOutput := 100 * 15e-6
	expectedActual := (textInput + textOutput + imageOutput) * 3.0

	require.InDelta(t, textInput+textOutput+imageOutput, usageRepo.lastLog.TotalCost, 1e-12)
	require.InDelta(t, imageOutput, usageRepo.lastLog.ImageOutputCost, 1e-12)
	require.InDelta(t, expectedActual, usageRepo.lastLog.ActualCost, 1e-12)
	billingRepo := requireGatewayRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, expectedActual, billingRepo.lastCmd.BillableAmountUSD, 1e-12)
}

func TestGatewayServiceRecordUsage_UsageLogWriteErrorDoesNotSkipBilling(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: false, err: MarkUsageLogCreateNotPersisted(context.Canceled)}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	quotaSvc := &openAIRecordUsageAPIKeyQuotaStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_not_persisted",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey: &APIKey{
			ID:    503,
			Quota: 100,
		},
		User:          &User{ID: 603},
		Account:       &Account{ID: 703},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	billingRepo := requireGatewayRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.calls)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, billingRepo.lastCmd.BillableAmountUSD, billingRepo.lastCmd.APIKeyQuotaCost, 1e-12)
}

func TestGatewayServiceRecordUsageWithLongContext_BillingUsesDetachedContext(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{inserted: false, err: context.DeadlineExceeded}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	quotaSvc := &openAIRecordUsageAPIKeyQuotaStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)

	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.RecordUsageWithLongContext(reqCtx, &RecordUsageLongContextInput{
		Result: &ForwardResult{
			RequestID: "gateway_long_context_detached_ctx",
			Usage: ClaudeUsage{
				InputTokens:  12,
				OutputTokens: 8,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey: &APIKey{
			ID:    502,
			Quota: 100,
		},
		User:                  &User{ID: 602},
		Account:               &Account{ID: 702},
		LongContextThreshold:  200000,
		LongContextMultiplier: 2,
		APIKeyService:         quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.calls)
	billingRepo := requireGatewayRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.calls)
	require.NoError(t, billingRepo.lastCtxErr)
	require.NotNil(t, billingRepo.lastCmd)
	require.InDelta(t, billingRepo.lastCmd.BillableAmountUSD, billingRepo.lastCmd.APIKeyQuotaCost, 1e-12)
}

func TestGatewayServiceRecordUsage_UsesFallbackRequestIDForUsageLog(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)

	ctx := context.WithValue(context.Background(), ctxkey.RequestID, "gateway-local-fallback")
	err := svc.RecordUsage(ctx, &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &APIKey{ID: 504},
		User:    &User{ID: 604},
		Account: &Account{ID: 704},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "local:gateway-local-fallback", usageRepo.lastLog.RequestID)
}

func TestGatewayServiceRecordUsage_PrefersClientRequestIDOverUpstreamRequestID(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, "client-stable-123")
	ctx = context.WithValue(ctx, ctxkey.RequestID, "req-local-ignored")
	err := svc.RecordUsage(ctx, &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "upstream-volatile-456",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &APIKey{ID: 506},
		User:    &User{ID: 606},
		Account: &Account{ID: 706},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.lastCmd)
	require.Equal(t, "client:client-stable-123", billingRepo.lastCmd.RequestID)
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, "client:client-stable-123", usageRepo.lastLog.RequestID)
}

func TestGatewayServiceRecordUsage_GeneratesRequestIDWhenAllSourcesMissing(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &APIKey{ID: 507},
		User:    &User{ID: 607},
		Account: &Account{ID: 707},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.lastCmd)
	require.True(t, strings.HasPrefix(billingRepo.lastCmd.RequestID, "generated:"))
	require.NotNil(t, usageRepo.lastLog)
	require.Equal(t, billingRepo.lastCmd.RequestID, usageRepo.lastLog.RequestID)
}

func TestGatewayServiceRecordUsage_DroppedUsageLogDoesNotSyncFallback(t *testing.T) {
	usageRepo := &openAIRecordUsageBestEffortLogRepoStub{
		bestEffortErr: MarkUsageLogCreateDropped(errors.New("usage log best-effort queue full")),
	}
	billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_drop_usage_log",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &APIKey{ID: 508},
		User:    &User{ID: 608},
		Account: &Account{ID: 708},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.bestEffortCalls)
	require.Equal(t, 0, usageRepo.createCalls)
}

func TestGatewayServiceRecordUsage_BillingErrorSkipsUsageLogWrite(t *testing.T) {
	usageRepo := &openAIRecordUsageLogRepoStub{}
	billingRepo := &openAIRecordUsageBillingRepoStub{err: context.DeadlineExceeded}
	userRepo := &openAIRecordUsageUserRepoStub{}
	subRepo := &openAIRecordUsageSubRepoStub{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo)

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "gateway_billing_fail",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &APIKey{ID: 505},
		User:    &User{ID: 605},
		Account: &Account{ID: 705},
	})

	require.Error(t, err)
	require.Equal(t, 1, billingRepo.calls)
	require.Equal(t, 0, usageRepo.calls)
}

func TestGatewayServiceRecordUsage_ReasoningEffortPersisted(t *testing.T) {
	usageRepo := &openAIRecordUsageBestEffortLogRepoStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	effort := "max"
	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "effort_test",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 5,
			},
			Model:           "claude-opus-4-6",
			Duration:        time.Second,
			ReasoningEffort: &effort,
		},
		APIKey:  &APIKey{ID: 1},
		User:    &User{ID: 1},
		Account: &Account{ID: 1},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.NotNil(t, usageRepo.lastLog.ReasoningEffort)
	require.Equal(t, "max", *usageRepo.lastLog.ReasoningEffort)
}

func TestGatewayServiceRecordUsage_ReasoningEffortNil(t *testing.T) {
	usageRepo := &openAIRecordUsageBestEffortLogRepoStub{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})

	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		Result: &ForwardResult{
			RequestID: "no_effort_test",
			Usage: ClaudeUsage{
				InputTokens:  10,
				OutputTokens: 5,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &APIKey{ID: 1},
		User:    &User{ID: 1},
		Account: &Account{ID: 1},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.lastLog)
	require.Nil(t, usageRepo.lastLog.ReasoningEffort)
}
