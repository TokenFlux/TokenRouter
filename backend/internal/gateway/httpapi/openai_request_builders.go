package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_request_builders.go builds Responses requests with selected targets,
// model attributes and identity callbacks. Responses builders resolve the
// selected provider's protocol target before adding credentials and caller
// metadata. Ordinary and passthrough requests share dynamic identity, session,
// and header callbacks; the upstream builders apply them in order, while this
// adapter applies model attributes and native Kimi, Zhipu, and DeepSeek
// normalization.

// Target supplies deferred Responses URL selection for the current execution
// provider. OAuth and eligible setup-token providers choose Codex; API-key
// providers use a validated protocol base. Passthrough affects setup-token
// eligibility, and every resolved target receives the validated inbound
// Responses path suffix.
func (s *OpenAIRequests) Target(c *gin.Context, a *gatewayprovider.ExecutionProvider, passthrough bool) forward.RequestTargetOptions {
	oauth := a.Record.Type == capability.ProviderTypeOAuth || a.Record.Type == capability.ProviderTypeSetupToken && (!passthrough || a.View().IsOpenAIOAuthLike())
	return forward.RequestTargetOptions{
		OAuthTarget: oauth, APIKey: a.Record.Type == capability.ProviderTypeAPIKey, DefaultURL: OpenaiPlatformAPIURL, CodexURL: ChatgptCodexURL,
		BaseURL: func() string {
			target := gatewayprovider.ExecutionProtocolTarget(a)
			// Legacy adaptive Kimi, Zhipu, and DeepSeek calls can reach the
			// Responses builder before an operation protocol is attached.
			// Preserve its Responses slot selection while BaseURL owns
			// platform-specific address resolution.
			if target.UsesStatelessResponses() {
				target.Protocol = capability.ProtocolOpenAIResponses
			}
			return target.BaseURL()
		}, Validate: s.ValidateBaseURL, FromBase: func(base string) string { return forward.ResponsesEndpoint(a.Record.Platform, base) }, AppendSuffix: func(base string) string {
			return openai.AppendResponsesPathSuffix(base, OpenAIResponsesRequestPathSuffix(c))
		},
	}
}

// ResponseOptions binds synchronous header-building callbacks to this HTTP
// attempt. Callers keep c, provider, and request services available until the
// builder finishes. Identity and fingerprint callbacks read the staged attempt
// state; provider overrides, OpenCode sessions, beta capabilities, and routing
// diagnostics use their final inputs.
func (s *OpenAIRequests) ResponseOptions(ctx context.Context, c *gin.Context, provider *gatewayprovider.ExecutionProvider, token, targetURL string, isCodexCLI bool, routerMatch ...egress.TLSFingerprintRouterMatchResult) openai.ResponsesRequestOptions {
	return openai.ResponsesRequestOptions{
		URL: targetURL, ForwardHeaders: func() http.Header { return c.Request.Header },
		Authenticate: func(ctx context.Context) (http.Header, error) {
			return s.Identity.Headers(ctx, provider, token)
		},
		ProviderHeaders: func(ctx context.Context, headers http.Header) error {
			return gatewayprovider.CredentialChatGPTHeaders(ctx, s.Providers, headers, provider)
		},
		UsesCodex:      provider.View().UsesOpenAICodexProtocol,
		IsCompact:      func() bool { return IsOpenAIResponsesCompactPath(c) },
		ForceCodexCLI:  func() bool { return s.Options.ForceCLI },
		AllowHeader:    func(name string) bool { return openaiAllowedHeaders[name] },
		GuardTurnState: func(headers http.Header) { s.Turns.Guard(c, provider, headers) },
		MessagesBridge: func(body []byte) bool {
			return IsOpenAICompatMessagesBridgeContext(c) || gatewayprovider.IsOpenAICompatMessagesBridgeBody(body)
		},
		Originator:     func() string { return ResolveOpenAIUpstreamOriginator(c, isCodexCLI, routerMatch...) },
		CompactSession: func() string { return ResolveOpenAICompactSessionID(c) },
		APIKeyID:       func() int64 { return APIKeyIDFromContext(c) },
		IsolateSession: func(keyID int64, raw string) string {
			return openai.IsolateOpenAIUpstreamSessionID(keyID, provideradapter.CodexIdentityNamespace(CodexIdentityRecord(c, provider.View())), raw)
		},
		ApplyUserAgent: func(req *http.Request) { s.ApplyUserAgent(ctx, c, provider, req, false, routerMatch...) },
		ApplyProviderIdentity: func(headers http.Header) {
			openai.ApplyCodexProviderIdentityHeaders(headers, provideradapter.CodexIdentityNamespace(CodexIdentityRecord(c, provider.View())), APIKeyIDFromContext(c))
		},
		ApplyFingerprint: func(headers http.Header) { ApplyStagedCodexFingerprintHeaders(c, provider.View(), headers) },
		OverrideHeaders:  gatewayprovider.BindExecutionHeaders(provider),
		FinalizeProviderHeaders: func(headers http.Header) {
			gatewayprovider.FinalizeRequestHeaders(provider, targetURL, clientHeaderReader(c), headers)
		},
		BetaFeatures: func(headers http.Header) {
			ApplyOpenAICodexBetaFeatures(c, provider != nil && provider.View().IsOpenAIOAuthLike(), headers)
		},
		RoutingHint: func(headers http.Header, body []byte) { SetOpenAICodexRoutingHintFromBody(headers, provider, body) },
		Diagnostics: func(headers http.Header, body []byte) {
			LogOpenAIRoutingDiagnosticsFromBody(ctx, provider, "http", headers, body, "not_applicable")
		},
	}
}

// Build constructs an ordinary Responses request for the selected provider.
// Model attribute preparation may fail before target construction. The
// delegated builder records the selected endpoint, normalizes Kimi and DeepSeek
// input, and applies request-specific headers using c and the supplied
// execution context.
func (s *OpenAIRequests) Build(ctx context.Context, c *gin.Context, provider *gatewayprovider.ExecutionProvider, body []byte, token string, isStream bool, promptCacheKey string, isCodexCLI bool, routerMatch ...egress.TLSFingerprintRouterMatchResult) (*http.Request, error) {
	var err error
	body, err = s.applyModelAttributes(ctx, provider, body, "openai_responses")
	if err != nil {
		return nil, err
	}
	return forward.BuildResponsesRequest(ctx, body, promptCacheKey, s.Target(c, provider, false), func(path string) { SetActualOpenAIUpstreamEndpoint(c, path) }, func(b []byte) []byte {
		return forward.NormalizeStatelessResponsesBody(provider != nil && gatewayprovider.ExecutionProtocolTarget(provider).UsesStatelessResponses(), b)
	}, func(target string) openai.ResponsesRequestOptions {
		return s.ResponseOptions(ctx, c, provider, token, target, isCodexCLI, routerMatch...)
	})
}

// BuildPassthrough constructs a Responses request with the passthrough header
// policy. It applies model attributes and Kimi and DeepSeek normalization
// before the upstream builder. Absent inbound request headers become nil;
// timeout forwarding uses startup permission, and user-agent selection receives
// passthrough mode for the current provider.
func (s *OpenAIRequests) BuildPassthrough(
	ctx context.Context,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	body []byte,
	token string,
	routerMatch ...egress.TLSFingerprintRouterMatchResult,
) (*http.Request, error) {
	var err error
	body, err = s.applyModelAttributes(ctx, provider, body, "openai_responses")
	if err != nil {
		return nil, err
	}
	return forward.BuildPassthroughRequest(ctx, body, s.Target(c, provider, true), func(b []byte) []byte {
		return forward.NormalizeStatelessResponsesBody(provider != nil && gatewayprovider.ExecutionProtocolTarget(provider).UsesStatelessResponses(), b)
	}, func(target string) openai.PassthroughRequestOptions {
		options := s.ResponseOptions(ctx, c, provider, token, target, false, routerMatch...)
		// Passthrough can be built without inbound headers; a nil
		// source leaves authentication and required upstream headers to
		// the request builder callbacks.
		options.ForwardHeaders = func() http.Header {
			if c == nil || c.Request == nil {
				return nil
			}
			return c.Request.Header
		}
		options.ApplyUserAgent = func(req *http.Request) { s.ApplyUserAgent(ctx, c, provider, req, true, routerMatch...) }
		options.Diagnostics = func(headers http.Header, body []byte) {
			LogOpenAIRoutingDiagnosticsFromBody(ctx, provider, "http_passthrough", headers, body, "not_applicable")
		}
		return openai.PassthroughRequestOptions{
			ResponsesRequestOptions: options,
			AllowTimeoutHeaders:     s.AllowTimeoutHeaders,
			AllowPassthroughHeader:  isOpenAIPassthroughAllowedRequestHeader,
			MatchedOriginator: func() string {
				if len(routerMatch) > 0 && routerMatch[0].Matched {
					return strings.TrimSpace(routerMatch[0].UpstreamOriginator)
				}
				return ""
			},
		}
	})
}

// isOpenAIPassthroughAllowedRequestHeader reports whether a lowercase header
// may pass through. Recognized timeout headers follow allowTimeoutHeaders
// independently of the general allowlist. Empty names are rejected; callers
// normalize header names before checking.
func isOpenAIPassthroughAllowedRequestHeader(lowerKey string, allowTimeoutHeaders bool) bool {
	if lowerKey == "" {
		return false
	}
	if isOpenAIPassthroughTimeoutHeader(lowerKey) {
		return allowTimeoutHeaders
	}
	return openaiPassthroughAllowedHeaders[lowerKey]
}

// isOpenAIPassthroughTimeoutHeader recognizes the supported lowercase client
// timeout header names.
func isOpenAIPassthroughTimeoutHeader(lowerKey string) bool {
	switch lowerKey {
	case "x-stainless-timeout", "x-stainless-read-timeout", "x-stainless-connect-timeout", "x-request-timeout", "request-timeout", "grpc-timeout":
		return true
	default:
		return false
	}
}

// BuildImageResponses prepares a worker-owned OAuth request with no inbound headers.
// Creative execution supplies the hosted image body and resolved token; the
// synthetic HTTP context retains only request construction state, never a client
// writer or client cookies. The standard builder still applies provider identity.
func (s *OpenAIRequests) BuildImageResponses(ctx context.Context, provider *gatewayprovider.ExecutionProvider, body []byte, token string) (*http.Request, error) {
	inbound, err := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/responses", nil)
	if err != nil {
		return nil, err
	}
	c := &gin.Context{Request: inbound}
	ctx = openai.WithOpenAIImagesSelfBuiltRequest(ctx)
	req, err := s.Build(ctx, c, provider, body, token, true, "", false)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	return req, nil
}
