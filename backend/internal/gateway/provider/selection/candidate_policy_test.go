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
)

// candidate_policy_test.go checks provider selection against final model and
// protocol capabilities. Mixed-provider fixtures exercise model mapping,
// hosted image tools, and GIF restrictions while distinguishing a locally
// unsupported route from an available alternative.

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
