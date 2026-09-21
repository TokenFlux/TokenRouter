package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	protocolforward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	tierpolicy "github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	uuid "github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func grokChatResponsesBridgeEligibility(body []byte) (bool, string) {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		GrokChatResponsesBridgeEligibility(body)
}

func grokChatResponsesCacheIntentBody(body []byte) ([]byte, error) {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		GrokChatResponsesCacheIntentBody(body)
}

func grokChatResponsesBridgeModel(model string) bool {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		GrokChatResponsesBridgeModel(model)
}

func grokChatResponsesRuntimeEligible(upstreamModel, cacheIdentity string) bool {
	return (grok.BodyCodec{
		NewID: uuid.NewString}).
		GrokChatResponsesRuntimeEligible(upstreamModel, cacheIdentity)
}

// forwardGrokChatCompletionsViaResponses 将严格兼容的 Chat 请求转换为 xAI
// Responses 格式，并复用既有的 Responses-to-Chat 响应转换器。Grok CLI 使用独立的
// 上游协议，因此这里不会执行 Codex OAuth 转换。
func (s *OpenAIGatewayService) forwardGrokChatCompletionsViaResponses(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	promptCacheKey string,
	defaultMappedModel string,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*protocolforward.OpenAIResult, error) {
	startTime := time.Now()

	var chatReq protocolopenai.ChatCompletionsRequest
	if err := json.Unmarshal(body, &chatReq); err != nil {
		return nil, fmt.Errorf("parse grok chat completions request: %w", err)
	}
	originalModel := chatReq.Model
	clientStream := chatReq.Stream
	billingModel := resolveOpenAIForwardModel(account, originalModel, defaultMappedModel)
	upstreamModel := normalizeOpenAIModelForUpstream(account, billingModel)
	cacheIdentity := resolveGrokCacheIdentity(c, body, promptCacheKey, upstreamModel)
	// 图片输入必须通过 Responses 桥接：原始 Chat Completions 路径无法把 image_url
	// 转发给非 Composer 模型的 Grok 原生视觉能力，否则图片会被静默丢弃；
	// 因此即使没有 prompt-cache 身份也要路由到 Responses。
	hasImageInput := protocolopenai.JSONValueMayContainImageInput(gjson.GetBytes(body, "messages"))
	if account.attemptRoute.Protocol() == "" && !grokChatResponsesRuntimeEligible(upstreamModel, cacheIdentity) && (!hasImageInput || !grokChatResponsesBridgeModel(upstreamModel)) {
		return s.forwardAsRawChatCompletions(ctx, c, account, body, defaultMappedModel, tlsRouterMatch...)
	}

	responsesReq, err := protocolbridge.ChatCompletionsToResponses(&chatReq, protocolforward.ConversionOptionsForModel(chatReq.Model))
	if err != nil {
		return nil, fmt.Errorf("convert grok chat completions to responses: %w", err)
	}
	responsesReq.Model = upstreamModel
	responsesReq.Stream = true
	// 让 Chat 与原生 Responses 对 OpenAI 兼容的 service_tier 别名保持一致；共享
	// 规范化器会丢弃未知值，避免其到达 xAI。
	normalizeResponsesRequestServiceTier(responsesReq)
	// 这些字段对 Codex 有用，但 Grok CLI 协议不需要；桥接请求应尽量贴近原生 Grok。
	responsesReq.Include = nil
	responsesReq.Store = nil

	responsesBody, err := json.Marshal(responsesReq)
	if err != nil {
		return nil, fmt.Errorf("marshal grok responses bridge request: %w", err)
	}
	// 在 Grok 能力清理前保留转换后的 Responses 意图；缓存路由必须看到真实客户端函数工具，
	// 而不是嵌套的 Chat Completions 声明或无工具副本。
	intentBody, err := grokChatResponsesCacheIntentBody(responsesBody)
	if err != nil {
		return nil, fmt.Errorf("normalize grok responses bridge cache intent: %w", err)
	}
	responsesBody, err = patchGrokResponsesBody(responsesBody, upstreamModel)
	if err != nil {
		return nil, fmt.Errorf("patch grok responses bridge request: %w", err)
	}
	responsesBody, err = grok.ApplyGrokResponsesCacheIdentity(responsesBody, intentBody, cacheIdentity, true)
	if err != nil {
		return nil, fmt.Errorf("apply grok responses bridge cache identity: %w", err)
	}
	responsesBody, err = applyGrokFreeRequestToolCacheRoute(c, responsesBody, intentBody, account, cacheIdentity)
	if err != nil {
		return nil, fmt.Errorf("apply grok responses bridge function-tool cache route: %w", err)
	}

	updatedBody, policyErr := s.applyOpenAIFastPolicyToBody(ctx, account, upstreamModel, responsesBody)
	if policyErr != nil {
		var blocked *tierpolicy.BlockedError
		if errors.As(policyErr, &blocked) {
			gatewayhttp.MarkOpsClientBusinessLimited(c, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied)
			writeChatCompletionsError(c, http.StatusForbidden, "permission_error", blocked.Message)
		}
		return nil, policyErr
	}
	responsesBody = updatedBody

	token, _, err := s.getRequestCredential(ctx, c, account)
	if err != nil {
		return nil, fmt.Errorf("get grok access token: %w", err)
	}
	upstreamCtx, releaseUpstreamCtx := detachUpstreamContext(ctx)
	upstreamReq, err := buildGrokResponsesRequest(upstreamCtx, c, account, responsesBody, token, cacheIdentity, s.cfg, s.settingService)
	releaseUpstreamCtx()
	if err != nil {
		return nil, fmt.Errorf("build grok responses bridge request: %w", err)
	}
	gatewayhttp.SetActualOpenAIUpstreamEndpoint(c, grok.GrokChatResponsesEndpoint)

	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	var result *protocolforward.OpenAIResult
	var handleErr error
	target := &grok.ResponsesTarget{

		AccountID: account.ID,

		Model: upstreamModel,

		Enter: s.nativeAttemptActivity,

		PassRawStream: true,

		Exchange: grok.ResponsesExchange{

			SingleExchange: true,

			Build: func([]byte) (*http.Request, error) { return upstreamReq, nil },

			Do: func(req *http.Request) (*http.Response, error) {
				return s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.resolveOpenAITLSProfile(account, tlsRouterMatch...))
			},

			ReadError: s.readUpstreamErrorBody,

			AfterExchange: func(err error) error {
				if err != nil {
					return s.handleOpenAIUpstreamTransportError(ctx, c, account, err, false)
				}
				return nil
			},
		},

		BeforeResponse: func(resp *http.Response, _ []byte) (bool, error) {

			if resp.StatusCode >= http.StatusBadRequest {
				respBody, upstreamMsg := s.readOpenAIUpstreamError(resp)
				if upstreamMsg == "" {
					upstreamMsg = fmt.Sprintf("xAI upstream returned status %d", resp.StatusCode)
				}
				decision := s.applyGrokAccountUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, upstreamModel)
				kind := "http_error"
				if decision.ShouldFailover(account, resp.StatusCode, s.shouldFailoverGrokUpstreamError(resp.StatusCode, respBody)) {
					kind = "failover"
				}
				gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{

					Platform: account.Platform,

					AccountID: account.ID,

					AccountName: account.Name,

					UpstreamStatusCode: resp.StatusCode,

					UpstreamRequestID: firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")),

					Kind: kind,

					Message: upstreamMsg,
				})
				if decision.ShouldReturnGenericError() {
					result, handleErr = s.handleChatCompletionsErrorResponse(resp, c, account, billingModel)
					return true, handleErr
				}
				if kind == "failover" {
					retryable, retryDelay, retryDeadline, retryMax := grokSameAccountRetryMetadata(account, resp.StatusCode, respBody)
					return true, &protocolforward.UpstreamFailoverError{

						StatusCode: resp.StatusCode,

						ResponseBody: respBody,

						ResponseHeaders: resp.Header.Clone(),

						RetryableOnSameAccount: retryable || decision.RetryableOnSameAccount(account, resp.StatusCode),

						RequestScopedTransient: retryable && resp.StatusCode == http.StatusTooManyRequests,

						SameAccountRetryDelay: retryDelay,

						SameAccountRetryDeadline: retryDeadline,

						SameAccountRetryMax: retryMax,
					}
				}
				result, handleErr = s.handleChatCompletionsErrorResponse(resp, c, account, billingModel)
				return true, handleErr
			}

			s.updateGrokUsageFromResponse(withGrokTeamRateLimitModel(ctx, upstreamModel), account, resp.Header, resp.StatusCode)
			return false, nil
		},

		ReadResponse: func(resp *http.Response, _ upstream.AttemptInput, _ upstream.OutputSink) (upstream.ResponsesObservation, error) {
			if clientStream {
				result, err = s.handleChatStreamingResponse(resp, c, account, originalModel, billingModel, upstreamModel, startTime, len(body))
			} else {
				result, err = s.handleChatBufferedStreamingResponse(resp, c, account, originalModel, billingModel, upstreamModel, startTime)
			}
			if result == nil {
				return upstream.ResponsesObservation{}, err
			}
			return upstream.ResponsesObservation{

				Usage: &result.Usage,

				HasUsage: protocolopenai.OpenAIUsageHasTokens(&result.Usage),

				FirstTokenMs: result.FirstTokenMs,

				ResponseID: result.ResponseID,

				SearchCount: result.SearchCount,

				ImageCount: result.ImageCount,

				ImageOutputSizes: result.ImageOutputSizes,

				HTTPCommitted: c.Writer.Written(),

				RetryCommitted: gatewayhttp.IsResponseCommitted(c),
			}, err
		},
	}
	var sink upstream.OutputSink
	if c != nil {
		sink = gatewayhttp.ResponseSink{Writer: c.Writer}
	}
	nativeResult, err := (grok.ResponsesExecutor{}).Execute(upstreamCtx, upstream.AttemptInput{

		Protocol: protocol.ProtocolOpenAIResponses,

		Body: responsesBody,

		ResponseModel: originalModel,

		Stream: clientStream,

		Target: target,
	}, sink)

	if result != nil {
		result.UpstreamEndpoint = grok.GrokChatResponsesEndpoint
		result.ResponseHeaders = nativeResult.UpstreamHeaders.Clone()
		if result.RequestID == "" {
			result.RequestID = firstNonEmpty(nativeResult.UpstreamHeaders.Get("x-request-id"), nativeResult.UpstreamHeaders.Get("xai-request-id"))
		}
		result.ReasoningEffort = extractOpenAIReasoningEffortFromBody(body, upstreamModel, billingModel, originalModel)
	}
	return result, err
}
