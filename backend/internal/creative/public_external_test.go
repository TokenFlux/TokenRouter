package creative_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
)

type creativeCatalogTestProviders struct{ values []creative.CatalogProvider }

// creativePricingConfigFixture 提供测试价格配置。
type creativePricingConfigFixture struct {
	routing.PricingConfigRepository
	cards    []routing.ModelPricingEntry
	platform string
}

// TestCreativeCatalogueIncludesGroupMappingTargets 空白名单提供商可展示分组映射中的具体图片目标。
func TestCreativeCatalogueIncludesGroupMappingTargets(t *testing.T) {
	for _, source := range []string{"draw-alias", "draw-*"} {
		t.Run(source, func(t *testing.T) {
			value := &providercore.Record{Platform: creative.PlatformOpenAI, Type: "apikey", Status: "active", Schedulable: true, Credentials: map[string]any{}}
			service := &creative.Public{ProviderRepo: creativeCatalogTestProviders{[]creative.CatalogProvider{creativeprovider.CatalogProvider(value)}}}
			group := &creative.GroupView{ID: 1, Operations: map[string][]string{creative.PlatformOpenAI: {creative.CreativeOperationGenerate}}, RoutingPolicy: routing.GroupRoutingPolicy{Enabled: true, ModelMapping: map[string]string{source: "gpt-image-2"}}}
			models, err := service.CreativeModelsForGroup(context.Background(), group)
			require.NoError(t, err)
			want := map[string]string{"gpt-image-2": "gpt-image-2"}
			if source == "draw-alias" {
				want[source] = "gpt-image-2"
			}
			require.Equal(t, want, models)

			group.RoutingPolicy.RestrictModels = true
			group.RoutingPolicy.RestrictionModelSource = routing.BillingModelSourceRequested
			group.RoutingPolicy.AllowedModels = []string{source}
			models, err = service.CreativeModelsForGroup(context.Background(), group)
			require.NoError(t, err)
			require.NotContains(t, models, "gpt-image-2")

			group.RoutingPolicy.RestrictModels = false
			value.Credentials["model_whitelist"] = []string{"other-model"}
			service.ProviderRepo = creativeCatalogTestProviders{[]creative.CatalogProvider{creativeprovider.CatalogProvider(value)}}
			models, err = service.CreativeModelsForGroup(context.Background(), group)
			require.NoError(t, err)
			require.NotContains(t, models, "gpt-image-2")
		})
	}
}

// TestCreativeOperationsForPlatform 平台能力矩阵。
func TestCreativeOperationsForPlatform(t *testing.T) {
	require.Equal(t, []string{"generate", "edit"}, creative.CreativeOperationsForPlatform(capability.PlatformGemini))
	require.Equal(t, []string{"generate", "edit", "inpaint"}, creative.CreativeOperationsForPlatform(capability.PlatformOpenAI))
	require.Equal(t, []string{"generate", "edit"}, creative.CreativeOperationsForPlatform(capability.PlatformGrok))
	require.Nil(t, creative.CreativeOperationsForPlatform(capability.PlatformAnthropic))
}

// TestCreativeGrokDefaultImageCandidates 空配置提供商返回空目录。
func TestCreativeGrokDefaultImageCandidates(t *testing.T) {
	provider := &providercore.Record{Platform: capability.PlatformGrok, Credentials: map[string]any{}}
	models := creativeProviderModelsForTest(t, provider)
	require.Empty(t, models)
}

// TestCreativeGrokDefaultImageCandidatesWithQualityWhitelist 校验精确白名单不会漏掉画质模型。
func TestCreativeGrokDefaultImageCandidatesWithQualityWhitelist(t *testing.T) {
	provider := &providercore.Record{
		Platform: capability.PlatformGrok,
		Credentials: map[string]any{
			"model_whitelist": []string{"grok-imagine-image-quality"},
		},
	}
	models := creativeProviderModelsForTest(t, provider)
	require.Equal(t, []string{"grok-imagine-image-quality"}, models)
}

// TestCreativeMappedFinalModelCapability 检查文本请求别名映射到图片模型时的最终模型校验。
func TestCreativeMappedFinalModelCapability(t *testing.T) {
	provider := &providercore.Record{
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"draw-alias": "gpt-image-2", "text-alias": "gpt-5.4"},
			"model_whitelist": []string{"gpt-image-2", "gpt-5.4"},
		},
	}
	models := creativeProviderModelsForTest(t, provider)
	// 非图片目标不能进入目录。
	require.NotContains(t, models, "text-alias")
	// 反向映射目标为图片模型的别名进入目录。
	provider.Credentials["model_mapping"] = map[string]any{"draw-alias": "gpt-image-2"}
	models = creativeProviderModelsForTest(t, provider)
	require.Contains(t, models, "draw-alias")
	require.Contains(t, models, "gpt-image-2", "最终模型也可以直接请求")
}

// TestCreativeGrokConfiguredImageWhitelistCandidates 校验代理侧图片模型变体能从白名单进入候选。
func TestCreativeGrokConfiguredImageWhitelistCandidates(t *testing.T) {
	provider := &providercore.Record{
		Platform: capability.PlatformGrok,
		Credentials: map[string]any{
			"model_whitelist": []string{"grok-imagine-image-lite"},
		},
	}
	models := creativeProviderModelsForTest(t, provider)
	require.Equal(t, []string{"grok-imagine-image-lite"}, models)
}

// TestCreativeGeminiConfiguredImageWhitelistCandidates 校验 Gemini 无映射提供商不会漏掉图片模型变体。
func TestCreativeGeminiConfiguredImageWhitelistCandidates(t *testing.T) {
	provider := &providercore.Record{
		Platform: capability.PlatformGemini,
		Credentials: map[string]any{
			"model_whitelist": []string{"gemini-3-pro-image-quality", "gemini-2.5-flash"},
		},
	}
	models := creativeProviderModelsForTest(t, provider)
	require.Equal(t, []string{"gemini-3-pro-image-quality"}, models)
}

// TestCreativeDirectoryAndCreateUseGroupPolicy 检查目录和提交使用相同的模型映射，并在无共享价格配置时执行分组策略。
func TestCreativeDirectoryAndCreateUseGroupPolicy(t *testing.T) {
	for _, platform := range []string{creative.PlatformOpenAI, creative.PlatformGemini, creative.PlatformGrok} {
		for _, source := range []string{routing.BillingModelSourceRequested, routing.BillingModelSourceGroupMapped, routing.BillingModelSourceUpstream} {
			t.Run(platform+"/"+source, func(t *testing.T) {
				svc := newCreativeTestService()
				final := map[string]string{creative.PlatformOpenAI: "gpt-image-2", creative.PlatformGemini: "gemini-3-pro-image", creative.PlatformGrok: "grok-imagine-image-2.0"}[platform]
				allowed := map[string]string{routing.BillingModelSourceRequested: "DRAW-*", routing.BillingModelSourceGroupMapped: "provider-alias", routing.BillingModelSourceUpstream: final}[source]
				group := newCreativeTestGroup()

				group.RoutingPolicy = routing.GroupRoutingPolicy{
					Enabled: true, RestrictModels: true, RestrictionModelSource: source,
					ModelMapping:  map[string]string{"draw-*": "provider-alias", "provider-alias": "forbidden-group-hop"},
					AllowedModels: []string{allowed},
				}
				groups := testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source)
				groups.byID[group.ID] = group
				groups.active = []routing.Group{*group}
				providers := testassert.MustType[*creativeFakeProviderRepo](testassert.MustType[creativeProviderReader](svc.ProviderRepo).source)
				providers.byGroup[group.ID] = []providercore.Record{{
					ID: 55, Platform: platform, Status: "active", Schedulable: true,
					Credentials: map[string]any{
						"model_mapping":   map[string]any{"provider-alias": final, final: "forbidden-provider-hop"},
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

				// 空白名单使目录和提交同时拒绝该模型。
				group.RoutingPolicy.AllowedModels = nil
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
		ModelMapping: map[string]string{"gemini-3.1-flash-image": "forbidden-draft-model"},
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

func TestCreativeCreateRunPersistsOnlyMetadata(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()
	repo := testassert.MustType[*creativeFakeRunRepo](svc.Repo)
	store := testassert.MustType[*creativeFakeTransient](svc.TransientStore)

	params := validCreateParams()
	params.Prompt = "这是一段绝不应落库的 prompt 明文"
	params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 4, 4), Mime: "image/png"}}
	created, err := svc.CreateRun(ctx, testCreativeScope(7), params, "")
	require.NoError(t, err)
	require.True(t, creative.IsValidCreativeRunID(created.ID))

	require.Len(t, repo.createParams, 1)
	stored := repo.createParams[0]
	// prompt 只以 sha256 落库。
	require.Equal(t, creative.Sha256Hex([]byte(params.Prompt)), stored.PromptHash)
	require.NotContains(t, stored.PromptHash, "绝不应落库")
	require.NotEmpty(t, stored.RequestFingerprint)
	// 任务与输出行保存素材元数据。
	require.Equal(t, 1, stored.RequestedOutputCount)
	for _, output := range repo.outputs[created.ID] {
		require.Equal(t, creative.CreativeRunOutputStatusPending, output.Status)
		require.Nil(t, output.MimeType)
		require.Nil(t, output.ByteSize)
	}
	// 图片字节只进入临时存储。
	_, err = store.LoadInputs(ctx, created.ID, 1)
	require.NoError(t, err)
	require.Empty(t, store.outputs)
}

func TestCreativeListModelsFiltersAndContent(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()
	groupRepo := testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[

	// 无图片权限的分组不出现在模型列表。
	creativeGroupReader](svc.GroupRepo).source)

	noImage := newCreativeTestGroup()
	noImage.ID = 13
	noImage.AllowImageGeneration = false
	groupRepo.byID[13] = noImage
	groupRepo.active = append(groupRepo.active, *noImage)

	// 没有图片候选提供商的分组不出现。
	unsupported := newCreativeTestGroup()
	unsupported.ID = 14
	unsupported.Name = "Claude"
	groupRepo.byID[14] = unsupported
	groupRepo.active = append(groupRepo.active, *unsupported)

	got, err := svc.ListModels(ctx, 7)
	require.NoError(t, err)
	require.NotEmpty(t, got.Data)
	for _, item := range got.Data {
		require.Equal(t, int64(12), item.GroupID, "无图片权限或不受支持的分组不得进入模型列表")
		require.Equal(t, []string{"generate", "edit"}, item.Operations)
		require.Equal(t, []string{"512", "1K", "2K", "4K"}, item.ImageSizes)
		require.InDelta(t, 0.02, item.Price1K, 1e-9)
		require.InDelta(t, 0.04, item.Price2K, 1e-9)
		require.Equal(t, "gemini-3.1-flash-image", item.Model)
	}
}

// TestCreativeListModelsFallbacks 覆盖提供商映射和图片价格缺省时的模型目录：
// 提供商未配置 model_mapping 时使用平台图片模型候选。
// 分组未配置 image_price_* 时使用平台默认尺寸档位并按默认价计费。
func TestCreativeListModelsFallbacks(t *testing.T) {
	svc := newCreativeTestService()
	svc.ImageUnitPrice = creativePriceFixture(newCreativeMediaCalculator(), nil)
	ctx := context.Background()
	groupRepo := testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source)
	providerRepo := testassert.MustType[*creativeFakeProviderRepo](testassert.MustType[

	// openai 分组：未配置图片价格和提供商映射时，默认候选中的 GPT Image 2 支持三档尺寸。
	creativeProviderReader](svc.ProviderRepo).source)

	openaiGroup := newCreativeTestGroup()
	openaiGroup.ID = 21
	openaiGroup.Name = "ChatGPT Image"

	groupRepo.byID[21] = openaiGroup
	groupRepo.active = append(groupRepo.active, *openaiGroup)
	providerRepo.byGroup[21] = []providercore.Record{{
		ID:          61,
		Platform:    capability.PlatformOpenAI,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"model_whitelist": []string{"gpt-image-1", "gpt-image-2"},
		},
	}}

	// gemini 分组：未配置图片价格和提供商映射时，默认候选支持 1K、2K、4K 尺寸。
	geminiGroup := newCreativeTestGroup()
	geminiGroup.ID = 22

	groupRepo.byID[22] = geminiGroup
	groupRepo.active = append(groupRepo.active, *geminiGroup)
	providerRepo.byGroup[22] = []providercore.Record{{
		ID:          62,
		Platform:    capability.PlatformGemini,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"model_whitelist": []string{"gemini-2.5-flash-image", "gemini-3-pro-image", "gemini-3.1-flash-image"},
		},
	}}

	// grok 分组：未配置图片价格时支持 1K、2K 尺寸和 generate/edit 操作。
	grokGroup := newCreativeTestGroup()
	grokGroup.ID = 23
	grokGroup.Name = "Grok Imagine"

	groupRepo.byID[23] = grokGroup
	groupRepo.active = append(groupRepo.active, *grokGroup)
	providerRepo.byGroup[23] = []providercore.Record{{
		ID:          63,
		Platform:    capability.PlatformGrok,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"model_whitelist": []string{"grok-imagine-image-1.0", "grok-imagine-image-2.0"},
		},
	}}

	// openai 分组：配置 1K 价格时尺寸返回 1K，模型来自映射。
	pricedGroup := newCreativeTestGroup()
	pricedGroup.ID = 24
	pricedGroup.Name = "GPT Image Priced"

	price1k := 0.02
	setCreativeConfigPricing(svc, pricedGroup.ID, testImageModelPricing(map[string]*float64{"1K": &price1k}))
	groupRepo.byID[24] = pricedGroup
	groupRepo.active = append(groupRepo.active, *pricedGroup)
	providerRepo.byGroup[24] = []providercore.Record{{
		ID:          64,
		Platform:    capability.PlatformOpenAI,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2"},
		},
	}}
	testassert.MustType[*creativeFakeSettingReader](svc.Settings).models = append(testassert.MustType[*creativeFakeSettingReader](svc.Settings).models, creative.CreativeModelSetting{GroupID: 21, Model: "gpt-image-1", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit, creative.CreativeOperationInpaint}},
		creative.CreativeModelSetting{GroupID: 21, Model: "gpt-image-2", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit, creative.CreativeOperationInpaint}},
		creative.CreativeModelSetting{GroupID: 22, Model: "gemini-2.5-flash-image", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit}},
		creative.CreativeModelSetting{GroupID: 22, Model: "gemini-3-pro-image", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit}},
		creative.CreativeModelSetting{GroupID: 22, Model: "gemini-3.1-flash-image", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit}},
		creative.CreativeModelSetting{GroupID: 23, Model: "grok-imagine-image-1.0", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit}},
		creative.CreativeModelSetting{GroupID: 23, Model: "grok-imagine-image-2.0", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit}},
		creative.CreativeModelSetting{GroupID: 24, Model: "gpt-image-2", Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit, creative.CreativeOperationInpaint}},
	)

	got, err := svc.ListModels(ctx, 7)
	require.NoError(t, err)

	byGroup := map[int64][]creative.CreativeModelPublic{}
	for _, item := range got.Data {
		byGroup[item.GroupID] = append(byGroup[item.GroupID], item)
	}

	// openai 无映射回退：两个候选模型、默认价大于 0；GPT Image 2 额外开放 4K。
	require.Len(t, byGroup[21], 2)
	for _, item := range byGroup[21] {
		require.Equal(t, []string{"low", "medium", "high", "auto"}, item.Qualities)
		require.Empty(t, item.OutputFormats)
		require.Nil(t, item.OutputCompression)
		if item.Model == "gpt-image-2" {
			require.Equal(t, []string{"auto", "opaque", "transparent"}, item.BackgroundOptions)
		} else {
			require.Equal(t, []string{"auto", "opaque"}, item.BackgroundOptions)
		}
		require.Equal(t, 1, item.MaxOutputCount)
		require.Equal(t, 16, item.MaxReferenceImages)
		require.Greater(t, item.Price1K, 0.0)
		require.Equal(t, []string{"generate", "edit", "inpaint"}, item.Operations)
	}
	require.Equal(t, []string{"1K", "2K"}, byModel(byGroup[21], "gpt-image-1").ImageSizes)
	require.Equal(t, []string{"1K", "2K", "4K"}, byModel(byGroup[21], "gpt-image-2").ImageSizes)
	require.ElementsMatch(t, []string{"gpt-image-1", "gpt-image-2"},
		[]string{byGroup[21][0].Model, byGroup[21][1].Model})

	// gemini 无映射回退：固定 1K 模型只开放 1K，支持高分辨率的模型开放三档尺寸。
	require.Len(t, byGroup[22], 3)
	require.Equal(t, []string{"1K"}, byModel(byGroup[22], "gemini-2.5-flash-image").ImageSizes)
	require.Equal(t, []string{"1K", "2K", "4K"}, byModel(byGroup[22], "gemini-3-pro-image").ImageSizes)
	flash31 := byModel(byGroup[22], "gemini-3.1-flash-image")
	require.Equal(t, []string{"512", "1K", "2K", "4K"}, flash31.ImageSizes)
	require.Equal(t, []string{"minimal", "high"}, flash31.ThinkingLevels)
	require.Equal(t, 14, flash31.MaxReferenceImages)
	require.ElementsMatch(t, []string{"gemini-2.5-flash-image", "gemini-3-pro-image", "gemini-3.1-flash-image"},
		[]string{byGroup[22][0].Model, byGroup[22][1].Model, byGroup[22][2].Model})

	// grok 回退：1K/2K 档 + generate/edit。
	require.Len(t, byGroup[23], 2)
	grok1 := byModel(byGroup[23], "grok-imagine-image-1.0")
	require.Equal(t, []string{"1K", "2K"}, grok1.ImageSizes)
	require.Empty(t, grok1.Qualities)
	require.Equal(t, []string{"generate", "edit"}, grok1.Operations)
	grok2 := byModel(byGroup[23], "grok-imagine-image-2.0")
	require.Equal(t, []string{"low", "medium"}, grok2.Qualities)
	require.Contains(t, grok2.AspectRatios, "21:9")
	require.Contains(t, grok2.AspectRatios, "5:2")
	require.Contains(t, grok2.AspectRatios, "auto")
	require.Equal(t, 1, grok2.MaxOutputCount)
	require.Equal(t, 3, grok2.MaxReferenceImages)

	// GPT Image 2 即使未配置 4K 覆盖价，也开放 4K 并回退默认价格。
	require.Len(t, byGroup[24], 1)
	require.Equal(t, "gpt-image-2", byGroup[24][0].Model)
	require.Equal(t, []string{"1K", "2K", "4K"}, byGroup[24][0].ImageSizes)
	require.InDelta(t, 0.02, byGroup[24][0].Price1K, 1e-9)
}

func TestCreativeFilterImageSizesForModel(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		model    string
		input    []string
		want     []string
	}{
		{
			name:     "gemini 2.5 image is 1K only",
			platform: capability.PlatformGemini,
			model:    "gemini-2.5-flash-image-preview",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"1K"},
		},
		{
			name:     "gemini lite image is 1K only",
			platform: capability.PlatformGemini,
			model:    "models/gemini-3.1-flash-lite-image",
			input:    []string{"1K", "4K"},
			want:     []string{"1K"},
		},
		{
			name:     "gemini 3 pro keeps configured tiers",
			platform: capability.PlatformGemini,
			model:    "gemini-3-pro-image",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"1K", "2K", "4K"},
		},
		{
			name:     "gemini 3.1 flash prepends 512 tier",
			platform: capability.PlatformGemini,
			model:    "gemini-3.1-flash-image",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"512", "1K", "2K", "4K"},
		},
		{
			name:     "custom model keeps configured tiers",
			platform: capability.PlatformGemini,
			model:    "custom-image-model",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"1K", "2K", "4K"},
		},
		{
			name:     "non gemini is unchanged",
			platform: capability.PlatformOpenAI,
			model:    "gpt-image-2",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"1K", "2K", "4K"},
		},
		{
			name:     "grok image models only expose 1K and 2K",
			platform: capability.PlatformGrok,
			model:    "grok-imagine-image-2.0",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"1K", "2K"},
		},
		{
			name:     "older gpt image models do not expose 4K",
			platform: capability.PlatformOpenAI,
			model:    "gpt-image-1",
			input:    []string{"1K", "2K", "4K"},
			want:     []string{"1K", "2K"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, creative.CreativeFilterImageSizesForModel(tt.platform, tt.model, tt.input))
		})
	}
}

// TestCreativePricingUsesResolvedPricingConfigPrice 校验创作台与模型广场共用共享价格配置图片定价。
func TestCreativePricingUsesResolvedPricingConfigPrice(t *testing.T) {
	price := 1.0
	resolver := newResolverWithPricingConfig(t, []routing.ModelPricingEntry{{
		Models:          []string{"gpt-image-2"},
		BillingMode:     routing.BillingModeImage,
		PerRequestPrice: &price,
	}})
	svc := newCreativeTestService()
	svc.ImageUnitPrice = creativePriceFixture(nil, resolver)
	group := newCreativeTestGroup()
	group.ID = 100

	require.InDelta(t, 1, svc.CreativePrice(context.Background(), creativeGroupProjection(group), "gpt-image-2", "1K"), 1e-9)
	require.InDelta(t, 1, svc.CreativePrice(context.Background(), creativeGroupProjection(group), "gpt-image-2", "2K"), 1e-9)
	require.InDelta(t, 1, svc.CreativePrice(context.Background(), creativeGroupProjection(group), "gpt-image-2", "4K"), 1e-9)

	// Gemini 512 优先匹配共享价格配置自定义 tier；未配置 512 时回退共享价格配置默认价格。
	price512 := 0.5
	defaultPrice := 1.25
	resolver = newResolverWithPricingConfig(t, []routing.ModelPricingEntry{{
		Models:          []string{"gemini-3.1-flash-image"},
		BillingMode:     routing.BillingModeImage,
		PerRequestPrice: &defaultPrice,
		Intervals: []routing.PricingInterval{{
			TierLabel:       "512",
			PerRequestPrice: &price512,
		}},
	}})
	svc.ImageUnitPrice = creativePriceFixture(nil, resolver)
	geminiGroup := newCreativeTestGroup()
	geminiGroup.ID = 100

	require.InDelta(t, price512, svc.CreativePrice(context.Background(), creativeGroupProjection(geminiGroup), "gemini-3.1-flash-image", "512"), 1e-9)
	resolver = newResolverWithPricingConfig(t, []routing.ModelPricingEntry{{
		Models:          []string{"gemini-3.1-flash-image"},
		BillingMode:     routing.BillingModeImage,
		PerRequestPrice: &defaultPrice,
	}})
	svc.ImageUnitPrice = creativePriceFixture(nil, resolver)
	require.InDelta(t, defaultPrice, svc.CreativePrice(context.Background(), creativeGroupProjection(geminiGroup), "gemini-3.1-flash-image", "512"), 1e-9)
}

func TestCreativeModelSettingsEmptyListClosesCreativeDirectory(t *testing.T) {
	svc := newCreativeTestService()
	svc.Settings = &creativeFakeSettingReader{enabled: true, models: []creative.CreativeModelSetting{}}
	models, err := svc.ListModels(context.Background(), 7)
	require.NoError(t, err)
	require.Empty(t, models.Data)
}

// TestValidateCreateParams 覆盖 CreateRun 的参数校验矩阵。
func TestValidateCreateParams(t *testing.T) {
	t.Run("OpenAI 模型参数通过且固定单输出", func(t *testing.T) {
		svc := newCreativeTestService()
		configureOpenAICreativeTestService(svc)
		params := validCreateParams()
		params.Model = "gpt-image-2"
		params.AspectRatio = "16:9"
		params.Quality = "auto"
		params.Background = "transparent"
		validated, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)
		require.Equal(t, 1, validated.OutputCount)
		require.Equal(t, "16:9", validated.AspectRatio)
		require.Equal(t, "auto", validated.Quality)
		require.Equal(t, "transparent", validated.Background)
	})

	t.Run("固定 PNG 输出且不接受输出格式参数", func(t *testing.T) {
		svc := newCreativeTestService()
		configureOpenAICreativeTestService(svc)
		params := validCreateParams()
		params.Model = "gpt-image-2"
		params.Background = "transparent"
		validated, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)
		require.Equal(t, "1:1", validated.AspectRatio)
		require.Equal(t, "medium", validated.Quality)
		require.Equal(t, "transparent", validated.Background)

		params.OutputCount = 11
		_, err = svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInvalidParams)
	})

	t.Run("Grok 2.0 质量比例通过且固定单输出", func(t *testing.T) {
		svc := newCreativeTestService()
		configureGrok2CreativeTestService(svc)
		params := validCreateParams()
		params.Model = "grok-imagine-image-2.0"
		params.AspectRatio = "21:9"
		params.Quality = "low"
		validated, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)
		require.Equal(t, "low", validated.Quality)
		require.Equal(t, "21:9", validated.AspectRatio)
		require.Equal(t, 1, validated.OutputCount)
	})

	t.Run("支持模型缺省参数自动选择产品默认值", func(t *testing.T) {
		svc := newCreativeTestService()
		configureGrok2CreativeTestService(svc)
		params := validCreateParams()
		params.Model = "grok-imagine-image-2.0"
		validated, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)
		require.Equal(t, "auto", validated.AspectRatio)
		require.Equal(t, "medium", validated.Quality)

		svc = newCreativeTestService()
		configureOpenAICreativeTestService(svc)
		params = validCreateParams()
		params.Model = "gpt-image-2"
		validated, err = svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)
		require.Equal(t, "1:1", validated.AspectRatio)
		require.Equal(t, "medium", validated.Quality)
		require.Equal(t, "auto", validated.Background)

		svc = newCreativeTestService()
		params = validCreateParams()
		validated, err = svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)
		require.Equal(t, "1:1", validated.AspectRatio)
		require.Equal(t, "minimal", validated.ThinkingLevel)
	})

	t.Run("Gemini 3.1 支持 512 和思考强度但保持单输出", func(t *testing.T) {
		svc := newCreativeTestService()
		params := validCreateParams()
		params.ImageSize = "512"
		params.AspectRatio = "21:9"
		params.ThinkingLevel = "high"
		validated, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)
		require.Equal(t, "512", validated.ImageSize)
		require.Equal(t, "high", validated.ThinkingLevel)

		params.OutputCount = 2
		_, err = svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInvalidParams)
	})

	t.Run("模型不存在", func(t *testing.T) {
		svc := newCreativeTestService()
		params := validCreateParams()
		params.Model = "gemini-9.9-unknown"
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInvalidModel)
	})

	t.Run("分组未开启图片生成", func(t *testing.T) {
		svc := newCreativeTestService()
		group := newCreativeTestGroup()
		group.AllowImageGeneration = false
		testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source).byID[12] = group
		params := validCreateParams()
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeGroupImageDisabled)
	})

	t.Run("grok 平台不支持 inpaint", func(t *testing.T) {
		svc := newCreativeTestService()
		group := newCreativeTestGroup()

		testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source).byID[12] = group
		testassert.MustType[*creativeFakeProviderRepo](testassert.MustType[creativeProviderReader](svc.ProviderRepo).source).byGroup[12] = []providercore.Record{{
			ID:          56,
			Platform:    capability.PlatformGrok,
			Status:      billing.StatusActive,
			Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{"grok-imagine": "grok-imagine"}},
		}}
		params := validCreateParams()
		params.Model = "grok-imagine"
		params.Operation = creative.CreativeOperationInpaint
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeOperationUnsupported)
	})

	t.Run("grok edit 支持单图且最多三张源图", func(t *testing.T) {
		svc := newCreativeTestService()
		group := newCreativeTestGroup()

		testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source).byID[12] = group
		testassert.MustType[*creativeFakeProviderRepo](testassert.MustType[creativeProviderReader](svc.ProviderRepo).source).byGroup[12] = []providercore.Record{{
			ID: 56, Platform: capability.PlatformGrok, Status: billing.StatusActive, Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{"grok-imagine": "grok-imagine"}},
		}}
		params := validCreateParams()
		params.Model = "grok-imagine"
		params.Operation = creative.CreativeOperationEdit
		params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)

		params.SourceImages = []creative.CreativeInputImage{
			{Bytes: makeTestPNG(t, 2, 2), Mime: "image/png"},
			{Bytes: makeTestPNG(t, 2, 2), Mime: "image/png"},
			{Bytes: makeTestPNG(t, 2, 2), Mime: "image/png"},
			{Bytes: makeTestPNG(t, 2, 2), Mime: "image/png"},
		}
		_, err = svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInvalidParams)
	})

	t.Run("prompt 超长", func(t *testing.T) {
		svc := newCreativeTestService()
		params := validCreateParams()
		params.Prompt = strings.Repeat("a", 9000)
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativePromptTooLong)
	})

	t.Run("非法 MIME", func(t *testing.T) {
		svc := newCreativeTestService()
		params := validCreateParams()
		params.SourceImages = []creative.CreativeInputImage{{Bytes: []byte("not-an-image"), Mime: "image/gif"}}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInvalidMime)
	})

	t.Run("单文件超限", func(t *testing.T) {
		svc := newCreativeTestService()
		svc.Options.MaxAssetBytes = 16
		params := validCreateParams()
		params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 4, 4), Mime: "image/png"}}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeAssetTooLarge)
	})

	t.Run("总输入超限", func(t *testing.T) {
		svc := newCreativeTestService()
		svc.Options.MaxTotalInputBytes = 100
		params := validCreateParams()
		params.SourceImages = []creative.CreativeInputImage{
			{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"},
			{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"},
		}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInputTooLarge)
	})

	t.Run("generate 参考图数量超限", func(t *testing.T) {
		svc := newCreativeTestService()
		configureOpenAICreativeTestService(svc)
		params := validCreateParams()
		params.Model = "gpt-image-2"
		params.SourceImages = make([]creative.CreativeInputImage, 17)
		for i := range params.SourceImages {
			params.SourceImages[i] = creative.CreativeInputImage{Bytes: makeTestPNG(t, 2, 2), Mime: "image/png"}
		}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInvalidParams)
	})

	t.Run("inpaint 缺 mask", func(t *testing.T) {
		svc := newCreativeTestService()
		configureOpenAICreativeTestService(svc)
		params := validCreateParams()
		params.Model = "gpt-image-2"
		params.Operation = creative.CreativeOperationInpaint
		params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeMaskRequired)
	})

	t.Run("gemini inpaint 直接拒绝", func(t *testing.T) {
		svc := newCreativeTestService()
		params := validCreateParams()
		params.Operation = creative.CreativeOperationInpaint
		params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}}
		params.Mask = &creative.CreativeInputImage{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeOperationUnsupported)
	})

	t.Run("gemini edit 不接受 mask", func(t *testing.T) {
		svc := newCreativeTestService()
		params := validCreateParams()
		params.Operation = creative.CreativeOperationEdit
		params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}}
		params.Mask = &creative.CreativeInputImage{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInvalidParams)
	})

	t.Run("mask 非 PNG", func(t *testing.T) {
		svc := newCreativeTestService()
		configureOpenAICreativeTestService(svc)
		params := validCreateParams()
		params.Model = "gpt-image-2"
		params.Operation = creative.CreativeOperationInpaint
		params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}}
		params.Mask = &creative.CreativeInputImage{Bytes: []byte{0xFF, 0xD8, 0xFF, 0x00}, Mime: "image/jpeg"}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeMaskRequired)
	})

	t.Run("mask 尺寸不一致", func(t *testing.T) {
		svc := newCreativeTestService()
		configureOpenAICreativeTestService(svc)
		params := validCreateParams()
		params.Model = "gpt-image-2"
		params.Operation = creative.CreativeOperationInpaint
		params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}}
		params.Mask = &creative.CreativeInputImage{Bytes: makeTestPNG(t, 16, 16), Mime: "image/png"}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeMaskSizeMismatch)
	})

	t.Run("非 inpaint 不允许 mask", func(t *testing.T) {
		svc := newCreativeTestService()
		configureOpenAICreativeTestService(svc)
		params := validCreateParams()
		params.Model = "gpt-image-2"
		params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}}
		params.Mask = &creative.CreativeInputImage{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInvalidParams)
	})

	t.Run("edit 必须带源图", func(t *testing.T) {
		svc := newCreativeTestService()
		params := validCreateParams()
		params.Operation = creative.CreativeOperationEdit
		_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.ErrorIs(t, err, creative.ErrCreativeInvalidParams)
	})

	t.Run("合法 inpaint 通过且默认值生效", func(t *testing.T) {
		svc := newCreativeTestService()
		configureOpenAICreativeTestService(svc)
		params := validCreateParams()
		params.Model = "gpt-image-2"
		params.Operation = creative.CreativeOperationInpaint
		params.ImageSize = ""
		params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}}
		params.Mask = &creative.CreativeInputImage{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}
		validated, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)
		require.Equal(t, "1K", validated.ImageSize)
		require.Equal(t, 1, validated.OutputCount)
		require.NotEmpty(t, validated.Fingerprint)
		require.NotEmpty(t, validated.PromptHash)
	})
}

// TestCreateRunIdempotency 覆盖幂等重放与幂等冲突。
func TestCreateRunIdempotency(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()

	first, err := svc.CreateRun(ctx, testCreativeScope(7), validCreateParams(), "idem-key-1")
	require.NoError(t, err)
	require.False(t, first.IdempotentReplay)
	require.Equal(t, creative.CreativeRunStatusQueued, first.Status)
	require.True(t, creative.IsValidCreativeRunID(first.ID))

	// 相同 Key + 相同请求体：返回原任务并标记重放。
	replay, err := svc.CreateRun(ctx, testCreativeScope(7), validCreateParams(), "idem-key-1")
	require.NoError(t, err)
	require.True(t, replay.IdempotentReplay)
	require.Equal(t, first.ID, replay.ID)

	// 相同 Key + 不同请求体：返回冲突。
	conflictParams := validCreateParams()
	conflictParams.Prompt = "完全不同的 prompt"
	_, err = svc.CreateRun(ctx, testCreativeScope(7), conflictParams, "idem-key-1")
	require.ErrorIs(t, err, creative.ErrCreativeRunIdempotencyConflict)

	// 相同请求体 + 不同 Key：创建独立任务。
	retry, err := svc.CreateRun(ctx, testCreativeScope(7), validCreateParams(), "idem-key-2")
	require.NoError(t, err)
	require.False(t, retry.IdempotentReplay)
	require.NotEqual(t, first.ID, retry.ID)

	// 计费预占与入队各发生两次（重放不重复扣费/入队）。
	require.Equal(t, 2, testassert.MustType[*creativeFakeBillingRepo](creativeFixtureBilling(svc)).reserveN)
	require.Len(t, testassert.MustType[*creativeFakeQueue](svc.Queue).enqueued, 2)
}

// TestCreativePricingIgnoresQuality 校验质量不改价且每次任务固定一张输出。
func TestCreativePricingIgnoresQuality(t *testing.T) {
	svc := newCreativeTestService()
	configureOpenAICreativeTestService(svc)
	high := validCreateParams()
	high.Model = "gpt-image-2"
	high.Quality = "high"
	highRun, err := svc.CreateRun(context.Background(), testCreativeScope(7), high, "quality-high")
	require.NoError(t, err)

	low := high
	low.Quality = "low"
	lowRun, err := svc.CreateRun(context.Background(), testCreativeScope(7), low, "quality-low")
	require.NoError(t, err)

	require.Equal(t, highRun.EstimatedCost, lowRun.EstimatedCost)
	require.InDelta(t, highRun.EstimatedCost, highRun.HoldAmount, 1e-9)
	require.Len(t, testassert.MustType[*creativeFakeRunRepo](svc.Repo).outputs[highRun.ID], 1)
}

// TestCreativeEnabledGate 检查数据库运行时开关 creative_enabled：
// 关闭时 ListModels 返回空列表（前端展示“已停用”空态），CreateRun 返回 ErrCreativeDisabled。
func TestCreativeEnabledGate(t *testing.T) {
	t.Run("运行时关闭时 ListModels 返回空列表", func(t *testing.T) {
		svc := newCreativeTestService()
		svc.Settings = &creativeFakeSettingReader{enabled: false}

		models, err := svc.ListModels(context.Background(), 7)
		require.NoError(t, err)
		require.NotNil(t, models)
		require.Empty(t, models.Data)
	})

	t.Run("运行时关闭时 CreateRun 拒绝", func(t *testing.T) {
		svc := newCreativeTestService()
		svc.Settings = &creativeFakeSettingReader{enabled: false}

		_, err := svc.CreateRun(context.Background(), testCreativeScope(7), validCreateParams(), "")
		require.ErrorIs(t, err, creative.ErrCreativeDisabled)
	})

	t.Run("运行时开启时 ListModels 正常返回", func(t *testing.T) {
		svc := newCreativeTestService()
		svc.Settings = &creativeFakeSettingReader{enabled: true, models: []creative.CreativeModelSetting{{
			GroupID: 12, Model: "gemini-3.1-flash-image", Operations: []string{creative.CreativeOperationGenerate},
		}}}

		models, err := svc.ListModels(context.Background(), 7)
		require.NoError(t, err)
		require.NotEmpty(t, models.Data)
	})

	t.Run("进程配置关闭时运行时开关无法打开", func(t *testing.T) {
		svc := newCreativeTestService()
		svc.Options.Enabled = false
		svc.Settings = &creativeFakeSettingReader{enabled: true}

		models, err := svc.ListModels(context.Background(), 7)
		require.NoError(t, err)
		require.Empty(t, models.Data)
	})
}

// TestCreativeGeminiNanoBananaCandidates 校验创作台支持 Gemini nano-banana 别名族。
func TestCreativeGeminiNanoBananaCandidates(t *testing.T) {
	for _, model := range []string{"nano-banana-pro", "nano-banana-2", "NANO-BANANA-PRO", "models/nano-banana-2"} {
		require.True(t, creative.IsCreativeGeminiImageModel(model), "模型 %q 应识别为 Gemini 生图模型", model)
		require.True(t, creative.CreativePlatformImageModel(capability.PlatformGemini, model), "模型 %q 应通过执行器图片模型校验", model)
		capabilities := creative.CreativeCapabilitiesForModel(capability.PlatformGemini, model)
		require.NotEmpty(t, capabilities.AspectRatios, "模型 %q 应暴露 Gemini 图片能力", model)
	}
	require.False(t, creative.IsCreativeGeminiImageModel("nano-banana"), "不完整的 nano-banana 名称不应被识别")

	provider := &providercore.Record{Platform: capability.PlatformGemini, Credentials: map[string]any{}}
	models := creativeProviderModelsForTest(t, provider)
	require.NotContains(t, models, "nano-banana-pro")
	provider.Credentials["model_whitelist"] = []string{"nano-banana-pro", "nano-banana-2"}
	models = creativeProviderModelsForTest(t, provider)
	require.Contains(t, models, "nano-banana-pro")
	require.Contains(t, models, "nano-banana-2")
}

func TestCreativeModelSettingsFilterAndCreateValidation(t *testing.T) {
	svc := newCreativeTestService()
	svc.Settings = &creativeFakeSettingReader{
		enabled: true,
		models: []creative.CreativeModelSetting{{
			GroupID:    12,
			Model:      "gemini-3.1-flash-image",
			Operations: []string{creative.CreativeOperationEdit},
		}},
	}

	models, err := svc.ListModels(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, models.Data, 1)
	require.Equal(t, []string{creative.CreativeOperationEdit}, models.Data[0].Operations)

	params := validCreateParams()
	params.Operation = creative.CreativeOperationGenerate
	_, err = svc.ValidateCreateParams(context.Background(), 7, &params)
	require.ErrorIs(t, err, creative.ErrCreativeOperationUnsupported)

	params.Operation = creative.CreativeOperationEdit
	params.SourceImages = []creative.CreativeInputImage{{Bytes: makeTestPNG(t, 8, 8), Mime: "image/png"}}
	_, err = svc.ValidateCreateParams(context.Background(), 7, &params)
	require.NoError(t, err)
}

// TestCreativeUnpricedModelDoesNotReserve 检查缺价型号的目录过滤和创建拒绝。
func TestCreativeUnpricedModelDoesNotReserve(t *testing.T) {
	svc := newCreativeTestService()
	svc.ImageUnitPrice = creativePriceFixture(billingtestkit.Calculator(nil, nil), nil)
	listed, err := svc.ListModels(context.Background(), 7)
	require.NoError(t, err)
	require.Empty(t, listed.Data)
	_, err = svc.CreateRun(context.Background(), testCreativeScope(7), validCreateParams(), "unpriced-image")
	require.Error(t, err)
	require.Zero(t, testassert.MustType[*creativeFakeBillingRepo](creativeFixtureBilling(svc)).reserveN)
}

// TestBuildCreativeRequestFingerprint 校验指纹确定性：输入不变指纹不变，任一字段变化指纹变化。
func TestBuildCreativeRequestFingerprint(t *testing.T) {
	base := creative.CreativeFingerprintPayload{
		GroupID:      12,
		Model:        "gemini-3.1-flash-image",
		Operation:    creative.CreativeOperationGenerate,
		PromptSHA256: creative.Sha256Hex([]byte("hello")),
		ImageSHA256:  []string{creative.Sha256Hex([]byte("img"))},
		ImageSize:    "1K",
		AspectRatio:  "1:1",
		OutputCount:  1,
	}
	first := creative.BuildCreativeRequestFingerprint(base)
	require.NotEmpty(t, first)
	require.Equal(t, first, creative.BuildCreativeRequestFingerprint(base))

	changedPrompt := base
	changedPrompt.PromptSHA256 = creative.Sha256Hex([]byte("world"))
	require.NotEqual(t, first, creative.BuildCreativeRequestFingerprint(changedPrompt))

	changedGroup := base
	changedGroup.GroupID = 13
	require.NotEqual(t, first, creative.BuildCreativeRequestFingerprint(changedGroup))
}

// TestCreativeMixedGroupCatalog 按实际提供商能力提供同组多平台模型，并固化创建时的供应商。
func TestCreativeMixedGroupCatalog(t *testing.T) {
	svc := newCreativeTestService()
	rows := testassert.MustType[*creativeFakeProviderRepo](testassert.MustType[creativeProviderReader](svc.ProviderRepo).source)
	settings := testassert.MustType[*creativeFakeSettingReader](svc.Settings)
	settings.models = nil
	rows.byGroup[12] = nil
	models := map[string]string{creative.PlatformOpenAI: "gpt-image-2", creative.PlatformGemini: "gemini-3.1-flash-image", creative.PlatformGrok: "grok-imagine-image-2.0"}
	for platform, model := range models {
		rows.byGroup[12] = append(rows.byGroup[12], providercore.Record{ID: int64(len(rows.byGroup[12]) + 55), Platform: platform, Type: "apikey", Status: "active", Schedulable: true, Credentials: map[string]any{"model_whitelist": []string{model}}})
		settings.models = append(settings.models, creative.CreativeModelSetting{GroupID: 12, Model: model, Operations: []string{"generate", "edit", "inpaint"}})
	}
	listed, err := svc.ListModels(context.Background(), 7)
	require.NoError(t, err)
	require.Len(t, listed.Data, 3)
	for platform, model := range models {
		params := validCreateParams()
		params.Model = model
		validated, err := svc.ValidateCreateParams(context.Background(), 7, &params)
		require.NoError(t, err)
		require.Equal(t, platform, validated.Platform)
		for _, entry := range listed.Data {
			if entry.Model != model {
				continue
			}
			require.Equal(t, platform == creative.PlatformOpenAI, creative.CreativeContainsOption(entry.Operations, "inpaint"))
		}
	}
	// 禁用提供商媒体协议后，模型不能只凭白名单继续出现在目录。
	for i := range rows.byGroup[12] {
		rows.byGroup[12][i].Credentials["upstream_protocols"] = []string{}
	}
	listed, err = svc.ListModels(context.Background(), 7)
	require.NoError(t, err)
	require.Empty(t, listed.Data)
}

// TestCreativeRejectsClientOnlyGroupBeforeFunding 检查专用客户端组在列出候选和预留资金前被拒绝。
func TestCreativeRejectsClientOnlyGroupBeforeFunding(t *testing.T) {
	svc := newCreativeTestService()
	groups := testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source)
	groups.byID[12].ClaudeCodeOnly = true
	fallback := int64(99)
	groups.byID[12].FallbackGroupID = &fallback
	groups.active[0] = *groups.byID[12]
	listed, err := svc.ListModels(context.Background(), 7)
	require.NoError(t, err)
	require.Empty(t, listed.Data)
	candidates, err := svc.ListCreativeModelCandidates(context.Background())
	require.NoError(t, err)
	require.Empty(t, candidates)
	_, err = svc.CreateRun(context.Background(), testCreativeScope(7), validCreateParams(), "client-only")
	require.ErrorIs(t, err, creative.ErrCreativeGroupForbidden)
	require.Zero(t, testassert.MustType[*creativeFakeBillingRepo](creativeFixtureBilling(svc)).reserveN)
}

func (a creativeCatalogTestProviders) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]creative.CatalogProvider, error) {
	return a.values, nil
}

// creativeProviderModelsForTest 通过 CreativeModelsForGroup 读取提供商的模型候选。
func creativeProviderModelsForTest(t *testing.T, value *providercore.Record) []string {
	t.Helper()
	value = providercore.CloneRecord(value)
	value.Status = "active"
	if value.Type == "" {
		value.Type = "apikey"
	}
	value.Schedulable = true
	svc := &creative.Public{ProviderRepo: creativeCatalogTestProviders{[]creative.CatalogProvider{creativeprovider.CatalogProvider(value)}}}

	models, err := svc.CreativeModelsForGroup(context.Background(), &creative.GroupView{ID: 12, Operations: creative.OperationsForGroup(false, nil)})
	require.NoError(t, err)
	result := make([]string, 0, len(models))
	for model := range models {
		result = append(result, model)
	}
	sort.Strings(result)
	return result
}

func byModel(models []creative.CreativeModelPublic, model string) creative.CreativeModelPublic {
	for _, item := range models {
		if item.Model == model {
			return item
		}
	}
	return creative.CreativeModelPublic{}
}

func makeTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// configureOpenAICreativeTestService 将校验夹具切换到 OpenAI，检查 PNG inpaint 规则。
func configureOpenAICreativeTestService(svc *creative.Public) {
	group := newCreativeTestGroup()
	group.Name = "OpenAI Image"

	testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source).byID[group.ID] = group
	testassert.MustType[*creativeFakeProviderRepo](testassert.MustType[creativeProviderReader](svc.ProviderRepo).source).byGroup[group.ID] = []providercore.Record{{
		ID:          57,
		Platform:    capability.PlatformOpenAI,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"gpt-image-2": "gpt-image-2"}},
	}}
	testassert.MustType[*creativeFakeSettingReader](svc.Settings).models = []creative.CreativeModelSetting{{
		GroupID: group.ID, Model: "gpt-image-2",
		Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit, creative.CreativeOperationInpaint},
	}}
}

// configureGrok2CreativeTestService 将校验夹具切换到支持质量的 Grok 2.0。
func configureGrok2CreativeTestService(svc *creative.Public) {
	group := newCreativeTestGroup()
	group.Name = "Grok Imagine 2"

	testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source).byID[group.ID] = group
	testassert.MustType[*creativeFakeProviderRepo](testassert.MustType[creativeProviderReader](svc.ProviderRepo).source).byGroup[group.ID] = []providercore.Record{{
		ID:          58,
		Platform:    capability.PlatformGrok,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"grok-imagine-image-2.0": "grok-imagine-image-2.0"}},
	}}
	testassert.MustType[*creativeFakeSettingReader](svc.Settings).models = []creative.CreativeModelSetting{{
		GroupID: group.ID, Model: "grok-imagine-image-2.0",
		Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit},
	}}
}

func (r *creativePricingConfigFixture) ListAll(context.Context) ([]routing.PricingConfig, error) {
	return []routing.PricingConfig{{ID: 1, Name: "test-price-config", Status: billing.StatusActive, GroupIDs: []int64{100}, ModelPricing: r.cards}}, nil
}

func (r *creativePricingConfigFixture) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return map[int64]string{100: r.platform}, nil
}

func newResolverWithPricingConfig(t *testing.T, cards []routing.ModelPricingEntry) *billing.PriceResolver {
	t.Helper()
	platform := capability.PlatformAnthropic
	calculator := billingtestkit.Calculator(nil, map[string]*pricing.ModelPricing{"claude-sonnet-4": {InputPricePerToken: 3e-6, OutputPricePerToken: 15e-6, CacheCreationPricePerToken: 3.75e-6, CacheReadPricePerToken: 0.3e-6, SupportsCacheBreakdown: false}})
	pricingConfigs := routing.NewPricingConfigService(&creativePricingConfigFixture{cards: cards, platform: platform}, nil, routing.PricingConfigOptions{Warn: slog.Warn, Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation})
	return billing.NewPriceResolver(pricingConfigs, calculator, modelidentity.Identity, func(model string, err error) {
		slog.Debug("failed to get model pricing from model catalog, using fallback", "model", model, "error", err)
	})
}

// BenchmarkCreativeConfiguredValidation 测量 65 个提供商的单型号校验，包含提供商规则准备。
func BenchmarkCreativeConfiguredValidation(b *testing.B) {
	service := newCreativeTestService()
	configureOpenAICreativeTestService(service)
	rows := testassert.MustType[*creativeFakeProviderRepo](testassert.MustType[creativeProviderReader](service.ProviderRepo).source)
	whitelist := make([]any, 186)
	mapping := map[string]any{}
	for i := range whitelist {
		model := fmt.Sprintf("probe-model-%03d", i)
		if i == 0 {
			model = "gpt-image-2"
		}
		whitelist[i] = model
		mapping[model] = model
	}
	records := make([]providercore.Record, 65)
	for i := range records {
		records[i] = providercore.Record{ID: int64(i + 1), Platform: "openai", Type: "apikey", Status: "active", Schedulable: true, Credentials: map[string]any{"model_whitelist": whitelist, "model_mapping": mapping}}
	}
	rows.byGroup[12] = records
	params := validCreateParams()
	params.Model = "gpt-image-2"
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := service.ValidateCreateParams(context.Background(), 7, &params); err != nil {
			b.Fatal(err)
		}
	}
}
