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
	"github.com/TokenFlux/TokenRouter/internal/provider/qodermodel"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
)

// Client model names can differ from the group-mapped and upstream identities
// used for billing. Catalogue resolution keeps the client ID while checking
// provider model eligibility and the configured pricing basis; marketplace
// fixtures then inspect the resulting public token prices or unknown status.

// modelRateLimitsKey selects the fixture's per-model cooldown data consumed by
// provider eligibility during catalogue resolution.
const modelRateLimitsKey = "model_rate_limits"

// requestableModelsPricingConfigRepoStub injects pricing query failures into
// catalogue resolution fixtures.
type requestableModelsPricingConfigRepoStub struct {
	// PricingConfigRepository supplies unused methods through a nil interface;
	// these fixtures exercise ListAll and leave other repository calls unsupported.
	routing.PricingConfigRepository
	err error // injected pricing query failure
}

// sequencedRequestableModelsProviderRepoStub reproduces recovery on a later provider query.
type sequencedRequestableModelsProviderRepoStub struct {
	// catalogueRows supplies the unused global read through a nil interface;
	// this fixture is queried through its group-specific override.
	catalogueRows
	// providers is shallow-copied after the first call. Nested provider data
	// stays shared, so tests keep it stable across each catalogue query.
	providers []providercore.Record
	// calls chooses the first failure and counts later queries; callers access
	// this fixture sequentially because its counter is not synchronized.
	calls int
}

// TestResolveRequestableModels_RequiresModelLevelSchedulability puts the mapped
// upstream model in a provider's future cooldown. The alias must disappear when
// all providers are limited and remain visible when one provider is healthy.
func TestResolveRequestableModels_RequiresModelLevelSchedulability(t *testing.T) {
	groupID := int64(4120)
	pricingConfig := routingtestkit.Configuration{
		ID:           70,
		Status:       billing.StatusActive,
		ModelMapping: map[string]string{"client-alias": "group-model"},
	}
	// Keep the cooldown ahead of wall-clock time while both provider pools resolve.
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

// ListAll returns the injected pricing-repository error with no configuration
// rows. This stub exercises pricing lookup failure while the test independently
// controls the group-policy reader used by catalogue resolution.
func (s *requestableModelsPricingConfigRepoStub) ListAll(context.Context) ([]routing.PricingConfig, error) {
	return nil, s.err
}

// ListSchedulableByGroupID fails the first group query and returns shallow-copied
// provider rows on later calls. The group argument is ignored, and sequential
// callers share the unsynchronized attempt counter and nested provider data.
func (s *sequencedRequestableModelsProviderRepoStub) ListSchedulableByGroupID(context.Context, int64) ([]providercore.Record, error) {
	s.calls++
	// Fail one complete query so the next catalogue request exercises recovery
	// through a fresh read, rather than a retry inside this repository method.
	if s.calls == 1 {
		return nil, errors.New("temporary provider query failure")
	}
	return append([]providercore.Record(nil), s.providers...), nil
}

// requestableModelByID returns the first exact client-ID match and whether it
// exists. Missing IDs yield a zero-valued model with false, while a found model's
// nested values remain shared with the input slice.
func requestableModelByID(models []routing.RequestableModel, id string) (routing.RequestableModel, bool) {
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	return routing.RequestableModel{}, false
}

// TestResolveRequestableModels_UsesConfiguredPricingBasis supplies distinct
// requested, group-mapped, and upstream names for one alias. Each configured
// billing source must select its expected pricing model while keeping the alias
// visible and its pricing unambiguous.
func TestResolveRequestableModels_UsesConfiguredPricingBasis(t *testing.T) {
	groupID := int64(4101)
	inputPrice := 0.01
	for _, test := range []struct {
		name string // subtest display name
		// billingSource selects the model identity used for charging, and
		// pricingModel names the expected result for the same request, group,
		// and provider mapping chain.
		billingSource string // configured billing identity stage
		pricingModel  string // expected resolved pricing identity
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

// TestResolveRequestableModels_WildcardsMatchConcreteCandidateOnly combines
// wildcard mapping and price patterns with a concrete provider candidate. The
// concrete alias must resolve to the group pricing model, and wildcard patterns
// must remain absent from returned IDs.
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

// TestResolveRequestableModels_UnrestrictedProviderAddsDefaultsAndMappingSource
// supplies case-colliding aliases and a wildcard provider mapping. The catalogue
// must retain the selected alias spelling and concrete target while excluding
// the duplicate spelling and wildcard pattern.
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

// TestResolveRequestableModels_QoderAccountUnion checks overlapping account
// catalogs, including one disabled route. Removing the final account offering
// a route must remove its alias from the group's public model list.
func TestResolveRequestableModels_QoderAccountUnion(t *testing.T) {
	groupID := int64(4121)
	global := providercore.Record{ID: 90, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, Credentials: map[string]any{"site": "global"}}
	cn := providercore.Record{ID: 91, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, Credentials: map[string]any{"site": "cn"}}
	shared := qodermodel.Model{Key: "qfmodel", Alias: "qwen3.8-flash", Enabled: true}
	global = withQoderModels(global, shared, qodermodel.Model{Key: "cmodel", Alias: "cantus", Enabled: true})
	cn = withQoderModels(cn, shared, qodermodel.Model{Key: "jmodel", Alias: "step-5-preview", Enabled: true}, qodermodel.Model{Key: "dmodel", Enabled: false})
	// Resolve each pool independently so the group cache cannot carry candidates
	// from the previous membership into the next catalogue observation.
	resolve := func(providers ...providercore.Record) []string {
		svc := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: providers}}, nil, nil)
		result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformQoder)
		return routing.RequestableModelIDs(result.Models)
	}
	require.ElementsMatch(t, []string{"qwen3.8-flash", "cantus"}, resolve(global))
	require.ElementsMatch(t, []string{"qwen3.8-flash", "step-5-preview"}, resolve(cn))
	require.ElementsMatch(t, []string{"qwen3.8-flash", "cantus", "step-5-preview"}, resolve(global, cn))
	cn.Credentials["model_mapping"] = map[string]any{"disabled-alias": "dmodel"}
	require.NotContains(t, resolve(cn), "disabled-alias")
}

// withQoderModels installs an identity-matched successful observation. An empty
// list keeps explicit custom mappings eligible without adding automatic routes.
func withQoderModels(record providercore.Record, models ...qodermodel.Model) providercore.Record {
	record.Extra = map[string]any{providercore.QoderCatalogExtraKey: &providercore.QoderCatalogSnapshot{
		Identity:  providercore.QoderCatalogIdentity(&record),
		Version:   "fixture",
		FetchedAt: time.Now(),
		Models:    append([]qodermodel.Model{}, models...),
	}}
	return record
}

// TestResolveRequestableModels_ProviderWhitelistRemovesUnsupportedCandidate
// maps a group alias to a model outside the provider's whitelist. The alias
// must remain absent from the resolved requestable model IDs.
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

// TestResolveRequestableModels_UpstreamPricingAmbiguousAcrossProviders maps
// one group model to two different upstream names with upstream billing selected.
// The alias must remain visible with an empty pricing model and ambiguity set.
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

// TestResolveRequestableModels_UpstreamUsesBedrockRegionalModel configures
// upstream billing for a Claude model through a us-east-1 Bedrock provider. The
// visible model must use the regional inference ID as its unambiguous pricing key.
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

// TestResolveRequestableModels_UpstreamMarksAntigravityThinkingVariantAmbiguous
// lists a Claude model through an Antigravity provider with upstream billing.
// The model must remain visible with an empty pricing key and the ambiguity
// flag set for its available variants.
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

// TestResolveRequestableModels_UpstreamNormalizesAnthropicOAuthMapping maps a
// client alias to a Claude model through Anthropic OAuth. The alias must use the
// configured target as its unambiguous upstream pricing model.
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

// TestResolveRequestableModels_OpenAIUsesActualForwardedModel combines
// OAuth mapping cases with and without passthrough enabled. Prepared provider
// rules and the alias's upstream pricing model must agree on the configured
// forwarded identity in each case.
func TestResolveRequestableModels_OpenAIUsesActualForwardedModel(t *testing.T) {
	price := 0.09
	tests := []struct {
		name    string // subtest display name
		groupID int64  // fixture group lookup ID
		// pricingConfigModel is the target of the group alias; pricingModel
		// is the expected identity after this case's provider mapping. Both
		// participate in the upstream-billing assertion.
		pricingConfigModel string // configured group mapping target
		pricingModel       string // expected forwarded pricing identity
		// provider supplies the case's query record. The catalogue prepares its
		// mappings and reads its nested values while the test keeps them stable.
		provider providercore.Record
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

// TestResolveRequestableModels_RestrictionEmptyDoesNotFallBack enables group
// model restriction with no configured allowed entries. The catalogue must
// report restriction and return an empty model list for the supplied provider.
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

// TestResolveRequestableModelsUsesUnifiedPriceSpace supplies an Anthropic
// provider and a requested-model price entry for the same concrete name. The
// restricted catalogue must return that model as its sole ID.
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

// TestResolveRequestableModels_QoderRequiresEffectivePricing supplies a Qoder
// alias with an empty exact entry and a priced wildcard fixture entry. The
// requestable ID list must include that configured alias.
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
	provider = withQoderModels(provider)
	svc := newCatalogueFixture(&modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: {provider}}}, routingtestkit.PricingConfig(groupID, capability.PlatformQoder, pricingConfig), nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformQoder)
	require.Contains(t, routing.RequestableModelIDs(result.Models), "qoder-model")
}

// TestResolveRequestableModels_ProviderQueryFailureKeepsEmpty injects a failed
// provider query for one group. Catalogue resolution must report restriction
// and return empty IDs for that attempted read.
func TestResolveRequestableModels_ProviderQueryFailureKeepsEmpty(t *testing.T) {
	groupID := int64(4109)
	svc := newCatalogueFixture(&modelsListProviderRepoStub{err: errors.New("temporary failure")}, nil, nil)

	result := svc.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformOpenAI)
	require.True(t, result.Restricted)
	require.Empty(t, routing.RequestableModelIDs(result.Models))
}

// TestResolveRequestableModels_NextQueryRecoversProviderFailure injects a
// single failed provider read followed by configured rows. The first result must
// be restricted and empty; the next query must read again and expose the private model.
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

// TestResolveRequestableModels_PricingConfigQueryFailureKeepsProviderCandidates
// fails pricing lookup while supplying an independent empty group-policy read.
// Provider-configured aliases must remain visible, and the result must remain
// unrestricted for that fixture.
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
		// Supply a successful policy read while ListAll fails, isolating the
		// pricing failure from a policy failure that would restrict the catalogue.
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

// TestResolveRequestableModels_PricingConfigQueryFailureKeepsEmptyProviderPoolEmpty
// configures a failing pricing source beside a successfully read empty provider
// pool. The observed no-provider path must return no models with Restricted false
// before pricing rules are needed.
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

// TestModelMarketplaceUsesResolvedGroupMappedPricingModel configures distinct
// client, group, and upstream names with group-mapped billing. The public alias
// must display the configured group-model token prices and report a priced status.
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

// TestModelMarketplaceKeepsAmbiguousUpstreamModelUnpriced gives one alias
// two upstream identities, each with a price entry. The marketplace must retain
// the alias with unpriced status and an unknown pricing mode.
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

// TestModelMarketplaceQoderUsesResolvedRequestedPricingModelWithoutRemapping
// quotes a supplied requested pricing identity while a price exists for its group
// mapping target. The quote must remain unpriced with unknown mode for that identity.
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
