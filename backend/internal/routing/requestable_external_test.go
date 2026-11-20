package routing_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// Finite request catalogues combine saved model mappings with group
// restrictions and provider eligibility. These fixtures measure policy read
// reuse and selected-target checks while retaining Compact's dedicated final
// model identity for attributes; the benchmark includes preparation of a fixed
// multi-provider workload.

// countedCataloguePolicies embeds group policy and counts independent
// pricing-source reads.
type countedCataloguePolicies struct {
	*routing.PricingConfigService     // group policy fixture implementation
	pricingReads                  int // counted pricing-source query calls
}

// countedCatalogueRules wraps prepared provider rules and records model
// eligibility checks.
type countedCatalogueRules struct {
	routing.CatalogueRules // wrapped prepared eligibility rules
	// checks points to the test's eligibility count. Supports updates it
	// synchronously during resolution so the test can compare selected-name work
	// with full candidate enumeration.
	checks *int
}

// TestCatalogueIncludesGroupMappingTargets supplies exact and wildcard group
// mappings with a concrete target. The target must enter eligibility checks,
// then disappear when requested-stage restrictions or provider whitelists
// reject it.
func TestCatalogueIncludesGroupMappingTargets(t *testing.T) {
	for _, source := range []string{"client-alias", "client-*"} {
		t.Run(source, func(t *testing.T) {
			groupID := int64(1)
			group := &routing.Group{ID: groupID, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}, RoutingPolicy: routing.GroupRoutingPolicy{Enabled: true, ModelMapping: map[string]string{source: "gpt-5.6-sol"}}}
			policies := &countedCataloguePolicies{PricingConfigService: routing.NewPricingConfigService(nil, nil, routing.PricingConfigOptions{ReadGroup: func(context.Context, int64) (*routing.Group, error) {
				return group, nil
			}})}
			resolver := routing.RequestableResolver{GroupPolicies: policies}
			records := []provider.Record{{Platform: "openai", Type: "apikey", Credentials: map[string]any{}}}
			providers := gatewayprovider.CatalogueProviders(records)
			result := resolver.ResolveWithProviders(context.Background(), &groupID, "", nil, providers)
			want := []string{"gpt-5.6-sol"}
			if source == "client-alias" {
				want = append(want, source)
			}
			require.ElementsMatch(t, want, routing.RequestableModelIDs(result.Models))
			selected := resolver.ResolveSelectedWithProviders(context.Background(), &groupID, "", []string{"gpt-5.6-sol"}, providers)
			require.Equal(t, []string{"gpt-5.6-sol"}, routing.RequestableModelIDs(selected.Models))

			group.RoutingPolicy.RestrictModels = true
			group.RoutingPolicy.RestrictionModelSource = routing.BillingModelSourceRequested
			group.RoutingPolicy.AllowedModels = []string{source}
			result = resolver.ResolveWithProviders(context.Background(), &groupID, "", nil, providers)
			require.NotContains(t, routing.RequestableModelIDs(result.Models), "gpt-5.6-sol")

			group.RoutingPolicy.RestrictModels = false
			records[0].Credentials["model_whitelist"] = []string{"other-model"}
			result = resolver.ResolveWithProviders(context.Background(), &groupID, "", nil, gatewayprovider.CatalogueProviders(records))
			require.NotContains(t, routing.RequestableModelIDs(result.Models), "gpt-5.6-sol")
		})
	}
}

// TestCatalogueReadsGroupOnce evaluates a large candidate list under each model
// restriction stage. The allowed result must retain its pricing identity while
// group and pricing policy are each read once.
func TestCatalogueReadsGroupOnce(t *testing.T) {
	for _, stage := range []string{routing.BillingModelSourceRequested, routing.BillingModelSourceGroupMapped, routing.BillingModelSourceUpstream} {
		t.Run(stage, func(t *testing.T) {
			ids := []string{"allowed"}
			for i := range 12000 {
				ids = append(ids, fmt.Sprintf("model-%d", i))
			}
			reads := 0
			policies := &countedCataloguePolicies{PricingConfigService: routing.NewPricingConfigService(nil, nil, routing.PricingConfigOptions{ReadGroup: func(context.Context, int64) (*routing.Group, error) {
				reads++
				return &routing.Group{ID: 1, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}, RoutingPolicy: routing.GroupRoutingPolicy{Enabled: true, RestrictModels: true, RestrictionModelSource: stage, AllowedModels: []string{"allowed"}}}, nil
			}})}
			resolver := routing.RequestableResolver{GroupPolicies: policies}
			records := []provider.Record{{Platform: "openai", Type: "apikey", Credentials: map[string]any{"model_whitelist": []string{"allowed"}}}}
			group := int64(1)
			result := resolver.ResolveWithProviders(context.Background(), &group, "", ids, gatewayprovider.CatalogueProviders(records))
			require.Equal(t, []string{"allowed"}, routing.RequestableModelIDs(result.Models))
			require.Equal(t, "allowed", result.Models[0].PricingModel)
			require.Equal(t, 1, reads)
			require.Equal(t, 1, policies.pricingReads)
		})
	}
}

// GetPricingConfigForGroup counts a pricing read and returns upstream billing
// identity for the fixture. The group ID is ignored so each test can isolate
// read reuse from repository behavior.
func (p *countedCataloguePolicies) GetPricingConfigForGroup(context.Context, int64) (*routing.PricingConfig, error) {
	p.pricingReads++
	return &routing.PricingConfig{BillingModelSource: routing.BillingModelSourceUpstream}, nil
}

// Supports increments the fixture counter before delegating prepared model
// eligibility. The counter pointer must remain available during resolution, and
// only selected names should contribute checks in the selected-query test.
func (r countedCatalogueRules) Supports(ctx context.Context, model string) bool {
	*r.checks++
	return r.CatalogueRules.Supports(ctx, model)
}

// SupportsClientProtocol delegates invocation-shape checks when the wrapped
// rules provide that capability. Otherwise it permits the protocol so this
// counting wrapper retains the optional behavior expected by catalogue
// resolution.
func (r countedCatalogueRules) SupportsClientProtocol(model string, source protocol.ProtocolID) bool {
	if rules, ok := r.CatalogueRules.(interface {
		// SupportsClientProtocol reports invocation-shape eligibility for a mapped
		// name and entry. The wrapper forwards this optional check before allowing
		// its counted model-support callback to contribute a route.
		SupportsClientProtocol(string, protocol.ProtocolID) bool
	}); ok {
		return rules.SupportsClientProtocol(model, source)
	}
	return true
}

// TestSelectedCatalogueChecksOnlyTargets requests a mapping target, a custom
// name, and an undeclared name. Only the two declared targets must reach
// eligibility checks, with one group and one pricing read.
func TestSelectedCatalogueChecksOnlyTargets(t *testing.T) {
	groupID := int64(1)
	reads := 0
	policies := &countedCataloguePolicies{PricingConfigService: routing.NewPricingConfigService(nil, nil, routing.PricingConfigOptions{ReadGroup: func(context.Context, int64) (*routing.Group, error) {
		reads++
		return &routing.Group{ID: groupID, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}, ModelsListConfig: routing.GroupModelsListConfig{Enabled: true, Models: []string{"manual"}}}, nil
	}})}
	resolver := routing.RequestableResolver{GroupPolicies: policies}
	records := []provider.Record{{Platform: "openai", Type: "apikey", Credentials: map[string]any{"model_whitelist": []string{}, "model_mapping": map[string]any{"alias": "target"}}}}
	providers := gatewayprovider.CatalogueProviders(records)
	checks := 0
	providers[0].Rules = countedCatalogueRules{providers[0].Rules, &checks}
	result := resolver.ResolveSelectedWithProviders(context.Background(), &groupID, "", []string{"target", "manual", "not-declared"}, providers)
	require.ElementsMatch(t, []string{"target", "manual"}, routing.RequestableModelIDs(result.Models))
	require.Equal(t, 2, checks)
	require.Equal(t, 1, reads)
	require.Equal(t, 1, policies.pricingReads)
	result = resolver.ResolveWithProviders(context.Background(), &groupID, "", nil, providers)
	require.ElementsMatch(t, []string{"alias", "target", "manual"}, routing.RequestableModelIDs(result.Models))
}

// BenchmarkRequestableConfiguredProviders measures cold resolution of 186
// concrete names across 65 providers. Rule preparation stays inside the timer,
// and each iteration checks that the complete configured catalogue is returned.
func BenchmarkRequestableConfiguredProviders(b *testing.B) {
	whitelist := make([]any, 186)
	mapping := map[string]any{}
	for i := range whitelist {
		model := fmt.Sprintf("probe-model-%03d", i)
		whitelist[i] = model
		mapping[model] = model
	}
	records := make([]provider.Record, 65)
	for i := range records {
		records[i] = provider.Record{ID: int64(i + 1), Platform: "openai", Type: "apikey", Credentials: map[string]any{"model_whitelist": whitelist, "model_mapping": mapping}}
	}
	groupID := int64(1)
	policies := routing.NewPricingConfigService(nil, nil, routing.PricingConfigOptions{ReadGroup: func(context.Context, int64) (*routing.Group, error) {
		return &routing.Group{ID: groupID, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}}, nil
	}})
	resolver := routing.RequestableResolver{GroupPolicies: &countedCataloguePolicies{PricingConfigService: policies}}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result := resolver.ResolveWithProviders(context.Background(), &groupID, "", nil, gatewayprovider.CatalogueProviders(records))
		if len(result.Models) != 186 {
			b.Fatalf("models=%d", len(result.Models))
		}
	}
}

// TestRequestableCompactAttributesUseFinalModel supplies an OpenAI OAuth
// Compact mapping distinct from its ordinary requested model. The retained
// attribute target must equal the final model selected by runtime Compact
// mapping.
func TestRequestableCompactAttributesUseFinalModel(t *testing.T) {
	ctx := context.Background()
	id := int64(1)
	record := provider.Record{ID: 1, Platform: "openai", Type: "oauth", Credentials: map[string]any{"model_whitelist": []string{"gpt-5.4", "gpt-5.6-sol"}, "compact_model_mapping": map[string]any{"gpt-5.4": "gpt-5.6-sol"}}}
	group := &routing.Group{ID: id, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolResponsesCompact}}
	policies := routing.NewPricingConfigService(nil, nil, routing.PricingConfigOptions{ReadGroup: func(context.Context, int64) (*routing.Group, error) { return group, nil }})
	resolver := routing.RequestableResolver{GroupPolicies: &countedCataloguePolicies{PricingConfigService: policies}}
	result := resolver.ResolveSelectedWithProviders(ctx, &id, "", []string{"gpt-5.4"}, gatewayprovider.CatalogueProviders([]provider.Record{record}))
	require.Len(t, result.Models, 1)
	require.Len(t, result.Models[0].AttributeTargets, 1)
	_, runtimeModel := (gatewayprovider.ModelPolicy{Record: &record}).ForwardMappedModels("gpt-5.4", true)
	require.Equal(t, "gpt-5.6-sol", runtimeModel)
	require.Equal(t, runtimeModel, result.Models[0].AttributeTargets[0].Model)
}

// TestCatalogueUsesFinalRouteOutput combines an unknown provider route with a
// configured image route behind the same alias. Image eligibility must follow
// each final model, retain the supported route, and keep mixed outputs on Chat.
func TestCatalogueUsesFinalRouteOutput(t *testing.T) {
	groupID := int64(1)
	group := &routing.Group{ID: groupID, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolImagesGenerations, protocol.ProtocolOpenAIChatCompletions}}
	policies := &countedCataloguePolicies{PricingConfigService: routing.NewPricingConfigService(nil, nil, routing.PricingConfigOptions{ReadGroup: func(context.Context, int64) (*routing.Group, error) { return group, nil }})}
	image, text, mixed := []string{"image"}, []string{"text"}, []string{"text", "image"}
	attributes := &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{Lookup: func(model string) modelcatalog.Attributes {
		switch model {
		case "canvas":
			return modelcatalog.Attributes{OutputModalities: &image}
		case "image-named-text":
			return modelcatalog.Attributes{OutputModalities: &text}
		case "mixed":
			return modelcatalog.Attributes{OutputModalities: &mixed}
		default:
			return modelcatalog.Attributes{}
		}
	}}}
	records := []provider.Record{
		{Platform: "openai", Type: "apikey", Credentials: map[string]any{"model_mapping": map[string]any{"alias": "unknown"}, "model_whitelist": []string{"unknown"}}},
		{Platform: "openai", Type: "apikey", Credentials: map[string]any{"model_mapping": map[string]any{"alias": "canvas"}, "model_whitelist": []string{"canvas", "image-named-text", "mixed"}}},
	}
	resolver := routing.RequestableResolver{GroupPolicies: policies, Attributes: attributes}
	result := resolver.ResolveWithProviders(context.Background(), &groupID, "", nil, gatewayprovider.CatalogueProviders(records))
	require.NoError(t, result.Err)
	byID := map[string]routing.RequestableModel{}
	for _, model := range result.Models {
		byID[model.ID] = model
	}
	require.Contains(t, byID["alias"].Protocols, protocol.ProtocolImagesGenerations)
	require.NotContains(t, byID["image-named-text"].Protocols, protocol.ProtocolImagesGenerations)
	require.Contains(t, byID["mixed"].Protocols, protocol.ProtocolOpenAIChatCompletions)
	require.Contains(t, byID["mixed"].Protocols, protocol.ProtocolImagesGenerations)
	for _, target := range byID["alias"].AttributeTargets {
		if target.ClientProtocol == string(protocol.ProtocolImagesGenerations) {
			require.Equal(t, "canvas", target.Model)
		}
	}
}

// TestCatalogueRecognizesGeminiLiteImage resolves the shipped Gemini Lite
// image record for batch execution. Its image protocol must come from catalog
// output metadata even though the former family-prefix classifier omitted it.
func TestCatalogueRecognizesGeminiLiteImage(t *testing.T) {
	groupID := int64(1)
	group := &routing.Group{ID: groupID, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolImageBatches}}
	policies := &countedCataloguePolicies{PricingConfigService: routing.NewPricingConfigService(nil, nil, routing.PricingConfigOptions{ReadGroup: func(context.Context, int64) (*routing.Group, error) { return group, nil }})}
	record := provider.Record{Platform: "gemini", Type: "apikey", Credentials: map[string]any{"model_whitelist": []string{"gemini-3.1-flash-lite-image"}}}
	resolver := routing.RequestableResolver{GroupPolicies: policies, Attributes: &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}}
	result := resolver.ResolveWithProviders(context.Background(), &groupID, "", nil, gatewayprovider.CatalogueProviders([]provider.Record{record}))
	require.NoError(t, result.Err)
	require.Len(t, result.Models, 1)
	require.Contains(t, result.Models[0].Protocols, protocol.ProtocolImageBatches)
}

// TestCatalogueExposesEffortMappedAliasWithSuffixedTargets configures an
// API-key provider exposing effort-suffixed upstream IDs via
// effort_model_mapping. The catalogue must list the base alias, retain both
// whitelisted suffixed upstream models, and flag upstream pricing ambiguity.
func TestCatalogueExposesEffortMappedAliasWithSuffixedTargets(t *testing.T) {
	groupID := int64(7)
	group := &routing.Group{ID: groupID, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages}}
	policies := &countedCataloguePolicies{
		PricingConfigService: routing.NewPricingConfigService(nil, nil, routing.PricingConfigOptions{
			ReadGroup: func(context.Context, int64) (*routing.Group, error) { return group, nil },
		}),
	}
	record := provider.Record{
		Platform: "anthropic",
		Type:     "apikey",
		Credentials: map[string]any{
			"model_whitelist": []string{"claude-opus-5-5-low", "claude-opus-5-5-high"},
			provider.EffortModelMappingCredentialKey: map[string]any{
				"claude-opus-5-5": map[string]any{
					"low":    "claude-opus-5-5-low",
					"high":   "claude-opus-5-5-high",
					"medium": "claude-opus-5-5-medium",
				},
			},
		},
	}
	resolver := routing.RequestableResolver{
		GroupPolicies: policies,
		Attributes:    &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}},
	}
	result := resolver.ResolveWithProviders(context.Background(), &groupID, "", nil, gatewayprovider.CatalogueProviders([]provider.Record{record}))
	require.NoError(t, result.Err)
	byID := map[string]routing.RequestableModel{}
	for _, m := range result.Models {
		byID[m.ID] = m
	}
	alias, ok := byID["claude-opus-5-5"]
	require.True(t, ok, "base model exposed via effort_model_mapping must be requestable")
	require.Equal(t, []string{"claude-opus-5-5-high", "claude-opus-5-5-low"}, alias.UpstreamModels)
	require.True(t, alias.PricingAmbiguous)
	require.Contains(t, alias.Protocols, protocol.ProtocolAnthropicMessages)
}
