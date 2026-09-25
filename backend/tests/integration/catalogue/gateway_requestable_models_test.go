package catalogue_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// TestResolveRequestableModels_RequiresModelLevelSchedulability 验证可见模型至少存在一个未被模型级限流的账号。
func TestResolveRequestableModels_RequiresModelLevelSchedulability(t *testing.T) {
	groupID := int64(4120)
	channel := routing.Channel{
		ID:     70,
		Status: billing.StatusActive,
		ModelMapping: map[string]map[string]string{
			capability.PlatformOpenAI: {"client-alias": "channel-model"},
		},
	}
	future := time.Now().Add(10 * time.Minute).Format(time.RFC3339)
	limitedAccount := accountcore.Record{
		ID:       80,
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"channel-model": "upstream-model"},
			"model_whitelist": []any{"upstream-model"},
		},
		Extra: map[string]any{
			modelRateLimitsKey: map[string]any{
				"upstream-model": map[string]any{"rate_limit_reset_at": future},
			},
		},
	}
	healthyAccount := limitedAccount
	healthyAccount.ID = 81
	healthyAccount.Extra = nil
	channelService := routingtestkit.Channel(groupID, capability.PlatformOpenAI, channel)

	t.Run("全部账号均被模型限流时隐藏", func(t *testing.T) {
		svc := newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: {limitedAccount}}}, channelService, nil)
		result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
		require.NotContains(t, routing.RequestableModelIDs(result.Models), "client-alias")
	})

	t.Run("至少一个健康账号时保留", func(t *testing.T) {
		svc := newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: {limitedAccount, healthyAccount}}}, channelService, nil)
		result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
		require.Contains(t, routing.RequestableModelIDs(result.Models), "client-alias")
	})
}

type requestableModelsChannelRepoStub struct {
	routing.ChannelRepository
	err error
}

func (s *requestableModelsChannelRepoStub) ListAll(context.Context) ([]routing.Channel, error) {
	return nil, s.err
}

// sequencedRequestableModelsAccountRepoStub 模拟第一次账号查询失败、第二次查询恢复。
type sequencedRequestableModelsAccountRepoStub struct {
	catalogueRows
	accounts []accountcore.Record
	calls    int
}

// ListSchedulableByGroupID 在首次调用返回临时错误，后续调用返回当前账号快照。
func (s *sequencedRequestableModelsAccountRepoStub) ListSchedulableByGroupID(context.Context, int64) ([]accountcore.Record, error) {
	s.calls++
	if s.calls == 1 {
		return nil, errors.New("temporary account query failure")
	}
	return append([]accountcore.Record(nil), s.accounts...), nil
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
		{name: "channel mapped", billingSource: routing.BillingModelSourceChannelMapped, pricingModel: "channel-model"},
		{name: "upstream", billingSource: routing.BillingModelSourceUpstream, pricingModel: "upstream-model"},
	} {
		t.Run(test.name, func(t *testing.T) {
			channel := routing.Channel{
				ID:                 51,
				Status:             billing.StatusActive,
				BillingModelSource: test.billingSource,
				RestrictModels:     true,
				ModelMapping: map[string]map[string]string{
					capability.PlatformOpenAI: {"client-alias": "channel-model"},
				},
				ModelPricing: []routing.ChannelModelPricing{{
					Platform:   capability.PlatformOpenAI,
					Models:     []string{test.pricingModel},
					InputPrice: &inputPrice,
				}},
			}
			account := accountcore.Record{
				ID:       61,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"model_mapping":   map[string]any{"channel-model": "upstream-model"},
					"model_whitelist": []any{"upstream-model"},
				},
			}
			repo := &modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: {account}}}
			svc := newCatalogueFixture(repo, routingtestkit.Channel(groupID, capability.PlatformOpenAI, channel), nil)

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
	channel := routing.Channel{
		ID:                 52,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceChannelMapped,
		RestrictModels:     true,
		ModelMapping: map[string]map[string]string{
			capability.PlatformOpenAI: {"client-*": "channel-model"},
		},
		ModelPricing: []routing.ChannelModelPricing{{
			Platform:   capability.PlatformOpenAI,
			Models:     []string{"channel-*"},
			InputPrice: &price,
		}},
	}
	account := accountcore.Record{
		ID:       62,
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"client-one": "client-one",
				"channel-*":  "upstream-model",
			},
			"model_whitelist": []any{"upstream-model"},
		},
	}
	svc := newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: {account}}}, routingtestkit.Channel(groupID, capability.PlatformOpenAI, channel), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
	model, ok := requestableModelByID(result.Models, "client-one")
	require.True(t, ok)
	require.Equal(t, "channel-model", model.PricingModel)
	require.NotContains(t, routing.RequestableModelIDs(result.Models), "client-*")
	require.NotContains(t, routing.RequestableModelIDs(result.Models), "channel-*")
}

func TestResolveRequestableModels_UnrestrictedAccountAddsDefaultsAndMappingSource(t *testing.T) {
	groupID := int64(4103)
	account := accountcore.Record{
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
	svc := newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: {account}}}, nil, nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
	ids := routing.RequestableModelIDs(result.Models)
	require.Contains(t, ids, "CUSTOM-Alias")
	require.Contains(t, ids, "gpt-5.5")
	require.NotContains(t, ids, "custom-alias")
	require.NotContains(t, ids, "gpt-*")
}

func TestResolveRequestableModels_QoderUsesSchedulableAccountSiteUnion(t *testing.T) {
	groupID := int64(4121)
	global := accountcore.Record{
		ID:          90,
		Platform:    capability.PlatformQoder,
		Type:        capability.AccountTypeCosy,
		Credentials: map[string]any{"site": "global"},
	}
	cn := accountcore.Record{
		ID:          91,
		Platform:    capability.PlatformQoder,
		Type:        capability.AccountTypeCosy,
		Credentials: map[string]any{"site": "cn"},
	}
	resolve := func(accounts ...accountcore.Record) []string {
		svc := newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: accounts}}, nil, nil)
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
	require.Contains(t, resolve(cn), "claude-opus-4-6")
}

func TestResolveRequestableModels_AccountWhitelistRemovesUnsupportedCandidate(t *testing.T) {
	groupID := int64(4104)
	channel := routing.Channel{
		ID:     54,
		Status: billing.StatusActive,
		ModelMapping: map[string]map[string]string{
			capability.PlatformOpenAI: {"blocked-alias": "blocked-final"},
		},
	}
	account := accountcore.Record{
		ID:       64,
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_whitelist": []any{"allowed-final"},
		},
	}
	svc := newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: {account}}}, routingtestkit.Channel(groupID, capability.PlatformOpenAI, channel), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
	require.NotContains(t, routing.RequestableModelIDs(result.Models), "blocked-alias")
}

func TestResolveRequestableModels_UpstreamPricingAmbiguousAcrossAccounts(t *testing.T) {
	groupID := int64(4105)
	channel := routing.Channel{
		ID:                 55,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceUpstream,
		ModelMapping: map[string]map[string]string{
			capability.PlatformOpenAI: {"client-alias": "channel-model"},
		},
	}
	accounts := []accountcore.Record{
		{ID: 65, Platform: capability.PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"channel-model": "upstream-a"}}},
		{ID: 66, Platform: capability.PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"channel-model": "upstream-b"}}},
	}
	svc := newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: accounts}}, routingtestkit.Channel(groupID, capability.PlatformOpenAI, channel), nil)

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
	channel := routing.Channel{
		ID:                 62,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceUpstream,
		RestrictModels:     true,
		ModelMapping: map[string]map[string]string{
			capability.PlatformAnthropic: {"claude-sonnet-4-5": "claude-sonnet-4-5"},
		},
		ModelPricing: []routing.ChannelModelPricing{{
			Platform:   capability.PlatformAnthropic,
			Models:     []string{upstreamModel},
			InputPrice: &price,
		}},
	}
	account := accountcore.Record{
		ID:       74,
		Platform: capability.PlatformAnthropic,
		Type:     capability.AccountTypeBedrock,
		Credentials: map[string]any{
			"aws_region": "us-east-1",
		},
	}
	svc := newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: {account}}}, routingtestkit.Channel(groupID, capability.PlatformAnthropic, channel), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformAnthropic)
	model, ok := requestableModelByID(result.Models, "claude-sonnet-4-5")
	require.True(t, ok)
	require.Equal(t, upstreamModel, model.PricingModel)
	require.False(t, model.PricingAmbiguous)
}

func TestResolveRequestableModels_UpstreamMarksAntigravityThinkingVariantAmbiguous(t *testing.T) {
	groupID := int64(4115)
	channel := routing.Channel{
		ID:                 63,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceUpstream,
	}
	account := accountcore.Record{ID: 75, Platform: capability.PlatformAntigravity}
	svc := newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: {account}}}, routingtestkit.Channel(groupID, capability.PlatformAntigravity, channel), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformAntigravity)
	model, ok := requestableModelByID(result.Models, "claude-sonnet-4-5")
	require.True(t, ok)
	require.Empty(t, model.PricingModel)
	require.True(t, model.PricingAmbiguous)
}

func TestResolveRequestableModels_UpstreamNormalizesAnthropicOAuthMapping(t *testing.T) {
	groupID := int64(4116)
	channel := routing.Channel{
		ID:                 64,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceUpstream,
	}
	account := accountcore.Record{
		ID:       76,
		Platform: capability.PlatformAnthropic,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"client-alias": "claude-sonnet-4-5"},
		},
	}
	svc := newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: {account}}}, routingtestkit.Channel(groupID, capability.PlatformAnthropic, channel), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformAnthropic)
	model, ok := requestableModelByID(result.Models, "client-alias")
	require.True(t, ok)
	require.Equal(t, "claude-sonnet-4-5-20250929", model.PricingModel)
	require.False(t, model.PricingAmbiguous)
}

// TestResolveRequestableModels_OpenAIUsesActualForwardedModel 验证 OpenAI OAuth 与自动透传账号使用真实上游模型定价。
func TestResolveRequestableModels_OpenAIUsesActualForwardedModel(t *testing.T) {
	price := 0.09
	tests := []struct {
		name         string
		groupID      int64
		channelModel string
		pricingModel string
		account      accountcore.Record
	}{
		{
			name:         "OAuth 别名归一化",
			groupID:      4118,
			channelModel: "gpt-5.6-sol-high",
			pricingModel: "gpt-5.6-sol",
			account: accountcore.Record{
				ID:       78,
				Platform: capability.PlatformOpenAI,
				Type:     capability.AccountTypeOAuth,
			},
		},
		{
			name:         "自动透传忽略普通账号映射",
			groupID:      4119,
			channelModel: "passthrough-model",
			pricingModel: "passthrough-model",
			account: accountcore.Record{
				ID:       79,
				Platform: capability.PlatformOpenAI,
				Type:     capability.AccountTypeOAuth,
				Extra:    map[string]any{"openai_passthrough": true},
				Credentials: map[string]any{
					"model_mapping": map[string]any{"passthrough-model": "mapped-model"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := routing.Channel{
				ID:                 tt.groupID,
				Status:             billing.StatusActive,
				BillingModelSource: routing.BillingModelSourceUpstream,
				RestrictModels:     true,
				ModelMapping: map[string]map[string]string{
					capability.PlatformOpenAI: {"client-alias": tt.channelModel},
				},
				ModelPricing: []routing.ChannelModelPricing{{
					Platform:   capability.PlatformOpenAI,
					Models:     []string{tt.pricingModel},
					InputPrice: &price,
				}},
			}
			svc := newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{tt.groupID: {tt.account}}}, routingtestkit.Channel(tt.groupID, capability.PlatformOpenAI, channel), nil)

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
	channel := routing.Channel{
		ID:                 56,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceRequested,
		RestrictModels:     true,
	}
	account := accountcore.Record{ID: 67, Platform: capability.PlatformOpenAI}
	svc := newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: {account}}}, routingtestkit.Channel(groupID, capability.PlatformOpenAI, channel), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
	require.True(t, result.Restricted)
	require.Empty(t, result.Models)
}

func TestResolveRequestableModels_PricingDoesNotCrossPlatforms(t *testing.T) {
	groupID := int64(4107)
	price := 0.03
	channel := routing.Channel{
		ID:                 57,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceRequested,
		RestrictModels:     true,
		ModelPricing: []routing.ChannelModelPricing{{
			Platform:   capability.PlatformOpenAI,
			Models:     []string{"same-model"},
			InputPrice: &price,
		}},
	}
	account := accountcore.Record{
		ID:       68,
		Platform: capability.PlatformAnthropic,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"same-model": "same-model"},
		},
	}
	svc := newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: {account}}}, routingtestkit.Channel(groupID, capability.PlatformAnthropic, channel), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformAnthropic)
	require.True(t, result.Restricted)
	require.Empty(t, result.Models)
}

func TestResolveRequestableModels_QoderRequiresEffectivePricing(t *testing.T) {
	groupID := int64(4108)
	effectivePrice := 0.04
	channel := routing.Channel{
		ID:                 58,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceRequested,
		RestrictModels:     true,
		ModelPricing: []routing.ChannelModelPricing{
			{Platform: capability.PlatformQoder, Models: []string{"qoder-model"}},
			{Platform: capability.PlatformQoder, Models: []string{"qoder-*"}, InputPrice: &effectivePrice},
		},
	}
	account := accountcore.Record{ID: 69, Platform: capability.PlatformQoder}
	svc := newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: {account}}}, routingtestkit.Channel(groupID, capability.PlatformQoder, channel), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformQoder)
	require.Contains(t, routing.RequestableModelIDs(result.Models), "qoder-model")
}

func TestResolveRequestableModels_AccountQueryFailureKeepsFallback(t *testing.T) {
	groupID := int64(4109)
	svc := newCatalogueFixture(&modelsListAccountRepoStub{err: errors.New("temporary failure")}, nil, nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
	require.False(t, result.Restricted)
	require.Contains(t, routing.RequestableModelIDs(result.Models), "gpt-5.5")
}

// TestResolveRequestableModels_SecondAccountQueryRestoresWhitelistCandidates 验证缓存层查询失败后仍使用当前账号白名单。
func TestResolveRequestableModels_SecondAccountQueryRestoresWhitelistCandidates(t *testing.T) {
	groupID := int64(4121)
	repo := &sequencedRequestableModelsAccountRepoStub{accounts: []accountcore.Record{{
		ID:       82,
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_whitelist": []any{"private-model"},
		},
	}}}
	svc := newCatalogueFixture(repo, nil, nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)

	require.Equal(t, 2, repo.calls)
	require.True(t, result.HadExplicitAccountModels)
	require.Equal(t, []string{"private-model"}, routing.RequestableModelIDs(result.Models))
}

func TestResolveRequestableModels_ChannelQueryFailureKeepsAccountCandidates(t *testing.T) {
	groupID := int64(4113)
	account := accountcore.Record{
		ID:       73,
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"client-alias": "upstream-model"},
			"model_whitelist": []any{"upstream-model"},
		},
	}
	svc := newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: {account}}}, routing.NewChannelService(&requestableModelsChannelRepoStub{
		err: errors.New("temporary channel failure"),
	}, nil, routing.ChannelOptions{Warn: slog.Warn,
		Now: time.
			Now, LoadLocation: pricingprovider.
			LoadPricingLocation},
	), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
	require.False(t, result.Restricted)
	require.Contains(t, routing.RequestableModelIDs(result.Models), "client-alias")
}

// TestResolveRequestableModels_ChannelQueryFailureKeepsEmptyAccountPoolEmpty 验证渠道故障不会为无账号分组伪造默认模型。
func TestResolveRequestableModels_ChannelQueryFailureKeepsEmptyAccountPoolEmpty(t *testing.T) {
	groupID := int64(4117)
	svc := newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: {}}}, routing.NewChannelService(&requestableModelsChannelRepoStub{
		err: errors.New("temporary channel failure"),
	}, nil, routing.ChannelOptions{Warn: slog.Warn,
		Now: time.
			Now, LoadLocation: pricingprovider.
			LoadPricingLocation},
	), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)

	require.False(t, result.Restricted)
	require.Empty(t, result.Models)
}

func TestModelMarketplaceUsesResolvedChannelMappedPricingModel(t *testing.T) {
	groupID := int64(4110)
	inputPrice := 0.05
	outputPrice := 0.06
	channel := routing.Channel{
		ID:                 59,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceChannelMapped,
		RestrictModels:     true,
		ModelMapping: map[string]map[string]string{
			capability.PlatformOpenAI: {"client-alias": "channel-model"},
		},
		ModelPricing: []routing.ChannelModelPricing{{
			Platform:    capability.PlatformOpenAI,
			Models:      []string{"channel-model"},
			InputPrice:  &inputPrice,
			OutputPrice: &outputPrice,
		}},
	}
	account := accountcore.Record{
		ID:       70,
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"channel-model": "upstream-model"},
			"model_whitelist": []any{"upstream-model"},
		},
	}
	channelService := routingtestkit.Channel(groupID, capability.PlatformOpenAI, channel)
	billingService := billingtestkit.Calculator(0, nil, nil)
	gatewayService := newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: {account}}}, channelService, cataloguePriceResolver(channelService, billingService))
	marketplace := newCatalogueMarketplace(nil, gatewayService, billingService)

	models := marketplace.ModelsForGroup(context.Background(), &routing.Group{ID: groupID, Platform: capability.PlatformOpenAI, RateMultiplier: 1})
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
	channel := routing.Channel{
		ID:                 60,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceUpstream,
		ModelMapping: map[string]map[string]string{
			capability.PlatformOpenAI: {"client-alias": "channel-model"},
		},
		ModelPricing: []routing.ChannelModelPricing{
			{Platform: capability.PlatformOpenAI, Models: []string{"upstream-a"}, InputPrice: &price},
			{Platform: capability.PlatformOpenAI, Models: []string{"upstream-b"}, InputPrice: &price},
		},
	}
	accounts := []accountcore.Record{
		{ID: 71, Platform: capability.PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"channel-model": "upstream-a"}, "model_whitelist": []any{"upstream-a"}}},
		{ID: 72, Platform: capability.PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"channel-model": "upstream-b"}, "model_whitelist": []any{"upstream-b"}}},
	}
	channelService := routingtestkit.Channel(groupID, capability.PlatformOpenAI, channel)
	billingService := billingtestkit.Calculator(0, nil, nil)
	gatewayService := newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: accounts}}, channelService, cataloguePriceResolver(channelService, billingService))
	marketplace := newCatalogueMarketplace(nil, gatewayService, billingService)

	models := marketplace.ModelsForGroup(context.Background(), &routing.Group{ID: groupID, Platform: capability.PlatformOpenAI, RateMultiplier: 1})
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
	channelMappedPrice := 0.08
	channel := routing.Channel{
		ID:                 61,
		Status:             billing.StatusActive,
		BillingModelSource: routing.BillingModelSourceRequested,
		ModelMapping: map[string]map[string]string{
			capability.PlatformQoder: {"client-model": "channel-model"},
		},
		ModelPricing: []routing.ChannelModelPricing{{
			Platform:   capability.PlatformQoder,
			Models:     []string{"channel-model"},
			InputPrice: &channelMappedPrice,
		}},
	}
	channelService := routingtestkit.Channel(groupID, capability.PlatformQoder, channel)
	billingService := billingtestkit.Calculator(0, nil, nil)
	marketplace := newCatalogueMarketplace(nil, newCatalogueFixture(nil, channelService, cataloguePriceResolver(channelService, billingService)), billingService)

	pricing := marketplace.RequestableModelPricing(context.Background(), &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1}, routing.MarketplaceModelDef{ID: "client-model", PricingModel: "client-model"})

	require.Equal(t, "unpriced", pricing.PriceStatus)
	require.Equal(t, "unknown", pricing.PricingMode)
}
