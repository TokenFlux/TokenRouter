package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	requeststate "github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	protocolforward "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	tierpolicy "github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	protocolbridge "github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// ChatResponses 执行兼容桥接；不适用时由调用者选择原生 Chat 分支。
// ChatResponses 将严格兼容的 Chat 请求转换为 xAI
// Responses 格式，并复用既有的 Responses-to-Chat 响应转换器。Grok CLI 使用独立的
// 上游协议，因此这里不会执行 Codex OAuth 转换。
func (s *GrokExecutor) ChatResponses(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
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
	billingModel := gatewayprovider.ExecutionModelPolicy(account).ForwardModel(originalModel, defaultMappedModel)
	upstreamModel := gatewayprovider.ExecutionModelPolicy(account).NormalizeOpenAI(billingModel)
	cacheIdentity := ResolveGrokCacheIdentity(c, body, promptCacheKey, upstreamModel)
	// 图片输入必须通过 Responses 桥接：原始 Chat Completions 路径无法把 image_url
	// 转发给非 Composer 模型的 Grok 原生视觉能力，否则图片会被静默丢弃；
	// 因此即使没有 prompt-cache 身份也要路由到 Responses。
	hasImageInput := protocolopenai.JSONValueMayContainImageInput(gjson.GetBytes(body, "messages"))
	if account.Route.Protocol() == "" && !gatewayprovider.GrokBodyCodec().GrokChatResponsesRuntimeEligible(upstreamModel, cacheIdentity) && (!hasImageInput || !gatewayprovider.GrokBodyCodec().GrokChatResponsesBridgeModel(upstreamModel)) {
		return nil, false, nil
	}

	responsesReq, err := protocolbridge.ChatCompletionsToResponses(&chatReq, protocolforward.ConversionOptionsForModel(chatReq.Model))
	if err != nil {
		return nil, true, fmt.Errorf("convert grok chat completions to responses: %w", err)
	}
	responsesReq.Model = upstreamModel
	responsesReq.Stream = true
	// 让 Chat 与原生 Responses 对 OpenAI 兼容的 service_tier 别名保持一致；共享
	// 规范化器会丢弃未知值，避免其到达 xAI。
	responsesReq.ServiceTier = protocolopenai.ServiceTierValue(responsesReq.ServiceTier)
	// 这些字段对 Codex 有用，但 Grok CLI 协议不需要；桥接请求应尽量贴近原生 Grok。
	responsesReq.Include = nil
	responsesReq.Store = nil

	responsesBody, err := json.Marshal(responsesReq)
	if err != nil {
		return nil, true, fmt.Errorf("marshal grok responses bridge request: %w", err)
	}
	// 在 Grok 能力清理前保留转换后的 Responses 意图；缓存路由必须看到真实客户端函数工具，
	// 而不是嵌套的 Chat Completions 声明或无工具副本。
	intentBody, err := gatewayprovider.GrokBodyCodec().GrokChatResponsesCacheIntentBody(responsesBody)
	if err != nil {
		return nil, true, fmt.Errorf("normalize grok responses bridge cache intent: %w", err)
	}
	responsesBody, err = gatewayprovider.GrokBodyCodec().PatchGrokResponsesBody(responsesBody, upstreamModel)
	if err != nil {
		return nil, true, fmt.Errorf("patch grok responses bridge request: %w", err)
	}
	responsesBody, err = grok.ApplyGrokResponsesCacheIdentity(responsesBody, intentBody, cacheIdentity, true)
	if err != nil {
		return nil, true, fmt.Errorf("apply grok responses bridge cache identity: %w", err)
	}
	responsesBody, err = ApplyGrokFreeRequestToolCacheRoute(c, responsesBody, intentBody, account, cacheIdentity)
	if err != nil {
		return nil, true, fmt.Errorf("apply grok responses bridge function-tool cache route: %w", err)
	}

	updatedBody, policyErr := tierpolicy.ApplyBody(responsesBody, s.FastPolicy.Input(ctx, account, upstreamModel))
	if policyErr != nil {
		var blocked *tierpolicy.BlockedError
		if errors.As(policyErr, &blocked) {
			MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)
			WriteForwardChatError(c, http.StatusForbidden, "permission_error", blocked.Message)
		}
		return nil, true, policyErr
	}
	responsesBody = updatedBody

	token, _, err := s.Credentials.Resolve(ctx, RequestCredentialBudget(c), CredentialObserver{Context: c}, account)
	if err != nil {
		return nil, true, fmt.Errorf("get grok access token: %w", err)
	}
	upstreamCtx, releaseUpstreamCtx := gatewayprovider.DetachUpstreamContext(ctx)
	upstreamReq, err := s.BuildResponsesRequest(upstreamCtx, c, account, responsesBody, token, cacheIdentity, true)
	releaseUpstreamCtx()
	if err != nil {
		return nil, true, fmt.Errorf("build grok responses bridge request: %w", err)
	}
	SetActualOpenAIUpstreamEndpoint(c, grok.GrokChatResponsesEndpoint)

	proxyURL := ""
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		proxyURL = account.Record.Proxy.URL()
	}

	var result *protocolforward.OpenAIResult
	var handleErr error
	target := &grok.ResponsesTarget{

		AccountID: account.Record.ID,

		Model: upstreamModel,

		Enter: s.Enter,

		PassRawStream: true,

		Exchange: grok.ResponsesExchange{

			SingleExchange: true,

			Build: func([]byte) (*http.Request, error) { return upstreamReq, nil },

			Do: func(req *http.Request) (*http.Response, error) {
				return s.Transport.DoWithTLS(req, proxyURL, account.Record.ID, account.Record.Concurrency, s.TLSProfile(account, tlsRouterMatch...))
			},

			ReadError: s.Output.ReadErrorBody,

			AfterExchange: func(err error) error {
				if err != nil {
					return s.Failure.Handle(ctx, c, account, err, false)
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
				decision := gatewayprovider.ApplyGrokExecutionHealth(ctx, s.Health, account, resp.StatusCode, resp.Header, respBody, "", upstreamModel)
				kind := "http_error"
				if decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode, gatewayprovider.ShouldFailoverGrokResponse(resp.StatusCode, respBody)) {
					kind = "failover"
				}
				AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{

					Platform: account.Record.Platform,

					AccountID: account.Record.ID,

					AccountName: account.Record.Name,

					UpstreamStatusCode: resp.StatusCode,

					UpstreamRequestID: requeststate.FirstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")),

					Kind: kind,

					Message: upstreamMsg,
				})
				if decision.ShouldReturnGenericError() {
					result, handleErr = s.Output.CompatError(resp, c, account, WriteForwardChatError, WriteForwardChatErrorBody, billingModel)
					return true, handleErr
				}
				if kind == "failover" {
					retryable, retryDelay, retryDeadline, retryMax := gatewayprovider.GrokSameAccountRetryMetadata(account, resp.StatusCode, respBody)
					return true, &protocolforward.UpstreamFailoverError{

						StatusCode: resp.StatusCode,

						ResponseBody: respBody,

						ResponseHeaders: resp.Header.Clone(),

						RetryableOnSameAccount: retryable || decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode),

						RequestScopedTransient: retryable && resp.StatusCode == http.StatusTooManyRequests,

						SameAccountRetryDelay: retryDelay,

						SameAccountRetryDeadline: retryDeadline,

						SameAccountRetryMax: retryMax,
					}
				}
				result, handleErr = s.Output.CompatError(resp, c, account, WriteForwardChatError, WriteForwardChatErrorBody, billingModel)
				return true, handleErr
			}

			s.Health.ObserveResponse(ctx, account.View(), resp.Header, resp.StatusCode, upstreamModel)
			return false, nil
		},

		ReadResponse: func(resp *http.Response, _ upstream.AttemptInput, _ upstream.OutputSink) (upstream.ResponsesObservation, error) {
			if clientStream {
				result, err = s.Output.ChatStreaming(resp, c, account, originalModel, billingModel, upstreamModel, startTime, len(body))
			} else {
				result, err = s.Output.ChatBuffered(resp, c, account, originalModel, billingModel, upstreamModel, startTime)
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

				RetryCommitted: IsResponseCommitted(c),
			}, err
		},
	}
	var sink upstream.OutputSink
	if c != nil {
		sink = ResponseSink{Writer: c.Writer}
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
			result.RequestID = requeststate.FirstNonEmpty(nativeResult.UpstreamHeaders.Get("x-request-id"), nativeResult.UpstreamHeaders.Get("xai-request-id"))
		}
		result.ReasoningEffort = requeststate.ExtractOpenAIReasoningEffortFromBody(body, upstreamModel, billingModel, originalModel)
	}
	return result, true, err
}
