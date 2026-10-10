package catalogue_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
)

// modelRateLimitsKey 是模型限流快照的字段名。
const modelRateLimitsKey = "model_rate_limits"

type requestableModelsPricingConfigRepoStub struct {
	routing.PricingConfigRepository
	err error
}

// sequencedRequestableModelsProviderRepoStub 模拟第一次提供商查询失败、第二次查询恢复。
type sequencedRequestableModelsProviderRepoStub struct {
	catalogueRows
	providers []providercore.Record
	calls     int
}

// TestResolveRequestableModels_RequiresModelLevelSchedulability 验证可见模型至少存在一个未被模型级限流的提供商。
func TestResolveRequestableModels_RequiresModelLevelSchedulability(t *testing.T) {
	groupID := int64(4120)
	pricingConfig := routingtestkit.Configuration{
		ID:           70,
		Status:       billing.StatusActive,
		ModelMapping: map[string]string{"client-alias": "group-model"},
	}
	future := time.Now().Add(10 * time.Minute).Format(time.RFC3339)
	limitedProvider := providercore.Record{
		ID:       80,
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"group-model": "upstream-model"},
			"model_whitelist": []any{"upstream-model"},
		},
		Extra: map[string]any{
			modelRateLimitsKey: map[string]any{
				"upstream-model": map[string]any{"rate_limit_reset_at": future},
			},
		},
	}
	healthyProvider := limitedProvider
	healthyProvider.ID = 81
	healthyProvider.Extra = nil
	pricingConfigService := routingtestkit.PricingConfig(groupID, capability.PlatformOpenAI, pricingConfig)

	t.Run("全部提供商均被模型限流时隐藏", func(t *testing.T) {
		svc := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: {limitedProvider}}}, pricingConfigService, nil)
		result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
		require.NotContains(t, routing.RequestableModelIDs(result.Models), "client-alias")
	})

	t.Run("至少一个健康提供商时保留", func(t *testing.T) {
		svc := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: {limitedProvider, healthyProvider}}}, pricingConfigService, nil)
		result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
		require.Contains(t, routing.RequestableModelIDs(result.Models), "client-alias")
	})
}

func (s *requestableModelsPricingConfigRepoStub) ListAll(context.Context) ([]routing.PricingConfig, error) {
	return nil, s.err
}

// ListSchedulableByGroupID 在首次调用返回临时错误，后续调用返回当前提供商快照。
func (s *sequencedRequestableModelsProviderRepoStub) ListSchedulableByGroupID(context.Context, int64) ([]providercore.Record, error) {
	s.calls++
	if s.calls == 1 {
		return nil, errors.New("temporary provider query failure")
	}
	return append([]providercore.Record(nil), s.providers...), nil
}

// requestableModelByID 从解析结果中查找指定客户端模型。
func requestableModelByID(models []routing.RequestableModel, id string) (routing.RequestableModel, bool) {
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	return routing.RequestableModel{}, false
}

func TestResolveRequestableModels_UsesConfiguredPricingBasis(t *testing.T) {
	groupID := int64(4101)
	inputPrice := 0.01
	for _, test := range []struct {
		name          string
		billingSource string
		pricingModel  string
	}{
		{name: "requested", billingSource: routing.BillingModelSourceRequested, pricingModel: "client-alias"},
		{name: "channel mapped", billingSource: routing.BillingModelSourceGroupMapped, pricingModel: "group-model"},
		{name: "upstream", billingSource: routing.BillingModelSourceUpstream, pricingModel: "upstream-model"},
	} {
		t.Run(test.name, func(t *testing.T) {
			pricingConfig := routingtestkit.Configuration{
				ID:                 51,
				Status:             billing.StatusActive,
				BillingModelSource: test.billingSource,
				RestrictModels:     true,
				ModelMapping:       map[string]string{"client-alias": "group-model"},
				ModelPricing: []routing.ModelPricingEntry{{
					Models:     []string{test.pricingModel},
					InputPrice: &inputPrice,
				}},
			}
			provider := providercore.Record{
				ID:       61,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"model_mapping":   map[string]any{"group-model": "upstream-model"},
					"model_whitelist": []any{"upstream-model"},
				},
			}
			repo := &modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: {provider}}}
			svc := newCatalogueFixture(repo, routingtestkit.PricingConfig(groupID, capability.PlatformOpenAI, pricingConfig), nil)

			result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
			model, ok := requestableModelByID(result.Models, "client-alias")
			require.True(t, ok)
			require.True(t, result.Restricted)
			require.Equal(t, test.pricingModel, model.PricingModel)
			require.False(t, model.PricingAmbiguous)
		})
	}
}

func TestResolveRequestableModels_WildcardsMatchConcreteCandidateOnly(t *testing.T) {
	groupID := int64(4102)
	price := 0.02
	pricingConfig := routingtestkit.Configuration{
		ID:                 52,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceGroupMapped,
		RestrictModels:     true,
		ModelMapping:       map[string]string{"client-*": "group-model"},
		ModelPricing: []routing.ModelPricingEntry{{
			Models:     []string{"group-*"},
			InputPrice: &price,
		}},
	}
	provider := providercore.Record{
		ID:       62,
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"client-one": "client-one",
				"group-*":    "upstream-model",
			},
			"model_whitelist": []any{"upstream-model"},
		},
	}
	svc := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: {provider}}}, routingtestkit.PricingConfig(groupID, capability.PlatformOpenAI, pricingConfig), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
	model, ok := requestableModelByID(result.Models, "client-one")
	require.True(t, ok)
	require.Equal(t, "group-model", model.PricingModel)
	require.NotContains(t, routing.RequestableModelIDs(result.Models), "client-*")
	require.NotContains(t, routing.RequestableModelIDs(result.Models), "group-*")
}

func TestResolveRequestableModels_UnrestrictedProviderAddsDefaultsAndMappingSource(t *testing.T) {
	groupID := int64(4103)
	provider := providercore.Record{
		ID:       63,
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"CUSTOM-Alias": "gpt-5.5",
				"custom-alias": "gpt-5.6",
				"gpt-*":        "gpt-5.5",
			},
		},
	}
	svc := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: {provider}}}, nil, nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
	ids := routing.RequestableModelIDs(result.Models)
	require.Contains(t, ids, "CUSTOM-Alias")
	require.Contains(t, ids, "gpt-5.5")
	require.NotContains(t, ids, "custom-alias")
	require.NotContains(t, ids, "gpt-*")
}

func TestResolveRequestableModels_QoderUsesSchedulableProviderSiteUnion(t *testing.T) {
	groupID := int64(4121)
	global := providercore.Record{
		ID:          90,
		Platform:    capability.PlatformQoder,
		Type:        capability.ProviderTypeCosy,
		Credentials: map[string]any{"site": "global"},
	}
	cn := providercore.Record{
		ID:          91,
		Platform:    capability.PlatformQoder,
		Type:        capability.ProviderTypeCosy,
		Credentials: map[string]any{"site": "cn"},
	}
	resolve := func(providers ...providercore.Record) []string {
		svc := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: providers}}, nil, nil)
		result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformQoder)
		return routing.RequestableModelIDs(result.Models)
	}

	globalIDs := resolve(global)
	require.Contains(t, globalIDs, "claude-opus-4-6")
	require.Contains(t, globalIDs, "qwen3.8-max")
	require.NotContains(t, globalIDs, "qwen3.8-max-preview")
	require.NotContains(t, globalIDs, "qwen3.6-flash")
	require.NotContains(t, globalIDs, "minimax-m2.7")

	cnIDs := resolve(cn)
	require.Contains(t, cnIDs, "qwen3.8-max")
	require.NotContains(t, cnIDs, "qwen3.8-max-preview")
	require.Contains(t, cnIDs, "qwen3.6-flash")
	require.Contains(t, cnIDs, "minimax-m2.7")
	require.NotContains(t, cnIDs, "claude-opus-4-6")
	require.NotContains(t, cnIDs, "minimax-m3")

	mixedIDs := resolve(global, cn)
	require.Contains(t, mixedIDs, "claude-opus-4-6")
	require.Contains(t, mixedIDs, "qwen3.6-flash")
	require.Contains(t, mixedIDs, "minimax-m3")
	require.Contains(t, mixedIDs, "minimax-m2.7")
	require.NotContains(t, mixedIDs, "qwen3.8-max-preview")
	qwen38Count := 0
	for _, model := range mixedIDs {
		if model == "qwen3.8-max" {
			qwen38Count++
		}
	}
	require.Equal(t, 1, qwen38Count, "两站模型并集只能包含一个 Qwen3.8-Max")

	cn.Credentials["model_mapping"] = map[string]any{"claude-opus-4-6": "ultimate"}
	require.NotContains(t, resolve(cn), "claude-opus-4-6", "显式映射不能扩大CN站点硬能力")
}

func TestResolveRequestableModels_ProviderWhitelistRemovesUnsupportedCandidate(t *testing.T) {
	groupID := int64(4104)
	pricingConfig := routingtestkit.Configuration{
		ID:           54,
		Status:       billing.StatusActive,
		ModelMapping: map[string]string{"blocked-alias": "blocked-final"},
	}
	provider := providercore.Record{
		ID:       64,
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_whitelist": []any{"allowed-final"},
		},
	}
	svc := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: {provider}}}, routingtestkit.PricingConfig(groupID, capability.PlatformOpenAI, pricingConfig), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
	require.NotContains(t, routing.RequestableModelIDs(result.Models), "blocked-alias")
}

func TestResolveRequestableModels_UpstreamPricingAmbiguousAcrossProviders(t *testing.T) {
	groupID := int64(4105)
	pricingConfig := routingtestkit.Configuration{
		ID:                 55,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceUpstream,
		ModelMapping:       map[string]string{"client-alias": "group-model"},
	}
	providers := []providercore.Record{
		{ID: 65, Platform: capability.PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"group-model": "upstream-a"}}},
		{ID: 66, Platform: capability.PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"group-model": "upstream-b"}}},
	}
	svc := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: providers}}, routingtestkit.PricingConfig(groupID, capability.PlatformOpenAI, pricingConfig), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
	model, ok := requestableModelByID(result.Models, "client-alias")
	require.True(t, ok)
	require.Empty(t, model.PricingModel)
	require.True(t, model.PricingAmbiguous)
}

func TestResolveRequestableModels_UpstreamUsesBedrockRegionalModel(t *testing.T) {
	groupID := int64(4114)
	price := 0.08
	upstreamModel := "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
	pricingConfig := routingtestkit.Configuration{
		ID:                 62,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceUpstream,
		RestrictModels:     true,
		ModelMapping:       map[string]string{"claude-sonnet-4-5": "claude-sonnet-4-5"},
		ModelPricing: []routing.ModelPricingEntry{{
			Models:     []string{upstreamModel},
			InputPrice: &price,
		}},
	}
	provider := providercore.Record{
		ID:       74,
		Platform: capability.PlatformAnthropic,
		Type:     capability.ProviderTypeBedrock,
		Credentials: map[string]any{
			"aws_region": "us-east-1",
		},
	}
	svc := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: {provider}}}, routingtestkit.PricingConfig(groupID, capability.PlatformAnthropic, pricingConfig), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformAnthropic)
	model, ok := requestableModelByID(result.Models, "claude-sonnet-4-5")
	require.True(t, ok)
	require.Equal(t, upstreamModel, model.PricingModel)
	require.False(t, model.PricingAmbiguous)
}

func TestResolveRequestableModels_UpstreamMarksAntigravityThinkingVariantAmbiguous(t *testing.T) {
	groupID := int64(4115)
	pricingConfig := routingtestkit.Configuration{
		ID:                 63,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceUpstream,
	}
	provider := providercore.Record{ID: 75, Platform: capability.PlatformAntigravity, Credentials: map[string]any{"model_mapping": map[string]any{"claude-sonnet-4-5": "claude-sonnet-4-5"}}}
	svc := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: {provider}}}, routingtestkit.PricingConfig(groupID, capability.PlatformAntigravity, pricingConfig), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformAntigravity)
	model, ok := requestableModelByID(result.Models, "claude-sonnet-4-5")
	require.True(t, ok)
	require.Empty(t, model.PricingModel)
	require.True(t, model.PricingAmbiguous)
}

func TestResolveRequestableModels_UpstreamNormalizesAnthropicOAuthMapping(t *testing.T) {
	groupID := int64(4116)
	pricingConfig := routingtestkit.Configuration{
		ID:                 64,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceUpstream,
	}
	provider := providercore.Record{
		ID:       76,
		Platform: capability.PlatformAnthropic,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"client-alias": "claude-sonnet-4-5"},
		},
	}
	svc := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: {provider}}}, routingtestkit.PricingConfig(groupID, capability.PlatformAnthropic, pricingConfig), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformAnthropic)
	model, ok := requestableModelByID(result.Models, "client-alias")
	require.True(t, ok)
	require.Equal(t, "claude-sonnet-4-5", model.PricingModel)
	require.False(t, model.PricingAmbiguous)
}

// TestResolveRequestableModels_OpenAIUsesActualForwardedModel 检查 OpenAI OAuth 与自动透传提供商按最终转发模型定价。
func TestResolveRequestableModels_OpenAIUsesActualForwardedModel(t *testing.T) {
	price := 0.09
	tests := []struct {
		name               string
		groupID            int64
		pricingConfigModel string
		pricingModel       string
		provider           providercore.Record
	}{
		{
			name:               "OAuth 显式后缀映射",
			groupID:            4118,
			pricingConfigModel: "gpt-5.6-sol-high",
			pricingModel:       "gpt-5.6-sol",
			provider: providercore.Record{
				ID:          78,
				Credentials: map[string]any{"model_mapping": map[string]any{"gpt-5.6-sol-high": "gpt-5.6-sol"}},
				Platform:    capability.PlatformOpenAI,
				Type:        capability.ProviderTypeOAuth,
			},
		},
		{
			name:               "自动透传保留提供商映射",
			groupID:            4119,
			pricingConfigModel: "passthrough-model",
			pricingModel:       "mapped-model",
			provider: providercore.Record{
				ID:       79,
				Platform: capability.PlatformOpenAI,
				Type:     capability.ProviderTypeOAuth,
				Extra:    map[string]any{"openai_passthrough": true},
				Credentials: map[string]any{
					"model_mapping": map[string]any{"passthrough-model": "mapped-model"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pricingConfig := routingtestkit.Configuration{
				ID:                 tt.groupID,
				Status:             billing.StatusActive,
				BillingModelSource: routing.BillingModelSourceUpstream,
				RestrictModels:     true,
				ModelMapping:       map[string]string{"client-alias": tt.pricingConfigModel},
				ModelPricing: []routing.ModelPricingEntry{{
					Models:     []string{tt.pricingModel},
					InputPrice: &price,
				}},
			}
			svc := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{tt.groupID: {tt.provider}}}, routingtestkit.PricingConfig(tt.groupID, capability.PlatformOpenAI, pricingConfig), nil)

			snapshots, err := svc.Read(context.Background(), &tt.groupID)
			require.NoError(t, err)
			require.True(t, snapshots[0].Rules.Supports(context.Background(), tt.pricingConfigModel), "提供商模型范围应接受最终规范模型")
			require.Equal(t, []string{tt.pricingModel}, snapshots[0].Rules.UpstreamModels(context.Background(), tt.pricingConfigModel), "目录应使用实际转发模型")
			result := svc.ResolveRequestableModels(context.Background(), &tt.groupID, capability.PlatformOpenAI)
			model, ok := requestableModelByID(result.Models, "client-alias")
			require.True(t, ok)
			require.Equal(t, tt.pricingModel, model.PricingModel)
			require.False(t, model.PricingAmbiguous)
		})
	}
}

func TestResolveRequestableModels_RestrictionEmptyDoesNotFallBack(t *testing.T) {
	groupID := int64(4106)
	pricingConfig := routingtestkit.Configuration{
		ID:                 56,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceRequested,
		RestrictModels:     true,
	}
	provider := providercore.Record{ID: 67, Platform: capability.PlatformOpenAI}
	svc := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: {provider}}}, routingtestkit.PricingConfig(groupID, capability.PlatformOpenAI, pricingConfig), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
	require.True(t, result.Restricted)
	require.Empty(t, result.Models)
}

func TestResolveRequestableModelsUsesUnifiedPriceSpace(t *testing.T) {
	groupID := int64(4107)
	price := 0.03
	pricingConfig := routingtestkit.Configuration{
		ID:                 57,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceRequested,
		RestrictModels:     true,
		ModelPricing: []routing.ModelPricingEntry{{
			Models:     []string{"same-model"},
			InputPrice: &price,
		}},
	}
	provider := providercore.Record{
		ID:       68,
		Platform: capability.PlatformAnthropic,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"same-model": "same-model"},
		},
	}
	svc := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: {provider}}}, routingtestkit.PricingConfig(groupID, capability.PlatformAnthropic, pricingConfig), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformAnthropic)
	require.True(t, result.Restricted)
	require.Equal(t, []string{"same-model"}, routing.RequestableModelIDs(result.Models))
}

func TestResolveRequestableModels_QoderRequiresEffectivePricing(t *testing.T) {
	groupID := int64(4108)
	effectivePrice := 0.04
	pricingConfig := routingtestkit.Configuration{
		ID:                 58,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceRequested,
		RestrictModels:     true,
		ModelPricing: []routing.ModelPricingEntry{
			{Models: []string{"qoder-model"}},
			{Models: []string{"qoder-*"}, InputPrice: &effectivePrice},
		},
	}
	provider := providercore.Record{ID: 69, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, Credentials: map[string]any{"model_mapping": map[string]any{"qoder-model": "qmodel"}, "model_whitelist": []string{"qmodel"}}}
	svc := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: {provider}}}, routingtestkit.PricingConfig(groupID, capability.PlatformQoder, pricingConfig), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformQoder)
	require.Contains(t, routing.RequestableModelIDs(result.Models), "qoder-model")
}

func TestResolveRequestableModels_ProviderQueryFailureKeepsEmpty(t *testing.T) {
	groupID := int64(4109)
	svc := newCatalogueFixture(&modelsListProviderRepoStub{err: errors.New("temporary failure")}, nil, nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
	require.True(t, result.Restricted)
	require.Empty(t, routing.RequestableModelIDs(result.Models))
}

// TestResolveRequestableModels_NextQueryRecoversProviderFailure 提供商读取失败后，下一次请求读取已恢复的配置。
func TestResolveRequestableModels_NextQueryRecoversProviderFailure(t *testing.T) {
	groupID := int64(4121)
	repo := &sequencedRequestableModelsProviderRepoStub{providers: []providercore.Record{{
		ID:       82,
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_whitelist": []any{"private-model"},
		},
	}}}
	svc := newCatalogueFixture(repo, nil, nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)

	require.Equal(t, 1, repo.calls)
	require.True(t, result.Restricted)
	require.Empty(t, result.Models)
	result = svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
	require.Equal(t, 2, repo.calls)
	require.True(t, result.HadExplicitProviderModels)
	require.Equal(t, []string{"private-model"}, routing.RequestableModelIDs(result.Models))
}

func TestResolveRequestableModels_PricingConfigQueryFailureKeepsProviderCandidates(t *testing.T) {
	groupID := int64(4113)
	provider := providercore.Record{
		ID:       73,
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"client-alias": "upstream-model"},
			"model_whitelist": []any{"upstream-model"},
		},
	}
	svc := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: {provider}}}, routing.NewPricingConfigService(&requestableModelsPricingConfigRepoStub{
		err: errors.New("temporary price configuration failure"),
	}, nil, routing.PricingConfigOptions{
		// 价格读取失败不影响独立的分组策略读取。
		ReadGroup: func(context.Context, int64) (*routing.Group, error) { return nil, nil },
		Warn:      slog.Warn,
		Now: time.
			Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	},
	), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
	require.False(t, result.Restricted)
	require.Contains(t, routing.RequestableModelIDs(result.Models), "client-alias")
}

// TestResolveRequestableModels_PricingConfigQueryFailureKeepsEmptyProviderPoolEmpty 验证价格配置读取失败不会为无提供商分组伪造默认模型。
func TestResolveRequestableModels_PricingConfigQueryFailureKeepsEmptyProviderPoolEmpty(t *testing.T) {
	groupID := int64(4117)
	svc := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: {}}}, routing.NewPricingConfigService(&requestableModelsPricingConfigRepoStub{
		err: errors.New("temporary price configuration failure"),
	}, nil, routing.PricingConfigOptions{
		Warn: slog.Warn,
		Now: time.
			Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	},
	), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)

	require.False(t, result.Restricted)
	require.Empty(t, result.Models)
}

func TestModelMarketplaceUsesResolvedGroupMappedPricingModel(t *testing.T) {
	groupID := int64(4110)
	inputPrice := 0.05
	outputPrice := 0.06
	pricingConfig := routingtestkit.Configuration{
		ID:                 59,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceGroupMapped,
		RestrictModels:     true,
		ModelMapping:       map[string]string{"client-alias": "group-model"},
		ModelPricing: []routing.ModelPricingEntry{{
			Models:      []string{"group-model"},
			InputPrice:  &inputPrice,
			OutputPrice: &outputPrice,
		}},
	}
	provider := providercore.Record{
		ID:       70,
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"group-model": "upstream-model"},
			"model_whitelist": []any{"upstream-model"},
		},
	}
	pricingConfigService := routingtestkit.PricingConfig(groupID, capability.PlatformOpenAI, pricingConfig)
	billingService := billingtestkit.Calculator(nil, nil)
	gatewayService := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: {provider}}}, pricingConfigService, cataloguePriceResolver(pricingConfigService, billingService))
	marketplace := newCatalogueMarketplace(nil, gatewayService, billingService)

	models := marketplace.ModelsForGroup(context.Background(), &routing.Group{ID: groupID, RateMultiplier: 1})
	var alias *routing.ModelMarketplaceModel
	for i := range models {
		if models[i].ID == "client-alias" {
			alias = &models[i]
			break
		}
	}
	require.NotNil(t, alias)
	require.Equal(t, "priced", alias.Pricing.PriceStatus)
	require.Equal(t, inputPrice, alias.Pricing.InputPricePerToken)
	require.Equal(t, outputPrice, alias.Pricing.OutputPricePerToken)
}

func TestModelMarketplaceKeepsAmbiguousUpstreamModelUnpriced(t *testing.T) {
	groupID := int64(4111)
	price := 0.07
	pricingConfig := routingtestkit.Configuration{
		ID:                 60,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceUpstream,
		ModelMapping:       map[string]string{"client-alias": "group-model"},
		ModelPricing: []routing.ModelPricingEntry{
			{Models: []string{"upstream-a"}, InputPrice: &price},
			{Models: []string{"upstream-b"}, InputPrice: &price},
		},
	}
	providers := []providercore.Record{
		{ID: 71, Platform: capability.PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"group-model": "upstream-a"}, "model_whitelist": []any{"upstream-a"}}},
		{ID: 72, Platform: capability.PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"group-model": "upstream-b"}, "model_whitelist": []any{"upstream-b"}}},
	}
	pricingConfigService := routingtestkit.PricingConfig(groupID, capability.PlatformOpenAI, pricingConfig)
	billingService := billingtestkit.Calculator(nil, nil)
	gatewayService := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: providers}}, pricingConfigService, cataloguePriceResolver(pricingConfigService, billingService))
	marketplace := newCatalogueMarketplace(nil, gatewayService, billingService)

	models := marketplace.ModelsForGroup(context.Background(), &routing.Group{ID: groupID, RateMultiplier: 1})
	var alias *routing.ModelMarketplaceModel
	for i := range models {
		if models[i].ID == "client-alias" {
			alias = &models[i]
			break
		}
	}
	require.NotNil(t, alias)
	require.Equal(t, "unpriced", alias.Pricing.PriceStatus)
	require.Equal(t, "unknown", alias.Pricing.PricingMode)
}

func TestModelMarketplaceQoderUsesResolvedRequestedPricingModelWithoutRemapping(t *testing.T) {
	groupID := int64(4112)
	groupMappedPrice := 0.08
	pricingConfig := routingtestkit.Configuration{
		ID:                 61,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceRequested,
		ModelMapping:       map[string]string{"client-model": "group-model"},
		ModelPricing: []routing.ModelPricingEntry{{
			Models:     []string{"group-model"},
			InputPrice: &groupMappedPrice,
		}},
	}
	pricingConfigService := routingtestkit.PricingConfig(groupID, capability.PlatformQoder, pricingConfig)
	billingService := billingtestkit.Calculator(nil, nil)
	marketplace := newCatalogueMarketplace(nil, newCatalogueFixture(nil, pricingConfigService, cataloguePriceResolver(pricingConfigService, billingService)), billingService)

	pricing := marketplace.RequestableModelPricing(context.Background(), &routing.Group{ID: groupID, RateMultiplier: 1}, routing.MarketplaceModelDef{ID: "client-model", PricingModel: "client-model"})

	require.Equal(t, "unpriced", pricing.PriceStatus)
	require.Equal(t, "unknown", pricing.PricingMode)
}
