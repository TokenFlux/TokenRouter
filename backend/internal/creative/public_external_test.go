package creative_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"slices"
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
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
)

// public_external_test.go verifies creative model admission, parameter validation, task identity, and pricing.
//
// Creative model discovery and submission share current group, provider and
// administrator settings rules. In-memory cases inspect finite image routes,
// requested parameters, pricing availability and sequential replay effects,
// while input bytes and call observations remain in their supplied fixtures.
// Assertions identify the specific model families and errors exercised here.

// creativeCatalogTestProviders supplies retained catalogue entries to direct
// route fixtures.
type creativeCatalogTestProviders struct {
	// values remains shared with the scenario after each lookup. The creative
	// query reads these provider adapters while the test keeps them stable.
	values []creative.CatalogProvider // retained fixture catalogue provider values
}

// creativePricingConfigFixture supplies one active configuration with retained
// image price cards.
type creativePricingConfigFixture struct {
	// PricingConfigRepository supplies unused methods through a nil embedding.
	// This fixture overrides the two reads used here; callers implement any
	// other promoted operation before invoking it.
	routing.PricingConfigRepository // unused price repository methods
	// cards retains the supplied slice and nested price pointers. Tests keep
	// those values stable while the shared price-config resolver reads them.
	cards    []routing.ModelPricingEntry // retained configured model price cards
	platform string                      // fixed fixture group platform
}

// creativeAttributeFailureRepo returns a configuration failure during discovery.
type creativeAttributeFailureRepo struct {
	routing.ModelAttributeRepository
}

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

// TestCreativeCreateRunPersistsOnlyMetadata submits a prompt and source through
// the in-memory service. It inspects creation hashes, pending output metadata
// and readable transient input, describing the fixture calls made during
// submission.
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
	// The repository call receives a digest, and the retained creation parameters
	// let this fixture compare identity without a database content-retention claim.
	require.Equal(t, creative.Sha256Hex([]byte(params.Prompt)), stored.PromptHash)
	require.NotContains(t, stored.PromptHash, "绝不应落库")
	require.NotEmpty(t, stored.RequestFingerprint)
	// Task and output rows contain media metadata.
	require.Equal(t, 1, stored.RequestedOutputCount)
	for _, output := range repo.outputs[created.ID] {
		require.Equal(t, creative.CreativeRunOutputStatusPending, output.Status)
		require.Nil(t, output.MimeType)
		require.Nil(t, output.ByteSize)
	}
	// The transient fixture retains the uploaded bytes for worker input loading;
	// this assertion checks the service write independently from metadata creation.
	_, err = store.LoadInputs(ctx, created.ID, 1)
	require.NoError(t, err)
	require.Empty(t, store.outputs)
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

// TestCreativePricingUsesResolvedPricingConfigPrice quotes the supplied image
// price cards through the shared resolver. It checks flat OpenAI tiers and
// Gemini 512 precedence over the card default, followed by default pricing when
// the tier is absent.
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

	// Permit floating-point rounding across the flat image-card tier quotes.
	require.InDelta(t, 1, svc.CreativePrice(context.Background(), creativeGroupProjection(group), "gpt-image-2", "1K"), 1e-9)
	require.InDelta(t, 1, svc.CreativePrice(context.Background(), creativeGroupProjection(group), "gpt-image-2", "2K"), 1e-9)
	require.InDelta(t, 1, svc.CreativePrice(context.Background(), creativeGroupProjection(group), "gpt-image-2", "4K"), 1e-9)

	// Gemini 512 prefers a matching price tier, then the price card's default.
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

	// Permit floating-point rounding when comparing the selected 512 dollar quote.
	require.InDelta(t, price512, svc.CreativePrice(context.Background(), creativeGroupProjection(geminiGroup), "gemini-3.1-flash-image", "512"), 1e-9)
	resolver = newResolverWithPricingConfig(t, []routing.ModelPricingEntry{{
		Models:          []string{"gemini-3.1-flash-image"},
		BillingMode:     routing.BillingModeImage,
		PerRequestPrice: &defaultPrice,
	}})
	svc.ImageUnitPrice = creativePriceFixture(nil, resolver)
	// Permit floating-point rounding when the card default supplies this quote.
	require.InDelta(t, defaultPrice, svc.CreativePrice(context.Background(), creativeGroupProjection(geminiGroup), "gemini-3.1-flash-image", "512"), 1e-9)
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

// TestValidateCreateParams exercises model-specific options, product defaults
// and the listed invalid input cases. Assertions inspect normalized controls
// and single-output counts, plus errors for unsupported models/operations,
// limits, MIME and inpaint mask requirements.
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
			Credentials: map[string]any{"model_mapping": map[string]any{"grok-imagine-image-2.0": "grok-imagine-image-2.0"}},
		}}
		params := validCreateParams()
		params.Model = "grok-imagine-image-2.0"
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
			Credentials: map[string]any{"model_mapping": map[string]any{"grok-imagine-image-2.0": "grok-imagine-image-2.0"}},
		}}
		params := validCreateParams()
		params.Model = "grok-imagine-image-2.0"
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

// TestCreateRunIdempotency submits same-key replays, a changed prompt and
// another key sequentially. It checks replay identity, conflict classification
// and two distinct creations, with two observed funds reservations and queue
// admissions.
func TestCreateRunIdempotency(t *testing.T) {
	svc := newCreativeTestService()
	ctx := context.Background()

	first, err := svc.CreateRun(ctx, testCreativeScope(7), validCreateParams(), "idem-key-1")
	require.NoError(t, err)
	require.False(t, first.IdempotentReplay)
	require.Equal(t, creative.CreativeRunStatusQueued, first.Status)
	require.True(t, creative.IsValidCreativeRunID(first.ID))

	// The same key and body return the existing task as a replay.
	replay, err := svc.CreateRun(ctx, testCreativeScope(7), validCreateParams(), "idem-key-1")
	require.NoError(t, err)
	require.True(t, replay.IdempotentReplay)
	require.Equal(t, first.ID, replay.ID)

	// Reusing the key for a different body is a conflict.
	conflictParams := validCreateParams()
	conflictParams.Prompt = "完全不同的 prompt"
	_, err = svc.CreateRun(ctx, testCreativeScope(7), conflictParams, "idem-key-1")
	require.ErrorIs(t, err, creative.ErrCreativeRunIdempotencyConflict)

	// A different key creates an independent task for the same body.
	retry, err := svc.CreateRun(ctx, testCreativeScope(7), validCreateParams(), "idem-key-2")
	require.NoError(t, err)
	require.False(t, retry.IdempotentReplay)
	require.NotEqual(t, first.ID, retry.ID)

	// Only the two newly created tasks reserve funds and enter the queue.
	require.Equal(t, 2, testassert.MustType[*creativeFakeBillingRepo](creativeFixtureBilling(svc)).reserveN)
	require.Len(t, testassert.MustType[*creativeFakeQueue](svc.Queue).enqueued, 2)
}

// TestCreativePricingIgnoresQuality creates OpenAI high and low quality tasks
// through one fixture. Their estimates match, the high-quality hold matches its
// estimate, and that task has one pending output row.
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
	// Permit floating-point rounding between the estimated amount and reserved hold.
	require.InDelta(t, highRun.EstimatedCost, highRun.HoldAmount, 1e-9)
	require.Len(t, testassert.MustType[*creativeFakeRunRepo](svc.Repo).outputs[highRun.ID], 1)
}

// TestCreativeEnabledGate varies runtime and process feature switches in
// isolated services. The checked combinations empty or populate listing, and a
// disabled runtime makes creation return the creative-disabled error.
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

// TestCreativeModelSettingsFilterAndCreateValidation permits editing alone for
// one saved Gemini model. Listing advertises edit, generation validation fails,
// and an edit request with its source image succeeds.
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

// TestCreativeUnpricedModelDoesNotReserve installs a calculator without an
// eligible image price. Listing stays empty and creation returns an error,
// while the fixture funds reservation count remains zero.
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

// TestBuildCreativeRequestFingerprint repeats one supplied normalized payload
// and then changes prompt digest or group. Repeated hashing is equal, while
// each of those two individual changes produces a different observed
// fingerprint.
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

// TestCreativeRejectsClientOnlyGroupBeforeFunding marks the fixture group
// client-restricted and configures a fallback group. Both discovery views stay
// empty and creation returns group-forbidden, with no funds reservation
// observed.
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

// ListSchedulableByGroupIDAndPlatform returns the retained catalogue provider
// slice and succeeds. Group, platform and context arguments do not filter it,
// so creative query checks determine candidate eligibility in these fixtures.
func (a creativeCatalogTestProviders) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]creative.CatalogProvider, error) {
	return a.values, nil
}

// creativeProviderModelsForTest clones the supplied nonnil native record,
// applies active/API-Key defaults and returns sorted public candidates.
// Catalogue lookup errors fail the test; the caller record stays available with
// its supplied fixture configuration.
func creativeProviderModelsForTest(t *testing.T, value *providercore.Record) []string {
	t.Helper()
	// Apply admission defaults to a copied record so model-discovery assertions
	// can reuse the caller credential configuration in subsequent fixture cases.
	value = providercore.CloneRecord(value)
	value.Status = "active"
	if value.Type == "" {
		value.Type = "apikey"
	}
	value.Schedulable = true
	svc := &creative.Public{Attributes: &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}, ProviderRepo: creativeCatalogTestProviders{[]creative.CatalogProvider{creativeprovider.CatalogProvider(value)}}}

	models, err := svc.CreativeModelsForGroup(context.Background(), &creative.GroupView{ID: 12, Operations: creative.OperationsForGroup(false, nil)})
	require.NoError(t, err)
	result := make([]string, 0, len(models))
	for model := range models {
		result = append(result, model)
	}
	sort.Strings(result)
	return result
}

// byModel returns the first directory entry with an exact model name, or a zero
// value when absent. Returned nested slices remain shared with the directory,
// and fixture callers inspect them as immutable values.
func byModel(models []creative.CreativeModelPublic, model string) creative.CreativeModelPublic {
	for _, item := range models {
		if item.Model == model {
			return item
		}
	}
	return creative.CreativeModelPublic{}
}

// makeTestPNG encodes an RGBA image at the supplied dimensions and returns PNG
// bytes owned by the fixture. Callers supply valid image dimensions, and an
// encoding error fails the current test.
func makeTestPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// configureOpenAICreativeTestService replaces direct group/provider lookup and
// model settings with a GPT Image two fixture. Callers supply the expected
// service fake adapters; the separately supplied active-group slice retains its
// existing values.
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

// configureGrok2CreativeTestService replaces direct group/provider lookup and
// settings with the Grok two image fixture. Callers supply the expected service
// fake adapters; editing and generation are configured, and active-group values
// remain separately supplied.
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

// ListAll returns one active group-one-hundred configuration retaining cards as
// its model prices. Each call allocates the outer configuration slice, while
// nested price entries and their pointers remain shared with the fixture.
func (r *creativePricingConfigFixture) ListAll(context.Context) ([]routing.PricingConfig, error) {
	return []routing.PricingConfig{{ID: 1, Name: "test-price-config", Status: billing.StatusActive, GroupIDs: []int64{100}, ModelPricing: r.cards}}, nil
}

// GetGroupPlatforms returns a fresh map binding group one hundred to the
// fixture platform. Supplied group identifiers are ignored, allowing
// price-config tests to focus on the retained cards.
func (r *creativePricingConfigFixture) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return map[int64]string{100: r.platform}, nil
}

// newResolverWithPricingConfig binds retained price cards and an offline
// calculator to the shared billing resolver. Group one hundred uses the fixture
// configuration, and unavailable catalogue quotes are observed through the
// supplied diagnostic callback.
func newResolverWithPricingConfig(t *testing.T, cards []routing.ModelPricingEntry) *billing.PriceResolver {
	t.Helper()
	platform := capability.PlatformAnthropic
	calculator := billingtestkit.Calculator(nil, map[string]*pricing.ModelPricing{"claude-sonnet-4": {InputPricePerToken: 3e-6, OutputPricePerToken: 15e-6, CacheCreationPricePerToken: 3.75e-6, CacheReadPricePerToken: 0.3e-6, SupportsCacheBreakdown: false}})
	pricingConfigs := routing.NewPricingConfigService(&creativePricingConfigFixture{cards: cards, platform: platform}, nil, routing.PricingConfigOptions{Warn: slog.Warn, Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation})
	return billing.NewPriceResolver(pricingConfigs, calculator, modelidentity.Identity, func(model string, err error) {
		slog.Debug("failed to get model pricing from model catalog, using fallback", "model", model, "error", err)
	})
}

// BenchmarkCreativeConfiguredValidation measures selected-model validation over
// sixty-five fixture providers sharing 186 configured allowlist and mapping
// entries. It resets timing after setup, reports allocations and fails if any
// measured validation returns an error.
func BenchmarkCreativeConfiguredValidation(b *testing.B) {
	service := newCreativeTestService()
	configureOpenAICreativeTestService(service)
	rows := testassert.MustType[*creativeFakeProviderRepo](testassert.MustType[creativeProviderReader](service.ProviderRepo).source)
	// Share the same large rules across many provider records to measure one
	// selected image model against configured candidates after service setup.
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
	// Exclude fixture assembly from the measured iterations while retaining
	// validation and rule preparation in each timed service call below.
	b.ResetTimer()
	for range b.N {
		if _, err := service.ValidateCreateParams(context.Background(), 7, &params); err != nil {
			b.Fatal(err)
		}
	}
}

// ForGroup returns the read failure used to exercise catalogue error propagation.
func (creativeAttributeFailureRepo) ForGroup(context.Context, int64) (*routing.ModelAttributeConfig, error) {
	return nil, errors.New("attribute store unavailable")
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
