package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_request_targets.go resolves protocol URLs and builds Chat and
// Anthropic upstream requests.
// Protocol targets follow the selected provider's configured base and
// credentials. Raw Chat has a Grok-specific route, native Anthropic uses its
// protocol base, and images choose generation or edit endpoints; each request
// path applies the matching body attributes and outgoing transport policy.
// Chat uses the selected provider's body policy for each outgoing attempt.

// ChatURL builds a validated Chat Completions URL for a non-Grok provider. An
// empty protocol base uses https://api.openai.com. The endpoint helper handles
// versioned bases, and validation failures are returned with base-URL context.
func (s *OpenAIRequests) ChatURL(provider *gatewayprovider.ExecutionProvider) (string, error) {
	baseURL := gatewayprovider.ExecutionProtocolTarget(provider).BaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	validatedURL, err := s.ValidateBaseURL(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	return httpclient.BuildOpenAIEndpointURL(validatedURL, "/v1/chat/completions"), nil
}

// ChatFallbackTarget resolves credentials and a URL for the Chat fallback
// execution. Grok uses execution credentials and its CLI-compatible route.
// Other providers need a nonblank protocol API key and pass their base URL
// through ChatURL validation.
func (s *OpenAIRequests) ChatFallbackTarget(ctx context.Context, provider *gatewayprovider.ExecutionProvider) (apiKey string, targetURL string, err error) {
	if provider.View().IsGrok() {
		// Grok fallback can use OAuth execution credentials; a protocol
		// API-key lookup would omit that credential path and select the
		// wrong request target.
		apiKey, _, err = s.Credentials.Resolve(ctx, gatewayprovider.ExecutionRecord(provider))
		if err != nil {
			return "", "", err
		}
		targetURL, err = s.GrokRoutes.Chat(provider, true)
		return apiKey, targetURL, err
	}
	apiKey = strings.TrimSpace(provider.View().GetOpenAIProtocolAPIKey())
	if apiKey == "" {
		return "", "", fmt.Errorf("provider %d missing api_key", provider.Record.ID)
	}
	targetURL, err = s.ChatURL(provider)
	if err != nil {
		return "", "", err
	}
	return apiKey, targetURL, nil
}

// SendChat prepares and sends Chat input using the selected provider's
// transport. It applies model attributes and then the provider's Chat body
// policy. Preparation errors return before sending. The upstream sender
// detaches its request context through DetachUpstreamContext, and callers read
// and close any returned response body.
//
// Transport preparation resolves TLS routing when a match was not supplied,
// applies platform user-agent and Grok cache headers, then finalizes provider
// overrides and the trusted OpenCode session. Exchange failures use the shared
// HTTP failure adapter.
func (s *OpenAIRequests) SendChat(
	ctx context.Context,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	targetURL string,
	body []byte,
	stream bool,
	bearerToken string,
	userAgent string,
	grokCacheIdentity string,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*http.Response, error) {
	var err error
	body, err = s.applyModelAttributes(ctx, provider, body, "openai_chat_completions")
	if err != nil {
		return nil, err
	}
	body, err = provider.View().PrepareChatRequestBody(body)
	if err != nil {
		return nil, err
	}
	return openai.SendChatRequest(ctx, body, openai.CCRequestOptions{
		URL: targetURL, Token: bearerToken, Stream: stream, Headers: c.Request.Header,
		RequestContext:  gatewayprovider.DetachUpstreamContext,
		ObserveEndpoint: func() { SetActualOpenAIUpstreamEndpoint(c, "/v1/chat/completions") },
		AllowHeader:     func(name string) bool { return openaiCCRawAllowedHeaders[name] },
		PrepareTransport: func(upstreamReq *http.Request) {
			// Direct Chat callers may omit a router match. Resolve
			// it before applying the user-agent policy and choosing
			// the profile for this exchange.
			if len(tlsRouterMatch) == 0 {
				tlsRouterMatch = []egress.TLSFingerprintRouterMatchResult{s.MatchTLS(c, provider)}
			}
			if provider.Record.Platform == capability.PlatformGrok && userAgent != "" {
				upstreamReq.Header.Set("user-agent", userAgent)
			} else if provider.Record.Platform != capability.PlatformGrok {
				s.ApplyUserAgent(c.Request.Context(), c, provider, upstreamReq, false, tlsRouterMatch[0])
			}

			// Grok OAuth requires its CLI request headers, and its
			// cache identity follows the prepared Chat conversation
			// independently of generic OpenAI headers.
			if provider.Record.Platform == capability.PlatformGrok {
				if provider.View().IsGrokOAuth() {
					grok.ApplyCLIHeaders(upstreamReq.Header)
				}
				grok.ApplyGrokCacheHeaders(upstreamReq.Header, grokCacheIdentity)
			}
		},
		FinalizeHeaders: func(headers http.Header) {
			provideradapter.ApplyProviderHeaderOverrides(gatewayprovider.ExecutionProtocolRecord(provider), headers)
			gatewayprovider.FinalizeRequestHeaders(provider, targetURL, clientHeaderReader(c), headers)
		},
		Do: func(req *http.Request) (*http.Response, error) {
			proxyURL := ""
			if provider.Record.Proxy != nil {
				proxyURL = provider.Record.Proxy.URL()
			}
			return s.Transport.DoWithTLS(req, proxyURL, provider.Record.ID, provider.Record.Concurrency, s.TLSProfile(provider, tlsRouterMatch...))
		},
		TransportError: func(err error) error { return s.Failure.Handle(ctx, c, provider, err, false) },
	})
}

// AnthropicURL builds the selected provider's validated native Anthropic
// Messages target. It delegates protocol-base and default-target handling to
// NativeAnthropicTargetURL; configuration errors reach the caller before
// request construction.
func (s *OpenAIRequests) AnthropicURL(provider *gatewayprovider.ExecutionProvider) (string, error) {
	return forward.NativeAnthropicTargetURL(provider.Record.ID, (providercore.ProtocolTarget{Record: provider.View(), Protocol: capability.ProtocolAnthropicMessages}).BaseURL(), s.ValidateBaseURL)
}

// BuildAnthropic constructs native Messages input with provider authentication
// and header policy. It applies model attributes before forwarding the inbound
// headers, if present. The returned body reflects beta sanitization used by the
// constructed request; callers retain both for the exchange and response
// processing.
func (s *OpenAIRequests) BuildAnthropic(ctx context.Context, c *gin.Context, provider *gatewayprovider.ExecutionProvider, body []byte, apiKey, targetURL string) (*http.Request, []byte, error) {
	var err error
	body, err = s.applyModelAttributes(ctx, provider, body, "anthropic_messages")
	if err != nil {
		return nil, nil, err
	}
	var headers http.Header
	if c != nil && c.Request != nil {
		headers = c.Request.Header
	}
	req, prepared, err := forward.BuildNativeAnthropicRequest(ctx, body, apiKey, targetURL, forward.NativeAnthropicRequestOptions{
		Headers: headers, GetHeader: anthropic.GetHeaderRaw, OverrideValue: gatewayprovider.BindExecutionHeaderValue(provider),
		Sanitize: anthropic.SanitizeAnthropicBodyForBetaTokens, AllowedHeader: func(key string) bool { return anthropic.AllowedHeaders[key] },
		WireCasing: anthropic.ResolveWireCasing, AddHeader: anthropic.AddHeaderRaw, SetHeader: anthropic.SetHeaderRaw,
		AuthHeader: func(h http.Header, key string) {
			anthropic.SetAPIKeyAuthHeader(h, gatewayprovider.ExecutionProtocolRecord(provider).GetAnthropicAPIKeyAuthScheme() == providercore.AnthropicAPIKeyAuthSchemeAuthorizationBearer, key)
		}, ApplyOverrides: gatewayprovider.BindExecutionHeaders(provider),
	})
	if err != nil {
		return nil, nil, err
	}
	gatewayprovider.FinalizeRequestHeaders(provider, targetURL, clientHeaderReader(c), req.Header)
	return req, prepared, nil
}

// RawChatURL resolves the selected provider's native Chat endpoint before
// execution. Grok delegates to its CLI-compatible route and wraps target errors
// with Grok context. Other providers use ChatURL's protocol base and URL
// validation.
func (s *OpenAIRequests) RawChatURL(provider *gatewayprovider.ExecutionProvider) (string, error) {
	if provider.Record.Platform == capability.PlatformGrok {
		targetURL, err := s.GrokRoutes.Chat(provider, true)
		if err != nil {
			return "", fmt.Errorf("invalid grok base_url: %w", err)
		}
		return targetURL, nil
	}

	return s.ChatURL(provider)
}

// ImagesURL selects an image-generation or image-edit target for the provider.
// Without a configured base, only the edits endpoint changes the generation
// default. A configured base is validated and combined with the caller's
// endpoint string.
func (s *OpenAIRequests) ImagesURL(provider *gatewayprovider.ExecutionProvider, endpoint string) (string, error) {
	targetURL := openAIImagesGenerationsURL
	if endpoint == upstream.OpenAIImagesEditsEndpoint {
		targetURL = openAIImagesEditsURL
	}
	baseURL := gatewayprovider.ExecutionProtocolTarget(provider).BaseURL()
	if baseURL != "" {
		validatedURL, err := s.ValidateBaseURL(baseURL)
		if err != nil {
			return "", err
		}
		targetURL = httpclient.BuildOpenAIEndpointURL(validatedURL, endpoint)
	}

	return targetURL, nil
}
