package selection

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerredis "github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache"
)

// candidate_policy_test.go checks provider selection against final model and
// protocol capabilities. Mixed-provider fixtures exercise model mapping,
// hosted image tools, and GIF restrictions while distinguishing a locally
// unsupported route from an available alternative.

// TestChatFunctionToolSelectionSkipsNativeGPT62 verifies candidate exhaustion
// and alternative selection in both scheduler modes. A relay or a configured
// bridge can still be selected, while official native Chat handles text.
func TestChatFunctionToolSelectionSkipsNativeGPT62(t *testing.T) {
	const chat = capability.ProtocolOpenAIChatCompletions
	const responses = capability.ProtocolOpenAIResponses
	const alias = "client-model"
	for _, mode := range []routing.GroupSchedulerType{routing.GroupSchedulerTypeBasic, routing.GroupSchedulerTypeAdvanced} {
		for _, model := range []string{"gpt-6.2-astra", "gpt-6.2-sol"} {
			t.Run(string(mode)+"/"+model, func(t *testing.T) {
				group := &routing.Group{
					ID: 91, Hydrated: true, Status: routing.StatusActive, SchedulerType: mode,
					AllowedProtocols:  []capability.ProtocolID{chat},
					ProtocolFallbacks: map[capability.ProtocolID][]capability.ProtocolID{chat: {responses}},
				}
				official := mixedGroupProvider(1, capability.PlatformOpenAI, model, group.ID)
				official.Record.Credentials["model_mapping"] = map[string]any{alias: model}
				official.Record.Credentials[providercore.UpstreamProtocolsKey] = []string{string(chat), string(responses)}
				relay := mixedGroupProvider(2, capability.PlatformOpenAI, model, group.ID)
				relay.Record.Credentials["model_mapping"] = map[string]any{alias: model}
				relay.Record.Credentials["base_url"] = " https://relay.example/v1 "
				relay.Record.Credentials[providercore.UpstreamProtocolsKey] = []string{string(chat)}

				base := requeststate.WithClientProtocol(requeststate.WithGroup(t.Context(), group), chat)
				plan := gatewayadapter.NewRoutePlanner(nil).PlanRoute(base, group, &group.ID, alias)
				base = requeststate.WithRoutePlan(base, plan)
				functions := requeststate.WithChatFunctionTools(base)

				selectProvider := func(ctx context.Context, records ...gatewayadapter.ExecutionProvider) (*gatewayadapter.SelectionResult, error) {
					selector := NewCompatible(CompatibleDependencies{Reads: Reads{Providers: &mixedGroupProviders{values: records}}}, DefaultOptions())
					result, _, err := selector.SelectProviderWithSchedulerForCapability(ctx, &group.ID, "", "", alias, nil, egress.OpenAIUpstreamTransportAny, providercore.OpenAIEndpointCapabilityTextGeneration, false, false)
					if result != nil && result.ReleaseFunc != nil {
						result.ReleaseFunc()
					}
					return result, err
				}

				selected, err := selectProvider(base, official)
				require.NoError(t, err)
				require.Equal(t, int64(1), selected.Provider.Record.ID)

				selected, err = selectProvider(functions, official)
				require.Nil(t, selected)
				require.Equal(t, gatewayadapter.NativeChatFunctionToolsUnsupported, apperror.Reason(err), "%v", err)
				require.Contains(t, err.Error(), "/v1/responses")

				selected, err = selectProvider(functions, official, relay)
				require.NoError(t, err)
				require.Equal(t, int64(2), selected.Provider.Record.ID)
				routed, err := gatewayadapter.ProviderForProtocolAttempt(functions, selected.Provider)
				require.NoError(t, err)
				require.Equal(t, chat, routed.Route.Protocol())

				selected, err = selectProvider(functions)
				require.Nil(t, selected)
				require.ErrorIs(t, err, schedulercore.ErrNoAvailableProviders)

				// Removing native Chat from this fixture explicitly selects the
				// configured Responses bridge through the original group policy.
				official.Record.Credentials[providercore.UpstreamProtocolsKey] = []string{string(responses)}
				selected, err = selectProvider(functions, official)
				require.NoError(t, err)
				routed, err = gatewayadapter.ProviderForProtocolAttempt(functions, selected.Provider)
				require.NoError(t, err)
				require.Equal(t, responses, routed.Route.Protocol())
			})
		}
	}
}

// TestChatToolExhaustionRespectsUpstreamWhitelist uses final provider aliases
// to exercise both directions of the group gate. A forbidden original route
// contributes no tool error, and a forbidden relay cannot mask a valid one.
func TestChatToolExhaustionRespectsUpstreamWhitelist(t *testing.T) {
	const chat = capability.ProtocolOpenAIChatCompletions
	const model = "gpt-6.2-sol"
	const alias = "request-alias"
	for _, mode := range []routing.GroupSchedulerType{routing.GroupSchedulerTypeBasic, routing.GroupSchedulerTypeAdvanced} {
		t.Run(string(mode), func(t *testing.T) {
			group := &routing.Group{ID: 91, Hydrated: true, Status: routing.StatusActive, SchedulerType: mode, AllowedProtocols: []capability.ProtocolID{chat}}
			official := mixedGroupProvider(1, capability.PlatformOpenAI, model, group.ID)
			official.Record.Credentials["model_mapping"] = map[string]any{alias: model}
			official.Record.Credentials[providercore.UpstreamProtocolsKey] = []string{string(chat)}
			relay := mixedGroupProvider(2, capability.PlatformOpenAI, "gpt-6.2-astra", group.ID)
			relay.Record.Credentials["model_mapping"] = map[string]any{alias: "gpt-6.2-astra"}
			relay.Record.Credentials["base_url"] = "https://relay.example/v1"
			relay.Record.Credentials[providercore.UpstreamProtocolsKey] = []string{string(chat)}
			selectOne := func(withFunction bool, allowed string, records ...gatewayadapter.ExecutionProvider) error {
				pricing := routingtestkit.NewConfigServiceFixture(routingtestkit.StandardPricingConfigRepository(routingtestkit.Configuration{
					ID: 1, Status: routing.StatusActive, GroupIDs: []int64{group.ID},
					RestrictModels: true, BillingModelSource: routing.BillingModelSourceUpstream,
					ModelPricing: []routing.ModelPricingEntry{{Models: []string{allowed}}},
				}, map[int64]string{group.ID: capability.PlatformOpenAI}))
				svc := NewCompatible(CompatibleDependencies{Reads: Reads{Providers: &mixedGroupProviders{values: records}}, Shared: Shared{GroupPolicies: pricing}}, DefaultOptions())
				base := requeststate.WithClientProtocol(requeststate.WithGroup(t.Context(), group), chat)
				require.True(t, svc.NeedsUpstreamGroupRestriction(base, &group.ID))
				plan := gatewayadapter.NewRoutePlanner(pricing).PlanRoute(base, group, &group.ID, alias)
				ctx := requeststate.WithRoutePlan(base, plan)
				if withFunction {
					ctx = requeststate.WithChatFunctionTools(ctx)
				}
				result, _, err := svc.SelectProviderWithSchedulerForCapability(ctx, &group.ID, "", "", alias, nil, egress.OpenAIUpstreamTransportAny, providercore.OpenAIEndpointCapabilityTextGeneration, false, false)
				require.Nil(t, result)
				return err
			}
			t.Run("disallowed official appears incompatible", func(t *testing.T) {
				baseline := selectOne(false, "gpt-5.4", official)
				require.ErrorIs(t, baseline, schedulercore.ErrNoAvailableProviders)
				err := selectOne(true, "gpt-5.4", official)
				require.ErrorIs(t, err, schedulercore.ErrNoAvailableProviders)
			})
			t.Run("disallowed relay hides real incompatibility", func(t *testing.T) {
				baseline := selectOne(true, model, official)
				require.Equal(t, gatewayadapter.NativeChatFunctionToolsUnsupported, apperror.Reason(baseline))
				err := selectOne(true, model, official, relay)
				require.Equal(t, gatewayadapter.NativeChatFunctionToolsUnsupported, apperror.Reason(err))
			})
		})
	}
}

// TestChatToolExhaustionUsesAuthoritativeCandidate starts with a cached relay
// and reloads the same provider as first-party Chat. Its latest rejection must
// replace the cached tool support in the exhaustion diagnosis.
func TestChatToolExhaustionUsesAuthoritativeCandidate(t *testing.T) {
	const chat = capability.ProtocolOpenAIChatCompletions
	const model = "gpt-6.2-sol"
	for _, mode := range []routing.GroupSchedulerType{routing.GroupSchedulerTypeBasic, routing.GroupSchedulerTypeAdvanced} {
		t.Run(string(mode), func(t *testing.T) {
			group := &routing.Group{ID: 91, Hydrated: true, Status: routing.StatusActive, SchedulerType: mode, AllowedProtocols: []capability.ProtocolID{chat}}
			stale := mixedGroupProvider(1, capability.PlatformOpenAI, model, group.ID)
			stale.Record.Credentials[providercore.UpstreamProtocolsKey] = []string{string(chat)}
			stale.Record.Credentials["base_url"] = "https://relay.example/v1"
			current := mixedGroupProvider(1, capability.PlatformOpenAI, model, group.ID)
			current.Record.Credentials[providercore.UpstreamProtocolsKey] = []string{string(chat)}
			snap := &openAISnapshotCacheStub{snapshotProviders: []*gatewayadapter.ExecutionProvider{&stale}, providersByID: map[int64]*gatewayadapter.ExecutionProvider{1: &stale}}
			svc := NewCompatible(CompatibleDependencies{Reads: Reads{
				Providers: &mixedGroupProviders{values: []gatewayadapter.ExecutionProvider{current}},
				Snapshot:  schedulerredis.NewSnapshotReader(schedulercore.NewSnapshotService(snap, nil, nil, nil, nil)),
			}}, DefaultOptions())
			ctx := requeststate.WithClientProtocol(requeststate.WithGroup(t.Context(), group), chat)
			plan := gatewayadapter.NewRoutePlanner(nil).PlanRoute(ctx, group, &group.ID, model)
			ctx = requeststate.WithChatFunctionTools(requeststate.WithRoutePlan(ctx, plan))
			result, _, err := svc.SelectProviderWithSchedulerForCapability(ctx, &group.ID, "", "", model, nil, egress.OpenAIUpstreamTransportAny, providercore.OpenAIEndpointCapabilityTextGeneration, false, false)
			require.Nil(t, result)
			require.Equal(t, gatewayadapter.NativeChatFunctionToolsUnsupported, apperror.Reason(err))
		})
	}
}

// TestMixedGroupImageCandidateUsesFinalMappedModel maps an image alias through
// a wildcard provider scope. A text target is rejected and an image-generation
// target passes, protecting final-model checks during image selection.
func TestMixedGroupImageCandidateUsesFinalMappedModel(t *testing.T) {
	selector := NewCompatible(CompatibleDependencies{}, DefaultOptions())
	value := mixedGroupProvider(1, capability.PlatformOpenAI, "*", 91)
	value.Record.Credentials["model_mapping"] = map[string]any{"image-alias": "gpt-test"}
	ctx := context.WithValue(routing.WithModelAttributes(context.Background(), &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}), imageModelRequiredKey{}, true)
	require.Equal(t, "MODEL_CAPABILITY_UNKNOWN", selector.candidateEligibilityReason(ctx, &value, "", "image-alias", false, ""))
	value.Record.Credentials["model_mapping"] = map[string]any{"image-alias": "gpt-image-1"}
	require.Empty(t, selector.candidateEligibilityReason(ctx, &value, "", "image-alias", false, ""))
}

// TestImageCandidateProtocolOverride declares image output on one upstream protocol.
// Candidate filtering must apply the same override as the resolved forwarding route,
// while leaving the shared candidate's route unset.
func TestImageCandidateProtocolOverride(t *testing.T) {
	selector := NewCompatible(CompatibleDependencies{}, DefaultOptions())
	value := mixedGroupProvider(1, capability.PlatformOpenAI, "canvas", 91)
	value.Record.Credentials["upstream_protocols"] = []string{"openai_images_generations"}
	image := []string{"image"}
	svc := &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}, Repo: candidateAttributeRepo{config: &routing.ModelAttributeConfig{Status: "active", Rules: []routing.ModelAttributeRule{{Models: []string{"canvas"}, Match: &routing.ModelAttributeMatch{UpstreamProtocol: "openai_images_generations"}, Attributes: modelcatalog.Attributes{OutputModalities: &image}}}}}}
	ctx := requeststate.WithClientProtocol(requeststate.WithGroup(context.Background(), &routing.Group{ID: 91, AllowedProtocols: []capability.ProtocolID{capability.ProtocolImagesGenerations}}), capability.ProtocolImagesGenerations)
	ctx = routing.WithModelAttributes(ctx, svc)
	ctx = context.WithValue(ctx, imageModelRequiredKey{}, true)
	routed, err := gatewayadapter.ProviderForProtocolAttempt(ctx, &value)
	require.NoError(t, err)
	attrs, err := gatewayadapter.ResolveModelAttributes(ctx, routed, "canvas", nil)
	require.NoError(t, err)
	require.NoError(t, routing.RequireImageOutput(attrs.Attributes))
	t.Logf("resolved route protocol=%q", routed.Route.Protocol())
	require.Empty(t, selector.candidateEligibilityReason(ctx, &value, "", "canvas", false, ""), "The candidate must use the same protocol-qualified override as forwarding")
}

// TestImageToolSelectionChecksEveryProvider gives the preferred provider no
// declaration and another provider a protocol-scoped image override. Both
// scheduler modes choose the valid provider and return typed errors when none qualify.
func TestImageToolSelectionChecksEveryProvider(t *testing.T) {
	for _, kind := range []string{"compatible", "generic"} {
		for _, mode := range []routing.GroupSchedulerType{routing.GroupSchedulerTypeBasic, routing.GroupSchedulerTypeAdvanced} {
			for _, source := range []capability.ProtocolID{capability.ProtocolOpenAIResponses, capability.ProtocolResponsesWebSocket} {
				t.Run(kind+"/"+string(mode)+"/"+string(source), func(t *testing.T) {
					group := &routing.Group{ID: 91, Hydrated: true, Status: routing.StatusActive, SchedulerType: mode, AllowedProtocols: []capability.ProtocolID{source}}
					first := mixedGroupProvider(1, capability.PlatformOpenAI, "driver", 91)
					second := mixedGroupProvider(2, capability.PlatformOpenAI, "driver", 91)
					second.Record.Type = capability.ProviderTypeOAuth
					for _, v := range []*gatewayadapter.ExecutionProvider{&first, &second} {
						v.Record.Credentials[providercore.UpstreamProtocolsKey] = []string{string(source)}
					}
					first.Record.Priority = 0
					second.Record.Priority = 10
					repo := &mixedGroupProviders{values: []gatewayadapter.ExecutionProvider{first, second}}
					image := []string{"image"}
					attrs := &candidateAttributeRepo{config: &routing.ModelAttributeConfig{Status: "active", Rules: []routing.ModelAttributeRule{{Models: []string{"canvas"}, Match: &routing.ModelAttributeMatch{ProviderType: "oauth", UpstreamProtocol: string(source)}, Attributes: modelcatalog.Attributes{OutputModalities: &image}}}}}
					service := &routing.ModelAttributeService{Repo: attrs, Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}
					base := requeststate.WithClientProtocol(requeststate.WithGroup(context.Background(), group), source)
					selectModel := func(ctx context.Context) (*gatewayadapter.SelectionResult, error) {
						if kind == "generic" {
							return NewGeneric(GenericDependencies{Reads: Reads{Providers: repo}}, DefaultOptions()).SelectProviderWithLoadAwareness(ctx, &group.ID, "", "driver", nil)
						}
						selection, _, err := NewCompatible(CompatibleDependencies{Reads: Reads{Providers: repo}}, DefaultOptions()).SelectProviderWithSchedulerForCapability(ctx, &group.ID, "", "", "driver", nil, egress.OpenAIUpstreamTransportHTTPSSE, providercore.OpenAIEndpointCapabilityResponses, false, false)
						return selection, err
					}
					ctx := media.WithResponsesImageSelection(routing.WithModelAttributes(base, service), []byte(`{"tools":[{"type":"image_generation","model":"canvas"}]}`), false)
					selected, err := selectModel(ctx)
					require.NoError(t, err)
					require.NotNil(t, selected)
					require.Equal(t, int64(2), selected.Provider.Record.ID)
					if selected.ReleaseFunc != nil {
						selected.ReleaseFunc()
					}
					// A new request observes the edited profile, while the first context keeps
					// its snapshot for any further attempts belonging to that request.
					attrs.config.Rules = nil
					ctx = media.WithResponsesImageSelection(routing.WithModelAttributes(base, service), []byte(`{"tools":[{"type":"image_generation","model":"canvas"}]}`), false)
					_, err = selectModel(ctx)
					require.Equal(t, "MODEL_CAPABILITY_UNKNOWN", apperror.Reason(err))
					text := []string{"text"}
					attrs.config.Rules = []routing.ModelAttributeRule{{Models: []string{"canvas"}, Attributes: modelcatalog.Attributes{OutputModalities: &text}}}
					ctx = media.WithResponsesImageSelection(routing.WithModelAttributes(base, service), []byte(`{"tools":[{"type":"image_generation","model":"canvas"}]}`), false)
					_, err = selectModel(ctx)
					require.Equal(t, "MODEL_CAPABILITY_UNSUPPORTED", apperror.Reason(err))
					attrs.failure = errors.New("profile read unavailable")
					ctx = media.WithResponsesImageSelection(routing.WithModelAttributes(base, service), []byte(`{"tools":[{"type":"image_generation","model":"canvas"}]}`), false)
					_, err = selectModel(ctx)
					require.Equal(t, "MODEL_ATTRIBUTES_UNAVAILABLE", apperror.Reason(err))
				})
			}
		}
	}
}

// TestImageToolCandidateStripping compares provider stripping with client and
// group overrides. Only tools retained by forwarding require declared image
// output, so removing an unknown tool keeps its text route selectable.
func TestImageToolCandidateStripping(t *testing.T) {
	for _, tc := range []struct {
		name        string // selection scenario label
		codex       bool   // client uses Codex preparation
		groupPolicy string // group image policy setting
		want        string // expected capability rejection reason
	}{
		{"codex strips", true, "", ""},
		{"ordinary caller", false, "", "MODEL_CAPABILITY_UNKNOWN"},
		{"group blocks", false, "block", ""},
		{"group allows", true, "enabled", "MODEL_CAPABILITY_UNKNOWN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := mixedGroupProvider(1, capability.PlatformOpenAI, "gpt-5.4", 91)
			value.Record.Credentials[providercore.UpstreamProtocolsKey] = []string{string(capability.ProtocolOpenAIResponses)}
			value.Record.Extra = map[string]any{providercore.CodexImageGenerationExplicitToolPolicyKey: providercore.CodexImagePolicyStrip}
			group := &routing.Group{ID: 91, ResponsesImagePolicy: tc.groupPolicy}
			ctx := requeststate.WithClientProtocol(requeststate.WithGroup(context.Background(), group), capability.ProtocolOpenAIResponses)
			ctx = routing.WithModelAttributes(ctx, &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}})
			ctx = media.WithResponsesImageSelection(ctx, []byte(`{"tools":[{"type":"image_generation","model":"unknown-canvas"}]}`), tc.codex)
			require.Equal(t, tc.want, candidateOutputEligibility(ctx, &value, "gpt-5.4"))
		})
	}
}

// TestImageToolSelectionUsesConvertedDefault declares a custom image-only model
// and disables the hosted default. Managed Responses conversion supplies the
// custom model to its unspecified tool, so candidate admission must accept it.
func TestImageToolSelectionUsesConvertedDefault(t *testing.T) {
	image, empty := []string{"image"}, []string{}
	attrs := &candidateAttributeRepo{config: &routing.ModelAttributeConfig{Status: "active", Rules: []routing.ModelAttributeRule{
		{Models: []string{"canvas"}, Attributes: modelcatalog.Attributes{OutputModalities: &image}},
		{Models: []string{"gpt-image-2"}, Attributes: modelcatalog.Attributes{OutputModalities: &empty}},
	}}}
	service := &routing.ModelAttributeService{Repo: attrs, Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}
	value := mixedGroupProvider(1, capability.PlatformOpenAI, "canvas", 91)
	value.Record.Credentials[providercore.UpstreamProtocolsKey] = []string{string(capability.ProtocolOpenAIResponses)}
	ctx := requeststate.WithClientProtocol(requeststate.WithGroup(context.Background(), &routing.Group{ID: 91}), capability.ProtocolOpenAIResponses)
	ctx = routing.WithModelAttributes(ctx, service)
	ctx = media.WithResponsesImageSelection(ctx, []byte(`{"tools":[{"type":"image_generation"}]}`), false)
	require.Empty(t, candidateOutputEligibility(ctx, &value, "canvas"))
	require.Equal(t, []string{""}, media.ResponsesImageSelectionFromContext(ctx).Models)
}

// TestGIFCandidateUsesProviderSwitch checks native and Responses GIF requirements.
// Disabled providers are rejected independently of model support; a new request
// sees an enabled switch while a streaming-only native endpoint remains ineligible.
func TestGIFCandidateUsesProviderSwitch(t *testing.T) {
	value := mixedGroupProvider(1, capability.PlatformOpenAI, "*", 91)
	value.Record.Type = providercore.ProviderTypeAPIKey
	value.Record.Extra = map[string]any{"images_gif_enabled": false}
	group := &routing.Group{ID: 91, AllowedProtocols: []capability.ProtocolID{capability.ProtocolImagesGenerations, capability.ProtocolOpenAIResponses}}
	base := requeststate.WithClientProtocol(requeststate.WithGroup(context.Background(), group), capability.ProtocolImagesGenerations)
	ctx := context.WithValue(routing.WithModelAttributes(base, &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}), imageModelRequiredKey{}, true)
	ctx = media.WithImageGIFRequirement(ctx, true)
	require.Equal(t, "IMAGE_GIF_UNSUPPORTED", candidateOutputEligibility(ctx, &value, "gpt-image-2.6-flare"))
	value.Record.Extra["images_gif_enabled"] = true
	require.Empty(t, candidateOutputEligibility(ctx, &value, "gpt-image-2.6-flare"))
	value.Record.Credentials["images_generations_mode"] = "streaming"
	require.Equal(t, "IMAGE_GIF_UNSUPPORTED", candidateOutputEligibility(ctx, &value, "gpt-image-2.6-flare"))
	value.Record.Type = providercore.ProviderTypeOAuth
	require.Empty(t, candidateOutputEligibility(ctx, &value, "gpt-image-2.6-flare"))
}
