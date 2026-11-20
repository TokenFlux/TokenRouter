package httpapi

import (
	"context"
	"errors"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_forward_prelude_adapter.go binds Responses preparation to HTTP
// admission, identity and tool-restoration state.
// Responses preparation clears request-local response mappings before deriving
// this attempt's provider identity and compatibility policy. The prelude
// adapter supplies group image controls, client admission and protocol
// transformations to the shared runner, so rejected preparation uses the
// active HTTP context before any upstream response is opened.

// openAIForwardPreludeAdapter binds one Responses preparation to its provider
// and HTTP request.
type openAIForwardPreludeAdapter struct {
	// s, c, provider and ctx are borrowed across one prelude and transformation
	// call. A copied adapter retains these same references; callers keep their
	// selected attempt and live HTTP state available until those phases finish.
	s *OpenAIResponsesExecutor // shared Responses execution services
	c *gin.Context             // active HTTP request context
	// selected protocol attempt provider
	provider *gatewayprovider.ExecutionProvider
	ctx      context.Context // admitted execution policy context
}

// openAIForwardProfile describes the selected provider's credential, platform
// and resolved protocol flags for forwarding. Callers supply a nonnil
// execution provider whose route is resolved; the result captures the policy
// values used by prelude dispatch and the provider-specific image text driver.
func openAIForwardProfile(provider *gatewayprovider.ExecutionProvider) forward.Profile {
	imageModel := openai.ImagesResponsesMainModel
	if provider.View().IsOpenAIOAuthLike() {
		imageModel = gatewayprovider.ExecutionProtocolRecord(provider).CodexImageResponsesModel()
	}
	return forward.Profile{
		ImageResponsesModel:        imageModel,
		Platform:                   provider.Record.Platform,
		Name:                       provider.Record.Name,
		Type:                       provider.Record.Type,
		HTTPContinuation:           gatewayprovider.ExecutionProtocolTarget(provider).SupportsHTTPResponsesContinuation(),
		UsesCodex:                  provider.View().UsesOpenAICodexProtocol(),
		OpenAI:                     provider.View().IsOpenAI(),
		OAuth:                      provider.View().IsOAuth(),
		OAuthLike:                  provider.View().IsOpenAIOAuthLike(),
		APIKey:                     provider.Record.Type == capability.ProviderTypeAPIKey,
		Grok:                       provider.Record.Platform == capability.PlatformGrok,
		EncodeResponsesClientTools: gatewayprovider.ExecutionProtocolTarget(provider).RequiresResponsesClientToolEncoding(),
		StatelessResponses:         gatewayprovider.ExecutionProtocolTarget(provider).UsesStatelessResponses(),
		Anthropic:                  gatewayprovider.ExecutionProtocolTarget(provider).IsAnthropicProtocol(),
		RawChat:                    gatewayprovider.ExecutionModelPolicy(provider).RawChat(),
		ResolvedChat:               provider.Route.Protocol() == protocol.ProtocolOpenAIChatCompletions,
		Passthrough:                provider.View().IsOpenAIPassthroughEnabled(),
	}
}

// BlockGroupImages reports whether the admitted Responses group selects image
// blocking. It considers the execution context's declared client protocol and
// current effective API-key group; other protocols or missing group return
// false.
func (p openAIForwardPreludeAdapter) BlockGroupImages() bool {
	if source, _ := requeststate.ClientProtocolFromContext(p.ctx); source == protocol.ProtocolOpenAIResponses {
		if key := GetExecutionAPIKey(p.c); key != nil && key.Group != nil {
			return key.Group.ResponsesImagePolicy == "block"
		}
	}
	return false
}

// Begin resets model observation and actual-endpoint metadata for this
// attempt. A provider resolved to raw Chat records that endpoint immediately,
// so later transport or conversion errors report the route chosen before
// sending.
func (p openAIForwardPreludeAdapter) Begin() {
	BeginUpstreamResponseModelObservation(p.c)
	ClearActualOpenAIUpstreamEndpoint(p.c)
	if shouldForwardOpenAIResponsesViaRawChatCompletions(p.provider) {
		SetActualOpenAIUpstreamEndpoint(p.c, "/v1/chat/completions")
	}
}

// ClearMappings removes request-local Grok, OpenAI, namespace and Codex
// reverse-tool state before preparation. Callers run it at attempt start so
// another provider's restoration data cannot be reused by the new response.
func (p openAIForwardPreludeAdapter) ClearMappings() {
	ClearGrokResponsesClientToolMapping(p.c)
	ClearOpenAIResponsesClientToolMapping(p.c)
	ClearOpenAIResponsesNamespaceNames(p.c)
	SetCodexToolNameReverse(p.c, nil)
}

// PrepareIdentity prepares Codex identity for this selected attempt provider
// and request. The configured provider reader supplies any parent identity
// information; errors return to PreparePrelude before client compatibility
// transformations proceed.
func (p openAIForwardPreludeAdapter) PrepareIdentity(ctx context.Context) error {
	_, err := PrepareCodexIdentity(ctx, p.c, p.s.Requests.Providers, p.provider)
	return err
}

// MatchTLS resolves this provider's TLS routing match from the inbound
// request.
func (p openAIForwardPreludeAdapter) MatchTLS() egress.TLSFingerprintRouterMatchResult {
	return p.s.Requests.MatchTLS(p.c, p.provider)
}

// ClientAllowed evaluates and logs client admission with the supplied TLS
// match. An enabled unmatched restriction returns its configured forbidden
// message; disabled or matched admission returns true with an empty rejection
// message.
func (p openAIForwardPreludeAdapter) ClientAllowed(ctx context.Context, tls egress.TLSFingerprintRouterMatchResult, body []byte) (bool, string) {
	result := p.s.Requests.DetectClient(p.c, p.provider, tls)
	LogCodexCLIOnlyDetection(ctx, p.c, p.provider, APIKeyIDFromContext(p.c), result, body)
	if result.Enabled && !result.Matched {
		return false, OpenAIClientPolicyForbiddenMessage(result)
	}
	return true, ""
}

// Reject publishes requested local-policy, feature or upstream observations
// before writing its JSON rejection. Each flag controls its corresponding
// request marker, and the supplied parameter is passed to the error writer for
// client diagnostics.
func (p openAIForwardPreludeAdapter) Reject(v forward.Rejection) {
	if v.PolicyDenied {
		MarkOpsClientBusinessLimited(p.c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
	}
	if v.FeatureDenied {
		MarkOpsClientBusinessLimited(p.c, OpsClientBusinessLimitedReasonLocalFeatureGate)
	}
	if v.ObserveUpstream {
		SetOpsUpstreamError(p.c, v.Status, v.Message, "")
	}
	WriteOpenAIForwardRejection(p.c, v.Status, v.Type, v.Message, v.Param)
}

// LiteHeader reports whether the inbound header declares Responses Lite
// execution.
func (p openAIForwardPreludeAdapter) LiteHeader() bool {
	return gatewayprovider.ImageIntent().IsOpenAIResponsesLiteHeader(p.c.GetHeader(media.ResponsesLiteHeader))
}

// LitePayload delegates credential-specific Responses Lite cleanup and returns
// its rejection parameter. The default parameter is tools; a
// ResponsesLiteValidationError supplies its own parameter so the prelude
// rejection identifies the invalid field.
func (p openAIForwardPreludeAdapter) LitePayload(body []byte) ([]byte, bool, string, error) {
	updated, changed, err := gatewayprovider.NormalizeResponsesLiteForProvider(p.provider.View(), body)
	param := "tools"
	if validation, ok := errors.AsType[*openai.ResponsesLiteValidationError](err); ok {
		param = validation.Parameter()
	}
	return updated, changed, param, err
}

// Transport selects HTTP SSE with the HTTP client-protocol decision reason.
func (p openAIForwardPreludeAdapter) Transport() forward.TransportDecision {
	return forward.TransportDecision{Transport: "http_sse", Reason: "client_protocol_http"}
}

// CompactPath reports whether the current HTTP request targets legacy
// Responses Compact.
func (p openAIForwardPreludeAdapter) CompactPath() bool {
	return IsOpenAIResponsesCompactPath(p.c)
}

// FlattenRequired delegates namespace-declaration flattening policy for this
// provider, transport and Compact state.
func (p openAIForwardPreludeAdapter) FlattenRequired(v forward.TransportDecision, passthrough, compact bool) bool {
	return gatewayprovider.ShouldFlattenOpenAIResponsesNamespaces(p.provider, egress.OpenAIUpstreamTransport(v.Transport), passthrough, compact)
}

// Flatten lowers namespace declarations and binds their restoration names in
// the active request. The shared helper controls body rewriting and errors;
// callers clear prior mappings before invoking it for another provider
// attempt.
func (p openAIForwardPreludeAdapter) Flatten(body []byte) ([]byte, error) {
	return FlattenOpenAIResponsesNamespaces(p.c, body)
}

// StripNamespacesRequired delegates historical namespace cleanup policy for
// the selected provider and transport.
func (p openAIForwardPreludeAdapter) StripNamespacesRequired(v forward.TransportDecision, passthrough bool) bool {
	return gatewayprovider.ShouldStripOpenAIResponsesInputNamespaces(p.provider, egress.OpenAIUpstreamTransport(v.Transport), passthrough)
}

// KeepNamespaces delegates whether historical tool calls retain namespaces
// under the selected protocol policy. Provider, transport, passthrough and
// Compact policy are supplied together; the prelude uses this result when
// applying input namespace cleanup.
func (p openAIForwardPreludeAdapter) KeepNamespaces(v forward.TransportDecision, passthrough, compact bool) bool {
	return gatewayprovider.ShouldKeepOpenAIResponsesToolCallNamespaces(p.provider, egress.OpenAIUpstreamTransport(v.Transport), passthrough, compact)
}

// AdaptClientTools lowers client tool representations and binds the
// response-restoration mapping on success. A bridge error leaves existing
// request mapping untouched; callers clear that state at attempt start before
// trying this preparation.
func (p openAIForwardPreludeAdapter) AdaptClientTools(body []byte) ([]byte, error) {
	body, mapping, err := protocolbridge.AdaptOpenAIResponsesClientTools(body)
	// Install restoration state only after successful lowering so the response
	// can recover names belonging to the body that the prelude actually adopts.
	if err == nil {
		SetOpenAIResponsesClientToolMapping(p.c, mapping)
	}
	return body, err
}

// BindMessagesBridge delegates the request's Messages-compatibility marker
// publication for this preparation. SetOpenAICompatMessagesBridgeContext
// records true values and ignores false, so this call does not clear a marker
// already present on the request.
func (p openAIForwardPreludeAdapter) BindMessagesBridge(v bool) {
	SetOpenAICompatMessagesBridgeContext(p.c, v)
}

// CodexClient recognizes official request identity or the configured force-CLI
// setting.
func (p openAIForwardPreludeAdapter) CodexClient() bool {
	return openai.IsCodexOfficialClientByHeaders(p.c.GetHeader("User-Agent"), p.c.GetHeader("originator")) || p.s.Requests.Options.ForceCLI
}

// ImageToolPolicy reads explicit image-tool handling from the selected
// provider record.
func (p openAIForwardPreludeAdapter) ImageToolPolicy() string {
	return gatewayprovider.ExecutionProtocolRecord(p.provider).CodexImageGenerationExplicitToolPolicy()
}

// ObserveTransport records the chosen transport and reason in the live HTTP
// context. A WSv2 decision also emits normalized debug fields; a missing Gin
// context skips request publication while the decision can still be logged.
func (p openAIForwardPreludeAdapter) ObserveTransport(v forward.TransportDecision, model string, stream bool) {
	if p.c != nil {
		p.c.Set("openai_ws_transport_decision", v.Transport)
		p.c.Set("openai_ws_transport_reason", v.Reason)
	}
	if v.Transport == string(egress.OpenAIUpstreamTransportResponsesWebsocketV2) {
		gatewayprovider.LogOpenAIWSModeDebug("selected provider_id=%d provider_type=%s transport=%s reason=%s model=%s stream=%v", p.provider.Record.ID, p.provider.Record.Type, gatewayprovider.NormalizeOpenAIWSLogValue(v.Transport), gatewayprovider.NormalizeOpenAIWSLogValue(v.Reason), model, stream)
	}
}

// MappedModel applies this selected provider's effort-aware single-hop model
// mapping for preparation.
func (p openAIForwardPreludeAdapter) MappedModel(model string) string {
	policy := gatewayprovider.ExecutionModelPolicy(p.provider)
	if p.c != nil && p.c.Request != nil {
		policy = policy.WithContext(p.c.Request.Context())
	}
	return policy.Mapped(model)
}
