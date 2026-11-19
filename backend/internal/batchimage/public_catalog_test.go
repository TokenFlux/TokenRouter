package batchimage_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchimageprovider "github.com/TokenFlux/TokenRouter/internal/batchimage/provider"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
)

// TestBatchImageCatalogueAndSubmissionAdmission closes a provider protocol or omits
// Vertex storage. Unavailable routes must be rejected before any job side
// effects, while a configured Gemini route remains usable without Vertex storage.
func TestBatchImageCatalogueAndSubmissionAdmission(t *testing.T) {
	for _, scenario := range []string{"protocol closed", "Vertex storage missing", "Gemini without Vertex storage"} {
		t.Run(scenario, func(t *testing.T) {
			svc, repo, queue, gemini, vertex, _ := newTestBatchImagePublicService()
			fixture := testassert.MustType[*batchProviderFixture](svc.ProviderRepo)
			source := testassert.MustType[*publicBatchImageProviderRepo](fixture.source)
			record := testBatchImageMappedProvider(101, capability.ProviderTypeAPIKey, map[string]any{
				"gemini-2.5-flash-image": "gemini-2.5-flash-image",
			})
			if scenario == "protocol closed" {
				record.Credentials["upstream_protocols"] = []string{}
			} else {
				record.Type = capability.ProviderTypeServiceAccount
				record.Credentials["service_account_json"] = map[string]any{
					"type": "service_account", "project_id": "test-project",
					"private_key": "fixture-key", "client_email": "test@example.com",
				}
			}
			source.providers = []providercore.Record{record}
			vertexAdapter := batchimageprovider.NewVertexBatchImageProvider(batchimageprovider.VertexBatchImageProviderOptions{}, nil, nil, nil)
			if scenario != "protocol closed" {
				require.True(t, vertexAdapter.SupportsProvider(&record))
			}
			if scenario == "Gemini without Vertex storage" {
				source.providers = append(source.providers, testBatchImageMappedProvider(202, capability.ProviderTypeAPIKey, map[string]any{
					"gemini-2.5-flash-image": "gemini-2.5-flash-image",
				}))
			}
			fixture.registry = batchimage.NewRegistry[batchimageprovider.BatchImageProvider](
				&admissionProvider{gemini, batchimageprovider.NewGeminiAPIBatchImageProvider(nil)},
				&admissionProvider{vertex, vertexAdapter},
			)
			models, err := svc.ListModels(context.Background(), testBatchImageOwner())
			require.NoError(t, err)
			if scenario == "Gemini without Vertex storage" {
				require.Len(t, models.Data, 1)
				require.Equal(t, batchimage.BatchImageProviderGeminiAPI, models.Data[0].Platform)
				job, err := svc.Submit(context.Background(), testBatchImageOwner(), validBatchImageSubmitRequest(), "")
				require.NoError(t, err)
				require.Equal(t, batchimage.BatchImageProviderGeminiAPI, job.Platform)
				require.Len(t, gemini.submits, 1)
				require.Empty(t, vertex.submits)
				return
			}
			require.Empty(t, models.Data)
			_, err = svc.Submit(context.Background(), testBatchImageOwner(), validBatchImageSubmitRequest(), "")
			require.ErrorIs(t, err, batchimage.ErrBatchImageNoProviderAvailable)
			require.Empty(t, repo.jobs)
			require.Empty(t, testassert.MustType[*fakeBatchImageBillingRepo](taskFixtureBilling(svc)).reserves)
			require.Empty(t, queue.enqueued)
			require.Empty(t, gemini.submits)
			require.Empty(t, vertex.submits)
		})
	}
}

// TestBatchImageCatalogueIncludesGroupMappingTargets enumerates concrete
// group mapping sources and targets with valid image pricing. It checks the
// visible names before requested-stage restrictions or provider allowlists
// remove otherwise configured targets.
func TestBatchImageCatalogueIncludesGroupMappingTargets(t *testing.T) {
	for _, source := range []string{"draw-alias", "draw-*"} {
		t.Run(source, func(t *testing.T) {
			service, _, _, _, _, _ := newTestBatchImagePublicService()
			service.ProviderExists = func(name string) bool { return name == batchimage.BatchImageProviderGeminiAPI }
			owner := testBatchImageOwner()
			value := testBatchImageMappedProvider(303, "apikey", nil)
			service.ProviderRepo = rebindBatchFixtureProviders(service, &publicBatchImageProviderRepo{providers: []providercore.Record{value}})
			policy := makePublicPricingConfigFixture(routing.PricingConfig{ID: 1, Status: "active", GroupIDs: []int64{*owner.GroupID}}, nil, routing.GroupRoutingPolicy{Enabled: true, ModelMapping: map[string]string{source: "custom-image"}})
			service.PricingConfigService = newPublicPricingConfigFixture(policy)
			service.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1}
			service.Attributes = &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{Lookup: func(model string) modelcatalog.Attributes {
				image := []string{"image"}
				if model == "custom-image" {
					return modelcatalog.Attributes{OutputModalities: &image}
				}
				return modelcatalog.Attributes{}
			}}}
			result, err := service.ListModels(context.Background(), owner)
			require.NoError(t, err)
			var ids []string
			for _, model := range result.Data {
				ids = append(ids, model.ID)
			}
			want := []string{"custom-image"}
			if source == "draw-alias" {
				want = append(want, source)
			}
			require.ElementsMatch(t, want, ids)

			policy.policy.RestrictModels = true
			policy.policy.RestrictionModelSource = routing.BillingModelSourceRequested
			policy.policy.AllowedModels = []string{source}
			result, err = service.ListModels(context.Background(), owner)
			require.NoError(t, err)
			for _, model := range result.Data {
				require.NotEqual(t, "custom-image", model.ID)
			}

			policy.policy.RestrictModels = false
			value.Credentials["model_whitelist"] = []string{"other-model"}
			result, err = service.ListModels(context.Background(), owner)
			require.NoError(t, err)
			for _, model := range result.Data {
				require.NotEqual(t, "custom-image", model.ID)
			}
		})
	}
}

// TestBatchImageUnboundKeyCannotUseGlobalProviders submits an owner with no
// group while providers are available. Permission rejection occurs before job
// creation and provider submission, protecting the bound-group requirement
// for batch admission.
func TestBatchImageUnboundKeyCannotUseGlobalProviders(t *testing.T) {
	svc, repo, _, platform, _, _ := newTestBatchImagePublicService()
	owner := testBatchImageOwner()
	owner.GroupID = nil
	_, err := svc.Submit(context.Background(), owner, validBatchImageSubmitRequest(), "")
	require.ErrorIs(t, err, batchimage.ErrBatchImageGroupDisabled)
	require.Empty(t, repo.jobs)
	require.Empty(t, platform.submits)
}

// TestBatchImageCatalogueResolvesBeforeModalities combines wildcard and
// staged mappings with image, text and unknown final modalities. It checks
// visible request names and final pricing models while provider restrictions
// exclude unsupported candidates.
func TestBatchImageCatalogueResolvesBeforeModalities(t *testing.T) {
	for _, tc := range []struct {
		name            string            // scenario display name
		groupMapping    map[string]string // group model mapping
		providerMapping map[string]any    // provider model mapping
		whitelist       []string          // allowed upstream models
		final           string            // resolved upstream model
		visible         bool              // expected catalogue visibility
	}{
		{name: "provider wildcard", providerMapping: map[string]any{"gpt-*": "catalog-image"}, whitelist: []string{"catalog-image"}, final: "catalog-image", visible: true},
		{name: "group wildcard", groupMapping: map[string]string{"gpt-*": "catalog-image"}, whitelist: []string{"catalog-image"}, final: "catalog-image", visible: true},
		{name: "two mapping stages", groupMapping: map[string]string{"gpt-*": "route-model"}, providerMapping: map[string]any{"route-*": "catalog-image"}, whitelist: []string{"catalog-image"}, final: "catalog-image", visible: true},
		{name: "unknown configured final", providerMapping: map[string]any{"gpt-*": "nano-banana-pro"}, whitelist: []string{"nano-banana-pro"}, final: "nano-banana-pro", visible: false},
		{name: "mapped text final", providerMapping: map[string]any{"gpt-*": "catalog-text"}, whitelist: []string{"catalog-text"}, final: "catalog-text"},
		{name: "provider restriction", providerMapping: map[string]any{"gpt-*": "catalog-image"}, whitelist: []string{"catalog-text"}, final: "catalog-image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _, _, _, _ := newTestBatchImagePublicService()
			owner := testBatchImageOwner()
			group := *owner.GroupID
			value := testBatchImageMappedProvider(303, "apikey", tc.providerMapping)
			value.Credentials["model_whitelist"] = tc.whitelist
			svc.ProviderRepo = rebindBatchFixtureProviders(svc, &publicBatchImageProviderRepo{providers: []providercore.Record{value}})
			image, text := []string{"image"}, []string{"text"}
			svc.Attributes = nil
			svc.ModelOutputModalities = func(id string) *[]string {
				switch id {
				case "catalog-image":
					return &image
				case "gpt-5.4", "catalog-text":
					return &text
				default:
					return nil
				}
			}
			policy := newPublicPricingConfigFixture(makePublicPricingConfigFixture(routing.PricingConfig{ID: 1, Status: "active", GroupIDs: []int64{group}, BillingModelSource: routing.BillingModelSourceUpstream}, nil, routing.GroupRoutingPolicy{Enabled: true, AllowedModels: []string{"gpt-5.4"}, ModelMapping: tc.groupMapping}))
			svc.PricingConfigService = policy
			price := &fakeBatchImagePricingResolver{unitPrice: 0.1}
			svc.Pricing = price
			result, err := svc.ListModels(context.Background(), owner)
			require.NoError(t, err)
			ids := []string{}
			for _, model := range result.Data {
				ids = append(ids, model.ID)
			}
			if tc.visible {
				require.Contains(t, ids, "gpt-5.4")
				require.Contains(t, price.models, tc.final)
			} else {
				require.NotContains(t, ids, "gpt-5.4")
			}
			require.NotContains(t, ids, "unconfigured-unknown")
			require.NotContains(t, ids, "catalog-text")
		})
	}
}

// TestBatchImageCatalogueUsesOnePolicySnapshot queries each restriction stage
// through counting policy and price-source readers. It checks one read of
// each source and excludes text models while retaining the mapped image
// request.
func TestBatchImageCatalogueUsesOnePolicySnapshot(t *testing.T) {
	for _, stage := range []string{routing.BillingModelSourceRequested, routing.BillingModelSourceGroupMapped, routing.BillingModelSourceUpstream} {
		t.Run(stage, func(t *testing.T) {
			svc, _, _, _, _, _ := newTestBatchImagePublicService()
			owner := testBatchImageOwner()
			group := *owner.GroupID
			allowed := map[string]string{routing.BillingModelSourceRequested: "gpt-5.4", routing.BillingModelSourceGroupMapped: "route-model", routing.BillingModelSourceUpstream: "catalog-image"}[stage]
			policies := &batchCataloguePolicyReads{PricingConfigService: newPublicPricingConfigFixture(makePublicPricingConfigFixture(routing.PricingConfig{ID: 1, Status: "active", GroupIDs: []int64{group}, BillingModelSource: routing.BillingModelSourceUpstream}, nil, routing.GroupRoutingPolicy{Enabled: true, ModelMapping: map[string]string{"gpt-*": "route-model", "gpt-5.4": "route-model"}, RestrictModels: true, RestrictionModelSource: stage, AllowedModels: []string{allowed}}))}
			svc.PricingConfigService = policies
			value := testBatchImageMappedProvider(303, "apikey", map[string]any{"route-*": "catalog-image"})
			value.Credentials["model_whitelist"] = []string{"catalog-image"}
			svc.ProviderRepo = rebindBatchFixtureProviders(svc, &publicBatchImageProviderRepo{providers: []providercore.Record{value}})

			image, text := []string{"image"}, []string{"text"}
			svc.Attributes = nil
			svc.ModelOutputModalities = func(id string) *[]string {
				if id == "catalog-image" {
					return &image
				}
				return &text
			}
			svc.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1}
			result, err := svc.ListModels(context.Background(), owner)
			require.NoError(t, err)
			ids := []string{}
			for _, model := range result.Data {
				ids = append(ids, model.ID)
			}
			require.Contains(t, ids, "gpt-5.4")
			require.NotContains(t, ids, "text-0")
			require.Equal(t, 1, policies.policyReads)
			require.Equal(t, 1, policies.pricingReads)
		})
	}
}

// TestBatchImageCatalogueRejectsMissingPrice makes a mapped final model
// image-capable but injects missing image pricing. The catalogue still
// returns successfully with no entries, protecting price admission alongside
// output-modality checks.
func TestBatchImageCatalogueRejectsMissingPrice(t *testing.T) {
	svc, _, _, _, _, _ := newTestBatchImagePublicService()
	value := testBatchImageMappedProvider(303, "apikey", map[string]any{"gpt-*": "catalog-image"})
	svc.ProviderRepo = rebindBatchFixtureProviders(svc, &publicBatchImageProviderRepo{providers: []providercore.Record{value}})
	image := []string{"image"}
	svc.Attributes = nil
	svc.ModelOutputModalities = func(string) *[]string { return &image }
	svc.Pricing = &fakeBatchImagePricingResolver{err: batchimage.ErrBatchImageSettlementPricingMissing}
	result, err := svc.ListModels(context.Background(), testBatchImageOwner())
	require.NoError(t, err)
	require.Empty(t, result.Data)
}

// TestBatchImageAutomaticCandidates supplies unconfigured catalogue names and
// a configured image name mapped to text. The catalogue remains empty,
// protecting finite configured candidates and checks against the final
// model's output.
func TestBatchImageAutomaticCandidates(t *testing.T) {
	svc, _, _, _, _, _ := newTestBatchImagePublicService()
	value := testBatchImageMappedProvider(303, "apikey", map[string]any{"source-image": "final-text"})
	svc.ProviderRepo = rebindBatchFixtureProviders(svc, &publicBatchImageProviderRepo{providers: []providercore.Record{value}})
	image, text := []string{"image"}, []string{"text"}
	svc.Attributes = nil
	svc.ModelOutputModalities = func(id string) *[]string {
		switch id {
		case "catalog-image", "source-image":
			return &image
		case "final-text":
			return &text
		default:
			return nil
		}
	}
	svc.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1}
	result, err := svc.ListModels(context.Background(), testBatchImageOwner())
	require.NoError(t, err)
	require.Empty(t, result.Data)
}

// TestBatchImagePublicService_ListModels uses concrete and wildcard provider
// mappings with group and price controls. It checks backend-specific
// catalogue entries, missing-price filtering and group permission rejection
// before exposing available models.
func TestBatchImagePublicService_ListModels(t *testing.T) {
	ctx := context.Background()

	t.Run("requires explicit provider model mapping", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()

		got, err := svc.ListModels(ctx, testBatchImageOwner())
		require.NoError(t, err)
		require.Equal(t, "list", got.Object)
		require.Empty(t, got.Data)
	})

	t.Run("returns priced models from selected provider group", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		groupID := int64(7)
		svc.GroupRepo = batchGroupReader{&publicBatchImageGroupRepo{groups: map[int64]*batchimage.GroupView{
			groupID: {
				ID:                           groupID,
				RateMultiplier:               1,
				AllowBatchImageGeneration:    true,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			},
		}}}
		providerRepo := testassert.MustType[*publicBatchImageProviderRepo](testassert.MustType[*batchProviderFixture](svc.ProviderRepo).source)
		providerRepo.providers = []providercore.Record{testBatchImageMappedProvider(303, capability.ProviderTypeAPIKey, map[string]any{
			"gemini-2.5-flash-image": "gemini-2.5-flash-image",
		})}

		got, err := svc.ListModels(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID})
		require.NoError(t, err)
		require.Equal(t, []batchimage.BatchImagePublicModel{{
			ID:       "gemini-2.5-flash-image",
			Object:   "image.batch.model",
			Platform: batchimage.BatchImageProviderGeminiAPI,
		}}, got.Data)
	})

	t.Run("wildcard mappings enumerate configured targets", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		providerRepo := testassert.MustType[*publicBatchImageProviderRepo](testassert.MustType[*batchProviderFixture](svc.ProviderRepo).source)
		providerRepo.providers = []providercore.Record{testBatchImageMappedProvider(303, capability.ProviderTypeAPIKey, map[string]any{
			"gemini-3.1-*": "gemini-3.1-flash-lite-image",
		})}

		got, err := svc.ListModels(ctx, testBatchImageOwner())
		require.NoError(t, err)
		require.NotEmpty(t, got.Data)
		ids := make([]string, 0, len(got.Data))
		for _, model := range got.Data {
			ids = append(ids, model.ID)
		}
		require.NotContains(t, ids, "gemini-3.1-flash-image")
		require.Contains(t, ids, "gemini-3.1-flash-lite-image")
		require.NotContains(t, ids, "gemini-2.5-flash-image")
	})

	t.Run("filters models without batch image pricing", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		svc.Pricing = &fakeBatchImagePricingResolver{
			unitPrice:     0.25,
			missingModels: map[string]bool{"gemini-3.1-flash-lite-image": true},
		}
		providerRepo := testassert.MustType[*publicBatchImageProviderRepo](testassert.MustType[*batchProviderFixture](svc.ProviderRepo).source)
		providerRepo.providers = []providercore.Record{testBatchImageMappedProvider(303, capability.ProviderTypeAPIKey, map[string]any{
			"gemini-2.5-flash-image":      "gemini-2.5-flash-image",
			"gemini-3.1-flash-lite-image": "gemini-3.1-flash-lite-image",
		})}

		got, err := svc.ListModels(ctx, testBatchImageOwner())
		require.NoError(t, err)
		ids := make([]string, 0, len(got.Data))
		for _, model := range got.Data {
			ids = append(ids, model.ID)
		}
		require.Contains(t, ids, "gemini-2.5-flash-image")
		require.NotContains(t, ids, "gemini-3.1-flash-lite-image")
	})

	t.Run("rejects when group disables batch image", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		groupID := int64(7)
		svc.GroupRepo = batchGroupReader{&publicBatchImageGroupRepo{groups: map[int64]*batchimage.GroupView{
			groupID: {ID: groupID, AllowBatchImageGeneration: false},
		}}}

		_, err := svc.ListModels(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID})
		require.ErrorIs(t, err, batchimage.ErrBatchImageGroupDisabled)
	})
}

// TestBatchImageConfiguredAlias queries a custom image alias and its
// configured target with differing provider allowlists. Both names appear
// when allowed and are filtered when the provider permits another model.
func TestBatchImageConfiguredAlias(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		t.Run(fmt.Sprintf("allowed=%t", allowed), func(t *testing.T) {
			svc, _, _, _, _, _ := newTestBatchImagePublicService()
			repo := testassert.MustType[*publicBatchImageProviderRepo](testassert.MustType[*batchProviderFixture](svc.ProviderRepo).source)
			value := testBatchImageMappedProvider(303, capability.ProviderTypeAPIKey, map[string]any{"public-image": "nano-banana-pro"})
			if !allowed {
				value.Credentials["model_whitelist"] = []string{"other-model"}
			}
			repo.providers = []providercore.Record{value}
			svc.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1}
			svc.Attributes = &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{Lookup: func(model string) modelcatalog.Attributes {
				image := []string{"image"}
				if model == "nano-banana-pro" {
					return modelcatalog.Attributes{OutputModalities: &image}
				}
				return modelcatalog.Attributes{}
			}}}
			result, err := svc.ListModels(context.Background(), testBatchImageOwner())
			require.NoError(t, err)
			ids := []string{}
			for _, model := range result.Data {
				ids = append(ids, model.ID)
			}
			if allowed {
				require.Contains(t, ids, "public-image")
				require.Contains(t, ids, "nano-banana-pro")
			} else {
				require.NotContains(t, ids, "public-image")
				require.NotContains(t, ids, "nano-banana-pro")
			}
		})
	}
}

// BenchmarkBatchImageConfiguredCatalogue queries sixty-five providers with
// 186 configured names each. It measures allocations and repeated catalogue
// work while asserting that only the fixture's image-capable request name
// remains visible.
func BenchmarkBatchImageConfiguredCatalogue(b *testing.B) {
	service, _, _, _, _, _ := newTestBatchImagePublicService()
	rows := &publicBatchImageProviderRepo{}
	whitelist := make([]any, 186)
	mapping := map[string]any{}
	for i := range whitelist {
		model := fmt.Sprintf("probe-model-%03d", i)
		if i == 0 {
			model = "gemini-2.5-flash-image"
		}
		whitelist[i] = model
		mapping[model] = model
	}
	for i := range 65 {
		value := testBatchImageMappedProvider(int64(i+1), "apikey", mapping)
		value.Credentials["model_whitelist"] = whitelist
		rows.providers = append(rows.providers, value)
	}
	service.ProviderRepo = rebindBatchFixtureProviders(service, rows)
	service.ProviderExists = func(name string) bool { return name == batchimage.BatchImageProviderGeminiAPI }
	image, text := []string{"image"}, []string{"text"}
	service.ModelOutputModalities = func(model string) *[]string {
		if model == "gemini-2.5-flash-image" {
			return &image
		}
		return &text
	}
	service.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result, err := service.ListModels(context.Background(), testBatchImageOwner())
		if err != nil {
			b.Fatal(err)
		}
		if len(result.Data) != 1 {
			b.Fatalf("models=%d", len(result.Data))
		}
	}
}

// TestBatchImageCustomListDeclaresUnknownModality adds one concrete
// group-listed model whose output modality is unknown but priced. The
// catalogue returns entries for that declared name, protecting the
// configured-name admission path.
func TestBatchImageCustomListDeclaresUnknownModality(t *testing.T) {
	service, _, _, _, _, _ := newTestBatchImagePublicService()
	owner := testBatchImageOwner()
	value := testBatchImageMappedProvider(303, "apikey", nil)
	service.ProviderRepo = rebindBatchFixtureProviders(service, &publicBatchImageProviderRepo{providers: []providercore.Record{value}})
	service.PricingConfigService = &batchCataloguePolicyReads{PricingConfigService: newPublicPricingConfigFixture(makePublicPricingConfigFixture(routing.PricingConfig{ID: 1, Status: "active", GroupIDs: []int64{*owner.GroupID}}, nil, routing.GroupRoutingPolicy{Enabled: true})), modelsList: []string{"custom-image"}}
	service.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1}
	result, err := service.ListModels(context.Background(), owner)
	require.NoError(t, err)
	require.Empty(t, result.Data)
}
