package completion_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	completiontestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/provider"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	usagecore "github.com/TokenFlux/TokenRouter/internal/usage"
)

// Recording fixtures observe financial commands and attempted analysis rows
// through serial spies. They exercise protocol bucket interpretation,
// configured price identities, media units, and captured request ownership
// using fixed catalogues; these assertions do not run persistent settlement
// transactions or upstream exchanges.

// openAILadderCatalogJSON supplies absolute above_272k prices that the parser
// converts into thresholds and multipliers for fixed long-context expectations.
const openAILadderCatalogJSON = `{
	"gpt-5.4": {"provider": "openai", "mode": "chat", "fast_multiplier": 2, "flex_multiplier": 0.5,
		"input_cost_per_token": 2.5e-06, "output_cost_per_token": 1.5e-05,
		"cache_read_input_token_cost": 2.5e-07, "cache_creation_input_token_cost": 2.5e-06,
		"input_cost_per_token_above_272k_tokens": 5e-06,
		"output_cost_per_token_above_272k_tokens": 2.25e-05,
		"cache_read_input_token_cost_above_272k_tokens": 5e-07},
	"gpt-5.5-pro": {"provider": "openai", "mode": "chat",
		"input_cost_per_token": 3e-05, "output_cost_per_token": 1.8e-04,
		"input_cost_per_token_above_272k_tokens": 6e-05,
		"output_cost_per_token_above_272k_tokens": 2.7e-04}
}`

// modelCatalogFixture supplies parsed prices for an unstarted catalogue query service.
type modelCatalogFixture struct {
	// pricingData is transferred to NewServiceFromSnapshot; the fixture stops
	// mutating its map and pointed-to entries once that service owns them.
	pricingData map[string]*pricing.CatalogModelPricing
}

// configuredPrices overrides one group's settings/cards while retaining a prior
// resolver for other lookups. The serial fixtures retain supplied settings
// pointers and card storage until getters clone matching results.
type configuredPrices struct {
	// base handles other groups and unmatched cards; nil supplies default settings
	// through BillingSettings and leaves unmatched configured pricing absent.
	base    *billing.PriceResolver
	groupID int64 // owning fixture group identifier
	// settings and cards retain nested pointers or collection storage supplied
	// by the test. Keep those inputs stable; settings reads clone prices, and
	// MatchPriceCard clones a selected card before returning it to the resolver.
	settings pricing.BillingSettings     // owning shared charge settings
	cards    []routing.ModelPricingEntry // owning model price cards
}

// TestGatewayServiceRecordUsage_BillingUsesDetachedContext records native usage
// after cancelling its source while the log spy fails. It checks a live funds
// context, one financial call, and matching billable/key-quota amounts.
func TestGatewayServiceRecordUsage_BillingUsesDetachedContext(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: false, Err: context.DeadlineExceeded}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	quotaSvc := &completiontestkit.KeyQuotaUpdater{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)

	// Cancel before capture so live funds or analysis context observations distinguish
	// detached completion work from the request context that has already ended.
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
		Provider:      &providercore.Record{ID: 701},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	billingRepo := requireGatewayRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NoError(t, billingRepo.LastCtxErr)
	require.NotNil(t, billingRepo.LastCmd)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, billingRepo.LastCmd.BillableAmountUSD, billingRepo.LastCmd.APIKeyQuotaCost, 1e-12)
}

// TestGatewayServiceRecordUsage_BillingFingerprintIncludesRequestPayloadHash
// supplies a semantic Messages payload digest to synchronous capture. It checks
// that the digest reaches the normalized financial command rather than being
// replaced by correlation evidence.
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
		Provider:           &providercore.Record{ID: 701},
		RequestPayloadHash: payloadHash,
	})
	require.NoError(t, err)
	require.NotNil(t, billingRepo.LastCmd)
	require.Equal(t, payloadHash, billingRepo.LastCmd.RequestPayloadHash)
}

// TestGatewayServiceRecordUsage_BillingFingerprintFallsBackToContextRequestID
// supplies only the internal request-ID context value and no payload digest. It
// checks the client-prefixed fingerprint produced because production
// compatibility and local context keys are aliases.
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
		APIKey:   &apikey.APIKey{ID: 501, Quota: 100},
		User:     &identity.User{ID: 601},
		Provider: &providercore.Record{ID: 701},
	})
	require.NoError(t, err)
	require.NotNil(t, billingRepo.LastCmd)
	require.Equal(t, "client:req-local-123", billingRepo.LastCmd.RequestPayloadHash)
}

// TestGatewayServiceRecordUsage_PreservesRequestedAndUpstreamModels records a
// logical Claude name alongside a dated outbound model. It checks that both
// logical/requested columns retain the logical name and the separate outbound
// column retains its dated value.
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
		APIKey:   &apikey.APIKey{ID: 501, Quota: 100},
		User:     &identity.User{ID: 601},
		Provider: &providercore.Record{ID: 701},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "claude-sonnet-4", usageRepo.LastLog.Model)
	require.Equal(t, "claude-sonnet-4", usageRepo.LastLog.RequestedModel)
	require.NotNil(t, usageRepo.LastLog.UpstreamModel)
	require.Equal(t, mappedModel, *usageRepo.LastLog.UpstreamModel)
}

// TestGatewayServiceRecordUsage_PreservesGroupMappedUpstreamModel captures a
// group mapping from one GPT model to another. It checks the original requested
// name separately from the mapped internal and outbound names in the usage row.
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
		APIKey:   &apikey.APIKey{ID: 501, Quota: 100},
		User:     &identity.User{ID: 601},
		Provider: &providercore.Record{ID: 701},
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

// TestGatewayServiceRecordUsage_PreservesLoopedPricingConfigAndProviderUpstreamModel
// captures a group remap whose provider returns to the original outbound model.
// It checks that the row retains all three observations in their respective
// model columns.
func TestGatewayServiceRecordUsage_PreservesLoopedPricingConfigAndProviderUpstreamModel(t *testing.T) {
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
		APIKey:   &apikey.APIKey{ID: 501, Quota: 100},
		User:     &identity.User{ID: 601},
		Provider: &providercore.Record{ID: 701},
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

// TestGatewayServiceRecordUsage_QoderUsesStandardRequestedModelPricing supplies
// a priced requested GPT identity and Qoder route key without a group ID. It
// checks calculator-matching costs and the funds amount, while captured key
// quota capability is absent.
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
			Group: &routing.Group{RateMultiplier: 1},
		},
		User:     &identity.User{ID: 602},
		Provider: &providercore.Record{ID: 702, Platform: capability.PlatformQoder},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "gpt-5.4", usageRepo.LastLog.Model)
	require.Equal(t, 1200, usageRepo.LastLog.InputTokens)
	require.Equal(t, 300, usageRepo.LastLog.OutputTokens)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, expectedCost.TotalCost, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost, 1e-12)
	require.True(t, usageRepo.LastLog.ActualCost > 0, "Qoder should reuse standard requested-model pricing instead of a Qoder-specific upstream/deferred branch")

	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expectedCost.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
	require.Zero(t, billingRepo.LastCmd.APIKeyQuotaCost)
	require.Zero(t, billingRepo.LastCmd.APIKeyRateLimitCost)
	require.Zero(t, billingRepo.LastCmd.ProviderQuotaCost)
}

// TestGatewayServiceRecordUsage_QoderGroupMappedBasisDoesNotUseRequestedStandardPricing
// selects an unpriced Qoder route key while the requested GPT model is priced.
// It checks one retained usage row and a zero billable financial command for
// the selected identity.
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
			Group:   &routing.Group{ID: groupID, RateMultiplier: 1},
		},
		User:     &identity.User{ID: 602},
		Provider: &providercore.Record{ID: 702, Platform: capability.PlatformQoder},
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

// TestGatewayServiceRecordUsage_QoderGroupMappedImageBasisDoesNotUseGlobalFallback
// selects an unpriced route key for two requested GPT image outputs. It checks
// retained image quantity and zero row/command costs when the selected mapped
// identity has no media price.
func TestGatewayServiceRecordUsage_QoderGroupMappedImageBasisDoesNotUseGlobalFallback(t *testing.T) {
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
			Group:   &routing.Group{ID: groupID, RateMultiplier: 1},
		},
		User:     &identity.User{ID: 603},
		Provider: &providercore.Record{ID: 703, Platform: capability.PlatformQoder},
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
	// The selected media identity is unpriced, so completion retains its observed
	// image count with zero costs instead of substituting another route's price.

	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
}

// TestGatewayServiceRecordUsage_QoderGroupMappedImageDoesNotUseGlobalFallback
// selects a custom mapped image identity with no available media price. It
// checks the observed image count remains in the attempted row while financial
// and analysis charges stay zero.
func TestGatewayServiceRecordUsage_QoderGroupMappedImageDoesNotUseGlobalFallback(t *testing.T) {
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
			Group:   &routing.Group{ID: groupID, RateMultiplier: 1},
		},
		User:     &identity.User{ID: 604},
		Provider: &providercore.Record{ID: 704, Platform: capability.PlatformQoder},
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
	// The selected media identity is unpriced, so completion retains its observed
	// image count with zero costs instead of substituting another route's price.

	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
}

// TestGatewayServiceRecordUsage_QoderRequestedBasisDoesNotFallBackToGroupMappedPricing
// configures a priced mapped key but chooses an unpriced requested Qoder
// identity. It checks that the selected source produces zero costs in the usage
// row and financial command.
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
			Group:   &routing.Group{ID: groupID, RateMultiplier: 1},
		},
		User:     &identity.User{ID: 605},
		Provider: &providercore.Record{ID: 705, Platform: capability.PlatformQoder},
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

// TestGatewayServiceRecordUsage_QoderRequestedImageDoesNotUseGlobalFallback
// chooses an unpriced custom requested image alias with a different mapped
// route. It checks retained image mode/count and zero model charges under that
// selected pricing identity.
func TestGatewayServiceRecordUsage_QoderRequestedImageDoesNotUseGlobalFallback(t *testing.T) {
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
			Group:   &routing.Group{ID: groupID, RateMultiplier: 1},
		},
		User:     &identity.User{ID: 606},
		Provider: &providercore.Record{ID: 706, Platform: capability.PlatformQoder},
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
	// The selected media identity is unpriced, so completion retains its observed
	// image count with zero costs instead of substituting another route's price.

	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
}

// TestGatewayServiceRecordUsage_QoderAliasesInheritAvailableBuiltinPrices
// supplies each listed Qoder request alias with its helper-resolved route key.
// It checks positive base cost when that full alias has fixture pricing, zero
// otherwise, and command/row charge agreement.
func TestGatewayServiceRecordUsage_QoderAliasesInheritAvailableBuiltinPrices(t *testing.T) {
	aliases := map[string]string{"auto": "auto", "ultimate": "ultimate", "cantus": "cmodel", "qwen3.8-max": "qmodel_38max", "qwen4-max": "qmodel_latest", "glm-5.3": "gmodel"}

	for alias, route := range aliases {
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
					UpstreamModel: route,
					Duration:      time.Second,
				},
				APIKey: &apikey.APIKey{
					ID:    502,
					Quota: 100,
					Group: &routing.Group{RateMultiplier: 1},
				},
				User:     &identity.User{ID: 602},
				Provider: &providercore.Record{ID: 702, Platform: capability.PlatformQoder},
			})

			require.NoError(t, err)
			require.Equal(t, 1, usageRepo.Calls)
			require.NotNil(t, usageRepo.LastLog)
			require.Equal(t, alias, usageRepo.LastLog.Model)
			// The requested full alias selects the catalogue row; unavailable aliases still
			// reach the measured-usage write with zero model charges under that selected identity.
			if _, pricingErr := svc.Dependencies.Calculator.GetModelPricing(alias); pricingErr == nil {
				require.Positive(t, usageRepo.LastLog.TotalCost)
			} else {
				require.Zero(t, usageRepo.LastLog.TotalCost)
			}
			// Floating-point comparisons allow rounding from price and multiplier arithmetic.
			require.InDelta(t, usageRepo.LastLog.TotalCost*usageRepo.LastLog.RateMultiplier, usageRepo.LastLog.ActualCost, 1e-12)

			require.Equal(t, 1, billingRepo.Calls)
			require.NotNil(t, billingRepo.LastCmd)
			require.Equal(t, usageRepo.LastLog.ActualCost, billingRepo.LastCmd.BillableAmountUSD)
		})
	}
}

// TestGatewayServiceRecordUsage_QoderGroupMappedRouteKeyWithoutManualPricingUsesZeroCost
// selects an unpriced mapped route key with observed token and cache usage. It
// checks the retained analysis attempt and zero total, actual, and billable
// command amounts.
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
			Group:   &routing.Group{ID: groupID, RateMultiplier: 1},
		},
		User:     &identity.User{ID: 602},
		Provider: &providercore.Record{ID: 702, Platform: capability.PlatformQoder},
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

// TestGatewayServiceRecordUsage_QoderGroupMappedBasisDoesNotUseOriginalAliasPricing
// prices the original Qoder alias but selects an unpriced group-mapped
// identity. It checks the chosen source leaves both row and command costs zero
// despite that other card.
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
			Group:   &routing.Group{ID: groupID, RateMultiplier: 1},
		},
		User:     &identity.User{ID: 602},
		Provider: &providercore.Record{ID: 702, Platform: capability.PlatformQoder},
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

// TestGatewayServiceRecordUsage_QoderGroupMappedBasisUsesRouteKeyPricing
// configures different requested-alias and mapped-route token rates. It checks
// that mapped-source pricing uses the route rates for the row and financial
// command at the selected group factor.
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
			Group:   &routing.Group{ID: groupID, RateMultiplier: 1},
		},
		User:     &identity.User{ID: 602},
		Provider: &providercore.Record{ID: 702, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "qwen3.7-plus",
			GroupMappedModel:   "qmodel",
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, expectedCost, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, expectedCost, usageRepo.LastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expectedCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

// TestGatewayServiceRecordUsage_QoderImplicitRequestedBasisDoesNotInferRouteKeyPricing
// configures a mapped-key card while leaving model source at its default. It
// checks the unpriced requested identity produces zero costs and a zero
// billable command.
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
			Group:   &routing.Group{ID: groupID, RateMultiplier: 1},
		},
		User:     &identity.User{ID: 602},
		Provider: &providercore.Record{ID: 702, Platform: capability.PlatformQoder},
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

// TestGatewayServiceRecordUsage_QoderGroupMappedBlankRouteKeyDoesNotUseOriginalAliasPricing
// gives the selected mapped identity a mode-only card and the requested alias
// priced buckets. It checks that the incomplete selected price retains zero row
// and command charges.
func TestGatewayServiceRecordUsage_QoderGroupMappedBlankRouteKeyDoesNotUseOriginalAliasPricing(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	outputPrice := 0.02
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qmodel"}] = &routing.ModelPricingEntry{
		Models:      []string{"qmodel"},
		BillingMode: routing.BillingModeToken,
	}
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qwen3.7-plus"}] = &routing.ModelPricingEntry{
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
			Group:   &routing.Group{ID: groupID, RateMultiplier: 1},
		},
		User:     &identity.User{ID: 602},
		Provider: &providercore.Record{ID: 702, Platform: capability.PlatformQoder},
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

// TestGatewayServiceRecordUsage_QoderGroupMappedBasisIgnoresRequestedCustomPartialPricing
// places an input-only card on the custom requested alias while selecting an
// unpriced mapped key. It checks that neither that input rate nor the large
// output quantity creates a charge.
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
			Group:   &routing.Group{ID: groupID, RateMultiplier: 1},
		},
		User:     &identity.User{ID: 602},
		Provider: &providercore.Record{ID: 702, Platform: capability.PlatformQoder},
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

// TestGatewayServiceRecordUsage_QoderGroupMappedBasisIgnoresRequestedStandardPartialPricing
// places an input-only card on a priced standard requested model while
// selecting another unpriced key. It checks zero row and command costs for the
// configured mapped source.
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
			Group:   &routing.Group{ID: groupID, RateMultiplier: 1},
		},
		User:     &identity.User{ID: 602},
		Provider: &providercore.Record{ID: 702, Platform: capability.PlatformQoder},
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

// TestGatewayServiceRecordUsage_QoderProviderMappedCustomAliasPartialManualPricingZerosMissingFields
// selects a custom alias with an input-only manual card and a separate outbound
// route. It checks the input-only expected amount in both analysis and command
// despite large unpriced output.
func TestGatewayServiceRecordUsage_QoderProviderMappedCustomAliasPartialManualPricingZerosMissingFields(t *testing.T) {
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
			RequestID:     "qoder_provider_mapped_custom_alias_partial_pricing",
			Usage:         usage,
			Model:         "custom-qoder",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, RateMultiplier: 1},
		},
		User:     &identity.User{ID: 602},
		Provider: &providercore.Record{ID: 702, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "custom-qoder",
			GroupMappedModel:   "custom-qoder",
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, expectedCost, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, expectedCost, usageRepo.LastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expectedCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

// TestGatewayServiceRecordUsage_QoderCustomMappedRouteKeyWithoutManualPricingUsesZeroCost
// records a custom requested Qoder model with a separate unpriced outbound key.
// It checks the logical model remains visible and no row or financial model
// charge is inferred.
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
			Group: &routing.Group{RateMultiplier: 1},
		},
		User:     &identity.User{ID: 602},
		Provider: &providercore.Record{ID: 702, Platform: capability.PlatformQoder},
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

// TestGatewayServiceRecordUsage_QoderProviderMappedImageDoesNotUseGlobalFallback
// records token observations and two outputs under a custom unpriced image
// identity. It checks image mode remains visible with zero row and billable
// command charges.
func TestGatewayServiceRecordUsage_QoderProviderMappedImageDoesNotUseGlobalFallback(t *testing.T) {
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
			Group: &routing.Group{RateMultiplier: 1},
		},
		User:     &identity.User{ID: 602},
		Provider: &providercore.Record{ID: 702, Platform: capability.PlatformQoder},
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
	// The selected media identity is unpriced, so completion retains its observed
	// image count with zero costs instead of substituting another route's price.

	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
}

// TestGatewayServiceRecordUsage_QoderUpstreamBasisDoesNotUseRequestedStandardPricing
// selects an unpriced outbound Qoder route while the requested GPT identity has
// prices. It checks retained analysis and zero financial costs under the
// upstream-source rule.
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
			Group:   &routing.Group{ID: groupID, RateMultiplier: 1},
		},
		User:     &identity.User{ID: 602},
		Provider: &providercore.Record{ID: 702, Platform: capability.PlatformQoder},
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

// TestGatewayServiceRecordUsage_QoderUpstreamBasisUsesStandardUpstreamPricing
// selects a priced outbound GPT identity for a different requested Qoder alias.
// It checks calculator-matching base, actual, and command amounts for that
// selected upstream price.
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
			Group:   &routing.Group{ID: groupID, RateMultiplier: 1},
		},
		User:     &identity.User{ID: 602},
		Provider: &providercore.Record{ID: 702, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "qwen3.7-plus",
			GroupMappedModel:   "qwen3.7-plus",
			BillingModelSource: routing.BillingModelSourceUpstream,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, expectedCost.TotalCost, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expectedCost.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

// TestGatewayServiceRecordUsage_QoderGroupMappedProviderStatsUsesOriginalAliasRule
// installs a requested-alias provider-statistics rule while user pricing
// selects a mapped route. It checks the independent provider base amount
// supplied to the attempted usage row.
func TestGatewayServiceRecordUsage_QoderGroupMappedProviderStatsUsesOriginalAliasRule(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	outputPrice := 0.02
	cache := routingtestkit.NewModelConfigData()
	cache.ByGroup[groupID] = &routingtestkit.Configuration{
		ID:     groupID,
		Status: billing.StatusActive,
		ProviderStatsPricingRules: []routing.ProviderStatsPricingRule{
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
			RequestID:     "qoder_group_mapped_provider_stats_alias",
			Usage:         usage,
			Model:         "qmodel",
			UpstreamModel: "qmodel",
			Duration:      time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      502,
			Quota:   100,
			GroupID: &groupID,
			Group:   &routing.Group{ID: groupID, RateMultiplier: 1},
		},
		User:     &identity.User{ID: 602},
		Provider: &providercore.Record{ID: 702, Platform: capability.PlatformQoder},
		PricingUsageFields: routing.PricingUsageFields{
			OriginalModel:      "qwen3.7-plus",
			GroupMappedModel:   "qmodel",
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.ProviderStatsCost)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, 2.0, *usageRepo.LastLog.ProviderStatsCost, 1e-12)
}

// TestGatewayServiceRecordUsage_QoderBlankConfigPricingUsesZeroCost supplies a
// selected Qoder identity with a token-mode card containing no price buckets.
// It checks observed usage is retained with zero row and billable command
// amounts.
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
			Group:   &routing.Group{ID: groupID, RateMultiplier: 1},
		},
		User:     &identity.User{ID: 602},
		Provider: &providercore.Record{ID: 702, Platform: capability.PlatformQoder},
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

// TestGatewayServiceRecordUsage_QoderManualConfigPricingOverridesDefaultAliasPricing
// installs explicit input/output prices for the selected auto alias. It checks
// the resulting fixed dollar total and user amount in both the analysis row and
// financial command.
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
			Group:   &routing.Group{ID: groupID, RateMultiplier: 1},
		},
		User:     &identity.User{ID: 602},
		Provider: &providercore.Record{ID: 702, Platform: capability.PlatformQoder},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, 18.0, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 18.0, usageRepo.LastLog.ActualCost, 1e-12)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, 18.0, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

// TestGatewayServiceRecordUsage_EmptyImageSizeDefaultsBeforeBillingAndPersistence
// supplies an auto input-size hint and a shared 2K image card. It checks
// normalized default-size metadata, retained input evidence, and the expected
// single-output base and actual costs.
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
			Group: configureBillingGroup(svc, &routing.Group{
				ID:             groupID,
				RateMultiplier: 1.0,
			}, pricing.BillingSettings{
				LongContextPricingEnabled:    true,
				PeakRateMultiplier:           1,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			}, testImageModelPricing(map[string]*float64{"2K": &imagePrice2K})),
		},
		User:     &identity.User{ID: 601},
		Provider: &providercore.Record{ID: 701},
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
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, 0.19, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.19, usageRepo.LastLog.ActualCost, 1e-12)
}

// TestGatewayServiceRecordUsage_PeakRateAffectsTokenModeImageOutputTokens fixes
// native recording inside a peak window with a token-priced image output
// subset. It checks token mode, one image-output cost contribution, and
// peak-scaled user/command amounts.
func TestGatewayServiceRecordUsage_PeakRateAffectsTokenModeImageOutputTokens(t *testing.T) {
	groupID := int64(902)
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, &completiontestkit.SubscriptionStore{})
	// Fix recording at noon inside the fixture's 11:59-12:01 window so token-mode
	// peak selection can be compared with the ordinary group-price base.
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
			Group: configureBillingGroup(svc, &routing.Group{
				ID:             groupID,
				RateMultiplier: 1.0,
			}, pricing.BillingSettings{
				PeakRateEnabled:              true,
				PeakStart:                    "11:59",
				PeakEnd:                      "12:01",
				PeakRateMultiplier:           3.0,
				LongContextPricingEnabled:    true,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			}, nil),
		},
		User:     &identity.User{ID: 602},
		Provider: &providercore.Record{ID: 702},
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

	// Floating-point comparisons allow rounding from price and multiplier arithmetic.

	require.InDelta(t, textInput+textOutput+imageOutput, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, imageOutput, usageRepo.LastLog.ImageOutputCost, 1e-12)
	require.InDelta(t, expectedActual, usageRepo.LastLog.ActualCost, 1e-12)
	billingRepo := requireGatewayRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expectedActual, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

// TestGatewayServiceRecordUsage_UsageLogWriteErrorDoesNotSkipBilling makes the
// analysis spy return a marked not-persisted error after funds application. It
// checks recording returns no funds error and the single command still contains
// the captured key quota cost.
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
		Provider:      &providercore.Record{ID: 703},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	billingRepo := requireGatewayRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, billingRepo.LastCmd.BillableAmountUSD, billingRepo.LastCmd.APIKeyQuotaCost, 1e-12)
}

// TestGatewayServiceRecordUsageWithLongContext_BillingUsesDetachedContext uses
// a cancelled source and a small native token fixture despite its retained
// name. It checks the live detached funds context and command quota amount; no
// long-context threshold is exercised.
func TestGatewayServiceRecordUsageWithLongContext_BillingUsesDetachedContext(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: false, Err: context.DeadlineExceeded}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	quotaSvc := &completiontestkit.KeyQuotaUpdater{}
	svc := newGatewayRecordUsageServiceForTest(usageRepo, userRepo, subRepo)

	// Cancel before capture so live funds or analysis context observations distinguish
	// detached completion work from the request context that has already ended.
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
		Provider:      &providercore.Record{ID: 702},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	billingRepo := requireGatewayRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NoError(t, billingRepo.LastCtxErr)
	require.NotNil(t, billingRepo.LastCmd)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, billingRepo.LastCmd.BillableAmountUSD, billingRepo.LastCmd.APIKeyQuotaCost, 1e-12)
}

// TestGatewayServiceRecordUsage_UsesFallbackRequestIDForUsageLog provides an
// internal context ID and an empty result operation ID. It checks that capture
// and analysis retain that internal diagnostic request identity in the usage
// row.
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
		APIKey:   &apikey.APIKey{ID: 504},
		User:     &identity.User{ID: 604},
		Provider: &providercore.Record{ID: 704},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "gateway-local-fallback", usageRepo.LastLog.RequestID)
}

// TestGatewayServiceRecordUsage_PrefersClientRequestIDOverUpstreamRequestID
// writes a compatibility ID then an internal ID to their shared context key. It
// checks the later value drives the prefixed financial key and the separate
// unprefixed diagnostic column.
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
		APIKey:   &apikey.APIKey{ID: 506},
		User:     &identity.User{ID: 606},
		Provider: &providercore.Record{ID: 706},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.LastCmd)
	require.Equal(t, "client:req-local-ignored", billingRepo.LastCmd.RequestID)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "req-local-ignored", usageRepo.LastLog.RequestID)
}

// TestGatewayServiceRecordUsage_GeneratesRequestIDWhenAllSourcesMissing
// provides no context or result operation identifier during native capture. It
// checks a generated financial prefix, matching usage BillingKey, and a
// thirty-six-character diagnostic identity.
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
		APIKey:   &apikey.APIKey{ID: 507},
		User:     &identity.User{ID: 607},
		Provider: &providercore.Record{ID: 707},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.LastCmd)
	require.True(t, strings.HasPrefix(billingRepo.LastCmd.RequestID, "generated:"))
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, billingRepo.LastCmd.RequestID, usageRepo.LastLog.BillingKey)
	require.Len(t, usageRepo.LastLog.RequestID, 36)
}

// TestGatewayServiceRecordUsage_DroppedUsageLogFallsBackToSyncCreate returns a
// marked best-effort queue rejection after an applied funds fixture result. It
// checks one queued attempt, one synchronous retry, and a live retry context
// rather than renewed-budget timing.
func TestGatewayServiceRecordUsage_DroppedUsageLogFallsBackToSyncCreate(t *testing.T) {
	// A marked queue rejection requests a synchronous analysis retry after the funds
	// outcome; this fixture returns its error while the context budget remains live.
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
		APIKey:   &apikey.APIKey{ID: 508},
		User:     &identity.User{ID: 608},
		Provider: &providercore.Record{ID: 708},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.BestEffortCalls)
	require.Equal(t, 1, usageRepo.CreateCalls)
	// The retry sees a usable context after immediate queue rejection; this fixture
	// leaves the source uncancelled and does not wait for the earlier budget to expire.
	require.NoError(t, usageRepo.LastCtxErr)
}

// TestGatewayServiceRecordUsage_BillingErrorWritesUnsettledUsageLog makes the
// financial store reject measured native input and output. It checks the
// returned funds error, one attempted row, retained counts/base costs, and
// ActualCost zero.
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
		APIKey:   &apikey.APIKey{ID: 505},
		User:     &identity.User{ID: 605},
		Provider: &providercore.Record{ID: 705},
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

// TestGatewayServiceRecordUsage_ReasoningEffortPersisted records a native
// result carrying final max effort through the best-effort analysis spy. It
// checks that the supplied final effort is retained in the attempted usage row.
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
		APIKey:   &apikey.APIKey{ID: 1},
		User:     &identity.User{ID: 1},
		Provider: &providercore.Record{ID: 1},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.ReasoningEffort)
	require.Equal(t, "max", *usageRepo.LastLog.ReasoningEffort)
}

// TestGatewayServiceRecordUsage_ReasoningEffortNil records native usage without
// a final effort observation. It checks the attempted usage row keeps a nil
// effort pointer, retaining absence instead of inventing an executed policy.
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
		APIKey:   &apikey.APIKey{ID: 1},
		User:     &identity.User{ID: 1},
		Provider: &providercore.Record{ID: 1},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Nil(t, usageRepo.LastLog.ReasoningEffort)
}

// TestGatewayServiceRecordUsage_FastSpeedDowngradedByUpstreamResponse supplies
// outbound fast and observed standard tiers with distinct fixture prices. It
// checks the recorded tier becomes standard and the base amount matches
// standard calculator pricing.
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
		APIKey:   apiKey,
		User:     &identity.User{ID: 1},
		Provider: &providercore.Record{ID: 1, Platform: capability.PlatformAnthropic},
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
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, standardCost.TotalCost, usageRepo.LastLog.TotalCost, 1e-10)
}

// TestGatewayServiceRecordUsage_FastSpeedHonouredKeepsPremium supplies matching
// outbound and observed fast tiers through a production-style resolver. It
// checks the retained fast label and base cost matching the premium calculator
// result.
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
		APIKey:   apiKey,
		User:     &identity.User{ID: 1},
		Provider: &providercore.Record{ID: 1, Platform: capability.PlatformAnthropic},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "fast", *usageRepo.LastLog.ServiceTier)

	fastCost, err := svc.Dependencies.Calculator.CalculateCostWithServiceTier("claude-opus-5", pricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1.0, "fast")
	require.NoError(t, err)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, fastCost.TotalCost, usageRepo.LastLog.TotalCost, 1e-10)
}

// TestOpenAIGatewayServiceRecordUsage_RejectsNilInput invokes capture with nil
// input and then an empty capture object. It checks recorder errors for absent
// input or result before pricing, financial submission, or log dependencies are
// needed.
func TestOpenAIGatewayServiceRecordUsage_RejectsNilInput(t *testing.T) {
	svc := completion.NewRecorder(completion.Dependencies{}, completion.RecorderOptions{DefaultMultiplier: 1})

	require.Error(t, svc.Record(context.Background(), gatewaycapture.CaptureOpenAI(context.Background(), nil), true))
	require.Error(t, svc.Record(context.Background(), gatewaycapture.CaptureOpenAI(context.Background(), &gatewaycapture.OpenAICapture{}), true))
}

// TestOpenAIGatewayServiceRecordUsage_ZeroUsageStillWritesUsageLog supplies a
// priced model with empty measured usage and an applied store response. It
// checks one analysis/financial invocation, distinct billing/diagnostic IDs,
// and zero token, cost, and command increments.
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
			Model:     "gpt-5.4",
			Duration:  time.Second,
		},
		APIKey:        &apikey.APIKey{ID: 1000, Quota: 100, Group: &routing.Group{RateMultiplier: 1}},
		User:          &identity.User{ID: 2000},
		Provider:      &providercore.Record{ID: 3000, Type: capability.ProviderTypeAPIKey},
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
	require.Equal(t, "resp_zero_usage", usageRepo.LastLog.BillingKey)
	require.Len(t, usageRepo.LastLog.RequestID, 36)
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
	require.Zero(t, billingRepo.LastCmd.ProviderQuotaCost)
}

// TestOpenAIGatewayServiceRecordUsage_MissingPricingRecordsZeroCostUsageLog
// supplies positive usage for an unknown selected model. It checks retained
// counts and model names with zero cost, token-mode metadata, and zero
// financial quota amounts.
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
		Provider:      &providercore.Record{ID: 3002, Type: capability.ProviderTypeAPIKey},
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
	require.Equal(t, "resp_missing_pricing", usageRepo.LastLog.BillingKey)
	require.Len(t, usageRepo.LastLog.RequestID, 36)
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
	require.Zero(t, billingRepo.LastCmd.ProviderQuotaCost)
}

// TestOpenAIGatewayServiceRecordUsage_UsesUserSpecificGroupRate supplies
// differing group and user balance factors with inclusive cached input. It
// checks one rate lookup, selected user factor, disjoint ordinary/cache counts,
// and matching row/command charges.
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
			Model:     "gpt-5.4",
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
		User:     &identity.User{ID: 2001},
		Provider: &providercore.Record{ID: 3001},
	})

	require.NoError(t, err)
	require.Equal(t, 1, rateRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, userRate, usageRepo.LastLog.RateMultiplier)
	require.Equal(t, 12, usageRepo.LastLog.InputTokens)
	require.Equal(t, 3, usageRepo.LastLog.CacheReadTokens)

	expected := expectedOpenAICost(t, svc, "gpt-5.4", usage, userRate)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, expected.ActualCost, usageRepo.LastLog.ActualCost, 1e-12)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expected.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

// TestOpenAIGatewayServiceRecordUsage_PeakRateAffectsTokenModeImageOutputTokens
// records a token-priced image subset at an injected peak instant. It checks
// the retained image-token cost, peak factor, and peak-scaled user and
// financial command amounts.
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
	// Fix recording at noon inside the fixture's 11:59-12:01 window so token-mode
	// peak selection can be compared with the ordinary group-price base.
	svc.Options.Now = func() time.Time {
		return time.Date(2026, 7, 1, 12, 0, 0, 0, time.Local)
	}
	svc.Dependencies.Prices = newOpenAITokenImageConfigPricingResolverForTest(t, groupID, "gpt-5.4")

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID:  "resp_peak_image_tokens",
			Usage:      usage,
			Model:      "gpt-5.4",
			Duration:   time.Second,
			ImageCount: 1,
		},
		APIKey: &apikey.APIKey{
			ID:      1004,
			GroupID: i64p(groupID),
			Group: configureBillingGroup(svc, &routing.Group{
				ID:             groupID,
				RateMultiplier: groupRate,
			}, pricing.BillingSettings{
				PeakRateEnabled:              true,
				PeakStart:                    "11:59",
				PeakEnd:                      "12:01",
				PeakRateMultiplier:           3.0,
				LongContextPricingEnabled:    true,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			}, nil),
		},
		User:     &identity.User{ID: 2004},
		Provider: &providercore.Record{ID: 3004},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 3.0, usageRepo.LastLog.RateMultiplier)
	require.Equal(t, usage.ImageOutputTokens, usageRepo.LastLog.ImageOutputTokens)

	expected, err := svc.Dependencies.Calculator.CalculateCostUnified(billing.CostInput{
		Ctx:     context.Background(),
		Model:   "gpt-5.4",
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

	// Floating-point comparisons allow rounding from price and multiplier arithmetic.

	require.InDelta(t, expected.TotalCost, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, expected.ImageOutputCost, usageRepo.LastLog.ImageOutputCost, 1e-12)
	require.InDelta(t, expectedActual, usageRepo.LastLog.ActualCost, 1e-12)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.InDelta(t, expectedActual, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

// TestOpenAIGatewayServiceRecordUsage_IncludesEndpointMetadata captures padded
// client and upstream endpoint strings with measured token usage. It checks the
// attempted analysis row stores their separately trimmed operation paths after
// recording.
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
			Model:    "gpt-5.4",
			Duration: time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:    1002,
			Group: &routing.Group{RateMultiplier: 1},
		},
		User:             &identity.User{ID: 2002},
		Provider:         &providercore.Record{ID: 3002},
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

// TestOpenAIGatewayServiceRecordUsage_FallsBackToGroupDefaultRateOnResolverError
// makes user-rate lookup return an error for a selected group. It checks one
// lookup, the group factor retained in analysis, and a financial amount
// matching that factor.
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
			Model:     "gpt-5.4",
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
		User:     &identity.User{ID: 2002},
		Provider: &providercore.Record{ID: 3002},
	})

	require.NoError(t, err)
	require.Equal(t, 1, rateRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, groupRate, usageRepo.LastLog.RateMultiplier)

	expected := expectedOpenAICost(t, svc, "gpt-5.4", usage, groupRate)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, expected.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

// TestOpenAIGatewayServiceRecordUsage_FallsBackToGroupDefaultRateWhenResolverMissing
// removes the recorder rate binding while supplying a selected group factor. It
// checks the attempted usage row retains that base factor after recording
// succeeds.
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
			Model:     "gpt-5.4",
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
		User:     &identity.User{ID: 2003},
		Provider: &providercore.Record{ID: 3003},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, groupRate, usageRepo.LastLog.RateMultiplier)
}

// TestOpenAIGatewayServiceRecordUsage_DuplicateUsageLogSkipsBilling supplies a
// noninserted analysis response and an unapplied financial response. It checks
// each delegate is called once; the legacy user and subscription arguments
// remain unbound to this fixture.
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
			Model:    "gpt-5.4",
			Duration: time.Second,
		},
		APIKey:   &apikey.APIKey{ID: 1004},
		User:     &identity.User{ID: 2004},
		Provider: &providercore.Record{ID: 3004},
	})

	require.NoError(t, err)
	require.Equal(t, 1, billingRepo.Calls)
	require.Equal(t, 1, usageRepo.Calls)
	require.Equal(t, 0, userRepo.DeductCalls)
	require.Equal(t, 0, subRepo.IncrementCalls)
}

// TestOpenAIGatewayServiceRecordUsage_DuplicateBillingKeySkipsBillingWithRepo
// supplies an unapplied financial result with captured key quota capability. It
// checks one financial and analysis call while the legacy direct-deduction and
// quota-update spies receive no calls.
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
			Model:    "gpt-5.4",
			Duration: time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:    10045,
			Quota: 100,
		},
		User:          &identity.User{ID: 20045},
		Provider:      &providercore.Record{ID: 30045},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, billingRepo.Calls)
	require.Equal(t, 1, usageRepo.Calls)
	require.Equal(t, 0, userRepo.DeductCalls)
	require.Equal(t, 0, subRepo.IncrementCalls)
	require.Equal(t, 0, quotaSvc.QuotaCalls)
}

// TestOpenAIGatewayServiceRecordUsage_BillsWhenUsageLogCreateReturnsError makes
// analysis creation report uncertain batch state after recording priced usage.
// It checks a single financial command still carries the calculated user charge
// and recording returns no funds error.
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
			Model:     "gpt-5.4",
			Duration:  time.Second,
		},
		APIKey:   &apikey.APIKey{ID: 10041},
		User:     &identity.User{ID: 20041},
		Provider: &providercore.Record{ID: 30041},
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, usageRepo.LastLog.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

// TestOpenAIGatewayServiceRecordUsage_UsageLogWriteErrorDoesNotSkipBilling
// makes analysis creation report marked nonpersistence with key quota
// capability captured. It checks the single financial command and equality
// between its billable and total key-quota amounts.
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
			Model:    "gpt-5.4",
			Duration: time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:    10043,
			Quota: 100,
		},
		User:          &identity.User{ID: 20043},
		Provider:      &providercore.Record{ID: 30043},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	require.Equal(t, 1, usageRepo.Calls)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, billingRepo.LastCmd.BillableAmountUSD, billingRepo.LastCmd.APIKeyQuotaCost, 1e-12)
}

// TestOpenAIGatewayServiceRecordUsage_BillingUsesDetachedContext records
// positive inclusive usage after cancelling its source while the log spy fails.
// It checks a live financial context and retained billable/key-quota amounts on
// the single command.
func TestOpenAIGatewayServiceRecordUsage_BillingUsesDetachedContext(t *testing.T) {
	usage := openai.ForwardUsage{InputTokens: 10, OutputTokens: 6, CacheReadInputTokens: 2}
	usageRepo := &completiontestkit.UsageLogStore{Inserted: false, Err: context.DeadlineExceeded}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	quotaSvc := &completiontestkit.KeyQuotaUpdater{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)

	// Cancel before capture so live funds or analysis context observations distinguish
	// detached completion work from the request context that has already ended.
	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.RecordOpenAI(reqCtx, &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_detached_billing_ctx",
			Usage:     usage,
			Model:     "gpt-5.4",
			Duration:  time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:    10042,
			Quota: 100,
		},
		User:          &identity.User{ID: 20042},
		Provider:      &providercore.Record{ID: 30042},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NoError(t, billingRepo.LastCtxErr)
	require.NotNil(t, billingRepo.LastCmd)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, billingRepo.LastCmd.BillableAmountUSD, billingRepo.LastCmd.APIKeyQuotaCost, 1e-12)
}

// TestOpenAIGatewayServiceRecordUsage_BillingRepoUsesDetachedContext records
// measured work from a cancelled source with successful analysis and funds
// spies. It checks both delegates receive live contexts and each is invoked
// once.
func TestOpenAIGatewayServiceRecordUsage_BillingRepoUsesDetachedContext(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	// Cancel before capture so live funds or analysis context observations distinguish
	// detached completion work from the request context that has already ended.
	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err := svc.RecordOpenAI(reqCtx, &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_detached_billing_repo_ctx",
			Usage: openai.ForwardUsage{
				InputTokens:  8,
				OutputTokens: 4,
			},
			Model:    "gpt-5.4",
			Duration: time.Second,
		},
		APIKey:   &apikey.APIKey{ID: 10046},
		User:     &identity.User{ID: 20046},
		Provider: &providercore.Record{ID: 30046},
	})

	require.NoError(t, err)
	require.Equal(t, 1, billingRepo.Calls)
	require.NoError(t, billingRepo.LastCtxErr)
	require.Equal(t, 1, usageRepo.Calls)
	require.NoError(t, usageRepo.LastCtxErr)
}

// TestOpenAIGatewayServiceRecordUsage_BillingFingerprintIncludesRequestPayloadHash
// captures a semantic OpenAI request digest beside result metadata. It checks
// the supplied digest is retained in the normalized financial command as
// payload fingerprint evidence.
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
		Provider:           &providercore.Record{ID: 701},
		RequestPayloadHash: payloadHash,
	})
	require.NoError(t, err)
	require.NotNil(t, billingRepo.LastCmd)
	require.Equal(t, payloadHash, billingRepo.LastCmd.RequestPayloadHash)
}

// TestOpenAIGatewayServiceRecordUsage_UsesFallbackRequestIDForBillingAndUsageLog
// supplies only an internal context identifier and no result operation ID. It
// checks the compatibility-prefixed financial key and the unprefixed diagnostic
// ID retained in analysis.
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
			Model:    "gpt-5.4",
			Duration: time.Second,
		},
		APIKey:   &apikey.APIKey{ID: 10047},
		User:     &identity.User{ID: 20047},
		Provider: &providercore.Record{ID: 30047},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.LastCmd)
	require.Equal(t, "client:req-local-fallback", billingRepo.LastCmd.RequestID)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "req-local-fallback", usageRepo.LastLog.RequestID)
}

// TestOpenAIGatewayServiceRecordUsage_PrefersClientRequestIDOverUpstreamRequestID
// supplies the compatibility request-ID key beside a different result operation
// ID. It checks its value selects the prefixed financial key and remains the
// analysis diagnostic identity.
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
			Model:    "gpt-5.4",
			Duration: time.Second,
		},
		APIKey:   &apikey.APIKey{ID: 10049},
		User:     &identity.User{ID: 20049},
		Provider: &providercore.Record{ID: 30049},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.LastCmd)
	require.Equal(t, "client:openai-client-stable-123", billingRepo.LastCmd.RequestID)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "openai-client-stable-123", usageRepo.LastLog.RequestID)
}

// TestOpenAIGatewayServiceRecordUsage_WSModeSeparatesTurnAndBillingIDs captures
// a WebSocket result with a turn operation ID and compatibility context ID. It
// checks the result ID supplies financial RequestID/BillingKey while analysis
// RequestID retains the context value.
func TestOpenAIGatewayServiceRecordUsage_WSModeSeparatesTurnAndBillingIDs(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{}
	billingRepo := &completiontestkit.SettlementStore{Result: &billing.UsageBillingApplyResult{Applied: true}}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)

	ctx := context.WithValue(context.Background(), telemetry.ClientRequestID, "openai-ws-turn-123")
	err := svc.RecordOpenAI(ctx, &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID:    "resp_openai_ws_turn_456",
			OpenAIWSMode: true,
			Usage: openai.ForwardUsage{
				InputTokens:  8,
				OutputTokens: 4,
			},
			Model:    "gpt-5.4",
			Duration: time.Second,
		},
		APIKey:   &apikey.APIKey{ID: 10050},
		User:     &identity.User{ID: 20050},
		Provider: &providercore.Record{ID: 30050},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.LastCmd)
	require.Equal(t, "resp_openai_ws_turn_456", billingRepo.LastCmd.RequestID)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "resp_openai_ws_turn_456", usageRepo.LastLog.BillingKey)
	require.Equal(t, "openai-ws-turn-123", usageRepo.LastLog.RequestID)
}

// TestOpenAIGatewayServiceRecordUsage_GeneratesRequestIDWhenAllSourcesMissing
// captures no request context identifier or result operation identity. It
// checks the generated financial prefix, analysis BillingKey agreement, and the
// separately derived diagnostic ID length.
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
			Model:    "gpt-5.4",
			Duration: time.Second,
		},
		APIKey:   &apikey.APIKey{ID: 10050},
		User:     &identity.User{ID: 20050},
		Provider: &providercore.Record{ID: 30050},
	})

	require.NoError(t, err)
	require.NotNil(t, billingRepo.LastCmd)
	require.True(t, strings.HasPrefix(billingRepo.LastCmd.RequestID, "generated:"))
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, billingRepo.LastCmd.RequestID, usageRepo.LastLog.BillingKey)
	require.Len(t, usageRepo.LastLog.RequestID, 36)
}

// TestOpenAIGatewayServiceRecordUsage_BillingErrorWritesUnsettledUsageLog
// rejects the funds command for positive OpenAI usage. It checks the returned
// store error, one financial and analysis attempt, retained token/base-cost
// evidence, and zero ActualCost.
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
			Model:    "gpt-5.4",
			Duration: time.Second,
		},
		APIKey:   &apikey.APIKey{ID: 10048},
		User:     &identity.User{ID: 20048},
		Provider: &providercore.Record{ID: 30048},
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

// TestOpenAIGatewayServiceRecordUsage_UpdatesAPIKeyQuotaWhenConfigured supplies
// positive key quota and an updater capability during capture. It checks the
// command includes calculated total-quota cost while its unconfigured
// rolling-rate cost remains zero.
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
			Model:     "gpt-5.4",
			Duration:  time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:    1005,
			Quota: 100,
		},
		User:          &identity.User{ID: 2005},
		Provider:      &providercore.Record{ID: 3005},
		APIKeyService: quotaSvc,
	})

	require.NoError(t, err)
	expected := expectedOpenAICost(t, svc, "gpt-5.4", usage, 1.1)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, expected.ActualCost, billingRepo.LastCmd.APIKeyQuotaCost, 1e-12)
	require.InDelta(t, 0.0, billingRepo.LastCmd.APIKeyRateLimitCost, 1e-12)
}

// TestOpenAIGatewayServiceRecordUsage_ClampsActualInputTokensToZero supplies
// cache-read count larger than inclusive input for a priced model. It checks
// ordinary input in the attempted row is floored at zero after cache
// subtraction.
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
			Model:    "gpt-5.4",
			Duration: time.Second,
		},
		APIKey:   &apikey.APIKey{ID: 1006},
		User:     &identity.User{ID: 2006},
		Provider: &providercore.Record{ID: 3006},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 0, usageRepo.LastLog.InputTokens)
}

// TestOpenAIGatewayServiceRecordUsage_GPT56SeparatesCacheWriteForBillingAndStats
// supplies inclusive input with both cache-write and cache-read buckets using
// fixed GPT rates. It checks disjoint row counts, total token accounting,
// individual bucket costs, and the scaled user total.
func TestOpenAIGatewayServiceRecordUsage_GPT56SeparatesCacheWriteForBillingAndStats(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	svc.Dependencies.Calculator = billingtestkit.Calculator(newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*pricing.CatalogModelPricing{
		"gpt-5.6-sol": {
			InputCostPerToken:           5e-6,
			CacheCreationInputTokenCost: 6.25e-6,
			OutputCostPerToken:          30e-6,
			CacheReadInputTokenCost:     0.5e-6,
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
		APIKey:   &apikey.APIKey{ID: 1056},
		User:     &identity.User{ID: 2056},
		Provider: &providercore.Record{ID: 3056},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 700, usageRepo.LastLog.InputTokens)
	require.Equal(t, 200, usageRepo.LastLog.CacheCreationTokens)
	require.Equal(t, 100, usageRepo.LastLog.CacheReadTokens)
	require.Equal(t, 1050, usageRepo.LastLog.TotalTokens())
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, 700*5e-6, usageRepo.LastLog.InputCost, 1e-12)
	require.InDelta(t, 200*6.25e-6, usageRepo.LastLog.CacheCreationCost, 1e-12)
	require.InDelta(t, 100*0.5e-6, usageRepo.LastLog.CacheReadCost, 1e-12)
	require.InDelta(t, 50*30e-6, usageRepo.LastLog.OutputCost, 1e-12)
	require.InDelta(t, usageRepo.LastLog.TotalCost*1.1, usageRepo.LastLog.ActualCost, 1e-12)
}

// TestOpenAIGatewayServiceRecordUsage_LongContextBillingIgnoresLegacyProviderExtra
// varies absent, false, true, and shadow-provider legacy long-context flags
// against a parsed ladder catalogue. It checks identical ladder costs and
// parent lookup only for the shadow fixture.
func TestOpenAIGatewayServiceRecordUsage_LongContextBillingIgnoresLegacyProviderExtra(t *testing.T) {
	parentID := int64(4016)
	tests := []struct {
		name     string               // fixture scenario label
		provider *providercore.Record // executing provider and flags
	}{
		{name: "missing legacy value", provider: &providercore.Record{ID: 3014, Platform: capability.PlatformOpenAI}},
		{name: "legacy false", provider: &providercore.Record{ID: 3015, Platform: capability.PlatformOpenAI, Extra: map[string]any{"openai_long_context_billing_enabled": false}}},
		{name: "legacy true", provider: &providercore.Record{ID: 3016, Platform: capability.PlatformOpenAI, Extra: map[string]any{"openai_long_context_billing_enabled": true}}},
		{name: "spark shadow", provider: &providercore.Record{
			ID:               3017,
			Platform:         capability.PlatformOpenAI,
			Type:             capability.ProviderTypeOAuth,
			ParentProviderID: &parentID,
			QuotaDimension:   providercore.QuotaDimensionSpark,
			Extra:            map[string]any{"openai_long_context_billing_enabled": false},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
			providerRepo := &completiontestkit.ProviderLookup{Provider: &providercore.Record{ID: parentID, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth}}
			svc := newOpenAIRecordUsageServiceForTest(
				usageRepo,
				&completiontestkit.UserStore{},
				&completiontestkit.SubscriptionStore{},
				nil,
			)
			swapInOpenAILadderCatalog(t, svc)
			svc.ProviderLookup = func(ctx context.Context, id int64) (*providercore.Record, error) {
				value,

					err := providerRepo.
					GetByID(ctx, id)
				return value, err
			}

			err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
				Result: &forwardcore.OpenAIResult{
					RequestID: "resp_gpt54_long_context_" + tt.name,
					Usage:     openai.ForwardUsage{InputTokens: 300000, OutputTokens: 2000},
					Model:     "gpt-5.4",
					Duration:  time.Second,
				},
				APIKey:   &apikey.APIKey{ID: 1014},
				User:     &identity.User{ID: 2014},
				Provider: tt.provider,
			})

			require.NoError(t, err)
			require.NotNil(t, usageRepo.LastLog)
			expectedInput := 300000 * 2.5e-6 * 2.0
			expectedOutput := 2000 * 15e-6 * 1.5
			// Floating-point comparisons allow rounding from price and multiplier arithmetic.
			require.InDelta(t, expectedInput, usageRepo.LastLog.InputCost, 1e-10)
			require.InDelta(t, expectedOutput, usageRepo.LastLog.OutputCost, 1e-10)
			require.InDelta(t, expectedInput+expectedOutput, usageRepo.LastLog.TotalCost, 1e-10)
			require.InDelta(t, (expectedInput+expectedOutput)*1.1, usageRepo.LastLog.ActualCost, 1e-10)
			require.True(t, usageRepo.LastLog.LongContextBillingApplied)
			billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
			require.Equal(t, 1, billingRepo.Calls)
			require.InDelta(t, usageRepo.LastLog.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-10)
			// The shadow fixture requests a credential parent; the parsed catalogue and
			// default long-context setting supply the same ladder prices in every scenario.
			wantProviderRepoCalls := 0
			if tt.provider.IsShadow() {
				wantProviderRepoCalls = 1
			}
			require.Equal(t, wantProviderRepoCalls, providerRepo.Calls)
		})
	}
}

// TestOpenAIGatewayServiceRecordUsage_GroupControlsLongContextBilling varies
// shared long-context enablement while providing the opposite legacy provider
// flag. It checks ladder application and expected input/output costs from the
// configured group policy.
func TestOpenAIGatewayServiceRecordUsage_GroupControlsLongContextBilling(t *testing.T) {
	// The group setting controls the long-context policy.
	tests := []struct {
		name        string // fixture scenario label
		groupEnable bool   // shared long-context enablement
		wantApplied bool   // expected ladder application flag
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
					Model:     "gpt-5.4",
					Duration:  time.Second,
				},
				APIKey: &apikey.APIKey{
					ID:      int64(1020 + i),
					GroupID: &groupID,
					Group: configureBillingGroup(svc, &routing.Group{
						ID:             groupID,
						RateMultiplier: 1,
					}, pricing.BillingSettings{
						LongContextPricingEnabled:    tt.groupEnable,
						PeakRateMultiplier:           1,
						BatchImageDiscountMultiplier: 0.5,
						BatchImageHoldMultiplier:     0.6,
					}, nil),
				},
				User: &identity.User{ID: int64(2020 + i)},
				Provider: &providercore.Record{
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
			// Floating-point comparisons allow rounding from price and multiplier arithmetic.
			require.InDelta(t, 300000*2.5e-6*inputMultiplier, usageRepo.LastLog.InputCost, 1e-10)
			require.InDelta(t, 2000*15e-6*outputMultiplier, usageRepo.LastLog.OutputCost, 1e-10)
		})
	}
}

// TestOpenAIGatewayServiceRecordUsage_GrokLongContextFollowsGroupToggle records
// above-threshold Grok usage with shared long-context policy enabled or
// disabled. It checks the application marker and both token cost buckets follow
// that toggle.
func TestOpenAIGatewayServiceRecordUsage_GrokLongContextFollowsGroupToggle(t *testing.T) {
	baseInput := 250000 * 2e-6
	baseOutput := 1000 * 6e-6

	for i, tt := range []struct {
		name        string // fixture scenario label
		groupEnable bool   // shared long-context enablement
		wantApplied bool   // expected ladder application flag
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
					Group: configureBillingGroup(svc, &routing.Group{
						ID:             groupID,
						RateMultiplier: 1,
					}, pricing.BillingSettings{
						LongContextPricingEnabled:    tt.groupEnable,
						PeakRateMultiplier:           1,
						BatchImageDiscountMultiplier: 0.5,
						BatchImageHoldMultiplier:     0.6,
					}, nil),
				},
				User:     &identity.User{ID: int64(2030 + i)},
				Provider: &providercore.Record{ID: int64(3030 + i), Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth},
			})

			require.NoError(t, err)
			require.NotNil(t, usageRepo.LastLog)
			require.Equal(t, tt.wantApplied, usageRepo.LastLog.LongContextBillingApplied)
			multiplier := 1.0
			if tt.wantApplied {
				multiplier = 2
			}
			// Floating-point comparisons allow rounding from price and multiplier arithmetic.
			require.InDelta(t, baseInput*multiplier, usageRepo.LastLog.InputCost, 1e-10)
			require.InDelta(t, baseOutput*multiplier, usageRepo.LastLog.OutputCost, 1e-10)
		})
	}
}

// TestOpenAIGatewayServiceRecordUsage_ServiceTierPriorityUsesFastPricing
// supplies outbound priority for a fixture model with a premium multiplier. It
// checks the captured tier and base total equal the standard calculator amount
// times that premium factor.
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
		APIKey:   &apikey.APIKey{ID: 1015},
		User:     &identity.User{ID: 2015},
		Provider: &providercore.Record{ID: 3015},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.ServiceTier)
	require.Equal(t, serviceTier, *usageRepo.LastLog.ServiceTier)

	baseCost, calcErr := svc.Dependencies.Calculator.CalculateCost("gpt-5.4", pricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1.0)
	require.NoError(t, calcErr)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, baseCost.TotalCost*2, usageRepo.LastLog.TotalCost, 1e-10)
}

// TestOpenAIGatewayServiceRecordUsage_ServiceTierFlexHalvesCost supplies flex
// with inclusive input containing a cache-read subset. It checks the base total
// is half the standard cost calculated from disjoint ordinary and cached
// buckets.
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
		APIKey:   &apikey.APIKey{ID: 1016},
		User:     &identity.User{ID: 2016},
		Provider: &providercore.Record{ID: 3016},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)

	baseCost, calcErr := svc.Dependencies.Calculator.CalculateCost("gpt-5.4", pricing.UsageTokens{InputTokens: 80, OutputTokens: 50, CacheReadTokens: 20}, 1.0)
	require.NoError(t, calcErr)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, baseCost.TotalCost*0.5, usageRepo.LastLog.TotalCost, 1e-10)
}

// TestOpenAIGatewayServiceRecordUsage_UsesRequestedModelAndUpstreamModelMetadataFields
// captures a logical model, a different sent model, and a raw response
// declaration with effort/client metadata. It checks recorded model columns,
// response mismatch, metadata, and the command amount matching calculated
// analysis cost.
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
			UpstreamResponseModel:    "gpt-runtime-version",
			BillingModel:             "gpt-5.1-codex",
			Model:                    "gpt-5.4",
			UpstreamModel:            "gpt-5.1-codex",
			ServiceTier:              &serviceTier,
			ReasoningEffort:          &reasoning,
			RequestedReasoningEffort: &reasoning,
			Usage: openai.ForwardUsage{
				InputTokens:  20,
				OutputTokens: 10,
			},
			Duration: 2 * time.Second,
			Timings:  &timing.Performance{Stream: true, Values: map[string]int64{"upstream_started_ms": 0, "upstream_first_output_ms": 120}},
		},
		APIKey:    &apikey.APIKey{ID: 10, GroupID: i64p(11), Group: &routing.Group{ID: 11, RateMultiplier: 1.2}},
		User:      &identity.User{ID: 20},
		Provider:  &providercore.Record{ID: 30},
		UserAgent: "codex-cli/1.0",
		IPAddress: "127.0.0.1",
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "gpt-5.4", usageRepo.LastLog.Model)
	require.Equal(t, "gpt-5.4", usageRepo.LastLog.RequestedModel)
	require.Equal(t, "gpt-runtime-version", *usageRepo.LastLog.UpstreamResponseModel)
	require.True(t, *usageRepo.LastLog.UpstreamModelMismatch)
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
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, usageRepo.LastLog.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
}

// TestOpenAIGatewayServiceRecordUsage_PersistsRequestedReasoningEffort supplies
// different requested and final effort observations in one result. It checks
// the attempted row keeps both values independently after capture and
// recording.
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
		APIKey:   &apikey.APIKey{ID: 10},
		User:     &identity.User{ID: 20},
		Provider: &providercore.Record{ID: 30},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.RequestedReasoningEffort)
	require.Equal(t, requested, *usageRepo.LastLog.RequestedReasoningEffort)
	require.Equal(t, forwarded, *usageRepo.LastLog.ReasoningEffort)
}

// TestOpenAIGatewayServiceRecordUsage_PreservesGroupMappedUpstreamModel
// captures original requested and mapped execution model names. It checks
// original display identity remains distinct from the internal and actual
// outbound names in analysis.
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
		APIKey:   &apikey.APIKey{ID: 10},
		User:     &identity.User{ID: 20},
		Provider: &providercore.Record{ID: 30},
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

// TestOpenAIGatewayServiceRecordUsage_PreservesLoopedPricingConfigAndProviderUpstreamModel
// captures a mapped internal name with outbound execution returning to the
// original name. It checks requested, internal, and outbound observations
// remain separately available in analysis.
func TestOpenAIGatewayServiceRecordUsage_PreservesLoopedPricingConfigAndProviderUpstreamModel(t *testing.T) {
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
		APIKey:   &apikey.APIKey{ID: 10},
		User:     &identity.User{ID: 20},
		Provider: &providercore.Record{ID: 30},
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

// TestOpenAIGatewayServiceRecordUsage_BillsMappedRequestsUsingRequestedModel
// supplies a priced logical GPT model and a different priced outbound model
// under default source selection. It checks row and command amounts match the
// logical model calculation.
func TestOpenAIGatewayServiceRecordUsage_BillsMappedRequestsUsingRequestedModel(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}

	// Bill the requested gpt-5.4 model.
	expectedCost, err := svc.Dependencies.Calculator.CalculateCost("gpt-5.4", pricing.UsageTokens{
		InputTokens:  20,
		OutputTokens: 10,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID:     "resp_upstream_model_billing_fallback",
			Model:         "gpt-5.4",
			UpstreamModel: "gpt-5.1-codex",
			Usage:         usage,
			Duration:      time.Second,
		},
		APIKey:   &apikey.APIKey{ID: 10},
		User:     &identity.User{ID: 20},
		Provider: &providercore.Record{ID: 30},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "gpt-5.4", usageRepo.LastLog.Model)
	require.Equal(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost)
	require.Equal(t, expectedCost.TotalCost, usageRepo.LastLog.TotalCost)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Equal(t, 1, billingRepo.Calls)
	require.NotNil(t, billingRepo.LastCmd)
	require.Equal(t, expectedCost.ActualCost, billingRepo.LastCmd.BillableAmountUSD)
}

// TestOpenAIGatewayServiceRecordUsage_GroupMappedDoesNotOverrideBillingModelWhenUnmapped
// supplies equal original/mapped aliases and a separately priced explicit
// BillingModel. It checks unchanged mapping leaves that explicit candidate's
// positive calculated user amount selected.
func TestOpenAIGatewayServiceRecordUsage_GroupMappedDoesNotOverrideBillingModelWhenUnmapped(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}

	// Without a group remap, use the upstream billing model captured in result.BillingModel.
	expectedCost, err := svc.Dependencies.Calculator.CalculateCost("gpt-5.4", pricing.UsageTokens{
		InputTokens:  20,
		OutputTokens: 10,
	}, 1.1)
	require.NoError(t, err)

	err = svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID:     "resp_channel_unmapped_billing",
			Model:         "glm",
			BillingModel:  "gpt-5.4",
			UpstreamModel: "gpt-5.4",
			Usage:         usage,
			Duration:      time.Second,
		},
		APIKey:   &apikey.APIKey{ID: 10},
		User:     &identity.User{ID: 20},
		Provider: &providercore.Record{ID: 30},
		PricingUsageFields: routing.PricingUsageFields{
			PricingConfigID:    1,
			OriginalModel:      "glm",
			GroupMappedModel:   "glm", // No group remap.
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost)
	require.True(t, usageRepo.LastLog.ActualCost > 0, "cost must not be zero")
}

// TestOpenAIGatewayServiceRecordUsage_GroupMappedOverridesBillingModelWhenMapped
// supplies a changed group mapping beside a different explicit billing
// candidate. It checks the selected mapped identity determines the attempted
// row's positive user amount.
func TestOpenAIGatewayServiceRecordUsage_GroupMappedOverridesBillingModelWhenMapped(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}

	// A changed GroupMappedModel determines the billing model.
	expectedCost, err := svc.Dependencies.Calculator.CalculateCost("gpt-5.4", pricing.UsageTokens{
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
		APIKey:   &apikey.APIKey{ID: 10},
		User:     &identity.User{ID: 20},
		Provider: &providercore.Record{ID: 30},
		PricingUsageFields: routing.PricingUsageFields{
			PricingConfigID:    1,
			OriginalModel:      "glm",
			GroupMappedModel:   "gpt-5.4", // The group maps glm to gpt-5.4.
			BillingModelSource: routing.BillingModelSourceGroupMapped,
		},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost)
	require.True(t, usageRepo.LastLog.ActualCost > 0, "cost must not be zero")
}

// TestOpenAIGatewayServiceRecordUsage_UpstreamBillingSourceOverridesRequestedModel
// supplies different requested and outbound GPT prices while selecting upstream
// source. It checks the attempted row's positive user charge matches the
// outbound model calculation.
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
		APIKey:   &apikey.APIKey{ID: 10},
		User:     &identity.User{ID: 20},
		Provider: &providercore.Record{ID: 30},
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

// TestOpenAIGatewayServiceRecordUsage_ResponsesMappedBillingModelHonorsBillingModelSource
// varies requested versus upstream source for a result carrying mapped billing
// evidence. It checks the internal model remains unchanged while row/command
// amounts follow the selected price identity.
func TestOpenAIGatewayServiceRecordUsage_ResponsesMappedBillingModelHonorsBillingModelSource(t *testing.T) {
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}
	tokens := pricing.UsageTokens{InputTokens: 20, OutputTokens: 10}

	tests := []struct {
		name               string // fixture scenario label
		billingModelSource string // configured billing model source
		wantBillingModel   string // expected selected price identity
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
				APIKey:   &apikey.APIKey{ID: 10},
				User:     &identity.User{ID: 20},
				Provider: &providercore.Record{ID: 30},
				PricingUsageFields: routing.PricingUsageFields{
					OriginalModel:      "gpt-5.4",
					GroupMappedModel:   "gpt-5.4",
					BillingModelSource: tt.billingModelSource,
				},
			})

			require.NoError(t, err)
			require.NotNil(t, usageRepo.LastLog)
			require.Equal(t, "gpt-5.4", usageRepo.LastLog.Model)
			// Floating-point comparisons allow rounding from price and multiplier arithmetic.
			require.InDelta(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost, 1e-12)
			billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
			require.NotNil(t, billingRepo.LastCmd)
			require.InDelta(t, expectedCost.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
			require.Zero(t, userRepo.DeductCalls)
			require.True(t, usageRepo.LastLog.ActualCost > 0, "cost must not be zero")
		})
	}
}

// TestOpenAIGatewayServiceRecordUsage_BillsCompactOpenAIModelAlias supplies an
// unpriced compact-spelled alias beside a priced canonical name and different
// outbound model. It checks retained model metadata with zero row and command
// charges for the selected alias.
func TestOpenAIGatewayServiceRecordUsage_BillsCompactOpenAIModelAlias(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}

	_, err := svc.Dependencies.Calculator.CalculateCost("gpt-5.5", pricing.UsageTokens{
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
		APIKey:   &apikey.APIKey{ID: 10},
		User:     &identity.User{ID: 20},
		Provider: &providercore.Record{ID: 30},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, "gpt5.5", usageRepo.LastLog.Model)
	require.NotNil(t, usageRepo.LastLog.UpstreamModel)
	require.Equal(t, "gpt-5.4", *usageRepo.LastLog.UpstreamModel)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
}

// TestOpenAIGatewayServiceRecordUsage_FallsBackToUpstreamModelWhenPrimaryUnpriceable
// supplies an unpriced explicit/logical candidate alongside a known priced
// outbound model. It checks zero row and command amounts, retaining the
// selected identity rather than borrowing the outbound price.
func TestOpenAIGatewayServiceRecordUsage_FallsBackToUpstreamModelWhenPrimaryUnpriceable(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	userRepo := &completiontestkit.UserStore{}
	subRepo := &completiontestkit.SubscriptionStore{}
	svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
	usage := openai.ForwardUsage{InputTokens: 20, OutputTokens: 10}

	_, err := svc.Dependencies.Calculator.CalculateCost("gpt-5.4", pricing.UsageTokens{
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
		APIKey:   &apikey.APIKey{ID: 10},
		User:     &identity.User{ID: 20},
		Provider: &providercore.Record{ID: 30},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Zero(t, usageRepo.LastLog.ActualCost)
	require.Zero(t, usageRepo.LastLog.TotalCost)
	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.Zero(t, billingRepo.LastCmd.BillableAmountUSD)
}

// TestOpenAIGatewayServiceRecordUsage_UnpricedTokenModelFallsBackToZeroCostUsageLog
// records positive tokens for an unpriced alias with no outbound alternative.
// It checks retained model/count metadata and zero costs in the single
// attempted analysis row.
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
		APIKey:   &apikey.APIKey{ID: 10},
		User:     &identity.User{ID: 20},
		Provider: &providercore.Record{ID: 30},
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

// TestOpenAIGatewayServiceRecordUsage_SubscriptionBillingSetsSubscriptionFields
// supplies an applied store fixture with an allocation identifying one
// subscription and plan. It checks the analysis funding category, subscription
// ID, and single financial call while legacy repositories remain unbound.
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
			Model:     "gpt-5.4",
			Duration:  time.Second,
		},
		APIKey:       &apikey.APIKey{ID: 100, GroupID: i64p(88), Group: &routing.Group{ID: 88, RateMultiplier: 1.0}},
		User:         &identity.User{ID: 200},
		Provider:     &providercore.Record{ID: 300},
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

// TestOpenAIGatewayServiceRecordUsage_SubscriptionBillingUsesPlanGroupRateOverUserGroupRate
// supplies a covered subscription plan rate and a competing balance user
// override. It checks the plan factor, subscription metadata, calculated
// row/command charge, and absence of user-rate lookup.
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
	expectedCost := expectedOpenAICost(t, svc, "gpt-5.4", usage, 0.5)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_subscription_group_rate",
			Usage:     usage,
			Model:     "gpt-5.4",
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
		Provider:     &providercore.Record{ID: 300},
		Subscription: subscription,
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, usagecore.BillingTypeSubscription, usageRepo.LastLog.BillingType)
	require.NotNil(t, usageRepo.LastLog.SubscriptionID)
	require.Equal(t, subscription.ID, *usageRepo.LastLog.SubscriptionID)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, 0.5, usageRepo.LastLog.RateMultiplier, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
	require.Equal(t, 0, rateRepo.Calls)
}

// TestOpenAIGatewayServiceRecordUsage_InferredSubscriptionUsesPlanGroupRate
// returns a preliminary subscription from the financial reader instead of
// capturing one. It checks one subscription lookup, its plan rate and row
// identity, calculated command amount, and no balance-rate lookup.
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
	expectedCost := expectedOpenAICost(t, svc, "gpt-5.4", usage, 0.5)

	err := svc.RecordOpenAI(context.Background(), &gatewaycapture.OpenAICapture{
		Result: &forwardcore.OpenAIResult{
			RequestID: "resp_inferred_subscription_group_rate",
			Usage:     usage,
			Model:     "gpt-5.4",
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
		User:     &identity.User{ID: 200},
		Provider: &providercore.Record{ID: 300},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, usagecore.BillingTypeSubscription, usageRepo.LastLog.BillingType)
	require.NotNil(t, usageRepo.LastLog.SubscriptionID)
	require.Equal(t, subscription.ID, *usageRepo.LastLog.SubscriptionID)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, 0.5, usageRepo.LastLog.RateMultiplier, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, usageRepo.LastLog.ActualCost, 1e-12)
	require.InDelta(t, expectedCost.ActualCost, billingRepo.LastCmd.BillableAmountUSD, 1e-12)
	require.Equal(t, 0, rateRepo.Calls)
	require.Equal(t, 1, billingRepo.ResolveCalls)
}

// TestOpenAIGatewayServiceRecordUsage_ImageOnlyUsageStillPersists supplies two
// image outputs without token observations using fixture media prices. It
// checks the attempted row retains image count, size, and image billing mode
// after successful recording.
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
		APIKey:   &apikey.APIKey{ID: 1007},
		User:     &identity.User{ID: 2007},
		Provider: &providercore.Record{ID: 3007},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 2, usageRepo.LastLog.ImageCount)
	require.NotNil(t, usageRepo.LastLog.ImageSize)
	require.Equal(t, "1K", *usageRepo.LastLog.ImageSize)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.LastLog.BillingMode)
}

// TestOpenAIGatewayServiceRecordUsage_EmptyImageSizeDefaultsBeforeBillingAndPersistence
// supplies two image outputs without dimension evidence and a labeled 2K card.
// It checks default-size/source metadata, nil input/output dimensions, and the
// resulting doubled media charge.
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
			Group: configureBillingGroup(svc, &routing.Group{
				ID:             groupID,
				RateMultiplier: 1.0,
			}, pricing.BillingSettings{
				LongContextPricingEnabled:    true,
				PeakRateMultiplier:           1,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			}, testImageModelPricing(map[string]*float64{"2K": &imagePrice2K})),
		},
		User:     &identity.User{ID: 21201},
		Provider: &providercore.Record{ID: 31201},
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
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, 0.62, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.62, usageRepo.LastLog.ActualCost, 1e-12)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.LastLog.BillingMode)
}

// TestOpenAIGatewayServiceRecordUsage_OutputImageSizeWinsBeforeBillingAndPersistence
// supplies a small input hint and larger decoded output dimensions with two
// labeled card prices. It checks output-selected tier/evidence/breakdown and
// the corresponding media dollar charge.
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
			Group: configureBillingGroup(svc, &routing.Group{
				ID:             groupID,
				RateMultiplier: 1.0,
			}, pricing.BillingSettings{
				LongContextPricingEnabled:    true,
				PeakRateMultiplier:           1,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			}, testImageModelPricing(map[string]*float64{"1K": &imagePrice1K, "4K": &imagePrice4K})),
		},
		User:     &identity.User{ID: 21202},
		Provider: &providercore.Record{ID: 31202},
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
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, 0.44, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.44, usageRepo.LastLog.ActualCost, 1e-12)
}

// TestOpenAIGatewayServiceRecordUsage_ImageUsesPerImageBillingEvenWithUsageTokens
// supplies token counts alongside two outputs and a configured image card. It
// checks image mode/unit charges and zero ordinary or image-token cost buckets
// under that card.
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
			Group: configureBillingGroup(svc, &routing.Group{
				ID:             groupID,
				RateMultiplier: 1.0,
			}, pricing.BillingSettings{
				LongContextPricingEnabled:    true,
				PeakRateMultiplier:           1,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			}, testImageModelPricing(map[string]*float64{"1K": &imagePrice})),
		},
		User:     &identity.User{ID: 2008},
		Provider: &providercore.Record{ID: 3008},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.LastLog.BillingMode)
	require.Equal(t, 2, usageRepo.LastLog.ImageCount)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, 0.04, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.04, usageRepo.LastLog.ActualCost, 1e-12)
	require.InDelta(t, 0.0, usageRepo.LastLog.InputCost, 1e-12)
	require.InDelta(t, 0.0, usageRepo.LastLog.OutputCost, 1e-12)
	require.InDelta(t, 0.0, usageRepo.LastLog.ImageOutputCost, 1e-12)
}

// TestOpenAIGatewayServiceRecordUsage_ImageSharedMultiplierPreservesExistingBehavior
// supplies one configured image output with a fractional group base factor. It
// checks the base media price, scaled user amount, recorded factor, and image
// mode in analysis.
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
			Group: configureBillingGroup(svc, &routing.Group{
				ID:             groupID,
				RateMultiplier: 0.15,
			}, pricing.BillingSettings{
				LongContextPricingEnabled:    true,
				PeakRateMultiplier:           1,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			}, testImageModelPricing(map[string]*float64{"1K": &imagePrice})),
		},
		User:     &identity.User{ID: 20121},
		Provider: &providercore.Record{ID: 30121},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, 0.2, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.03, usageRepo.LastLog.ActualCost, 1e-12)
	require.InDelta(t, 0.15, usageRepo.LastLog.RateMultiplier, 1e-12)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.LastLog.BillingMode)
}

// TestOpenAIGatewayServiceRecordUsage_ImageSharedMultiplierUsesUserGroupOverride
// supplies a balance user factor different from the group factor for a
// configured image card. It checks the media base stays fixed and the user
// amount uses that override.
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
			Group: configureBillingGroup(svc, &routing.Group{
				ID:             groupID,
				RateMultiplier: 0.15,
			}, pricing.BillingSettings{
				LongContextPricingEnabled:    true,
				PeakRateMultiplier:           1,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			}, testImageModelPricing(map[string]*float64{"1K": &imagePrice})),
		},
		User:     &identity.User{ID: 20125},
		Provider: &providercore.Record{ID: 30125},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, 0.5, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.1, usageRepo.LastLog.ActualCost, 1e-12)
	require.InDelta(t, 0.2, usageRepo.LastLog.RateMultiplier, 1e-12)
}

// TestOpenAIGatewayServiceRecordUsage_GrokVideoUsesDefaultRateCard supplies one
// 720p video without duration under fixture catalogue media rates. It checks
// the eight-second default charge, retained video metadata/mode, and absent
// image metadata.
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
				ID: groupID,

				RateMultiplier: 1,
			},
		},
		User:     &identity.User{ID: 201261},
		Provider: &providercore.Record{ID: 301261, Platform: capability.PlatformGrok},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Nil(t, usageRepo.LastLog.ImageSize)
	// Missing duration uses the default eight seconds: USD 0.14/s * 8s.
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, 0.14*8, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.14*8, usageRepo.LastLog.ActualCost, 1e-12)
	require.Equal(t, 0, usageRepo.LastLog.ImageCount)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeVideo), *usageRepo.LastLog.BillingMode)
	require.Equal(t, 1, usageRepo.LastLog.VideoCount)
	require.NotNil(t, usageRepo.LastLog.VideoDurationSeconds)
	require.Equal(t, pricing.VideoBillingDefaultDurationSeconds, *usageRepo.LastLog.VideoDurationSeconds)
}

// TestOpenAIGatewayServiceRecordUsage_GroupImagePriceOverridesPricingConfigImagePrice
// installs an owning fixture image card over an underlying image resolver card.
// It checks the selected 2K tier uses that owning card's smaller base and
// actual amounts.
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
			Group: configureBillingGroup(svc, &routing.Group{
				ID:             groupID,
				RateMultiplier: 1,
			}, pricing.BillingSettings{
				LongContextPricingEnabled:    true,
				PeakRateMultiplier:           1,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			}, testImageModelPricing(map[string]*float64{"2K": &groupImagePrice2K})),
		},
		User:     &identity.User{ID: 20127},
		Provider: &providercore.Record{ID: 30127, Platform: capability.PlatformGrok},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 1, usageRepo.LastLog.ImageCount)
	require.Equal(t, pricing.ImageBillingSize2K, *usageRepo.LastLog.ImageSize)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, 0.021, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.021, usageRepo.LastLog.ActualCost, 1e-12)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.LastLog.BillingMode)
}

// TestOpenAIGatewayServiceRecordUsage_GroupVideoPriceOverridesPricingConfigImagePrice
// installs an owning fixture video card over an underlying image-mode card. It
// checks positive video identity, absent image metadata, video mode, and the
// configured sixty-second media amount with its stored custom resolution.
func TestOpenAIGatewayServiceRecordUsage_GroupVideoPriceOverridesPricingConfigImagePrice(t *testing.T) {
	groupID := int64(128)
	pricingConfigPrice := 0.201
	groupVideoPrice := 0.037
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
			VideoResolution:      "custom-resolution",
			VideoDurationSeconds: 60,
			Duration:             time.Second,
		},
		APIKey: &apikey.APIKey{
			ID:      10128,
			GroupID: i64p(groupID),
			Group: configureBillingGroup(svc, &routing.Group{
				ID:             groupID,
				RateMultiplier: 1,
			}, pricing.BillingSettings{
				LongContextPricingEnabled:    true,
				PeakRateMultiplier:           1,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			}, testVideoModelPricing(map[string]*float64{"custom-resolution": &groupVideoPrice})),
		},
		User:     &identity.User{ID: 20128},
		Provider: &providercore.Record{ID: 30128, Platform: capability.PlatformGrok},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Equal(t, 0, usageRepo.LastLog.ImageCount)
	require.Nil(t, usageRepo.LastLog.ImageSize)
	require.Equal(t, "custom-resolution", *usageRepo.LastLog.VideoResolution)
	require.Equal(t, 60, *usageRepo.LastLog.VideoDurationSeconds)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, 0.037*60, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.037*60, usageRepo.LastLog.ActualCost, 1e-12)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeVideo), *usageRepo.LastLog.BillingMode)
}

// TestOpenAIGatewayServiceRecordUsage_GrokVideoWithTokenConfigPricingKeepsVideoMetadata
// supplies video quantity/duration and token observations under a configured
// token card. It checks token mode retains video count, normalized
// resolution/duration, and nil image-size metadata.
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
				ID: groupID,

				RateMultiplier: 1,
			},
		},
		User:     &identity.User{ID: 20132},
		Provider: &providercore.Record{ID: 30132, Platform: capability.PlatformGrok},
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

// TestOpenAIGatewayServiceRecordUsage_PricingConfigImageBillingUsesImageCountAndSharedMultiplier
// supplies three outputs under a configured per-image card with a fractional
// group rate. It checks base cost, scaled user cost, recorded factor, count,
// and image mode.
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
		User:     &identity.User{ID: 20123},
		Provider: &providercore.Record{ID: 30123},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, 0.75, usageRepo.LastLog.TotalCost, 1e-12)
	require.InDelta(t, 0.1125, usageRepo.LastLog.ActualCost, 1e-12)
	require.InDelta(t, 0.15, usageRepo.LastLog.RateMultiplier, 1e-12)
	require.Equal(t, 3, usageRepo.LastLog.ImageCount)
	require.NotNil(t, usageRepo.LastLog.BillingMode)
	require.Equal(t, string(routing.BillingModeImage), *usageRepo.LastLog.BillingMode)
}

// TestOpenAIGatewayServiceRecordUsage_ServiceTierDowngradedByUpstreamResponse
// supplies priority request evidence and a default response on an API-key
// provider. It checks the recorded default tier and base price matching the
// standard calculator result.
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
		APIKey:   &apikey.APIKey{ID: 1017},
		User:     &identity.User{ID: 2017},
		Provider: &providercore.Record{ID: 3017, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.ServiceTier)
	require.Equal(t, "default", *usageRepo.LastLog.ServiceTier, "usage log must record the tier actually billed")

	baseCost, calcErr := svc.Dependencies.Calculator.CalculateCost("gpt-5.4", pricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1.0)
	require.NoError(t, calcErr)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, baseCost.TotalCost, usageRepo.LastLog.TotalCost, 1e-10, "a request served at default must not pay the priority price")
}

// TestOpenAIGatewayServiceRecordUsage_CodexDefaultEchoKeepsFastBilling supplies
// a default response echo for OAuth and setup-token providers with outbound
// priority. It checks the captured premium tier and corresponding premium model
// base cost.
func TestOpenAIGatewayServiceRecordUsage_CodexDefaultEchoKeepsFastBilling(t *testing.T) {
	for _, providerType := range []string{capability.ProviderTypeOAuth, capability.ProviderTypeSetupToken} {
		t.Run(providerType, func(t *testing.T) {
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
				APIKey:   &apikey.APIKey{ID: 1019},
				User:     &identity.User{ID: 2019},
				Provider: &providercore.Record{ID: 3019, Platform: capability.PlatformOpenAI, Type: providerType},
			})

			require.NoError(t, err)
			require.NotNil(t, usageRepo.LastLog)
			require.NotNil(t, usageRepo.LastLog.ServiceTier)
			require.Equal(t, "priority", *usageRepo.LastLog.ServiceTier)

			fastCost, calcErr := svc.Dependencies.Calculator.CalculateCostWithServiceTier("gpt-5.6-sol", tokens, 1.0, "priority")
			require.NoError(t, calcErr)
			// Floating-point comparisons allow rounding from price and multiplier arithmetic.
			require.InDelta(t, fastCost.TotalCost, usageRepo.LastLog.TotalCost, 1e-10)
		})
	}
}

// TestOpenAIGatewayServiceRecordUsage_ShadowUsesParentCredentialTierContract
// supplies an API-key shadow whose credential parent is OAuth-like. It checks
// one parent read and retention of premium tier/base cost despite the upstream
// default echo.
func TestOpenAIGatewayServiceRecordUsage_ShadowUsesParentCredentialTierContract(t *testing.T) {
	usageRepo := &completiontestkit.UsageLogStore{Inserted: true}
	svc := newOpenAIRecordUsageServiceForTest(
		usageRepo,
		&completiontestkit.UserStore{},
		&completiontestkit.SubscriptionStore{},
		nil,
	)
	parentID := int64(9001)
	providerRepo := &completiontestkit.ProviderLookup{Provider: &providercore.Record{
		ID: parentID, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth,
	}}
	svc.ProviderLookup = func(ctx context.Context, id int64) (*providercore.Record, error) {
		value,

			err := providerRepo.
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
		Provider: &providercore.Record{
			ID: 3020, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey,
			ParentProviderID: &parentID,
		},
	})

	require.NoError(t, err)
	require.Equal(t, 1, providerRepo.Calls)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.ServiceTier)
	require.Equal(t, "priority", *usageRepo.LastLog.ServiceTier)

	fastCost, calcErr := svc.Dependencies.Calculator.CalculateCostWithServiceTier("gpt-5.6-sol", tokens, 1.0, "priority")
	require.NoError(t, calcErr)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, fastCost.TotalCost, usageRepo.LastLog.TotalCost, 1e-10)
}

// TestOpenAIGatewayServiceRecordUsage_FreeOpenAIFastChargesStandard enables
// free Fast with manual premium prices and a fractional group factor. It checks
// premium TotalCost, standard user ActualCost, retained tier, and standard
// financial allocation base.
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
		Group: configureBillingGroup(svc, &routing.Group{
			ID:             groupID,
			Status:         billing.StatusActive,
			Hydrated:       true,
			RateMultiplier: 0.5,
		}, pricing.BillingSettings{
			FreeOpenAIFast:               true,
			LongContextPricingEnabled:    true,
			PeakRateMultiplier:           1,
			BatchImageDiscountMultiplier: 0.5,
			BatchImageHoldMultiplier:     0.6,
		}, []routing.ModelPricingEntry{{
			Models:         []string{"gpt-5.6-sol"},
			BillingMode:    routing.BillingModeToken,
			InputPrice:     &inputPrice,
			OutputPrice:    &outputPrice,
			FastMultiplier: &fastMultiplier,
		}}),
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
		APIKey:   apiKey,
		User:     &identity.User{ID: 2020},
		Provider: &providercore.Record{ID: 3020, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.NotNil(t, usageRepo.LastLog.ServiceTier)
	require.Equal(t, "priority", *usageRepo.LastLog.ServiceTier)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, 0.5, usageRepo.LastLog.RateMultiplier, 1e-12)

	standardTotal := float64(tokens.InputTokens)*inputPrice + float64(tokens.OutputTokens)*outputPrice
	require.InDelta(t, standardTotal*fastMultiplier, usageRepo.LastLog.TotalCost, 1e-10)
	require.InDelta(t, standardTotal*0.5, usageRepo.LastLog.ActualCost, 1e-10)

	billingRepo := requireOpenAIRecordUsageBillingRepoStub(t, svc)
	require.NotNil(t, billingRepo.LastCmd)
	// Premium TotalCost stays in analysis, while this command base allocates the
	// standard-price user amount across subscription and balance inside real billing.
	require.InDelta(t, standardTotal, billingRepo.LastCmd.BaseAmountUSD, 1e-10)
	require.InDelta(t, standardTotal*0.5, billingRepo.LastCmd.BillableAmountUSD, 1e-10)
}

// TestOpenAIGatewayServiceRecordUsage_ServiceTierNeverRaisedByUpstreamResponse
// supplies an observed priority tier with no outbound premium selection on an
// API-key provider. It checks the row keeps nil tier and standard model base
// cost.
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
		APIKey:   &apikey.APIKey{ID: 1018},
		User:     &identity.User{ID: 2018},
		Provider: &providercore.Record{ID: 3018, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey},
	})

	require.NoError(t, err)
	require.NotNil(t, usageRepo.LastLog)
	require.Nil(t, usageRepo.LastLog.ServiceTier)

	baseCost, calcErr := svc.Dependencies.Calculator.CalculateCost("gpt-5.4", pricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1.0)
	require.NoError(t, calcErr)
	// Floating-point comparisons allow rounding from price and multiplier arithmetic.
	require.InDelta(t, baseCost.TotalCost, usageRepo.LastLog.TotalCost, 1e-10)
}

// requireGatewayRecordUsageBillingRepoStub returns the native fixture's
// financial spy after asserting its concrete type. A mismatched fixture fails
// the test assertion; callers inspect its retained command and invocation
// observations after recording.
func requireGatewayRecordUsageBillingRepoStub(t *testing.T, svc *completiontestkit.Recording) *completiontestkit.SettlementStore {
	t.Helper()

	billingRepo, ok := svc.Dependencies.Funds.(*completiontestkit.SettlementStore)
	require.True(t, ok)
	return billingRepo
}

// newGatewayRecordUsageServiceWithResolverForTest builds the native recording
// fixture with a production-style catalogue resolver and group factor one. The
// caller owns the returned mutable fixture and key for synchronous tier
// assertions.
func newGatewayRecordUsageServiceWithResolverForTest(usageRepo usagecore.UsageLogRepository) (*completiontestkit.Recording, *apikey.APIKey) {
	svc := newGatewayRecordUsageServiceForTest(usageRepo, &completiontestkit.UserStore{}, &completiontestkit.SubscriptionStore{})
	svc.Dependencies.Prices = billingtestkit.PriceResolver(nil, svc.Dependencies.Calculator)
	groupID := int64(7)
	return svc, &apikey.APIKey{ID: 1, GroupID: &groupID, Group: &routing.Group{ID: groupID, RateMultiplier: 1.0}}
}

// newGatewayRecordUsageServiceForTest builds native recording with a fresh
// financial spy and default resolver. The legacy user/subscription repository
// arguments are unused; financial observations come from the bound completion
// Store.
func newGatewayRecordUsageServiceForTest(logs usagecore.UsageLogRepository, _ identity.UserRepository, _ billing.UserSubscriptionRepository) *completiontestkit.Recording {
	return completiontestkit.NewRecording(logs, &completiontestkit.SettlementStore{}, nil, true)
}

// newGatewayRecordUsageServiceWithBillingRepoForTest binds the supplied
// financial store and analysis repository to native recording. The legacy
// user/subscription arguments are unused, and configured pricing is left for
// the individual test to install.
func newGatewayRecordUsageServiceWithBillingRepoForTest(logs usagecore.UsageLogRepository, funds completion.Store, _ identity.UserRepository, _ billing.UserSubscriptionRepository) *completiontestkit.Recording {
	return completiontestkit.NewRecording(logs, funds, nil, false)
}

// requireOpenAIRecordUsageBillingRepoStub returns the OpenAI fixture's
// financial spy after checking its concrete type. Callers use its retained
// command, result, and counts for synchronous recording assertions rather than
// inspecting legacy repositories.
func requireOpenAIRecordUsageBillingRepoStub(t *testing.T, svc *completiontestkit.Recording) *completiontestkit.SettlementStore {
	t.Helper()

	billingRepo, ok := svc.Dependencies.Funds.(*completiontestkit.SettlementStore)
	require.True(t, ok)
	return billingRepo
}

// expectedOpenAICost calculates the expected model amount from inclusive OpenAI
// usage for a synchronous assertion. Callers supply a fixture with available
// model pricing; calculator errors fail the calling test through require.NoError.
//
// Cache read and write observations are subsets of the reported input, so both
// are subtracted before pricing ordinary input. Excess cache counts floor that
// ordinary bucket at zero. Output and the two cache buckets keep their supplied
// quantities, and the caller's multiplier scales the resulting amount. This
// comparison therefore avoids billing the cache subsets again as ordinary input.
func expectedOpenAICost(t *testing.T, svc *completiontestkit.Recording, model string, usage openai.ForwardUsage, multiplier float64) *pricing.CostBreakdown {
	t.Helper()

	// Separate both cache buckets from inclusive input before deriving expected cost,
	// so the assertion cannot count cache tokens again at the ordinary input rate.
	cost, err := svc.Dependencies.Calculator.CalculateCost(model, pricing.UsageTokens{
		InputTokens:         max(usage.InputTokens-usage.CacheReadInputTokens-usage.CacheCreationInputTokens, 0),
		OutputTokens:        usage.OutputTokens,
		CacheCreationTokens: usage.CacheCreationInputTokens,
		CacheReadTokens:     usage.CacheReadInputTokens,
	}, multiplier)
	require.NoError(t, err)
	return cost
}

// swapInOpenAILadderCatalog replaces the fixture calculator with prices parsed
// from the fixed above-272k JSON catalogue. It mutates the caller-owned
// recording fixture before the test records usage; no remote refresh is
// started.
func swapInOpenAILadderCatalog(t *testing.T, svc *completiontestkit.Recording) {
	t.Helper()
	svc.Dependencies.Calculator = NewBillingService(newStubCatalogFromJSON(t, openAILadderCatalogJSON))
}

// newOpenAITokenImageConfigPricingResolverForTest installs a token card with
// separate ordinary input, output, and image-output prices for one model/group.
// The returned resolver combines that configuration with the fixed historical
// catalogue fixture.
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
	return billingtestkit.PriceResolver(cs, NewBillingService(nil))
}

// newOpenAIRecordUsageServiceForTest builds OpenAI recording with a fresh
// financial spy, optional balance-rate reader, and default price resolver.
// Legacy user/subscription repository arguments are unused; tests inspect
// financial Store and analysis writer observations.
func newOpenAIRecordUsageServiceForTest(logs usagecore.UsageLogRepository, _ identity.UserRepository, _ billing.UserSubscriptionRepository, rates billing.UserGroupRateRepository) *completiontestkit.Recording {
	return completiontestkit.NewRecording(logs, &completiontestkit.SettlementStore{}, rates, true)
}

// newOpenAIRecordUsageServiceWithBillingRepoForTest binds supplied funds and
// analysis delegates with an optional balance-rate reader. The legacy
// user/subscription arguments are unused, and the returned mutable fixture lets
// tests install their own prices or parent lookup.
func newOpenAIRecordUsageServiceWithBillingRepoForTest(logs usagecore.UsageLogRepository, funds completion.Store, _ identity.UserRepository, _ billing.UserSubscriptionRepository, rates billing.UserGroupRateRepository) *completiontestkit.Recording {
	return completiontestkit.NewRecording(logs, funds, rates, false)
}

// testImageModelPricing returns a wildcard image-mode card containing the
// supplied labeled per-image dollar rates. Price pointers remain shared by the
// constructed fixture, so callers keep their values stable during recording.
func testImageModelPricing(prices map[string]*float64) []routing.ModelPricingEntry {
	return testMediaModelPricing(routing.BillingModeImage, prices)
}

// testVideoModelPricing returns a wildcard video-mode card containing the
// supplied labeled per-second dollar rates. Price pointers remain shared by the
// constructed fixture, so callers keep their values stable during recording.
func testVideoModelPricing(prices map[string]*float64) []routing.ModelPricingEntry {
	return testMediaModelPricing(routing.BillingModeVideo, prices)
}

// testMediaModelPricing builds one wildcard media card from labeled rate
// pointers. Map iteration leaves interval order unspecified, and the pointers
// remain shared; fixture callers supply distinct labels and keep prices stable
// during recording.
func testMediaModelPricing(mode routing.BillingMode, prices map[string]*float64) []routing.ModelPricingEntry {
	card := routing.ModelPricingEntry{Models: []string{"*"}, BillingMode: mode}
	for tier, price := range prices {
		card.Intervals = append(card.Intervals, routing.PricingInterval{TierLabel: tier, PerRequestPrice: price})
	}
	return []routing.ModelPricingEntry{card}
}

// newModelCatalogFixture creates an unstarted query service with the supplied
// parsed price map and model-identity options. The snapshot constructor takes
// ownership of that map; callers stop mutating its entries after construction.
func newModelCatalogFixture(fixture modelCatalogFixture) *provider.Service {
	return provider.NewServiceFromSnapshot(provider.Options{
		ModelLookupCandidates: modelidentity.CandidatesFactory,
	}, nil, provider.Snapshot{Data: fixture.pricingData})
}

// newStubCatalogFromJSON decodes and validates fixture catalogue entries with
// production parsing functions before constructing a query service. Encoding,
// validation, or parsing failures fail the test; the result starts no remote
// refresh work.
func newStubCatalogFromJSON(t *testing.T, body string) *provider.Service {
	t.Helper()
	raw, err := pricing.DecodeCatalogEntries([]byte(body))
	require.NoError(t, err)
	data, diagnostics, err := pricing.ParsePricingEntries(raw)
	require.NoError(t, diagnostics.ValidationError())
	require.NoError(t, err)
	service := newModelCatalogFixture(modelCatalogFixture{pricingData: data})
	return service
}

// configureBillingGroup installs fixture settings and model cards for the
// supplied group's ID, retaining the prior resolver as a delegate. It mutates
// the recording fixture and returns the same group; callers keep referenced
// settings/cards stable.
func configureBillingGroup(svc *completiontestkit.Recording, group *routing.Group, settings pricing.BillingSettings, cards []routing.ModelPricingEntry) *routing.Group {
	source := configuredPrices{base: svc.Dependencies.Prices, groupID: group.ID, settings: settings, cards: cards}
	svc.Dependencies.Prices = billing.NewPriceResolver(source, svc.Dependencies.Calculator, nil, nil)
	return group
}

// GetEffectiveBillingSettings returns cloned settings for the fixture's owning
// group, then delegates other groups to the prior resolver. A nil prior
// resolver supplies production default settings, while cloned optional prices
// isolate the returned values.
func (s configuredPrices) GetEffectiveBillingSettings(ctx context.Context, id int64) pricing.BillingSettings {
	if id == s.groupID {
		return s.settings.Clone()
	}
	return s.base.BillingSettings(ctx, &id)
}

// GetEffectiveConfigModelPricing returns a cloned matching owning-group card
// before consulting the prior resolver's normalized lookup. A missing match or
// different group can delegate; no prior resolver or available card yields nil
// pricing evidence.
func (s configuredPrices) GetEffectiveConfigModelPricing(ctx context.Context, id int64, model string) *pricing.ModelPricingEntry {
	if id == s.groupID {
		if card := pricing.MatchPriceCard(s.cards, model); card != nil {
			return card
		}
	}
	if s.base != nil {
		return s.base.LookupConfigPricingNormalized(ctx, id, model)
	}
	return nil
}

// TestRecordFundingPricingInstant supplies captured subscription pricing at an
// off-peak turn instant while recording at peak time. Native recording must use
// the current clock, whereas OpenAI honors PricingAt and otherwise uses that clock.
func TestRecordFundingPricingInstant(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.Local)
	turnAt := now.Add(-2 * time.Hour)
	for _, tc := range []struct {
		name      string    // descriptive pricing instant case
		openAI    bool      // inclusive OpenAI bucket interpretation
		pricingAt time.Time // captured turn pricing instant
		wantPeak  float64   // expected token peak factor
	}{
		{name: "anthropic uses current time", pricingAt: turnAt, wantPeak: 3},
		{name: "openai uses captured turn time", openAI: true, pricingAt: turnAt, wantPeak: 1},
		{name: "openai defaults to current time", openAI: true, wantPeak: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := &completiontestkit.UsageLogStore{Inserted: true}
			funds := &completiontestkit.SettlementStore{}
			svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(logs, funds, nil, nil, nil)
			clockCalls := 0
			svc.Options.Now = func() time.Time {
				clockCalls++
				return now
			}
			groupID := int64(7)
			configureBillingGroup(svc, &routing.Group{ID: groupID}, pricing.BillingSettings{
				PeakRateEnabled:    true,
				PeakStart:          "11:59",
				PeakEnd:            "12:01",
				PeakRateMultiplier: 3,
			}, nil)
			input := &completion.Input{
				RequestID: "funding-pricing-instant",
				PricingAt: tc.pricingAt,
				Result: &completion.Result{
					Model: "gpt-5.4",
					Usage: completion.TokenUsage{InputTokens: 10, OutputTokens: 2},
				},
				APIKey: &completion.KeySnapshot{
					ID: 2, GroupID: &groupID,
					Group: &completion.GroupSnapshot{ID: groupID, RateMultiplier: 1.4},
				},
				User:     &completion.PayerSnapshot{ID: 1},
				Provider: &completion.ProviderSnapshot{ID: 3, RateMultiplier: 1},
				Subscription: &billing.UserSubscription{
					ID: 99,
					Plan: &billing.SubscriptionPlan{
						GroupIDs:             []int64{groupID},
						GroupRateMultipliers: map[int64]float64{groupID: 0.5},
					},
				},
			}
			recorder := svc.Core(nil, tc.openAI)
			if tc.openAI {
				require.NoError(t, recorder.RecordOpenAI(t.Context(), input))
			} else {
				opts := &completion.PricingOptions{PricingAt: turnAt}
				require.NoError(t, recorder.RecordAnthropic(t.Context(), input, opts))
				require.Equal(t, now, opts.PricingAt)
			}
			require.Equal(t, 1, clockCalls)
			require.Zero(t, funds.ResolveCalls)
			require.Equal(t, 1, funds.Calls)
			require.Equal(t, 1, logs.Calls)
			require.Equal(t, 0.5*tc.wantPeak, logs.LastLog.RateMultiplier)
			require.Equal(t, tc.wantPeak, funds.LastCmd.SubscriptionRateMultiplierScale)
			require.Equal(t, 1.4*tc.wantPeak, funds.LastCmd.SubscriptionRateMultiplier)
			require.Equal(t, 1.4*tc.wantPeak, funds.LastCmd.BalanceRateMultiplier)
			require.Positive(t, logs.LastLog.TotalCost)
			// Tolerate rounding from multiplying the independently calculated base by
			// the captured plan factor and the protocol-selected peak window factor.
			require.InDelta(t, logs.LastLog.TotalCost*0.5*tc.wantPeak, funds.LastCmd.BillableAmountUSD, 1e-12)
		})
	}
}

// TestCatalogOnlyCompletionUsesCapturedPricingTime sends dated catalog usage
// through each completion price path without a group resolver. Both prices
// follow the recorded instant instead of the live calculation clock.
func TestCatalogOnlyCompletionUsesCapturedPricingTime(t *testing.T) {
	model := "dated-catalog-model"
	service := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*pricing.CatalogModelPricing{
		model: {
			InputCostPerToken: 1e-6, OutputCostPerToken: 1e-6,
			CatalogRules: pricing.CatalogRules{ServiceTierPrices: []pricing.CatalogServicePrice{
				{After: "2026-12-01", Zone: "UTC", Factor: new(2.0)},
			}},
		},
	}})
	recorder := completion.NewRecorder(
		completion.Dependencies{Calculator: billingtestkit.Calculator(service, nil)},
		completion.RecorderOptions{DefaultMultiplier: 1},
	)
	result := &completion.Result{Model: model, Usage: completion.TokenUsage{InputTokens: 1000000, OutputTokens: 1000000}}
	key := &completion.KeySnapshot{}
	at := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

	cost := recorder.CalculateTokenCost(context.Background(), result, key, nil, model, model, "", model, 1, &completion.PricingOptions{PricingAt: at})
	require.InDelta(t, 4, cost.TotalCost, 1e-12)
	tokens := pricing.UsageTokens{InputTokens: 1000000, OutputTokens: 1000000}
	cost, err := recorder.CalculateOpenAIRecordUsageTokenCostAt(context.Background(), key, model, 1, at, tokens, "", "")
	require.NoError(t, err)
	require.InDelta(t, 4, cost.TotalCost, 1e-12)
}

// TestV2GPTImageMiniUsesMeasuredImageTokenPrices restores default settlement
// from the bundled directory. A native Images result with no configured
// per-image amount must charge its text, image, and cached input buckets
// together with the image output.
func TestV2GPTImageMiniUsesMeasuredImageTokenPrices(t *testing.T) {
	catalog := provider.NewService(provider.Options{DataDir: t.TempDir()}, nil)
	require.NoError(t, catalog.Initialize())
	t.Cleanup(catalog.Stop)

	const model = "gpt-image-1-mini"
	require.Equal(t, []string{"image"}, *catalog.ModelAttributes(model).OutputModalities)
	recorder := completion.NewRecorder(
		completion.Dependencies{Calculator: billingtestkit.Calculator(catalog, nil)},
		completion.RecorderOptions{DefaultMultiplier: 1},
	)
	usage := completion.TokenUsage{
		InputTokens: 210, OutputTokens: 200, CacheReadInputTokens: 30,
		ImageInputTokens: 100, ImageCacheReadTokens: 20, ImageOutputTokens: 200,
	}
	result := &completion.Result{Model: model, UpstreamModel: model, ImageCount: 1, Usage: usage}
	tokens := pricing.UsageTokens{
		InputTokens: usage.InputTokens - usage.CacheReadInputTokens, OutputTokens: usage.OutputTokens,
		CacheReadTokens: usage.CacheReadInputTokens, ImageInputTokens: usage.ImageInputTokens,
		ImageCacheReadTokens: usage.ImageCacheReadTokens, ImageOutputTokens: usage.ImageOutputTokens,
	}
	cost, err := recorder.CalculateOpenAIRecordUsageCostAt(
		t.Context(), result, &completion.KeySnapshot{}, []string{model}, 1, 1, 1, 1, tokens, "", time.Time{},
	)
	require.NoError(t, err)
	require.Equal(t, string(completion.BillingModeToken), cost.BillingMode)
	require.InDelta(t, 0.0002, cost.InputCost, 1e-12)
	require.InDelta(t, 0.0002, cost.ImageInputCost, 1e-12)
	require.InDelta(t, 0.000007, cost.CacheReadCost, 1e-12)
	require.InDelta(t, 0.0016, cost.ImageOutputCost, 1e-12)
	require.InDelta(t, 0.002007, cost.ActualCost, 1e-12)
}
