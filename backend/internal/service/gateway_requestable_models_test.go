package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestResolveRequestableModels_RequiresModelLevelSchedulability 验证可见模型至少存在一个未被模型级限流的账号。
func TestResolveRequestableModels_RequiresModelLevelSchedulability(t *testing.T) {
	groupID := int64(4120)
	channel := Channel{
		ID:     70,
		Status: StatusActive,
		ModelMapping: map[string]map[string]string{
			PlatformOpenAI: {"client-alias": "channel-model"},
		},
	}
	future := time.Now().Add(10 * time.Minute).Format(time.RFC3339)
	limitedAccount := Account{
		ID:       80,
		Platform: PlatformOpenAI,
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
	channelService := newRequestableModelsChannelService(groupID, PlatformOpenAI, channel)

	t.Run("全部账号均被模型限流时隐藏", func(t *testing.T) {
		svc := &GatewayService{
			accountRepo:    &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {limitedAccount}}},
			channelService: channelService,
		}
		result := svc.ResolveRequestableModels(context.Background(), &groupID, PlatformOpenAI)
		require.NotContains(t, RequestableModelIDs(result.Models), "client-alias")
	})

	t.Run("至少一个健康账号时保留", func(t *testing.T) {
		svc := &GatewayService{
			accountRepo:    &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {limitedAccount, healthyAccount}}},
			channelService: channelService,
		}
		result := svc.ResolveRequestableModels(context.Background(), &groupID, PlatformOpenAI)
		require.Contains(t, RequestableModelIDs(result.Models), "client-alias")
	})
}

type requestableModelsChannelRepoStub struct {
	ChannelRepository
	err error
}

func (s *requestableModelsChannelRepoStub) ListAll(context.Context) ([]Channel, error) {
	return nil, s.err
}

// sequencedRequestableModelsAccountRepoStub 模拟第一次账号查询失败、第二次查询恢复。
type sequencedRequestableModelsAccountRepoStub struct {
	AccountRepository
	accounts []Account
	calls    int
}

// ListSchedulableByGroupID 在首次调用返回临时错误，后续调用返回当前账号快照。
func (s *sequencedRequestableModelsAccountRepoStub) ListSchedulableByGroupID(context.Context, int64) ([]Account, error) {
	s.calls++
	if s.calls == 1 {
		return nil, errors.New("temporary account query failure")
	}
	return append([]Account(nil), s.accounts...), nil
}

// newRequestableModelsChannelService 构造只读渠道缓存，避免测试依赖数据库仓储。
func newRequestableModelsChannelService(groupID int64, platform string, channel Channel) *ChannelService {
	channel.GroupIDs = []int64{groupID}
	cache := populateChannelCache([]Channel{channel}, map[int64]string{groupID: platform})
	service := &ChannelService{}
	service.cache.Store(cache)
	return service
}

// requestableModelByID 从解析结果中查找指定客户端模型。
func requestableModelByID(models []RequestableModel, id string) (RequestableModel, bool) {
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	return RequestableModel{}, false
}

func TestResolveRequestableModels_UsesConfiguredPricingBasis(t *testing.T) {
	groupID := int64(4101)
	inputPrice := 0.01
	for _, test := range []struct {
		name          string
		billingSource string
		pricingModel  string
	}{
		{name: "requested", billingSource: BillingModelSourceRequested, pricingModel: "client-alias"},
		{name: "channel mapped", billingSource: BillingModelSourceChannelMapped, pricingModel: "channel-model"},
		{name: "upstream", billingSource: BillingModelSourceUpstream, pricingModel: "upstream-model"},
	} {
		t.Run(test.name, func(t *testing.T) {
			channel := Channel{
				ID:                 51,
				Status:             StatusActive,
				BillingModelSource: test.billingSource,
				RestrictModels:     true,
				ModelMapping: map[string]map[string]string{
					PlatformOpenAI: {"client-alias": "channel-model"},
				},
				ModelPricing: []ChannelModelPricing{{
					Platform:   PlatformOpenAI,
					Models:     []string{test.pricingModel},
					InputPrice: &inputPrice,
				}},
			}
			account := Account{
				ID:       61,
				Platform: PlatformOpenAI,
				Credentials: map[string]any{
					"model_mapping":   map[string]any{"channel-model": "upstream-model"},
					"model_whitelist": []any{"upstream-model"},
				},
			}
			repo := &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account}}}
			svc := &GatewayService{
				accountRepo:    repo,
				channelService: newRequestableModelsChannelService(groupID, PlatformOpenAI, channel),
			}

			result := svc.ResolveRequestableModels(context.Background(), &groupID, PlatformOpenAI)
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
	channel := Channel{
		ID:                 52,
		Status:             StatusActive,
		BillingModelSource: BillingModelSourceChannelMapped,
		RestrictModels:     true,
		ModelMapping: map[string]map[string]string{
			PlatformOpenAI: {"client-*": "channel-model"},
		},
		ModelPricing: []ChannelModelPricing{{
			Platform:   PlatformOpenAI,
			Models:     []string{"channel-*"},
			InputPrice: &price,
		}},
	}
	account := Account{
		ID:       62,
		Platform: PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"client-one": "client-one",
				"channel-*":  "upstream-model",
			},
			"model_whitelist": []any{"upstream-model"},
		},
	}
	svc := &GatewayService{
		accountRepo:    &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account}}},
		channelService: newRequestableModelsChannelService(groupID, PlatformOpenAI, channel),
	}

	result := svc.ResolveRequestableModels(context.Background(), &groupID, PlatformOpenAI)
	model, ok := requestableModelByID(result.Models, "client-one")
	require.True(t, ok)
	require.Equal(t, "channel-model", model.PricingModel)
	require.NotContains(t, RequestableModelIDs(result.Models), "client-*")
	require.NotContains(t, RequestableModelIDs(result.Models), "channel-*")
}

func TestResolveRequestableModels_UnrestrictedAccountAddsDefaultsAndMappingSource(t *testing.T) {
	groupID := int64(4103)
	account := Account{
		ID:       63,
		Platform: PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"CUSTOM-Alias": "gpt-5.5",
				"custom-alias": "gpt-5.6",
				"gpt-*":        "gpt-5.5",
			},
		},
	}
	svc := &GatewayService{accountRepo: &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account}}}}

	result := svc.ResolveRequestableModels(context.Background(), &groupID, PlatformOpenAI)
	ids := RequestableModelIDs(result.Models)
	require.Contains(t, ids, "CUSTOM-Alias")
	require.Contains(t, ids, "gpt-5.5")
	require.NotContains(t, ids, "custom-alias")
	require.NotContains(t, ids, "gpt-*")
}

func TestResolveRequestableModels_QoderUsesSchedulableAccountSiteUnion(t *testing.T) {
	groupID := int64(4121)
	global := Account{
		ID:          90,
		Platform:    PlatformQoder,
		Type:        AccountTypeCosy,
		Credentials: map[string]any{"site": "global"},
	}
	cn := Account{
		ID:          91,
		Platform:    PlatformQoder,
		Type:        AccountTypeCosy,
		Credentials: map[string]any{"site": "cn"},
	}
	resolve := func(accounts ...Account) []string {
		svc := &GatewayService{
			accountRepo: &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: accounts}},
		}
		result := svc.ResolveRequestableModels(context.Background(), &groupID, PlatformQoder)
		return RequestableModelIDs(result.Models)
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
	channel := Channel{
		ID:     54,
		Status: StatusActive,
		ModelMapping: map[string]map[string]string{
			PlatformOpenAI: {"blocked-alias": "blocked-final"},
		},
	}
	account := Account{
		ID:       64,
		Platform: PlatformOpenAI,
		Credentials: map[string]any{
			"model_whitelist": []any{"allowed-final"},
		},
	}
	svc := &GatewayService{
		accountRepo:    &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account}}},
		channelService: newRequestableModelsChannelService(groupID, PlatformOpenAI, channel),
	}

	result := svc.ResolveRequestableModels(context.Background(), &groupID, PlatformOpenAI)
	require.NotContains(t, RequestableModelIDs(result.Models), "blocked-alias")
}

func TestResolveRequestableModels_UpstreamPricingAmbiguousAcrossAccounts(t *testing.T) {
	groupID := int64(4105)
	channel := Channel{
		ID:                 55,
		Status:             StatusActive,
		BillingModelSource: BillingModelSourceUpstream,
		ModelMapping: map[string]map[string]string{
			PlatformOpenAI: {"client-alias": "channel-model"},
		},
	}
	accounts := []Account{
		{ID: 65, Platform: PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"channel-model": "upstream-a"}}},
		{ID: 66, Platform: PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"channel-model": "upstream-b"}}},
	}
	svc := &GatewayService{
		accountRepo:    &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: accounts}},
		channelService: newRequestableModelsChannelService(groupID, PlatformOpenAI, channel),
	}

	result := svc.ResolveRequestableModels(context.Background(), &groupID, PlatformOpenAI)
	model, ok := requestableModelByID(result.Models, "client-alias")
	require.True(t, ok)
	require.Empty(t, model.PricingModel)
	require.True(t, model.PricingAmbiguous)
}

func TestResolveRequestableModels_UpstreamUsesBedrockRegionalModel(t *testing.T) {
	groupID := int64(4114)
	price := 0.08
	upstreamModel := "us.anthropic.claude-sonnet-4-5-20250929-v1:0"
	channel := Channel{
		ID:                 62,
		Status:             StatusActive,
		BillingModelSource: BillingModelSourceUpstream,
		RestrictModels:     true,
		ModelMapping: map[string]map[string]string{
			PlatformAnthropic: {"claude-sonnet-4-5": "claude-sonnet-4-5"},
		},
		ModelPricing: []ChannelModelPricing{{
			Platform:   PlatformAnthropic,
			Models:     []string{upstreamModel},
			InputPrice: &price,
		}},
	}
	account := Account{
		ID:       74,
		Platform: PlatformAnthropic,
		Type:     AccountTypeBedrock,
		Credentials: map[string]any{
			"aws_region": "us-east-1",
		},
	}
	svc := &GatewayService{
		accountRepo:    &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account}}},
		channelService: newRequestableModelsChannelService(groupID, PlatformAnthropic, channel),
	}

	result := svc.ResolveRequestableModels(context.Background(), &groupID, PlatformAnthropic)
	model, ok := requestableModelByID(result.Models, "claude-sonnet-4-5")
	require.True(t, ok)
	require.Equal(t, upstreamModel, model.PricingModel)
	require.False(t, model.PricingAmbiguous)
}

func TestResolveRequestableModels_UpstreamMarksAntigravityThinkingVariantAmbiguous(t *testing.T) {
	groupID := int64(4115)
	channel := Channel{
		ID:                 63,
		Status:             StatusActive,
		BillingModelSource: BillingModelSourceUpstream,
	}
	account := Account{ID: 75, Platform: PlatformAntigravity}
	svc := &GatewayService{
		accountRepo:    &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account}}},
		channelService: newRequestableModelsChannelService(groupID, PlatformAntigravity, channel),
	}

	result := svc.ResolveRequestableModels(context.Background(), &groupID, PlatformAntigravity)
	model, ok := requestableModelByID(result.Models, "claude-sonnet-4-5")
	require.True(t, ok)
	require.Empty(t, model.PricingModel)
	require.True(t, model.PricingAmbiguous)
}

func TestResolveRequestableModels_UpstreamNormalizesAnthropicOAuthMapping(t *testing.T) {
	groupID := int64(4116)
	channel := Channel{
		ID:                 64,
		Status:             StatusActive,
		BillingModelSource: BillingModelSourceUpstream,
	}
	account := Account{
		ID:       76,
		Platform: PlatformAnthropic,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"client-alias": "claude-sonnet-4-5"},
		},
	}
	svc := &GatewayService{
		accountRepo:    &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account}}},
		channelService: newRequestableModelsChannelService(groupID, PlatformAnthropic, channel),
	}

	result := svc.ResolveRequestableModels(context.Background(), &groupID, PlatformAnthropic)
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
		account      Account
	}{
		{
			name:         "OAuth 别名归一化",
			groupID:      4118,
			channelModel: "gpt-5.6-sol-high",
			pricingModel: "gpt-5.6-sol",
			account: Account{
				ID:       78,
				Platform: PlatformOpenAI,
				Type:     AccountTypeOAuth,
			},
		},
		{
			name:         "自动透传忽略普通账号映射",
			groupID:      4119,
			channelModel: "passthrough-model",
			pricingModel: "passthrough-model",
			account: Account{
				ID:       79,
				Platform: PlatformOpenAI,
				Type:     AccountTypeOAuth,
				Extra:    map[string]any{"openai_passthrough": true},
				Credentials: map[string]any{
					"model_mapping": map[string]any{"passthrough-model": "mapped-model"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := Channel{
				ID:                 tt.groupID,
				Status:             StatusActive,
				BillingModelSource: BillingModelSourceUpstream,
				RestrictModels:     true,
				ModelMapping: map[string]map[string]string{
					PlatformOpenAI: {"client-alias": tt.channelModel},
				},
				ModelPricing: []ChannelModelPricing{{
					Platform:   PlatformOpenAI,
					Models:     []string{tt.pricingModel},
					InputPrice: &price,
				}},
			}
			svc := &GatewayService{
				accountRepo:    &modelsListAccountRepoStub{byGroup: map[int64][]Account{tt.groupID: {tt.account}}},
				channelService: newRequestableModelsChannelService(tt.groupID, PlatformOpenAI, channel),
			}

			result := svc.ResolveRequestableModels(context.Background(), &tt.groupID, PlatformOpenAI)
			model, ok := requestableModelByID(result.Models, "client-alias")
			require.True(t, ok)
			require.Equal(t, tt.pricingModel, model.PricingModel)
			require.False(t, model.PricingAmbiguous)
		})
	}
}

func TestResolveRequestableModels_RestrictionEmptyDoesNotFallBack(t *testing.T) {
	groupID := int64(4106)
	channel := Channel{
		ID:                 56,
		Status:             StatusActive,
		BillingModelSource: BillingModelSourceRequested,
		RestrictModels:     true,
	}
	account := Account{ID: 67, Platform: PlatformOpenAI}
	svc := &GatewayService{
		accountRepo:    &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account}}},
		channelService: newRequestableModelsChannelService(groupID, PlatformOpenAI, channel),
	}

	result := svc.ResolveRequestableModels(context.Background(), &groupID, PlatformOpenAI)
	require.True(t, result.Restricted)
	require.Empty(t, result.Models)
}

func TestResolveRequestableModels_PricingDoesNotCrossPlatforms(t *testing.T) {
	groupID := int64(4107)
	price := 0.03
	channel := Channel{
		ID:                 57,
		Status:             StatusActive,
		BillingModelSource: BillingModelSourceRequested,
		RestrictModels:     true,
		ModelPricing: []ChannelModelPricing{{
			Platform:   PlatformOpenAI,
			Models:     []string{"same-model"},
			InputPrice: &price,
		}},
	}
	account := Account{
		ID:       68,
		Platform: PlatformAnthropic,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"same-model": "same-model"},
		},
	}
	svc := &GatewayService{
		accountRepo:    &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account}}},
		channelService: newRequestableModelsChannelService(groupID, PlatformAnthropic, channel),
	}

	result := svc.ResolveRequestableModels(context.Background(), &groupID, PlatformAnthropic)
	require.True(t, result.Restricted)
	require.Empty(t, result.Models)
}

func TestResolveRequestableModels_QoderRequiresEffectivePricing(t *testing.T) {
	groupID := int64(4108)
	effectivePrice := 0.04
	channel := Channel{
		ID:                 58,
		Status:             StatusActive,
		BillingModelSource: BillingModelSourceRequested,
		RestrictModels:     true,
		ModelPricing: []ChannelModelPricing{
			{Platform: PlatformQoder, Models: []string{"qoder-model"}},
			{Platform: PlatformQoder, Models: []string{"qoder-*"}, InputPrice: &effectivePrice},
		},
	}
	account := Account{ID: 69, Platform: PlatformQoder}
	svc := &GatewayService{
		accountRepo:    &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account}}},
		channelService: newRequestableModelsChannelService(groupID, PlatformQoder, channel),
	}

	result := svc.ResolveRequestableModels(context.Background(), &groupID, PlatformQoder)
	require.Contains(t, RequestableModelIDs(result.Models), "qoder-model")
}

func TestResolveRequestableModels_AccountQueryFailureKeepsFallback(t *testing.T) {
	groupID := int64(4109)
	svc := &GatewayService{accountRepo: &modelsListAccountRepoStub{err: errors.New("temporary failure")}}

	result := svc.ResolveRequestableModels(context.Background(), &groupID, PlatformOpenAI)
	require.False(t, result.Restricted)
	require.Contains(t, RequestableModelIDs(result.Models), "gpt-5.5")
}

// TestResolveRequestableModels_SecondAccountQueryRestoresWhitelistCandidates 验证缓存层查询失败后仍使用当前账号白名单。
func TestResolveRequestableModels_SecondAccountQueryRestoresWhitelistCandidates(t *testing.T) {
	groupID := int64(4121)
	repo := &sequencedRequestableModelsAccountRepoStub{accounts: []Account{{
		ID:       82,
		Platform: PlatformOpenAI,
		Credentials: map[string]any{
			"model_whitelist": []any{"private-model"},
		},
	}}}
	svc := &GatewayService{accountRepo: repo}

	result := svc.ResolveRequestableModels(context.Background(), &groupID, PlatformOpenAI)

	require.Equal(t, 2, repo.calls)
	require.True(t, result.HadExplicitAccountModels)
	require.Equal(t, []string{"private-model"}, RequestableModelIDs(result.Models))
}

func TestResolveRequestableModels_ChannelQueryFailureKeepsAccountCandidates(t *testing.T) {
	groupID := int64(4113)
	account := Account{
		ID:       73,
		Platform: PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"client-alias": "upstream-model"},
			"model_whitelist": []any{"upstream-model"},
		},
	}
	svc := &GatewayService{
		accountRepo: &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account}}},
		channelService: NewChannelService(&requestableModelsChannelRepoStub{
			err: errors.New("temporary channel failure"),
		}, nil),
	}

	result := svc.ResolveRequestableModels(context.Background(), &groupID, PlatformOpenAI)
	require.False(t, result.Restricted)
	require.Contains(t, RequestableModelIDs(result.Models), "client-alias")
}

// TestResolveRequestableModels_ChannelQueryFailureKeepsEmptyAccountPoolEmpty 验证渠道故障不会为无账号分组伪造默认模型。
func TestResolveRequestableModels_ChannelQueryFailureKeepsEmptyAccountPoolEmpty(t *testing.T) {
	groupID := int64(4117)
	svc := &GatewayService{
		accountRepo: &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {}}},
		channelService: NewChannelService(&requestableModelsChannelRepoStub{
			err: errors.New("temporary channel failure"),
		}, nil),
	}

	result := svc.ResolveRequestableModels(context.Background(), &groupID, PlatformOpenAI)

	require.False(t, result.Restricted)
	require.Empty(t, result.Models)
}

func TestModelMarketplaceUsesResolvedChannelMappedPricingModel(t *testing.T) {
	groupID := int64(4110)
	inputPrice := 0.05
	outputPrice := 0.06
	channel := Channel{
		ID:                 59,
		Status:             StatusActive,
		BillingModelSource: BillingModelSourceChannelMapped,
		RestrictModels:     true,
		ModelMapping: map[string]map[string]string{
			PlatformOpenAI: {"client-alias": "channel-model"},
		},
		ModelPricing: []ChannelModelPricing{{
			Platform:    PlatformOpenAI,
			Models:      []string{"channel-model"},
			InputPrice:  &inputPrice,
			OutputPrice: &outputPrice,
		}},
	}
	account := Account{
		ID:       70,
		Platform: PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"channel-model": "upstream-model"},
			"model_whitelist": []any{"upstream-model"},
		},
	}
	channelService := newRequestableModelsChannelService(groupID, PlatformOpenAI, channel)
	billingService := NewBillingService(nil, nil)
	gatewayService := &GatewayService{
		accountRepo:    &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: {account}}},
		channelService: channelService,
		resolver:       NewModelPricingResolver(channelService, billingService),
	}
	marketplace := NewModelMarketplaceService(nil, nil, gatewayService, billingService, nil, nil, nil)

	models := marketplace.listPublicModelsForGroup(context.Background(), &Group{ID: groupID, Platform: PlatformOpenAI, RateMultiplier: 1})
	var alias *ModelMarketplaceModel
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
	channel := Channel{
		ID:                 60,
		Status:             StatusActive,
		BillingModelSource: BillingModelSourceUpstream,
		ModelMapping: map[string]map[string]string{
			PlatformOpenAI: {"client-alias": "channel-model"},
		},
		ModelPricing: []ChannelModelPricing{
			{Platform: PlatformOpenAI, Models: []string{"upstream-a"}, InputPrice: &price},
			{Platform: PlatformOpenAI, Models: []string{"upstream-b"}, InputPrice: &price},
		},
	}
	accounts := []Account{
		{ID: 71, Platform: PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"channel-model": "upstream-a"}, "model_whitelist": []any{"upstream-a"}}},
		{ID: 72, Platform: PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"channel-model": "upstream-b"}, "model_whitelist": []any{"upstream-b"}}},
	}
	channelService := newRequestableModelsChannelService(groupID, PlatformOpenAI, channel)
	billingService := NewBillingService(nil, nil)
	gatewayService := &GatewayService{
		accountRepo:    &modelsListAccountRepoStub{byGroup: map[int64][]Account{groupID: accounts}},
		channelService: channelService,
		resolver:       NewModelPricingResolver(channelService, billingService),
	}
	marketplace := NewModelMarketplaceService(nil, nil, gatewayService, billingService, nil, nil, nil)

	models := marketplace.listPublicModelsForGroup(context.Background(), &Group{ID: groupID, Platform: PlatformOpenAI, RateMultiplier: 1})
	var alias *ModelMarketplaceModel
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
	channel := Channel{
		ID:                 61,
		Status:             StatusActive,
		BillingModelSource: BillingModelSourceRequested,
		ModelMapping: map[string]map[string]string{
			PlatformQoder: {"client-model": "channel-model"},
		},
		ModelPricing: []ChannelModelPricing{{
			Platform:   PlatformQoder,
			Models:     []string{"channel-model"},
			InputPrice: &channelMappedPrice,
		}},
	}
	channelService := newRequestableModelsChannelService(groupID, PlatformQoder, channel)
	billingService := NewBillingService(nil, nil)
	marketplace := NewModelMarketplaceService(nil, nil, &GatewayService{
		channelService: channelService,
		resolver:       NewModelPricingResolver(channelService, billingService),
	}, billingService, nil, nil, nil)

	pricing := marketplace.getRequestableModelDisplayPricing(context.Background(), &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1}, marketplaceModelDef{ID: "client-model", PricingModel: "client-model"})

	require.Equal(t, "unpriced", pricing.PriceStatus)
	require.Equal(t, "unknown", pricing.PricingMode)
}
