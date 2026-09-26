//go:build unit

package creative_test

import (
	"context"
	"sort"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	"github.com/stretchr/testify/require"
)

type creativeCatalogTestAccounts struct{ values []creative.CatalogAccount }

func (a creativeCatalogTestAccounts) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]creative.CatalogAccount, error) {
	return a.values, nil
}

// 通过正式目录入口验证账号候选，避免测试保留另一套不含分组策略的展开规则。
func creativeAccountModelsForTest(t *testing.T, value *accountcore.Record) []string {
	t.Helper()
	value = accountcore.CloneRecord(value)
	value.Status = "active"
	value.Schedulable = true
	svc := &creative.Public{AccountRepo: creativeCatalogTestAccounts{[]creative.CatalogAccount{creativeprovider.CatalogAccount(value)}}}
	models, err := svc.CreativeModelsForGroup(context.Background(), &creative.GroupView{ID: 12, Platform: value.Platform})
	require.NoError(t, err)
	result := make([]string, 0, len(models))
	for model := range models {
		result = append(result, model)
	}
	sort.Strings(result)
	return result
}

// TestCreativeOperationsForPlatform 平台能力矩阵。
func TestCreativeOperationsForPlatform(t *testing.T) {
	require.Equal(t, []string{"generate", "edit"}, creative.CreativeOperationsForPlatform(capability.PlatformGemini))
	require.Equal(t, []string{"generate", "edit", "inpaint"}, creative.CreativeOperationsForPlatform(capability.PlatformOpenAI))
	require.Equal(t, []string{"generate", "edit"}, creative.CreativeOperationsForPlatform(capability.PlatformGrok))
	require.Nil(t, creative.CreativeOperationsForPlatform(capability.PlatformAnthropic))
}

// TestCreativeGrokDefaultImageCandidates 校验无映射账号包含 Grok Imagine 图片候选，尤其是画质模型。
func TestCreativeGrokDefaultImageCandidates(t *testing.T) {
	account := &accountcore.Record{Platform: capability.PlatformGrok, Credentials: map[string]any{}}
	models := creativeAccountModelsForTest(t, account)
	require.Contains(t, models, "grok-imagine-image")
	require.Contains(t, models, "grok-imagine-image-quality")
	require.Contains(t, models, "grok-imagine-image-2.0")
}

// TestCreativeGrokDefaultImageCandidatesWithQualityWhitelist 校验精确白名单不会漏掉画质模型。
func TestCreativeGrokDefaultImageCandidatesWithQualityWhitelist(t *testing.T) {
	account := &accountcore.Record{
		Platform: capability.PlatformGrok,
		Credentials: map[string]any{
			"model_whitelist": []string{"grok-imagine-image-quality"},
		},
	}
	models := creativeAccountModelsForTest(t, account)
	require.Equal(t, []string{"grok-imagine-image-quality"}, models)
}

// TestCreativeMappedFinalModelCapability 确保文本请求别名映射到图片模型时按最终模型校验。
func TestCreativeMappedFinalModelCapability(t *testing.T) {
	account := &accountcore.Record{
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"draw-alias": "gpt-image-2", "text-alias": "gpt-5.4"},
			"model_whitelist": []string{"gpt-image-2", "gpt-5.4"},
		},
	}
	models := creativeAccountModelsForTest(t, account)
	// 非图片目标不能进入目录。
	require.NotContains(t, models, "text-alias")
	// 反向映射目标为图片模型的别名必须被保留。
	account.Credentials["model_mapping"] = map[string]any{"draw-alias": "gpt-image-2"}
	models = creativeAccountModelsForTest(t, account)
	require.Contains(t, models, "draw-alias")
	require.Contains(t, models, "gpt-image-2", "最终模型也可以直接请求")
}

// TestCreativeGrokConfiguredImageWhitelistCandidates 校验代理侧图片模型变体能从显式白名单进入候选。
func TestCreativeGrokConfiguredImageWhitelistCandidates(t *testing.T) {
	account := &accountcore.Record{
		Platform: capability.PlatformGrok,
		Credentials: map[string]any{
			"model_whitelist": []string{"grok-imagine-image-lite"},
		},
	}
	models := creativeAccountModelsForTest(t, account)
	require.Equal(t, []string{"grok-imagine-image-lite"}, models)
}

// TestCreativeGeminiConfiguredImageWhitelistCandidates 校验 Gemini 无映射账号不会漏掉图片模型变体。
func TestCreativeGeminiConfiguredImageWhitelistCandidates(t *testing.T) {
	account := &accountcore.Record{
		Platform: capability.PlatformGemini,
		Credentials: map[string]any{
			"model_whitelist": []string{"gemini-3-pro-image-quality", "gemini-2.5-flash"},
		},
	}
	models := creativeAccountModelsForTest(t, account)
	require.Equal(t, []string{"gemini-3-pro-image-quality"}, models)
}

// 目录和提交必须使用相同的模型链，无共享价格配置时也要执行全部分组策略。
func TestCreativeDirectoryAndCreateUseGroupPolicy(t *testing.T) {
	for _, platform := range []string{creative.PlatformOpenAI, creative.PlatformGemini, creative.PlatformGrok} {
		for _, source := range []string{routing.BillingModelSourceRequested, routing.BillingModelSourceGroupMapped, routing.BillingModelSourceUpstream} {
			t.Run(platform+"/"+source, func(t *testing.T) {
				svc := newCreativeTestService()
				final := map[string]string{creative.PlatformOpenAI: "gpt-image-2", creative.PlatformGemini: "gemini-3-pro-image", creative.PlatformGrok: "grok-imagine-image-2.0"}[platform]
				allowed := map[string]string{routing.BillingModelSourceRequested: "DRAW-*", routing.BillingModelSourceGroupMapped: "account-alias", routing.BillingModelSourceUpstream: final}[source]
				group := newCreativeTestGroup()
				group.Platform = platform
				group.ModelPricing = nil
				group.RoutingPolicy = routing.GroupRoutingPolicy{
					Enabled: true, RestrictModels: true, RestrictionModelSource: source,
					ModelMapping:  map[string]map[string]string{platform: {"draw-*": "account-alias", "account-alias": "forbidden-group-hop"}},
					AllowedModels: map[string][]string{platform: {allowed}},
				}
				groups := testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source)
				groups.byID[group.ID] = group
				groups.active = []routing.Group{*group}
				accounts := testassert.MustType[*creativeFakeAccountRepo](testassert.MustType[creativeAccountReader](svc.AccountRepo).source)
				accounts.byGroup[group.ID] = []accountcore.Record{{
					ID: 55, Platform: platform, Status: "active", Schedulable: true,
					Credentials: map[string]any{
						"model_mapping":   map[string]any{"account-alias": final, final: "forbidden-account-hop"},
						"model_whitelist": []string{final},
					},
				}}
				testassert.MustType[*creativeFakeSettingReader](svc.Settings).models = []creative.CreativeModelSetting{{
					GroupID: group.ID, Model: "draw-cat", Operations: []string{creative.CreativeOperationGenerate},
				}}
				params := validCreateParams()
				params.Model = "draw-cat"
				models, err := svc.ListModels(context.Background(), 7)
				require.NoError(t, err)
				require.Len(t, models.Data, 1)
				require.Equal(t, "draw-cat", models.Data[0].Model)
				validated, err := svc.ValidateCreateParams(context.Background(), 7, &params)
				require.NoError(t, err)
				require.Equal(t, final, validated.FinalModel)
				require.Nil(t, svc.GroupMapping, "准入不依赖共享价格配置服务")

				// 只保留另一平台的允许项，本平台空白名单必须在目录和提交时同时拒绝。
				group.RoutingPolicy.AllowedModels = map[string][]string{"anthropic": {allowed}}
				groups.active = []routing.Group{*group}
				models, err = svc.ListModels(context.Background(), 7)
				require.NoError(t, err)
				require.Empty(t, models.Data)
				_, err = svc.ValidateCreateParams(context.Background(), 7, &params)
				require.ErrorIs(t, err, creative.ErrCreativeInvalidModel)
			})
		}
	}
}

func TestCreativeDirectoryIgnoresDisabledPolicyDraft(t *testing.T) {
	svc := newCreativeTestService()
	groups := testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source)
	group := groups.byID[12]
	group.RoutingPolicy = routing.GroupRoutingPolicy{
		Enabled: false, RestrictModels: true,
		ModelMapping: map[string]map[string]string{group.Platform: {"gemini-3.1-flash-image": "forbidden-draft-model"}},
	}
	groups.active = []routing.Group{*group}
	models, err := svc.ListModels(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, models.Data, 1)
	params := validCreateParams()
	validated, err := svc.ValidateCreateParams(context.Background(), 7, &params)
	require.NoError(t, err)
	require.Equal(t, params.Model, validated.FinalModel)
}
