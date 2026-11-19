package openaiforward

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// raw_chat.go forwards Chat Completions requests using the selected provider
// and collects main and auxiliary usage.
// Native Chat request bodies still need mapped models, platform controls, and
// streaming usage flags before exchange. Grok Composer image descriptions can
// add an auxiliary usage amount, while the main response stays in Chat format
// and supplies the observed vendor model and completion evidence.
//
// Grok cache identity is derived before image bridging changes the client
// payload. Health processing classifies Grok failures separately from ordinary
// OpenAI-compatible failures, then raw response readers restore output and
// return observations beside errors. Auxiliary usage joins the result whenever
// a result exists, so failed reading with partial data retains the description
// usage in that returned observation.

// RawChatEndpoint identifies the upstream endpoint in results from all three compatible entry points.
const RawChatEndpoint = "/v1/chat/completions"

// RunRawChat sends a selected provider's native Chat request and returns its
// response observations. A missing model or blank resolved credential stops
// preparation, and successful responses remain owned by the runner through
// reading and error presentation. Streaming clients request usage in the
// outgoing Chat body.
//
// For Grok, cache identity is captured before image bridging, and adopted
// Composer descriptions contribute auxiliary usage. Parameter and fast-tier
// preparation precede the final effort observation. Grok health can return
// generic output or typed provider-loop retry evidence; otherwise the raw
// reader determines the result and error, and auxiliary usage is added whenever
// that result is nonnil.
func RunRawChat(ctx context.Context, body []byte, defaultMappedModel string, p RawChatPorts) (*Result, error) {
	profile := p.Profile()
	startTime := time.Now()

	originalModel := gjson.GetBytes(body, "model").String()
	if originalModel == "" {
		p.Error(http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}
	clientStream := gjson.GetBytes(body, "stream").Bool()

	// Resolve billing and vendor identities before Grok cache preparation so its
	// session seed follows the outgoing model while the caller model remains
	// available for response restoration.
	billingModel := p.BillingModel(originalModel, defaultMappedModel)
	upstreamModel := p.UpstreamModel(billingModel)
	p.ObserveModel(upstreamModel)
	grokCacheIdentity := ""
	if profile.Grok {
		// Derive identity before image bridging or body rewriting so it uses the stable client session prefix.
		grokCacheIdentity = p.GrokCacheIdentity(body, "", upstreamModel)
	}
	upstreamBody := body
	if upstreamModel != originalModel {
		upstreamBody = protocolopenai.ReplaceModelInBody(body, upstreamModel)
	}
	upstreamBody, err := p.PrepareModel(ctx, upstreamBody)
	if err != nil {
		return nil, err
	}

	updatedBody, policyErr := p.FastRaw(ctx, upstreamModel, upstreamBody)
	if policyErr != nil {
		return nil, policyErr
	}
	upstreamBody = updatedBody
	// Capture the tier after fast policy has changed the outgoing bytes; the
	// response reader can separately observe the vendor tier for completion
	// resolution.
	serviceTier := requeststate.ExtractOpenAIServiceTierFromBody(upstreamBody)
	// Remove redundant view_image declarations before Composer description
	// handling so the native Chat payload uses the platform's selected image
	// representation.
	if profile.Grok {
		strippedBody, stripErr := grok.StripRedundantGrokChatViewImageTool(upstreamBody)
		if stripErr != nil {
			return nil, fmt.Errorf("strip redundant Grok Chat view_image tool: %w", stripErr)
		}
		upstreamBody = strippedBody
	}

	// The image bridge uses these credentials to obtain Grok Build descriptions for Composer image input.
	token, tokenKind, err := p.RawCredential(ctx)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("provider %d missing %s credential", profile.ID, tokenKind)
	}

	var bridgeUsage protocolopenai.ForwardUsage
	// Composer description exchange precedes the main request; adopt both repaired
	// bytes and auxiliary usage together so only successful preparation changes
	// main-request accounting.
	if profile.Grok {
		bridgedBody, usage, bridged, bridgeErr := p.BridgeImages(ctx, upstreamBody, token)
		if bridgeErr != nil {
			return nil, bridgeErr
		}
		if bridged {
			upstreamBody = bridgedBody
			protocolopenai.AddForwardUsage(&bridgeUsage, usage)
		}
	}

	// Streaming Chat usage arrives through stream_options, so request it before
	// exchange and reject patch failures before any upstream response is read.
	if clientStream {
		var usageErr error
		upstreamBody, usageErr = protocolopenai.EnsureOpenAIChatStreamUsage(upstreamBody)
		if usageErr != nil {
			return nil, fmt.Errorf("enable stream usage: %w", usageErr)
		}
	}
	// Grok Chat accepts different cache and effort fields from Responses, so
	// remove the Responses key and apply model-specific effort normalization
	// before exchange.
	if profile.Grok {
		upstreamBody, err = grok.StripGrokChatPromptCacheKey(upstreamBody)
		if err != nil {
			return nil, fmt.Errorf("remove Responses-only Grok prompt cache key: %w", err)
		}
		upstreamBody, err = p.GrokEffort(upstreamBody, upstreamModel)
		if err != nil {
			return nil, fmt.Errorf("normalize Grok chat reasoning effort: %w", err)
		}
	}
	// Recognized Ollama Cloud targets receive their compatibility repair after
	// platform controls, letting the final effort observation describe the
	// outgoing native Chat payload.
	upstreamBody = p.OllamaBody(upstreamBody)

	// Record effort after applying platform parameter conversions.
	reasoningEffort := requeststate.ExtractOpenAIReasoningEffortFromBody(upstreamBody)
	reasoningEffort = gatewayprovider.ApplyThinkingEnabledFallback(reasoningEffort, upstreamBody, billingModel)

	p.Debug("openai chat_completions raw: forwarding without protocol conversion",
		zap.Int64("provider_id", profile.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
		zap.Bool("stream", clientStream),
	)

	// Use the shared Chat transport for upstream request construction and sending.
	targetURL, err := p.RawTarget()
	if err != nil {
		return nil, err
	}
	p.Endpoint(RawChatEndpoint)
	customUA := p.UserAgent()
	if customUA == "" && profile.GrokOAuth {
		customUA = grok.DefaultGrokUpstreamUserAgent()
	}
	resp, err := p.SendRaw(ctx, targetURL, upstreamBody, clientStream, token, customUA, grokCacheIdentity)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, upstreamMsg := p.ReadUpstreamError(resp)
		if profile.Grok {
			decision := p.GrokDecision(ctx, resp, respBody, upstreamModel)
			kind := "http_error"
			if decision.Failover {
				kind = "failover"
			}
			p.ObserveGrokError(resp, upstreamMsg, kind)
			if decision.Generic {
				return p.ChatErrorResponse(resp, billingModel)
			}
			// Translate the Grok health choice and response-derived retry limits
			// together into the typed error consumed by the outer provider loop.
			if kind == "failover" {
				return nil, p.GrokFailover(resp, respBody, p.GrokRetry(resp.StatusCode, respBody), decision.RetrySame)
			}
			return p.ChatErrorResponse(resp, billingModel)
		}
		if foErr := p.FailoverHTTP(ctx, resp, respBody, upstreamMsg, upstreamModel); foErr != nil {
			return nil, foErr
		}
		return p.ChatErrorResponse(resp, billingModel)
	}

	if profile.Grok {
		p.UpdateGrokUsage(ctx, upstreamModel, resp.Header, resp.StatusCode)
	}

	output := upstream.NewDeferredOutputContext(p.Sink())
	options := p.RawOptions(resp, billingModel, upstreamModel, serviceTier)
	var observed *openai.CompatResponseResult
	var forwardErr error
	if clientStream {
		observed, forwardErr = openai.ReadRawChatStreaming(output, resp, options, originalModel, upstreamModel, reasoningEffort, startTime, len(body))
	} else {
		observed, forwardErr = openai.ReadRawChatBuffered(output, resp, options, originalModel, upstreamModel, reasoningEffort, startTime)
	}
	result := FromCompatResult(observed, billingModel)
	if result != nil {
		protocolopenai.AddForwardUsage(&result.Usage, bridgeUsage)
		result.UpstreamEndpoint = RawChatEndpoint
	}
	return result, forwardErr
}
