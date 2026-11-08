package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	protocolforward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// grok_chat_execution.go executes eligible Grok Chat requests through
// Responses transport and converts output for Chat clients.
// A Grok Chat request can use Responses transport while returning Chat JSON or
// SSE to its client. The bridge selects that path from the provider's resolved
// model, cache identity and image input, returning handled=false when native
// Chat should perform this attempt instead.
//
// Client tools and service-tier controls are converted before credential
// acquisition. Tool intent is retained before capability filtering so cache
// routing can distinguish a deliberate client-tool request from a tool-free
// request. Upstream streaming remains enabled in either client mode; the
// output reader buffers for JSON clients or streams Chat chunks, retaining the
// original client model separately from billing and upstream identities.

// ChatResponses executes an eligible Chat request through Grok Responses and
// returns whether it handled the attempt. Malformed requests and preparation
// failures return handled=true; only failed bridge eligibility returns false
// so the caller can use native Chat with the same selected provider.
//
// The bridge derives billing, upstream and cache identities before converting
// client tools and applying tier policy. It forces upstream streaming while
// choosing Chat streaming or buffering from the client flag. One platform
// exchange owns response-body cleanup, health classification precedes final
// error presentation, and the returned result receives endpoint, headers and
// original reasoning controls when available.
func (s *GrokExecutor) ChatResponses(
	ctx context.Context,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	body []byte,
	promptCacheKey string,
	defaultMappedModel string,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*protocolforward.OpenAIResult, bool, error) {
	startTime := time.Now()

	var chatReq protocolopenai.ChatCompletionsRequest
	if err := json.Unmarshal(body, &chatReq); err != nil {
		return nil, true, fmt.Errorf("parse grok chat completions request: %w", err)
	}
	originalModel := chatReq.Model
	clientStream := chatReq.Stream
	billingModel := gatewayprovider.ExecutionModelPolicy(provider).ForwardModel(originalModel, defaultMappedModel)
	upstreamModel := gatewayprovider.ExecutionModelPolicy(provider).NormalizeOpenAI(billingModel)
	attributes, attributesErr := gatewayprovider.ResolveModelAttributes(ctx, provider, upstreamModel, s.Attributes, "openai_responses")
	if attributesErr != nil {
		return nil, true, attributesErr
	}
	codec := gatewayprovider.GrokBodyCodec()
	codec.Attributes = func(string) modelcatalog.Attributes { return attributes.Attributes }
	cacheIdentity := ResolveGrokCacheIdentity(c, body, promptCacheKey, upstreamModel)
	// Eligible image input can select Responses even without cache identity;
	// the Chat-to-Responses converter retains those image parts in the upstream input.
	hasImageInput := protocolopenai.JSONValueMayContainImageInput(gjson.GetBytes(body, "messages"))
	if provider.Route.Protocol() == "" && !gatewayprovider.GrokBodyCodec().GrokChatResponsesRuntimeEligible(upstreamModel, cacheIdentity) && (!hasImageInput || !gatewayprovider.GrokBodyCodec().GrokChatResponsesBridgeModel(upstreamModel)) {
		return nil, false, nil
	}

	responsesReq, err := protocolbridge.ChatCompletionsToResponses(&chatReq, gatewayprovider.ModelConversionOptions(attributes.ForRequest(gatewayprovider.ModelRequestState(body))))
	if err != nil {
		return nil, true, fmt.Errorf("convert grok chat completions to responses: %w", err)
	}
	responsesReq.Model = upstreamModel
	responsesReq.Stream = true
	// Normalize service_tier aliases with the same rules as native Responses and
	// remove unknown values before sending the request to xAI.
	responsesReq.ServiceTier = protocolopenai.ServiceTierValue(responsesReq.ServiceTier)
	// Grok Chat conversion sends its Responses stream directly; remove Include
	// and Store controls here because this path does not use Codex response persistence.
	responsesReq.Include = nil
	responsesReq.Store = nil

	responsesBody, err := json.Marshal(responsesReq)
	if err != nil {
		return nil, true, fmt.Errorf("marshal grok responses bridge request: %w", err)
	}
	// Save client-tool intent before capability filtering removes unsupported
	// tools, so cache-route selection can distinguish those requests from tool-free input.
	intentBody, err := gatewayprovider.GrokBodyCodec().GrokChatResponsesCacheIntentBody(responsesBody)
	if err != nil {
		return nil, true, fmt.Errorf("normalize grok responses bridge cache intent: %w", err)
	}
	responsesBody, err = codec.PatchGrokResponsesBody(responsesBody, upstreamModel)
	if err != nil {
		return nil, true, fmt.Errorf("patch grok responses bridge request: %w", err)
	}
	responsesBody, err = grok.ApplyGrokResponsesCacheIdentity(responsesBody, intentBody, cacheIdentity, true)
	if err != nil {
		return nil, true, fmt.Errorf("apply grok responses bridge cache identity: %w", err)
	}
	responsesBody, err = ApplyGrokFreeRequestToolCacheRoute(c, responsesBody, intentBody, provider, cacheIdentity)
	if err != nil {
		return nil, true, fmt.Errorf("apply grok responses bridge function-tool cache route: %w", err)
	}

	updatedBody, policyErr := tierpolicy.ApplyBody(responsesBody, s.FastPolicy.Input(ctx, provider, upstreamModel))
	if policyErr != nil {
		if blocked, ok := errors.AsType[*tierpolicy.BlockedError](policyErr); ok {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			WriteForwardChatError(c, http.StatusForbidden, "permission_error", blocked.Message)
		}
		return nil, true, policyErr
	}
	responsesBody = updatedBody

	token, _, err := s.Credentials.Resolve(ctx, RequestCredentialBudget(c), CredentialObserver{Context: c}, provider)
	if err != nil {
		return nil, true, fmt.Errorf("get grok access token: %w", err)
	}
	// Keep request values and server-abort state while detaching client deadlines;
	// the response reader separately chooses streaming or buffered client delivery.
	upstreamCtx, releaseUpstreamCtx := gatewayprovider.DetachUpstreamContext(ctx)
	upstreamReq, err := s.BuildResponsesRequest(upstreamCtx, c, provider, responsesBody, token, cacheIdentity, true)
	releaseUpstreamCtx()
	if err != nil {
		return nil, true, fmt.Errorf("build grok responses bridge request: %w", err)
	}
	SetActualOpenAIUpstreamEndpoint(c, grok.GrokChatResponsesEndpoint)

	proxyURL := ""
	if provider.Record.ProxyID != nil && provider.Record.Proxy != nil {
		proxyURL = provider.Record.Proxy.URL()
	}

	var result *protocolforward.OpenAIResult
	var handleErr error
	target := &grok.ResponsesTarget{
		ProviderID:    provider.Record.ID,
		Model:         upstreamModel,
		Enter:         s.Enter,
		PassRawStream: true,
		Exchange: grok.ResponsesExchange{
			SingleExchange: true,
			Build:          func([]byte) (*http.Request, error) { return upstreamReq, nil },
			Do: func(req *http.Request) (*http.Response, error) {
				return s.Transport.DoWithTLS(req, proxyURL, provider.Record.ID, provider.Record.Concurrency, s.TLSProfile(provider, tlsRouterMatch...))
			},
			ReadError: s.Output.ReadErrorBody,
			AfterExchange: func(err error) error {
				if err != nil {
					return s.Failure.Handle(ctx, c, provider, err, false)
				}
				return nil
			},
		},
		BeforeResponse: func(resp *http.Response, _ []byte) (bool, error) {
			if resp.StatusCode >= http.StatusBadRequest {
				respBody, upstreamMsg := s.Output.ReadReplayableError(resp)
				if upstreamMsg == "" {
					upstreamMsg = fmt.Sprintf("xAI upstream returned status %d", resp.StatusCode)
				}
				// Apply provider health before client presentation so custom output rules
				// cannot suppress rate-limit observation or alter the provider retry decision.
				decision := gatewayprovider.ApplyGrokExecutionHealth(ctx, s.Health, provider, resp.StatusCode, resp.Header, respBody, "", upstreamModel)
				kind := "http_error"
				if decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode, gatewayprovider.ShouldFailoverGrokResponse(resp.StatusCode, respBody)) {
					kind = "failover"
				}
				AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
					Platform:           provider.Record.Platform,
					ProviderID:         provider.Record.ID,
					ProviderName:       provider.Record.Name,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  requeststate.FirstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")),
					Kind:               kind,
					Message:            upstreamMsg,
				})
				if decision.ShouldReturnGenericError() {
					result, handleErr = s.Output.CompatError(resp, c, provider, WriteForwardChatError, WriteForwardChatErrorBody, billingModel)
					return true, handleErr
				}
				if kind == "failover" {
					retryable, retryDelay, retryDeadline, retryMax := gatewayprovider.GrokSameProviderRetryMetadata(provider, resp.StatusCode, respBody)
					return true, &protocolforward.UpstreamFailoverError{
						StatusCode:                resp.StatusCode,
						ResponseBody:              respBody,
						ResponseHeaders:           resp.Header.Clone(),
						RetryableOnSameProvider:   retryable || decision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode),
						RequestScopedTransient:    retryable && resp.StatusCode == http.StatusTooManyRequests,
						SameProviderRetryDelay:    retryDelay,
						SameProviderRetryDeadline: retryDeadline,
						SameProviderRetryMax:      retryMax,
					}
				}
				result, handleErr = s.Output.CompatError(resp, c, provider, WriteForwardChatError, WriteForwardChatErrorBody, billingModel)
				return true, handleErr
			}

			s.Health.ObserveResponse(ctx, provider.View(), resp.Header, resp.StatusCode, upstreamModel)
			return false, nil
		},
		ReadResponse: func(resp *http.Response, _ upstream.AttemptInput, _ upstream.OutputSink) (upstream.ResponsesObservation, error) {
			// The upstream is SSE in both modes; select the downstream converter from
			// client intent so a JSON caller receives one complete Chat response.
			if clientStream {
				result, err = s.Output.ChatStreaming(resp, c, provider, originalModel, billingModel, upstreamModel, startTime, len(body))
			} else {
				result, err = s.Output.ChatBuffered(resp, c, provider, originalModel, billingModel, upstreamModel, startTime)
			}
			if result == nil {
				return upstream.ResponsesObservation{}, err
			}
			return upstream.ResponsesObservation{
				Usage:            &result.Usage,
				HasUsage:         protocolopenai.OpenAIUsageHasTokens(&result.Usage),
				ResponseID:       result.ResponseID,
				SearchCount:      result.SearchCount,
				ImageCount:       result.ImageCount,
				ImageFrameCount:  result.ImageFrameCount,
				ImageOutputSizes: result.ImageOutputSizes,
				HTTPCommitted:    c.Writer.Written(),
				RetryCommitted:   IsResponseCommitted(c),
			}, err
		},
	}
	var sink upstream.OutputSink
	if c != nil {
		sink = NewResponseSink(c)
	}
	nativeResult, err := (grok.ResponsesExecutor{}).Execute(upstreamCtx, upstream.AttemptInput{
		Protocol:      protocol.ProtocolOpenAIResponses,
		Body:          responsesBody,
		ResponseModel: originalModel,
		Stream:        clientStream,
		Target:        target,
	}, sink)

	if result != nil {
		result.UpstreamEndpoint = grok.GrokChatResponsesEndpoint
		result.ResponseHeaders = nativeResult.UpstreamHeaders.Clone()
		if result.RequestID == "" {
			result.RequestID = requeststate.FirstNonEmpty(nativeResult.UpstreamHeaders.Get("x-request-id"), nativeResult.UpstreamHeaders.Get("xai-request-id"))
		}
		result.ReasoningEffort = requeststate.ExtractOpenAIReasoningEffortFromBody(body)
	}
	return result, true, err
}
