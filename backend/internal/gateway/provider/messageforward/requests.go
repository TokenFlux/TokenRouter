package messageforward

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"
)

// requests.go builds Messages and counting requests using model attributes,
// credentials, and selected endpoint policy.
// Messages attributes are applied to the final upstream model before choosing
// Anthropic or Vertex construction. Service accounts use provider project and
// location values, while ordinary builders receive request headers, beta
// filters, and fingerprint options.
//
// Relay selection validates a configured base before adding endpoint paths. An
// enabled empty custom base produces an error, and custom relays can receive an
// escaped provider proxy query. Request builders retain current attempt state
// in their callbacks so header assembly uses the successful beta evaluation
// already attached to the attempt. Counting shares those callbacks while
// selecting its own path and body preparation.

// buildRequest applies final-model parameters and selects the provider request
// builder. Anthropic service accounts use Vertex with deferred-tool cache
// controls stripped; other targets delegate prepared request and wire bytes to
// Anthropic.
func (r *Runtime) buildRequest(ctx context.Context, output HTTPBoundary, state *AttemptState, target *gatewayadapter.ExecutionProvider, body []byte, token, tokenType, model string, stream, mimic bool) (*http.Request, []byte, error) {
	attributes, err := gatewayadapter.ResolveModelAttributes(ctx, target, model, r.dependencies.Attributes, "anthropic_messages")
	if err != nil {
		return nil, nil, err
	}
	body, err = gatewayadapter.ApplyModelAttributeParameters(body, attributes)
	if err != nil {
		return nil, nil, err
	}
	body = stripAnthropicWireEffortIfMatched(ctx, target, body, model)
	// Apply one-hour ephemeral cache lifetimes after model attribute rules so
	// existing markers, including those added by OAuth conversion mimicry, use 1h.
	if r.injectTTL(ctx, target, model) {
		body = anthropic.InjectAnthropicCacheControlTTL1h(body)
	}
	if target.Record.Platform == capability.PlatformAnthropic && target.Record.Type == capability.ProviderTypeServiceAccount {
		// Vertex preparation removes deferred-tool cache controls
		// before its builder sanitizes beta-dependent body fields,
		// keeping this service-account payload compatible with that
		// platform variant.
		body = anthropic.StripDeferredToolCacheControl(body)
		request, err := r.buildVertexRequest(ctx, output, target, body, token, model, stream)
		return request, body, err
	}
	return anthropic.BuildRequest(ctx, body, token, tokenType, model, stream, mimic, r.requestOptions(ctx, output, state, target, model, tokenType, mimic))
}

// buildVertexRequest delegates Vertex construction using current headers, beta
// policy, project, and model location. The selected provider supplies Vertex
// context, and callers send and close the returned request body.
func (r *Runtime) buildVertexRequest(ctx context.Context, output HTTPBoundary, target *gatewayadapter.ExecutionProvider, body []byte, token, model string, stream bool) (*http.Request, error) {
	var headers http.Header
	var beta string
	if output.RequestPresent() {
		headers = output.RequestHeaders()
		beta = anthropic.GetHeaderRaw(headers, "anthropic-beta")
	}
	return vertex.BuildAnthropicRequest(ctx, body, token, model, stream, vertex.AnthropicRequestOptions{
		ClientBeta: beta, ClientHeaders: headers, AllowedHeaders: anthropic.AllowedHeaders,
		Project: func() string {
			return gatewayadapter.ExecutionProtocolRecord(target).VertexProjectID(vertex.ServiceAccountProjectID)
		},
		Location: func(model string) string {
			return gatewayadapter.ExecutionProtocolRecord(target).VertexLocation(model)
		},
		Policy: func(ctx context.Context, header string) (map[string]struct{}, error) {
			policy := r.evaluateBeta(ctx, target, header, model)
			if policy.BlockErr != nil {
				return nil, policy.BlockErr
			}
			return anthropic.MergeDropSets(policy.FilterSet), nil
		},
		SanitizeBody: anthropic.SanitizeAnthropicBodyForBetaTokens,
		WireCasing:   anthropic.ResolveWireCasing,
		AddHeader:    anthropic.AddHeaderRaw,
		SetHeader:    anthropic.SetHeaderRaw,
		DeleteHeader: anthropic.DeleteHeaderAllForms,
		Debug: func(header http.Header, body []byte, fields map[string]string) {
			if r.dependencies.Debug != nil {
				r.dependencies.Debug.Snapshot("UPSTREAM_FORWARD_VERTEX_ANTHROPIC", header, body, fields)
			}
		},
	})
}

// buildPassthroughRequest applies model attributes before native API key
// request preparation. Nonempty provider base URLs are validated before the
// Messages suffix is added, and policy block errors propagate from
// construction.
func (r *Runtime) buildPassthroughRequest(ctx context.Context, output HTTPBoundary, state *AttemptState, target *gatewayadapter.ExecutionProvider, body []byte, token string) (*http.Request, []byte, error) {
	model := strings.Clone(strings.TrimSpace(wirejson.ParseView(body).Get("model").String()))
	attributes, err := gatewayadapter.ResolveModelAttributes(ctx, target, model, r.dependencies.Attributes, "anthropic_messages")
	if err != nil {
		return nil, nil, err
	}
	body, err = gatewayadapter.ApplyModelAttributeParameters(body, attributes)
	if err != nil {
		return nil, nil, err
	}
	body = stripAnthropicWireEffortIfMatched(ctx, target, body, model)
	// Enforce the API Key's forced Claude 1h cache policy for direct passthrough
	// builders when not already applied by the outer passthrough flow.
	if (state == nil || !state.CacheTTL1hApplied) && gatewayadapter.ShouldForceClaude1hCache(ctx, target, model) {
		body = anthropic.InjectAnthropicCacheControlTTL1h(body)
	}
	options := r.requestOptions(ctx, output, state, target, model, "apikey", false)
	options.URL = func() (string, error) {
		if base := target.View().GetBaseURL(); base != "" {
			validated, err := r.validateBaseURL(base)
			if err != nil {
				return "", err
			}
			return validated + "/v1/messages?beta=true", nil
		}
		return anthropic.ClaudeAPIURL, nil
	}
	options.OriginalPolicy = func(ctx context.Context, header, model string) (map[string]struct{}, error) {
		policy := r.evaluateBeta(ctx, target, header, model)
		if policy.BlockErr != nil {
			return nil, policy.BlockErr
		}
		return policy.FilterSet, nil
	}
	return anthropic.BuildRequestPassthrough(ctx, body, token, options)
}

// stripAnthropicWireEffortIfMatched removes output_config.effort from outbound
// Messages bytes when the provider enables effort_model_mapping_strip_effort
// and an effort-scoped model mapping matched the request.
func stripAnthropicWireEffortIfMatched(ctx context.Context, target *gatewayadapter.ExecutionProvider, body []byte, fallbackModel string) []byte {
	if target == nil || len(body) == 0 {
		return body
	}
	model := fallbackModel
	if plan, ok := requeststate.RoutePlanFromContext(ctx); ok {
		if planned := plan.Models().GroupMappedModel; planned != "" {
			model = planned
		} else if requested := plan.Models().RequestedModel; requested != "" {
			model = requested
		}
	}
	if !gatewayadapter.ExecutionModelPolicy(target).ShouldStripWireEffort(ctx, model) {
		return body
	}
	return requeststate.StripWireReasoningEffort(body, protocol.ProtocolAnthropicMessages)
}

// buildCountRequest builds counting with shared request options and the
// selected passthrough mode. Passthrough returns nil wire bytes beside its
// request; ordinary counting returns the upstream builder wire bytes.
func (r *Runtime) buildCountRequest(ctx context.Context, output HTTPBoundary, state *AttemptState, target *gatewayadapter.ExecutionProvider, body []byte, token, tokenType, model string, mimic, passthrough bool) (*http.Request, []byte, error) {
	if passthrough {
		options := r.countOptions(ctx, output, state, target, "", "apikey", false, true)
		request, err := anthropic.BuildCountTokensRequestPassthrough(ctx, body, token, options)
		return request, nil, err
	}
	options := r.countOptions(ctx, output, state, target, model, tokenType, mimic, false)
	return anthropic.BuildCountTokensRequest(ctx, body, token, tokenType, model, mimic, options)
}

// requestOptions captures target, HTTP adapter, and attempt state for Anthropic
// builders. Callbacks read current settings, reuse evaluated beta filters, and
// retain optional fingerprint and debug services through synchronous
// preparation.
func (r *Runtime) requestOptions(ctx context.Context, output HTTPBoundary, state *AttemptState, target *gatewayadapter.ExecutionProvider, model, tokenType string, mimic bool) anthropic.RequestOptions {
	options := anthropic.RequestOptions{
		InjectAPIKeyBeta: r.options.InjectAPIKeyBeta,
		ProviderID:       target.Record.ID,
		OAuth:            target.View().IsOAuth(),
		AccountUUID:      target.View().GetExtraString("account_uuid"),
		MaskSession:      target.View().IsSessionIDMaskingEnabled(),
		APIKeyBearer:     gatewayadapter.ExecutionProtocolRecord(target).GetAnthropicAPIKeyAuthScheme() == provider.AnthropicAPIKeyAuthSchemeAuthorizationBearer,
		ClientHeaders:    output.RequestHeaders(),
		ApplyOverrides: func(headers http.Header) {
			provideradapter.ApplyProviderHeaderOverrides(gatewayadapter.ExecutionProtocolRecord(target), headers)
		},
	}
	if r.dependencies.Fingerprint != nil {
		options.Fingerprint = r.dependencies.Fingerprint
	}
	options.URL = func() (string, error) {
		return r.requestURL(target, false, false)
	}
	options.FastMode = func(ctx context.Context, body []byte, headers http.Header) ([]byte, http.Header, error) {
		return gatewayadapter.ApplyAnthropicFastMode(ctx, r.dependencies.Prices, target, model, body, headers)
	}
	options.Forwarding = func(ctx context.Context) (bool, bool) {
		if r.dependencies.Settings == nil {
			return true, false
		}
		fingerprint, mimic, _ := r.dependencies.Settings.GetGatewayForwardingSettings(ctx)
		return fingerprint, mimic
	}
	options.FilterSet = func(ctx context.Context) map[string]struct{} {
		return r.betaFilters(ctx, state, target, model)
	}
	options.BetaOverride = func() (string, bool) {
		return provideradapter.HeaderOverrideValue(gatewayadapter.ExecutionProtocolRecord(target), "anthropic-beta")
	}
	options.CheckFastBeta = func(ctx context.Context) error {
		//nolint:revive // checkBetaTokens returns a concrete pointer; returning a nil pointer directly would produce a non-nil error interface.
		if err := r.checkBetaTokens(ctx, []string{anthropic.BetaFastMode}, target, model); err != nil {
			return err
		}
		return nil
	}
	options.Debug = func(req *http.Request, body []byte, fields map[string]string) {
		if r.dependencies.Debug != nil {
			r.dependencies.Debug.Snapshot("UPSTREAM_FORWARD", req.Header, body, fields)
		}
	}
	options.Capture = func(req *http.Request, body []byte) {
		if r.dependencies.Debug == nil {
			return
		}
		retain := output.Present() && tokenType == "oauth"
		line := r.dependencies.Debug.Capture(req, body, target, tokenType, mimic, retain)
		if retain {
			state.MimicDebug = line
		}
	}
	return options
}

// requestURL selects the Messages or counting endpoint and validates any
// provider relay. API key bases use the dedicated endpoint suffix, and enabled
// empty custom bases return an error.
//
// The custom-base path removes trailing slashes before adding the endpoint and
// beta query. An attached provider proxy is escaped as one query value so its
// URL separators stay inside that relay parameter. Branches permitting an
// absent configured base retain the corresponding built-in endpoint.
func (r *Runtime) requestURL(target *gatewayadapter.ExecutionProvider, count, passthrough bool) (string, error) {
	endpoint, path := anthropic.ClaudeAPIURL, "/v1/messages"
	if count {
		endpoint, path = anthropic.ClaudeAPICountTokensURL, "/v1/messages/count_tokens"
	}
	if target.Record.Type == capability.ProviderTypeAPIKey || (count && passthrough) {
		if base := target.View().GetBaseURL(); base != "" {
			validated, err := r.validateBaseURL(base)
			if err != nil {
				return "", err
			}
			endpoint = validated + path + "?beta=true"
		}
	} else if target.View().IsCustomBaseURLEnabled() {
		custom := target.View().GetCustomBaseURL()
		if custom == "" {
			return "", fmt.Errorf("custom_base_url is enabled but not configured for provider %d", target.Record.ID)
		}
		validated, err := r.validateBaseURL(custom)
		if err != nil {
			return "", err
		}
		endpoint = strings.TrimRight(validated, "/") + path + "?beta=true"
		if target.Record.ProxyID != nil && target.Record.Proxy != nil {
			if proxy := target.Record.Proxy.URL(); proxy != "" {
				// Escape the attached proxy URL as one query
				// value so its own separators do not become
				// additional relay request parameters.
				endpoint += "&proxy=" + url.QueryEscape(proxy)
			}
		}
	}
	return endpoint, nil
}

// countOptions derives shared Anthropic options and substitutes the counting
// endpoint callback. Output, provider, and attempt state remain borrowed until
// request construction finishes, and passthrough affects the selected URL
// branch.
func (r *Runtime) countOptions(ctx context.Context, output HTTPBoundary, state *AttemptState, target *gatewayadapter.ExecutionProvider, model, tokenType string, mimic, passthrough bool) anthropic.RequestOptions {
	options := r.requestOptions(ctx, output, state, target, model, tokenType, mimic)
	options.URL = func() (string, error) { return r.requestURL(target, true, passthrough) }
	return options
}

// checkBetaTokens returns the block error for resolved tokens under current
// provider policy. Missing settings, empty token input, and failed settings
// reads return nil while construction continues its remaining beta rules.
func (r *Runtime) checkBetaTokens(ctx context.Context, tokens []string, target *gatewayadapter.ExecutionProvider, model string) *anthropic.BetaBlockedError {
	if r.dependencies.Settings == nil || len(tokens) == 0 {
		return nil
	}
	settings, err := r.dependencies.Settings.GetBetaPolicySettings(ctx)
	// An unavailable beta settings read supplies no block result here,
	// letting the upstream builder continue with its platform token
	// preparation rules.
	if err != nil || settings == nil {
		return nil
	}
	return anthropic.CheckBetaPolicyBlockForTokens(gatewayadapter.AnthropicBetaPolicy(settings), tokens, target.View().IsOAuth(), target.View().IsBedrock(), model)
}
