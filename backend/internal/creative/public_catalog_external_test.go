package creative_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
)

// TestCreativeCatalogueIncludesGroupMappingTargets combines exact or wildcard
// group aliases with an unrestricted OpenAI fixture. Concrete image targets
// appear, while requested-stage restriction and provider allowlist changes
// exclude the checked target.
func TestCreativeCatalogueIncludesGroupMappingTargets(t *testing.T) {
	for _, source := range []string{"draw-alias", "draw-*"} {
		t.Run(source, func(t *testing.T) {
			value := &providercore.Record{Platform: creative.PlatformOpenAI, Type: "apikey", Status: "active", Schedulable: true, Credentials: map[string]any{}}
			service := &creative.Public{Attributes: &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}, ProviderRepo: creativeCatalogTestProviders{[]creative.CatalogProvider{creativeprovider.CatalogProvider(value)}}}
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

// TestCreativeOperationsForPlatform compares operation lists for Gemini,
// OpenAI, Grok and an unsupported platform. The assertions retain OpenAI
// inpainting, two operations for Gemini/Grok and nil for Anthropic.
func TestCreativeOperationsForPlatform(t *testing.T) {
	require.Equal(t, []string{"generate", "edit"}, creative.CreativeOperationsForPlatform(capability.PlatformGemini))
	require.Equal(t, []string{"generate", "edit", "inpaint"}, creative.CreativeOperationsForPlatform(capability.PlatformOpenAI))
	require.Equal(t, []string{"generate", "edit"}, creative.CreativeOperationsForPlatform(capability.PlatformGrok))
	require.Nil(t, creative.CreativeOperationsForPlatform(capability.PlatformAnthropic))
}

// TestCreativeGrokDefaultImageCandidates queries a Grok provider with empty
// credentials. Its configured candidate directory stays empty, so an
// unconfigured fixture does not introduce default image names.
func TestCreativeGrokDefaultImageCandidates(t *testing.T) {
	provider := &providercore.Record{Platform: capability.PlatformGrok, Credentials: map[string]any{}}
	models := creativeProviderModelsForTest(t, provider)
	require.Empty(t, models)
}

// TestCreativeGrokDefaultImageCandidatesWithQualityWhitelist configures the
// exact Grok quality image name. The sorted directory contains that single
// name, allowing this proxy variant to pass finite candidate discovery.
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

// TestCreativeMappedFinalModelCapability maps public aliases to image and text
// upstream names. The text alias is excluded, while the image alias and
// directly requestable image target remain in the inspected directory.
func TestCreativeMappedFinalModelCapability(t *testing.T) {
	provider := &providercore.Record{
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"draw-alias": "gpt-image-2", "text-alias": "gpt-5.4"},
			"model_whitelist": []string{"gpt-image-2", "gpt-5.4"},
		},
	}
	models := creativeProviderModelsForTest(t, provider)
	// A text model target cannot enter the image catalogue.
	require.NotContains(t, models, "text-alias")
	// Aliases resolving to image models are eligible catalogue entries.
	provider.Credentials["model_mapping"] = map[string]any{"draw-alias": "gpt-image-2"}
	models = creativeProviderModelsForTest(t, provider)
	require.Contains(t, models, "draw-alias")
	require.Contains(t, models, "gpt-image-2", "最终模型也可以直接请求")
}

// TestCreativeGrokConfiguredImageWhitelistCandidates configures one Grok lite
// image name without mappings. The directory returns that exact variant as its
// sole candidate through provider allowlist discovery.
func TestCreativeGrokConfiguredImageWhitelistCandidates(t *testing.T) {
	provider := &providercore.Record{
		Platform: capability.PlatformGrok,
		Credentials: map[string]any{
			"model_whitelist": []string{"grok-imagine-image-lite"},
		},
	}
	models := creativeProviderModelsForTest(t, provider)
	require.Empty(t, models)
}

// TestCreativeGeminiConfiguredImageWhitelistCandidates mixes a Gemini image
// variant with a text name in the allowlist. The sorted result retains the
// image name alone despite absent provider mappings.
func TestCreativeGeminiConfiguredImageWhitelistCandidates(t *testing.T) {
	provider := &providercore.Record{
		Platform: capability.PlatformGemini,
		Credentials: map[string]any{
			"model_whitelist": []string{"gemini-3-pro-image-quality", "gemini-2.5-flash"},
		},
	}
	models := creativeProviderModelsForTest(t, provider)
	require.Empty(t, models)
}

// TestCreativeDirectoryAndCreateUseGroupPolicy exercises requested,
// group-mapped and upstream restrictions across the three image platforms.
// Listing and validation resolve one group hop and one provider hop without
// linked pricing configuration, then reject an empty restricted allowlist.
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

				// Keep the enabled restriction while clearing
				// its permitted names, so both
				// discovery and direct validation exercise
				// configured denial rather than an
				// absent policy.
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

// TestCreativeDirectoryIgnoresDisabledPolicyDraft stores a restrictive mapping
// behind a disabled policy. Listing keeps one configured model and validation
// returns its supplied model, leaving the inactive draft unapplied.
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

// TestCreativeListModelsFiltersAndContent adds image-disabled and providerless
// groups to the fixture directory. Returned entries stay in the eligible group
// and expose its checked operations, sizes, prices and model identity.
func TestCreativeListModelsFiltersAndContent(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()
	groupRepo := testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source)

	// Groups with image generation disabled are excluded.
	noImage := newCreativeTestGroup()
	noImage.ID = 13
	noImage.AllowImageGeneration = false
	groupRepo.byID[13] = noImage
	groupRepo.active = append(groupRepo.active, *noImage)

	// Groups without image-capable providers are excluded.
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
		// Permit floating-point rounding while comparing these
		// configured dollar prices.
		require.InDelta(t, 0.02, item.Price1K, 1e-9)
		require.InDelta(t, 0.04, item.Price2K, 1e-9)
		require.Equal(t, "gemini-3.1-flash-image", item.Model)
	}
}

// TestCreativeListModelsFallbacks supplies explicit image candidates across
// OpenAI, Gemini and Grok with catalogue pricing. Assertions compare their
// listed sizes, optional controls and reference limits, including GPT Image
// four-K support with only a one-K price override.
func TestCreativeListModelsFallbacks(t *testing.T) {
	svc := newCreativeTestService()
	svc.ImageUnitPrice = creativePriceFixture(newCreativeMediaCalculator(), nil)
	ctx := context.Background()
	groupRepo := testassert.MustType[*creativeFakeGroupRepo](testassert.MustType[creativeGroupReader](svc.GroupRepo).source)
	providerRepo := testassert.MustType[*creativeFakeProviderRepo](testassert.MustType[creativeProviderReader](svc.ProviderRepo).source)

	// The OpenAI allowlist supplies GPT Image candidates without provider
	// mappings or price overrides.
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

	// The Gemini allowlist mixes fixed-1K and higher-resolution image models.
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

	// Grok models expose 1K/2K and generate/edit with default pricing.
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

	// A one-K price override supplies pricing while the mapped GPT Image two
	// model retains all of its supported capability sizes.
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

	// Both allowlisted OpenAI models have default prices; GPT Image 2 also
	// exposes 4K.
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

	// Gemini tiers follow model capabilities, including 512 for Flash 3.1.
	require.Len(t, byGroup[22], 3)
	require.Equal(t, []string{"1K"}, byModel(byGroup[22], "gemini-2.5-flash-image").ImageSizes)
	require.Equal(t, []string{"1K", "2K", "4K"}, byModel(byGroup[22], "gemini-3-pro-image").ImageSizes)
	flash31 := byModel(byGroup[22], "gemini-3.1-flash-image")
	require.Equal(t, []string{"512", "1K", "2K", "4K"}, flash31.ImageSizes)
	require.Equal(t, []string{"minimal", "high"}, flash31.ThinkingLevels)
	require.Equal(t, 14, flash31.MaxReferenceImages)
	require.ElementsMatch(t, []string{"gemini-2.5-flash-image", "gemini-3-pro-image", "gemini-3.1-flash-image"},
		[]string{byGroup[22][0].Model, byGroup[22][1].Model, byGroup[22][2].Model})

	// Grok defaults include 1K/2K and generate/edit.
	require.Len(t, byGroup[23], 1)
	grok2 := byModel(byGroup[23], "grok-imagine-image-2.0")
	require.Equal(t, []string{"low", "medium"}, grok2.Qualities)
	require.Contains(t, grok2.AspectRatios, "21:9")
	require.Contains(t, grok2.AspectRatios, "5:2")
	require.Contains(t, grok2.AspectRatios, "auto")
	require.Equal(t, 1, grok2.MaxOutputCount)
	require.Equal(t, 3, grok2.MaxReferenceImages)

	// A missing 4K price override does not remove GPT Image 2's 4K capability.
	require.Len(t, byGroup[24], 1)
	require.Equal(t, "gpt-image-2", byGroup[24][0].Model)
	require.Equal(t, []string{"1K", "2K", "4K"}, byGroup[24][0].ImageSizes)
	// Permit floating-point rounding for the configured one-K dollar price.
	require.InDelta(t, 0.02, byGroup[24][0].Price1K, 1e-9)
}

// TestCreativeModelSettingsEmptyListClosesCreativeDirectory enables the runtime
// but supplies no saved model entries. Listing succeeds with an empty
// directory, keeping configured model admission separate from feature
// enablement.
func TestCreativeModelSettingsEmptyListClosesCreativeDirectory(t *testing.T) {
	svc := newCreativeTestService()
	svc.Settings = &creativeFakeSettingReader{enabled: true, models: []creative.CreativeModelSetting{}}
	models, err := svc.ListModels(context.Background(), 7)
	require.NoError(t, err)
	require.Empty(t, models.Data)
}

// TestCreativeGeminiNanoBananaCandidates checks the listed complete proxy
// aliases and rejects the incomplete name. Their unspecified controls stay
// empty, while configured allowlist entries make the named variants
// discoverable.
func TestCreativeGeminiNanoBananaCandidates(t *testing.T) {
	for _, model := range []string{"nano-banana-pro", "nano-banana-2", "NANO-BANANA-PRO", "models/nano-banana-2"} {
		capabilities := creative.CreativeCapabilitiesForModel(capability.PlatformGemini, model)
		require.Empty(t, capabilities.AspectRatios, "未配置公共身份的模型 %q 保持未知参数集合", model)
	}

	provider := &providercore.Record{Platform: capability.PlatformGemini, Credentials: map[string]any{}}
	models := creativeProviderModelsForTest(t, provider)
	require.NotContains(t, models, "nano-banana-pro")
	provider.Credentials["model_whitelist"] = []string{"nano-banana-pro", "nano-banana-2"}
	models = creativeProviderModelsForTest(t, provider)
	require.NotContains(t, models, "nano-banana-pro")
	require.NotContains(t, models, "nano-banana-2")
}

// TestCreativeMixedGroupCatalog configures three platform-specific image models
// in one group. It checks selected platforms and OpenAI inpaint exposure, then
// empties upstream protocol sets and requires an empty directory.
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
	// An explicitly empty native protocol set closes new creative routes even
	// for saved and allowlisted model names; discovery must evaluate this current
	// eligibility separately from the finite candidate inventory.
	for i := range rows.byGroup[12] {
		rows.byGroup[12][i].Credentials["upstream_protocols"] = []string{}
	}
	listed, err = svc.ListModels(context.Background(), 7)
	require.NoError(t, err)
	require.Empty(t, listed.Data)
}

// TestCreativeCatalogueReturnsAttributeFailure supplies configured image candidates
// while the attribute repository fails. Discovery must return unavailable rather
// than present a successfully resolved empty model list to the user.
func TestCreativeCatalogueReturnsAttributeFailure(t *testing.T) {
	value := &providercore.Record{Platform: creative.PlatformOpenAI, Type: "apikey", Status: "active", Schedulable: true, Credentials: map[string]any{"model_whitelist": []string{"gpt-image-2"}}}
	service := &creative.Public{Attributes: &routing.ModelAttributeService{Repo: creativeAttributeFailureRepo{}, Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}, ProviderRepo: creativeCatalogTestProviders{[]creative.CatalogProvider{creativeprovider.CatalogProvider(value)}}}
	group := &creative.GroupView{ID: 1, Operations: map[string][]string{creative.PlatformOpenAI: {creative.CreativeOperationGenerate}}}
	models, err := service.CreativeModelsForGroup(context.Background(), group)
	require.ErrorContains(t, err, "MODEL_ATTRIBUTES_UNAVAILABLE")
	require.Empty(t, models)
}

// TestCreativeGIFQuotesAndProviderSwitch exercises a mixed-capability group.
// An enabled route exposes GIF controls and quotes requested frames; disabling
// every matching provider rejects new GIF tasks without reserving additional funds.
func TestCreativeGIFQuotesAndProviderSwitch(t *testing.T) {
	svc := newCreativeTestService()
	configureOpenAICreativeTestService(svc)
	providers := testassert.MustType[*creativeFakeProviderRepo](testassert.MustType[creativeProviderReader](svc.ProviderRepo).source)
	model := "gpt-image-2.6-flare"
	providers.byGroup[12] = []providercore.Record{
		{ID: 57, Platform: capability.PlatformOpenAI, Type: providercore.ProviderTypeAPIKey, Status: billing.StatusActive, Schedulable: true, Credentials: map[string]any{"model_mapping": map[string]any{model: model}}, Extra: map[string]any{"images_gif_enabled": false}},
		{ID: 58, Platform: capability.PlatformOpenAI, Type: providercore.ProviderTypeOAuth, Status: billing.StatusActive, Schedulable: true, Credentials: map[string]any{"model_mapping": map[string]any{model: model}}},
	}
	testassert.MustType[*creativeFakeSettingReader](svc.Settings).models = []creative.CreativeModelSetting{{GroupID: 12, Model: model, Operations: []string{creative.CreativeOperationGenerate, creative.CreativeOperationEdit, creative.CreativeOperationInpaint}}}
	svc.ImageUnitPrice = func(context.Context, *creative.GroupView, string, string) (float64, bool) { return 0.25, true }
	params := validCreateParams()
	params.Model, params.OutputFormat = model, "gif"
	frames, delay := 4, 255
	params.FrameCount, params.FrameDurationMS = &frames, &delay
	run, err := svc.CreateRun(context.Background(), testCreativeScope(7), params, "gif-four")
	require.NoError(t, err)
	require.Equal(t, 4, run.FrameCount)
	require.Equal(t, 260, run.FrameDurationMS)
	require.Equal(t, "gif", run.OutputFormat)
	require.InDelta(t, 1.0, run.EstimatedCost, 1e-9)
	frames = 5
	_, err = svc.CreateRun(context.Background(), testCreativeScope(7), params, "gif-four")
	require.Error(t, err)
	providers.byGroup[12][1].Extra = map[string]any{"images_gif_enabled": false}
	_, err = svc.CreateRun(context.Background(), testCreativeScope(7), params, "gif-disabled")
	require.ErrorIs(t, err, creative.ErrCreativeOperationUnsupported)
}

// TestCreativeGIFFormatsByOperation compares discovery with operation admission.
// Generation-only, edit-only, disabled, and restricted routes must advertise GIF
// exactly where the same provider snapshots permit a submitted task.
func TestCreativeGIFFormatsByOperation(t *testing.T) {
	var reference bytes.Buffer
	require.NoError(t, png.Encode(&reference, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	for _, test := range []struct {
		name       string   // scenario display name
		generation string   // native generation transport mode
		edit       string   // native editing transport mode
		operations []string // administrator-admitted operation names
		disabled   bool     // provider GIF switch disabled
		gifOps     []string // operations with usable GIF routes
	}{
		{name: "generation only", generation: "both", edit: "streaming", operations: []string{"generate", "edit", "inpaint"}, gifOps: []string{"generate"}},
		{name: "editing only", generation: "streaming", edit: "both", operations: []string{"generate", "edit"}, gifOps: []string{"edit"}},
		{name: "disabled provider", generation: "both", edit: "both", operations: []string{"generate", "edit"}, disabled: true},
		{name: "generation not admitted", generation: "both", edit: "streaming", operations: []string{"edit"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc := newCreativeTestService()
			configureOpenAICreativeTestService(svc)
			providers := testassert.MustType[*creativeFakeProviderRepo](testassert.MustType[creativeProviderReader](svc.ProviderRepo).source)
			model := "gpt-image-2.6-flare"
			providers.byGroup[12] = []providercore.Record{{
				ID: 58, Platform: capability.PlatformOpenAI, Type: providercore.ProviderTypeAPIKey,
				Status: billing.StatusActive, Schedulable: true,
				Credentials: map[string]any{
					"model_mapping":           map[string]any{model: model},
					"images_generations_mode": test.generation, "images_edits_mode": test.edit,
				},
				Extra: map[string]any{"images_gif_enabled": !test.disabled},
			}}
			testassert.MustType[*creativeFakeSettingReader](svc.Settings).models = []creative.CreativeModelSetting{{GroupID: 12, Model: model, Operations: test.operations}}
			svc.ImageUnitPrice = func(context.Context, *creative.GroupView, string, string) (float64, bool) { return 0.25, true }
			catalog, err := svc.ListModels(context.Background(), 7)
			require.NoError(t, err)
			require.Len(t, catalog.Data, 1)
			entry := catalog.Data[0]
			require.Len(t, entry.OutputFormatsByOperation, len(test.operations))
			for _, operation := range test.operations {
				formats := entry.OutputFormatsByOperation[operation]
				require.Contains(t, formats, "png")
				supportsGIF := slices.Contains(test.gifOps, operation)
				require.Equal(t, supportsGIF, slices.Contains(formats, "gif"))
				params := validCreateParams()
				params.Model, params.Operation, params.OutputFormat = model, operation, "gif"
				if operation == creative.CreativeOperationEdit {
					params.SourceImages = []creative.CreativeInputImage{{Mime: "image/png", Bytes: reference.Bytes()}}
				}
				_, err := svc.ValidateCreateParams(context.Background(), 7, &params)
				if supportsGIF {
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, creative.ErrCreativeOperationUnsupported)
				}
			}
			if len(test.gifOps) == 0 {
				require.NotContains(t, entry.OutputFormats, "gif")
				require.Nil(t, entry.GIFFrameCount)
				require.Nil(t, entry.GIFFrameDurationMS)
			} else {
				require.Contains(t, entry.OutputFormats, "gif")
				require.NotNil(t, entry.GIFFrameCount)
			}
		})
	}
}
