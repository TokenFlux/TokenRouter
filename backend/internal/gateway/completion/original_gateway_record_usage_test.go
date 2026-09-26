//go:build unit

package completion_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	completiontestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	usagecore "github.com/TokenFlux/TokenRouter/internal/usage"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/stretchr/testify/require"
)

func requireGatewayRecordUsageBillingRepoStub(t *testing.T, svc *completiontestkit.Recording) *completiontestkit.SettlementStore {
	t.Helper()

	billingRepo, ok := svc.Dependencies.Funds.(*completiontestkit.SettlementStore)
	require.True(t, ok)
	return billingRepo
}

func TestGatewayServiceRecordUsage_BillingUsesDetachedContext(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: false, Err: context.DeadlineExceeded}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	quotaSvc := &completiontestkit.KeyQuotaUpdater{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)

	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.RecordMessages(reqCtx, &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID: "gateway_detached_ctx",
			Usage: upstream.TokenUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:    501,
			Quota: 100,
		},
		User:          &identity.User{ID: 601},
		Account:       &accountcore.Record{ID: 701},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	billingRepo := requireGatewayRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NoError(t, billingRepo.LastCtxErr)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, billingRepo.LastCmd.BillableAmountUSD, billingRepo.LastCmd.APIKeyQuotaCost, 1e-12)
}

func TestGatewayServiceRecordUsage_BillingFingerprintIncludesRequestPayloadHash(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	payloadHash := billing.HashUsageRequestPayload([]byte(`{"messages":[{"role":"user","content":"hello"}]}`))
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID: "gateway_payload_hash",
			Usage: upstream.TokenUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
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

func TestGatewayServiceRecordUsage_BillingFingerprintFallsBackToContextRequestID(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	ctx := context.WithValue(context.Background(), telemetry.RequestID, "req-local-123")
	err := svc.RecordMessages(ctx, &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID: "gateway_payload_fallback",
			Usage: upstream.TokenUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 501, Quota: 100},
		User:    &identity.User{ID: 601},
		Account: &accountcore.Record{ID: 701},
	})
	require.NoError(t, err)
	require.NotNil(t, billingRepo.LastCmd)
	require.Equal(t, "local:req-local-123", billingRepo.LastCmd.RequestPayloadHash)
}

func TestGatewayServiceRecordUsage_PreservesRequestedAndUpstreamModels(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})
	mappedModel := "claude-sonnet-4-20250514"

	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "gateway_models_split",
			Usage:         upstream.TokenUsage{InputTokens: 10, OutputTokens: 6},
			Model:         "claude-sonnet-4",
			UpstreamModel: mappedModel,
			Duration:      time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 501, Quota: 100},
		User:    &identity.User{ID: 601},
		Account: &accountcore.Record{ID: 701},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "claude-sonnet-4", usageRepo.LastLog.Model)
	require.Equal(t, "claude-sonnet-4", usageRepo.LastLog.RequestedModel)
	require.NotNil(t, usageRepo.LastLog.UpstreamModel)
	require.Equal(t, mappedModel, *usageRepo.LastLog.UpstreamModel)
}

func TestGatewayServiceRecordUsage_PreservesGroupMappedUpstreamModel(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "gateway_channel_mapping_models",
			Usage:         upstream.TokenUsage{InputTokens: 10, OutputTokens: 6},
			Model:         "gpt-5.6-terra",
			UpstreamModel: "gpt-5.6-terra",
			Duration:      time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 501, Quota: 100},
		User:    &identity.User{ID: 601},
		Account: &accountcore.Record{ID: 701},
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

func TestGatewayServiceRecordUsage_PreservesLoopedPricingConfigAndAccountUpstreamModel(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "gateway_looped_mapping_models",
			Usage:         upstream.TokenUsage{InputTokens: 10, OutputTokens: 6},
			Model:         "gpt-5.6-terra",
			UpstreamModel: "gpt-5.6-sol",
			Duration:      time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 501, Quota: 100},
		User:    &identity.User{ID: 601},
		Account: &accountcore.Record{ID: 701},
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

func TestGatewayServiceRecordUsage_QoderUsesStandardRequestedModelPricing(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	usage := upstream.TokenUsage{InputTokens: 1200, OutputTokens: 300}
	expectedCost, err := svc.Dependencies.Calculator.CalculateCost("gpt-5.4", pricing.UsageTokens{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_standard_requested_model_pricing",
			Usage:         usage,
			Model:         "gpt-5.4",
			UpstreamModel: "ultimate",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:    502,
			Quota: 100,
			Group: &routing.Group{Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 602},
		Account: &accountcore.Record{ID: 702, Platform: capability.PlatformQoder},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "gpt-5.4", usageRepo.LastLog.Model)
	require.Equal(t, 1200, usageRepo.LastLog.InputTokens)
	require.Equal(t, 300, usageRepo.LastLog.OutputTokens)
	require.InDelta(t, expectedCost.TotalCost, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost, 1e-12)
	require.True(t, usageRepo.LastLog.ActualCost > 0, "Qoder should reuse standard requested-model pricing instead of a Qoder-specific upstream/deferred branch")

	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expectedCost.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
	require.Zero(t, billingRepo.LastCmd.APIKeyQuotaCost)
	require.Zero(t, billingRepo.LastCmd.APIKeyRateLimitCost)
	require.Zero(t, billingRepo.LastCmd.AccountQuotaCost)
}

func TestGatewayServiceRecordUsage_QoderGroupMappedBasisDoesNotUseRequestedStandardPricing(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	usage := upstream.TokenUsage{InputTokens: 1200, OutputTokens: 300}
	groupID := int64(42)
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_group_mapped_standard_requested_model_pricing",
			Usage:         usage,
			Model:         "gpt-5.4",
			UpstreamModel: "ultimate",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 602},
		Account: &accountcore.Record{ID: 702, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "gpt-5.4",
			GroupMappedModel:   "ultimate",
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
}

func TestGatewayServiceRecordUsage_QoderGroupMappedImageBasisUsesGlobalFallback(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	groupID := int64(43)
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_group_mapped_standard_requested_image_pricing",
			Model:         "gpt-image-1",
			UpstreamModel: "ultimate",
			ImageCount:    2,
			ImageSize:     pricing.ImageBillingSize1K,
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      503,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 603},
		Account: &accountcore.Record{ID: 703, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "gpt-image-1",
			GroupMappedModel:   "ultimate",
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 2, usageRepo.LastLog.ImageCount)
	// 与其他平台一样按所选计费模型使用通用图片回退价。
	expected := svc.Dependencies.Calculator.CalculateImageCost("ultimate", pricing.ImageBillingSize1K, 2, 1)
	require.Positive(t, expected.TotalCost)
	require.InDelta(t, expected.TotalCost, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, expected.ActualCost, usageRepo.LastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expected.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

func TestGatewayServiceRecordUsage_QoderGroupMappedImageUsesGlobalFallback(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	groupID := int64(44)
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_group_mapped_custom_alias_image_unpriced",
			Model:         "qmodel",
			UpstreamModel: "qmodel",
			ImageCount:    1,
			ImageSize:     pricing.ImageBillingSize1K,
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      504,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 604},
		Account: &accountcore.Record{ID: 704, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "my-qoder-image",
			GroupMappedModel:   "qmodel",
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 1, usageRepo.LastLog.ImageCount)
	// 与其他平台一样按所选计费模型使用通用图片回退价。
	expected := svc.Dependencies.Calculator.CalculateImageCost("qmodel", pricing.ImageBillingSize1K, 1, 1)
	require.Positive(t, expected.TotalCost)
	require.InDelta(t, expected.TotalCost, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, expected.ActualCost, usageRepo.LastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expected.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

func TestGatewayServiceRecordUsage_QoderRequestedBasisDoesNotFallBackToGroupMappedPricing(t *testing.T) {
	groupID := int64(45)
	inputPrice := 0.01
	outputPrice := 0.02
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qmodel"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		InputPrice:  &inputPrice,
		OutputPrice: &outputPrice,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})
	svc.Dependencies.Prices = billingtestkit.PriceResolver(pricingConfigService, svc.Dependencies.Calculator)

	usage := upstream.TokenUsage{InputTokens: 100, OutputTokens: 200}
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_requested_source_group_mapped_manual_pricing",
			Usage:         usage,
			Model:         "ultimate",
			UpstreamModel: "ultimate",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      505,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 605},
		Account: &accountcore.Record{ID: 705, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "qwen3.7-plus",
			GroupMappedModel:   "qmodel",
			BillingModelSource: routing.BillingModelSourceRequested,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
}

func TestGatewayServiceRecordUsage_QoderRequestedImageUsesGlobalFallback(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	groupID := int64(46)
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_requested_source_custom_image_unpriced",
			Model:         "ultimate",
			UpstreamModel: "ultimate",
			ImageCount:    1,
			ImageSize:     pricing.ImageBillingSize1K,
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      506,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 606},
		Account: &accountcore.Record{ID: 706, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "custom-image-alias",
			GroupMappedModel:   "qmodel",
			BillingModelSource: routing.BillingModelSourceRequested,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 1, usageRepo.LastLog.ImageCount)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.LastLog.BillingMode)
	// 与其他平台一样按所选计费模型使用通用图片回退价。
	expected := svc.Dependencies.Calculator.CalculateImageCost("custom-image-alias", pricing.ImageBillingSize1K, 1, 1)
	require.Positive(t, expected.TotalCost)
	require.InDelta(t, expected.TotalCost, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, expected.ActualCost, usageRepo.LastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expected.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

func TestGatewayServiceRecordUsage_QoderAliasesInheritAvailableBuiltinPrices(t *testing.T) {
	aliases := make([]string, 0, len(qoder.DefaultQoderModelAliases))
	for alias := range qoder.DefaultQoderModelAliases {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)

	for _, alias := range aliases {
		t.Run(alias, func(t *testing.T) {
			usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
			billingRepo := &completiontestkit.SettlementStore{}
			svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

			usage := upstream.TokenUsage{InputTokens: 1200, OutputTokens: 300, CacheCreationInputTokens: 50, CacheReadInputTokens: 25}
			err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
				Result: &forwardcore.MessagesResult{
					RequestID:     "qoder_alias_" + alias,
					Usage:         usage,
					Model:         alias,
					UpstreamModel: lookupQoderAliasKeyForTest(alias),
					Duration:      time.Second,
				},
				APIKey: &apikey.APIKey{
					ID:    502,
					Quota: 100,
					Group: &routing.Group{Platform: capability.PlatformQoder, RateMultiplier: 1},
				},
				User:    &identity.User{ID: 602},
				Account: &accountcore.Record{ID: 702, Platform: capability.PlatformQoder},
			})

			require.NoError(t, err)
			require.Equal(t, 1, usageRepo.Calls)
			require.NotNil(t, usageRepo.LastLog)
			require.Equal(t, alias, usageRepo.LastLog.Model)
			// 有内置价的别名正常扣费；未知路由保持未定价记录。
			if _, pricingErr := svc.Dependencies.Calculator.GetModelPricing(alias); pricingErr == nil {
				require.Positive(t, usageRepo.LastLog.TotalCost)
			} else {
				require.Zero(t, usageRepo.LastLog.TotalCost)
			}
			require.InDelta(t, usageRepo.LastLog.TotalCost*usageRepo.LastLog.RateMultiplier, usageRepo.LastLog.ActualCost, 1e-12)

			require.Equal(t, 1, billingRepo.Calls)
			require.NotNil(t, billingRepo.LastCmd)
			require.Equal(t, usageRepo.LastLog.ActualCost, billingRepo.LastCmd.BillableAmountUSD)
		})
	}
}

func TestGatewayServiceRecordUsage_QoderGroupMappedRouteKeyWithoutManualPricingUsesZeroCost(t *testing.T) {
	groupID := int64(902)
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	usage := upstream.TokenUsage{InputTokens: 1200, OutputTokens: 300, CacheCreationInputTokens: 50, CacheReadInputTokens: 25}
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_group_mapped_route_key",
			Usage:         usage,
			Model:         "qmodel",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 602},
		Account: &accountcore.Record{ID: 702, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "qwen3.7-plus",
			GroupMappedModel:   "qmodel",
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
}

func TestGatewayServiceRecordUsage_QoderGroupMappedBasisDoesNotUseOriginalAliasPricing(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	outputPrice := 0.02
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qwen3.7-plus"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		InputPrice:  &inputPrice,
		OutputPrice: &outputPrice,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})
	svc.Dependencies.Prices = billingtestkit.PriceResolver(pricingConfigService, svc.Dependencies.Calculator)

	usage := upstream.TokenUsage{InputTokens: 1200, OutputTokens: 300}
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_group_mapped_route_key_original_alias_pricing",
			Usage:         usage,
			Model:         "qmodel",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 602},
		Account: &accountcore.Record{ID: 702, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "qwen3.7-plus",
			GroupMappedModel:   "qmodel",
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
}

func TestGatewayServiceRecordUsage_QoderGroupMappedBasisUsesRouteKeyPricing(t *testing.T) {
	groupID := int64(902)
	aliasInputPrice := 0.01
	aliasOutputPrice := 0.02
	routeInputPrice := 0.50
	routeOutputPrice := 0.75
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qmodel"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		InputPrice:  &routeInputPrice,
		OutputPrice: &routeOutputPrice,
	}
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qwen3.7-plus"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		InputPrice:  &aliasInputPrice,
		OutputPrice: &aliasOutputPrice,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})
	svc.Dependencies.Prices = billingtestkit.PriceResolver(pricingConfigService, svc.Dependencies.Calculator)

	usage := upstream.TokenUsage{InputTokens: 1200, OutputTokens: 300}
	expectedCost := float64(usage.InputTokens)*routeInputPrice + float64(usage.OutputTokens)*routeOutputPrice
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_alias_price_over_route_price",
			Usage:         usage,
			Model:         "qmodel",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 602},
		Account: &accountcore.Record{ID: 702, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "qwen3.7-plus",
			GroupMappedModel:   "qmodel",
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.InDelta(t, expectedCost, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, expectedCost, usageRepo.LastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expectedCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

func TestGatewayServiceRecordUsage_QoderImplicitRequestedBasisDoesNotInferRouteKeyPricing(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	outputPrice := 0.02
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qmodel"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		InputPrice:  &inputPrice,
		OutputPrice: &outputPrice,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})
	svc.Dependencies.Prices = billingtestkit.PriceResolver(pricingConfigService, svc.Dependencies.Calculator)

	usage := upstream.TokenUsage{InputTokens: 1200, OutputTokens: 300}
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_default_alias_route_key_pricing",
			Usage:         usage,
			Model:         "qwen3.7-plus",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 602},
		Account: &accountcore.Record{ID: 702, Platform: capability.PlatformQoder},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
}

func TestGatewayServiceRecordUsage_QoderGroupMappedBlankRouteKeyDoesNotUseOriginalAliasPricing(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	outputPrice := 0.02
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qmodel"}] = &routing.ModelPricingEntry{
		Platform:    capability.PlatformQoder,
		Models:      []string{"qmodel"},
		BillingMode: routing.BillingModeToken,
	}
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qwen3.7-plus"}] = &routing.ModelPricingEntry{
		Platform:    capability.PlatformQoder,
		Models:      []string{"qwen3.7-plus"},
		BillingMode: routing.BillingModeToken,
		InputPrice:  &inputPrice,
		OutputPrice: &outputPrice,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})
	svc.Dependencies.Prices = billingtestkit.PriceResolver(pricingConfigService, svc.Dependencies.Calculator)

	usage := upstream.TokenUsage{InputTokens: 1200, OutputTokens: 300}
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_blank_route_key_original_alias_pricing",
			Usage:         usage,
			Model:         "qmodel",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 602},
		Account: &accountcore.Record{ID: 702, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "qwen3.7-plus",
			GroupMappedModel:   "qmodel",
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
}

func TestGatewayServiceRecordUsage_QoderGroupMappedBasisIgnoresRequestedCustomPartialPricing(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "custom-qoder"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		InputPrice:  &inputPrice,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})
	svc.Dependencies.Prices = billingtestkit.PriceResolver(pricingConfigService, svc.Dependencies.Calculator)

	usage := upstream.TokenUsage{InputTokens: 100, OutputTokens: 100000}

	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_group_mapped_custom_alias_partial_pricing",
			Usage:         usage,
			Model:         "qmodel",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 602},
		Account: &accountcore.Record{ID: 702, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "custom-qoder",
			GroupMappedModel:   "qmodel",
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
}

func TestGatewayServiceRecordUsage_QoderGroupMappedBasisIgnoresRequestedStandardPartialPricing(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "gpt-5.4"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		InputPrice:  &inputPrice,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})
	svc.Dependencies.Prices = billingtestkit.PriceResolver(pricingConfigService, svc.Dependencies.Calculator)

	usage := upstream.TokenUsage{InputTokens: 100, OutputTokens: 100000}
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_group_mapped_standard_model_partial_pricing",
			Usage:         usage,
			Model:         "qmodel",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 602},
		Account: &accountcore.Record{ID: 702, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "gpt-5.4",
			GroupMappedModel:   "qmodel",
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
}

func TestGatewayServiceRecordUsage_QoderAccountMappedCustomAliasPartialManualPricingZerosMissingFields(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "custom-qoder"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		InputPrice:  &inputPrice,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})
	svc.Dependencies.Prices = billingtestkit.PriceResolver(pricingConfigService, svc.Dependencies.Calculator)

	usage := upstream.TokenUsage{InputTokens: 100, OutputTokens: 100000}
	expectedCost := float64(usage.InputTokens) * inputPrice

	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_account_mapped_custom_alias_partial_pricing",
			Usage:         usage,
			Model:         "custom-qoder",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 602},
		Account: &accountcore.Record{ID: 702, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "custom-qoder",
			GroupMappedModel:   "custom-qoder",
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.InDelta(t, expectedCost, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, expectedCost, usageRepo.LastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expectedCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

func TestGatewayServiceRecordUsage_QoderCustomMappedRouteKeyWithoutManualPricingUsesZeroCost(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	usage := upstream.TokenUsage{InputTokens: 1200, OutputTokens: 300, CacheCreationInputTokens: 50, CacheReadInputTokens: 25}
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_custom_alias_route_key",
			Usage:         usage,
			Model:         "custom-qoder-model",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:    502,
			Quota: 100,
			Group: &routing.Group{Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 602},
		Account: &accountcore.Record{ID: 702, Platform: capability.PlatformQoder},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "custom-qoder-model", usageRepo.LastLog.Model)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
}

func TestGatewayServiceRecordUsage_QoderAccountMappedImageUsesGlobalFallback(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_custom_image_alias_route_key",
			Usage:         upstream.TokenUsage{InputTokens: 10, OutputTokens: 5},
			Model:         "custom-qoder-image",
			UpstreamModel: "qmodel",
			ImageCount:    2,
			ImageSize:     "1K",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:    502,
			Quota: 100,
			Group: &routing.Group{Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 602},
		Account: &accountcore.Record{ID: 702, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "custom-qoder-image",
			GroupMappedModel:   "custom-qoder-image",
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.LastLog.BillingMode)
	// 与其他平台一样按所选计费模型使用通用图片回退价。
	expected := svc.Dependencies.Calculator.CalculateImageCost("custom-qoder-image", pricing.ImageBillingSize1K, 2, usageRepo.LastLog.RateMultiplier)
	require.Positive(t, expected.TotalCost)
	require.InDelta(t, expected.TotalCost, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, expected.ActualCost, usageRepo.LastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expected.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

func TestGatewayServiceRecordUsage_QoderUpstreamBasisDoesNotUseRequestedStandardPricing(t *testing.T) {
	groupID := int64(902)
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	usage := upstream.TokenUsage{InputTokens: 1200, OutputTokens: 300}
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_upstream_billing_source_route_key",
			Usage:         usage,
			Model:         "gpt-5.4-mini",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 602},
		Account: &accountcore.Record{ID: 702, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "gpt-5.4-mini",
			GroupMappedModel:   "gpt-5.4-mini",
			BillingModelSource: routing.BillingModelSourceUpstream,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
}

func TestGatewayServiceRecordUsage_QoderUpstreamBasisUsesStandardUpstreamPricing(t *testing.T) {
	groupID := int64(902)
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	usage := upstream.TokenUsage{InputTokens: 1200, OutputTokens: 300}
	expectedCost, err := svc.Dependencies.Calculator.CalculateCost("gpt-5.4-mini", pricing.UsageTokens{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
	}, 1)
	require.NoError(t, err)

	err = svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_manual_only_requested_standard_upstream",
			Usage:         usage,
			Model:         "qwen3.7-plus",
			UpstreamModel: "gpt-5.4-mini",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 602},
		Account: &accountcore.Record{ID: 702, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "qwen3.7-plus",
			GroupMappedModel:   "qwen3.7-plus",
			BillingModelSource: routing.BillingModelSourceUpstream,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.InDelta(t, expectedCost.TotalCost, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expectedCost.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

func TestGatewayServiceRecordUsage_QoderGroupMappedAccountStatsUsesOriginalAliasRule(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	outputPrice := 0.02
	cache := routingtestkit.NewModelConfigData()
	cache.ByGroup[groupID] = &routingtestkit.Configuration{
		ID:     groupID,
		Status: billing.StatusActive,
		AccountStatsPricingRules: []routing.AccountStatsPricingRule{
			{
				GroupIDs: []int64{groupID},
				Pricing: []routing.ModelPricingEntry{
					{
						Models:      []string{"qwen3.7-plus"},
						InputPrice:  &inputPrice,
						OutputPrice: &outputPrice,
					},
				},
			},
		},
	}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})
	svc.GroupPolicies = pricingConfigService

	usage := upstream.TokenUsage{InputTokens: 100, OutputTokens: 50}
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_group_mapped_account_stats_alias",
			Usage:         usage,
			Model:         "qmodel",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 602},
		Account: &accountcore.Record{ID: 702, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "qwen3.7-plus",
			GroupMappedModel:   "qmodel",
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.AccountStatsCost)
	require.InDelta(t, 2.0, *usageRepo.LastLog.AccountStatsCost, 1e-12)
}

func TestGatewayServiceRecordUsage_QoderBlankConfigPricingUsesZeroCost(t *testing.T) {
	groupID := int64(902)
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "auto"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})
	svc.Dependencies.Prices = billingtestkit.PriceResolver(pricingConfigService, svc.Dependencies.Calculator)

	usage := upstream.TokenUsage{InputTokens: 1200, OutputTokens: 300, CacheCreationInputTokens: 50, CacheReadInputTokens: 25}
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_alias_blank_channel_pricing",
			Usage:         usage,
			Model:         "auto",
			UpstreamModel: "auto",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 602},
		Account: &accountcore.Record{ID: 702, Platform: capability.PlatformQoder},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
}

func TestGatewayServiceRecordUsage_QoderManualConfigPricingOverridesDefaultAliasPricing(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	outputPrice := 0.02
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "auto"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		InputPrice:  &inputPrice,
		OutputPrice: &outputPrice,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	billingRepo := &completiontestkit.SettlementStore{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})
	svc.Dependencies.Prices = billingtestkit.PriceResolver(pricingConfigService, svc.Dependencies.Calculator)

	usage := upstream.TokenUsage{InputTokens: 1200, OutputTokens: 300}
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:     "qoder_alias_manual_pricing",
			Usage:         usage,
			Model:         "auto",
			UpstreamModel: "auto",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1},
		},
		User:    &identity.User{ID: 602},
		Account: &accountcore.Record{ID: 702, Platform: capability.PlatformQoder},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.InDelta(t, 18.0, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 18.0, usageRepo.LastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, 18.0, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

func lookupQoderAliasKeyForTest(alias string) string {
	if info, ok := qoder.DefaultQoderModelAliases[alias]; ok {
		return info.Key
	}
	return alias
}

func TestForwardResultBillingModelPrefersRequestedModel(t *testing.T) {
	require.Equal(t, "claude-opus-4-6", completion.ForwardResultBillingModel("claude-opus-4-6", "ultimate"))
	require.Equal(t, "ultimate", completion.ForwardResultBillingModel("", "ultimate"))
}

func TestGatewayServiceRecordUsage_EmptyImageSizeDefaultsBeforeBillingAndPersistence(t *testing.T) {
	imagePrice2K := 0.19
	groupID := int64(901)
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:      "gateway_image_default_size",
			Model:          "gemini-image",
			ImageCount:     1,
			ImageInputSize: "auto",
			Duration:       time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      801,
			GroupID: i64p(groupID),
			Group: &routing.Group{
				ID:             groupID,
				RateMultiplier: 1.0,
				ModelPricing:   testImageModelPricing(map[string]*float64{"2K": &imagePrice2K}),
			},
		},
		User:    &identity.User{ID: 601},
		Account: &accountcore.Record{ID: 701},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 1, usageRepo.LastLog.ImageCount)
	require.NotNil(t, usageRepo.LastLog.ImageSize)
	require.Equal(t, pricing.ImageBillingSize2K, *usageRepo.LastLog.ImageSize)
	require.NotNil(t, usageRepo.LastLog.ImageInputSize)
	require.Equal(t, "auto", *usageRepo.LastLog.ImageInputSize)
	require.NotNil(t, usageRepo.LastLog.ImageSizeSource)
	require.Equal(t, pricing.ImageSizeSourceDefault, *usageRepo.LastLog.ImageSizeSource)
	require.InDelta(t, 0.19, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.19, usageRepo.LastLog.ActualCost, 1e-12)
}

func TestGatewayServiceRecordUsage_PeakRateAffectsTokenModeImageOutputTokens(t *testing.T) {
	groupID := int64(902)
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, &completiontestkit.SubscriptionStore{})
	// 固定在峰值窗口内，避免测试在每天 23:59 的右开边界偶发失败。
	svc.Options.Now = func() time.Time {
		return time.Date(2026, 7, 1, 12, 0, 0, 0, time.Local)
	}
	svc.Dependencies.Prices = newOpenAITokenImageConfigPricingResolverForTest(t, groupID, "gemini-image")

	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:  "gateway_peak_image_tokens",
			Model:      "gemini-image",
			ImageCount: 1,
			Usage: upstream.TokenUsage{
				InputTokens:       1000,
				OutputTokens:      600,
				ImageOutputTokens: 100,
			},
			Duration: time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      802,
			GroupID: i64p(groupID),
			Group: &routing.Group{
				ID:                 groupID,
				RateMultiplier:     1.0,
				PeakRateEnabled:    true,
				PeakStart:          "11:59",
				PeakEnd:            "12:01",
				PeakRateMultiplier: 3.0,
			},
		},
		User:    &identity.User{ID: 602},
		Account: &accountcore.Record{ID: 702},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeToken), *usageRepo.LastLog.BillingMode)
	require.Equal(t, 3.0, usageRepo.LastLog.RateMultiplier)

	textInput := 1000 * 3e-6
	textOutput := 500 * 15e-6
	imageOutput := 100 * 15e-6
	expectedActual := (textInput + textOutput + imageOutput) * 3.0

	require.InDelta(t, textInput+textOutput+imageOutput, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, imageOutput, usageRepo.LastLog.ImageOutputCost, 1e-12)
	require.InDelta(t, expectedActual, usageRepo.LastLog.ActualCost, 1e-12)
	billingRepo := requireGatewayRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expectedActual, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

func TestGatewayServiceRecordUsage_UsageLogWriteErrorDoesNotSkipBilling(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: false, Err: usagecore.MarkUsageLogCreateNotPersisted(context.Canceled)}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	quotaSvc := &completiontestkit.KeyQuotaUpdater{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)

	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID: "gateway_not_persisted",
			Usage: upstream.TokenUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:    503,
			Quota: 100,
		},
		User:          &identity.User{ID: 603},
		Account:       &accountcore.Record{ID: 703},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	billingRepo := requireGatewayRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, billingRepo.LastCmd.BillableAmountUSD, billingRepo.LastCmd.APIKeyQuotaCost, 1e-12)
}

func TestGatewayServiceRecordUsageWithLongContext_BillingUsesDetachedContext(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: false, Err: context.DeadlineExceeded}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	quotaSvc := &completiontestkit.KeyQuotaUpdater{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)

	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.RecordMessages(reqCtx, &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID: "gateway_long_context_detached_ctx",
			Usage: upstream.TokenUsage{
				InputTokens:  12,
				OutputTokens: 8,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:    502,
			Quota: 100,
		},
		User:          &identity.User{ID: 602},
		Account:       &accountcore.Record{ID: 702},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	billingRepo := requireGatewayRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NoError(t, billingRepo.LastCtxErr)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, billingRepo.LastCmd.BillableAmountUSD, billingRepo.LastCmd.APIKeyQuotaCost, 1e-12)
}

func TestGatewayServiceRecordUsage_UsesFallbackRequestIDForUsageLog(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)

	ctx := context.WithValue(context.Background(), telemetry.RequestID, "gateway-local-fallback")
	err := svc.RecordMessages(ctx, &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID: "",
			Usage: upstream.TokenUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 504},
		User:    &identity.User{ID: 604},
		Account: &accountcore.Record{ID: 704},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "local:gateway-local-fallback", usageRepo.LastLog.RequestID)
}

func TestGatewayServiceRecordUsage_PrefersClientRequestIDOverUpstreamRequestID(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	ctx := context.WithValue(context.Background(), telemetry.ClientRequestID, "client-stable-123")
	ctx = context.WithValue(ctx, telemetry.RequestID, "req-local-ignored")
	err := svc.RecordMessages(ctx, &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID: "upstream-volatile-456",
			Usage: upstream.TokenUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 506},
		User:    &identity.User{ID: 606},
		Account: &accountcore.Record{ID: 706},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.LastCmd)
	require.Equal(t, "client:client-stable-123", billingRepo.LastCmd.RequestID)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "client:client-stable-123", usageRepo.LastLog.RequestID)
}

func TestGatewayServiceRecordUsage_GeneratesRequestIDWhenAllSourcesMissing(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID: "",
			Usage: upstream.TokenUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 507},
		User:    &identity.User{ID: 607},
		Account: &accountcore.Record{ID: 707},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.LastCmd)
	require.True(t, strings.HasPrefix(billingRepo.LastCmd.RequestID, "generated:"))
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, billingRepo.LastCmd.RequestID, usageRepo.LastLog.RequestID)
}

func TestGatewayServiceRecordUsage_DroppedUsageLogFallsBackToSyncCreate(t *testing.T) {
	// 计费成功后 best-effort 写入被丢弃（队列超时）时必须同步兜底，
	// 否则出现“已扣费但无 usage_log”的对账缺口（issue #3656）。
	usageRepo := &completiontestkit.BestEffortUsageLogStore{
		BestEffortErr: usagecore.MarkUsageLogCreateDropped(errors.New("usage log best-effort queue full")),
	}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID: "gateway_drop_usage_log",
			Usage: upstream.TokenUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 508},
		User:    &identity.User{ID: 608},
		Account: &accountcore.Record{ID: 708},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.BestEffortCalls)
	require.Equal(t, 1, usageRepo.CreateCalls)
	// 兜底调用使用的 ctx 必须仍然存活，不能带着已死的 ctx 走过场。
	require.NoError(t, usageRepo.LastCtxErr)
}

func TestGatewayServiceRecordUsage_BillingErrorWritesUnsettledUsageLog(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{}
	billingErr := errors.New("billing tx failed")
	billingRepo := &completiontestkit.SettlementStore{Err: billingErr}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo)

	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID: "gateway_billing_fail",
			Usage: upstream.TokenUsage{
				InputTokens:  10,
				OutputTokens: 6,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 505},
		User:    &identity.User{ID: 605},
		Account: &accountcore.Record{ID: 705},
	})

	require.ErrorIs(t, err, billingErr)
	require.Equal(t, 1, billingRepo.Calls)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 10, usageRepo.LastLog.InputTokens)
	require.Equal(t, 6, usageRepo.LastLog.OutputTokens)
	require.Greater(t, usageRepo.LastLog.InputCost, 0.0)
	require.Greater(t, usageRepo.LastLog.OutputCost, 0.0)
	require.Greater(t, usageRepo.LastLog.TotalCost, 0.0)
	require.Zero(t, usageRepo.LastLog.ActualCost)
}

func TestGatewayServiceRecordUsage_ReasoningEffortPersisted(t *testing.T) {
	usageRepo := &completiontestkit.BestEffortUsageLogStore{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	effort := "max"
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID: "effort_test",
			Usage: upstream.TokenUsage{
				InputTokens:  10,
				OutputTokens: 5,
			},
			Model:           "claude-opus-4-6",
			Duration:        time.Second,
			ReasoningEffort: &effort,
		},
		APIKey:  &apikey.APIKey{ID: 1},
		User:    &identity.User{ID: 1},
		Account: &accountcore.Record{ID: 1},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.ReasoningEffort)
	require.Equal(t, "max", *usageRepo.LastLog.ReasoningEffort)
}

func TestGatewayServiceRecordUsage_ReasoningEffortNil(t *testing.T) {
	usageRepo := &completiontestkit.BestEffortUsageLogStore{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})

	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID: "no_effort_test",
			Usage: upstream.TokenUsage{
				InputTokens:  10,
				OutputTokens: 5,
			},
			Model:    "claude-sonnet-4",
			Duration: time.Second,
		},
		APIKey:  &apikey.APIKey{ID: 1},
		User:    &identity.User{ID: 1},
		Account: &accountcore.Record{ID: 1},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Nil(t, usageRepo.LastLog.ReasoningEffort)
}

// newGatewayRecordUsageServiceWithResolverForTest 按生产装配方式构造带解析器的
// token 计费服务，确保测试走会处理服务档位的统一计费路径。
func newGatewayRecordUsageServiceWithResolverForTest(usageRepo usagecore.UsageLogRepository) (*completiontestkit.Recording, *apikey.APIKey) {
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})
	svc.Dependencies.Prices = billingtestkit.PriceResolver(nil, svc.Dependencies.Calculator)
	groupID := int64(7)
	return svc, &apikey.APIKey{ID: 1, GroupID: &groupID, Group: &routing.Group{ID: groupID, RateMultiplier: 1.0}}
}

func TestGatewayServiceRecordUsage_FastSpeedDowngradedByUpstreamResponse(t *testing.T) {
	usageRepo := &completiontestkit.BestEffortUsageLogStore{}
	svc, apiKey := newGatewayRecordUsageServiceWithResolverForTest(usageRepo)

	tier := "fast"
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:                   "fast_downgraded_test",
			Usage:                       upstream.TokenUsage{InputTokens: 100, OutputTokens: 50},
			Model:                       "claude-opus-5",
			Duration:                    time.Second,
			ServiceTier:                 &tier,
			UpstreamResponseServiceTier: "standard",
		},
		APIKey:  apiKey,
		User:    &identity.User{ID: 1},
		Account: &accountcore.Record{ID: 1, Platform: capability.PlatformAnthropic},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.ServiceTier)
	require.Equal(t, "standard", *usageRepo.LastLog.ServiceTier)

	tokens := pricing.UsageTokens{InputTokens: 100, OutputTokens: 50}
	standardCost, err := svc.Dependencies.Calculator.CalculateCost("claude-opus-5", tokens, 1.0)
	require.NoError(t, err)
	fastCost, err := svc.Dependencies.Calculator.CalculateCostWithServiceTier("claude-opus-5", tokens, 1.0, "fast")
	require.NoError(t, err)
	require.Greater(t, fastCost.TotalCost, standardCost.TotalCost, "fast mode must carry a premium for the test to be meaningful")
	require.InDelta(t, standardCost.TotalCost, usageRepo.LastLog.TotalCost, 1e-10)
}

func TestGatewayServiceRecordUsage_FastSpeedHonouredKeepsPremium(t *testing.T) {
	usageRepo := &completiontestkit.BestEffortUsageLogStore{}
	svc, apiKey := newGatewayRecordUsageServiceWithResolverForTest(usageRepo)

	tier := "fast"
	err := svc.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
		Result: &forwardcore.MessagesResult{
			RequestID:                   "fast_honoured_test",
			Usage:                       upstream.TokenUsage{InputTokens: 100, OutputTokens: 50},
			Model:                       "claude-opus-5",
			Duration:                    time.Second,
			ServiceTier:                 &tier,
			UpstreamResponseServiceTier: "fast",
		},
		APIKey:  apiKey,
		User:    &identity.User{ID: 1},
		Account: &accountcore.Record{ID: 1, Platform: capability.PlatformAnthropic},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "fast", *usageRepo.LastLog.ServiceTier)

	fastCost, err := svc.Dependencies.Calculator.CalculateCostWithServiceTier("claude-opus-5", pricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1.0, "fast")
	require.NoError(t, err)
	require.InDelta(t, fastCost.TotalCost, usageRepo.LastLog.TotalCost, 1e-10)
}

// 记录测试只装配原生完成器，不再构造旧网关或后台执行图。
func newGatewayRecordUsageServiceForTest(logs usagecore.UsageLogRepository, _ identity.UserRepository, _ billing.UserSubscriptionRepository) *completiontestkit.Recording {
	return completiontestkit.NewRecording(logs, &completiontestkit.SettlementStore{}, nil, true)
}

func newGatewayRecordUsageServiceWithBillingRepoForTest(logs usagecore.UsageLogRepository, funds completion.Store, _ identity.UserRepository, _ billing.UserSubscriptionRepository) *completiontestkit.Recording {
	return completiontestkit.NewRecording(logs, funds, nil, false)
}
