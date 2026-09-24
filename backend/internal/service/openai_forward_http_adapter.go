// 旧服务只投影单次 HTTP 执行所需的参数和健康/会话端口，恢复循环由目标 Adapter 拥有。
package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	compact "github.com/TokenFlux/TokenRouter/internal/gateway/compact"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	requeststate "github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	openaicore "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

// nativeForwardHTTPOptions 不预热读取器，保留响应到达后才读取运行设置及绑定输出观察。
func (s *OpenAIGatewayService) nativeForwardHTTPOptions(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, input forward.HTTPInput, exchange openai.HTTPExchangeOptions, retryEncrypted func([]byte) ([]byte, bool, error), markLineage func([]byte)) forward.HTTPOptions {
	return forward.HTTPOptions{
		Exchange: exchange, Sink: gatewayhttp.ResponseSink{Writer: c.Writer},
		StreamOptions: func() openai.StreamOptions {
			return s.nativeResponseStreamOptions(ctx, c, account, input.ReasoningEffortValue)
		},
		NonStreamOptions: func() openai.NonStreamOptions { return s.nativeNonStreamOptions(ctx, c, account) },
		ReadErrorBody:    s.readUpstreamErrorBody,
		IsAgentIdentity:  func(ctx context.Context) bool { return s.agentIdentity.UsesAgentIdentity(ctx, account) },
		InvalidAgentTask: openai.IsAgentTaskInvalidHTTPResponse,
		RecoverAgentTask: func(ctx context.Context) error {
			return s.agentIdentity.Recover(ctx, account, account.View().GetCredential("task_id"))
		},
		RedactErrorBody: func(ctx context.Context, body []byte) []byte {
			return s.agentIdentity.Redact(ctx, account, body)
		},
		ErrorDetails: func(body []byte) (string, string) {
			return logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(body))), upstream.ExtractErrorCode(body)
		},
		RetryEncrypted: retryEncrypted, MarkInvalidLineage: markLineage,
		CompactRetry: func(body []byte, status int, message string, payload []byte, tried bool) ([]byte, string, bool) {
			return s.compactExecutor.Prepare(c, account, input.RequestedModel, body, status, message, payload, tried)
		},
		CompactRetryObserved: func(resp *http.Response, payload []byte, message string) {
			s.compactExecutor.Observe(c, account, resp, payload, message, false)
		},
		CompactSignal: func(err error) (forward.CompactFailure, bool) {
			signal, ok := compact.AsFailure(err)
			if !ok {
				return forward.CompactFailure{}, false
			}
			return forward.CompactFailure{Message: signal.Message, Payload: signal.Payload}, true
		},
		CompactErrorResponse: func(resp *http.Response, signal forward.CompactFailure) (*http.Response, []byte) {
			return gatewayhttp.CompactFallbackErrorResponse(resp, &compact.Failure{Message: signal.Message, Payload: signal.Payload})
		},
		ShouldFailover: s.shouldFailoverOpenAIUpstreamResponse,
		HTTPFailover: func(resp *http.Response, payload []byte, message, model string) error {
			detail := ""
			if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
				limit := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
				if limit <= 0 {
					limit = 2048
				}
				detail = logredact.TruncateUTF8(string(payload), limit)
			}
			gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{Platform: account.Record.Platform, AccountID: account.Record.ID, AccountName: account.Record.Name, UpstreamStatusCode: resp.StatusCode, UpstreamRequestID: resp.Header.Get("x-request-id"), Kind: "failover", Message: message, Detail: detail})
			decision := s.applyFailoverSideEffects(ctx, resp, account, payload, model)
			if decision.ShouldReturnGenericError() {
				return nil
			}
			return newOpenAIUpstreamFailoverError(resp.StatusCode, resp.Header, payload, message, decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode))
		},
		CompactFailover: func(resp *http.Response, payload []byte, message, model string) error {
			gatewayhttp.AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{Platform: account.Record.Platform, AccountID: account.Record.ID, AccountName: account.Record.Name, UpstreamStatusCode: resp.StatusCode, UpstreamRequestID: resp.Header.Get("x-request-id"), Kind: "failover", Message: message})
			disabled := s.handleFailoverSideEffects(ctx, resp, account, payload, model)
			return s.newOpenAIAccountFailoverError(account, resp.StatusCode, resp.Header, payload, message, disabled, !disabled && account.View().IsPoolMode() && (account.View().IsPoolModeRetryableStatus(resp.StatusCode) || openai.IsOpenAITransientProcessingError(resp.StatusCode, message, payload)))
		},
		ErrorResponse: func(resp *http.Response, body []byte, model string) error {
			_, err := s.handleErrorResponse(ctx, resp, c, account, body, model)
			return err
		},
		ErrorSchedulingModel: gatewayprovider.ErrorSchedulingModel,
		WrapResponseBody: func(resp *http.Response) {
			if mapping, ok := gatewayhttp.OpenAIResponsesClientToolMapping(c); ok && isEventStreamResponse(resp.Header) {
				limit := defaultMaxLineSize
				if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
					limit = s.cfg.Gateway.MaxLineSize
				}
				resp.Body = upstream.NewResponsesClientToolStreamBody(resp.Body, mapping, limit)
			}
		},
		BindResponseOwner: func(ctx context.Context, id string) { s.bindHTTPResponseAccount(ctx, c, account, id) },
		UpdateUsageSnapshot: func(ctx context.Context, headers http.Header) {
			if snapshot := openai.ParseCodexRateLimitHeaders(headers); snapshot != nil {
				s.updateCodexUsageSnapshot(ctx, account.Record.ID, snapshot)
			}
		},
		ObserveUpstreamModel: func(model string) { gatewayhttp.SetOpsUpstreamModel(c, model) },
		ObservedServiceTier:  func() string { return gatewayhttp.ObservedUpstreamResponseServiceTier(c) },
		ResolvedServiceTier:  func(tier *string) *string { return gatewayhttp.ResolvedOpenAIUpstreamServiceTier(c, tier) },
		ExtractServiceTier:   requeststate.ExtractOpenAIServiceTierFromBody,
		Log: func(format string, args ...any) {
			logging.LegacyPrintf("service.openai_gateway", format, args...)
		},
	}
}

func openAIForwardResultFromHTTP(result *forward.Result) *forwardcore.OpenAIResult {
	if result == nil {
		return nil
	}
	value := &forwardcore.OpenAIResult{UpstreamEndpoint: result.UpstreamEndpoint, RequestedReasoningEffort: result.RequestedReasoningEffort, OpenAIWSMode: result.OpenAIWSMode, UpstreamTerminalEvent: result.UpstreamTerminalEvent, ResponseHeaders: result.ResponseHeaders, ImageOutputSize: result.ImageOutputSize, ImageSizeSource: result.ImageSizeSource, ImageSizeBreakdown: result.ImageSizeBreakdown, VideoCount: result.VideoCount, VideoResolution: result.VideoResolution, VideoDurationSeconds: result.VideoDurationSeconds, WebSearchCalls: result.WebSearchCalls, AudioUsage: result.AudioUsage, ClientDisconnect: result.ClientDisconnect, SearchCount: result.SearchCount, RequestID: result.RequestID, ResponseID: result.ResponseID, UpstreamHeaders: result.Headers, Usage: result.Usage, Model: result.Model, BillingModel: result.BillingModel, UpstreamModel: result.UpstreamModel, UpstreamResponseServiceTier: result.UpstreamResponseServiceTier, ServiceTier: result.ServiceTier, ReasoningEffort: result.ReasoningEffort, Stream: result.Stream, Duration: result.Duration, FirstTokenMs: result.FirstTokenMs, ImageCount: result.ImageCount, ImageSize: result.ImageSize, ImageInputSize: result.ImageInputSize, ImageOutputSizes: result.ImageOutputSizes}
	if result.UpstreamWarning != nil {
		value.UpstreamWarning = &forwardcore.UpstreamWarning{StatusCode: result.UpstreamWarning.StatusCode, ResponseBody: result.UpstreamWarning.ResponseBody, Message: result.UpstreamWarning.Message}
	}
	return value
}

// 按已有解码缓存读取并剥离一次密文，不改变 JSON 数字及缓存复用语义。
func prepareOpenAIHTTPEncryptedRetry(body []byte, decode func([]byte) (map[string]any, error)) ([]byte, bool, error) {
	decoded, err := decode(body)
	if err != nil {
		return nil, false, err
	}
	if !openaicore.TrimEncryptedReasoningItems(decoded) {
		return body, false, nil
	}
	result, err := wirejson.Marshal(decoded)
	if err != nil {
		return nil, false, fmt.Errorf("serialize invalid_encrypted_content retry body: %w", err)
	}
	return result, true, nil
}

// HTTP 子协议尚未改签名前，只在 Adapter 往返当前文本完成字段。
func openAIHTTPResultFromForward(r *forwardcore.OpenAIResult) *forward.Result {
	if r == nil {
		return nil
	}
	value := &forward.Result{UpstreamEndpoint: r.UpstreamEndpoint, RequestedReasoningEffort: r.RequestedReasoningEffort, OpenAIWSMode: r.OpenAIWSMode, UpstreamTerminalEvent: r.UpstreamTerminalEvent, ResponseHeaders: r.ResponseHeaders, ImageOutputSize: r.ImageOutputSize, ImageSizeSource: r.ImageSizeSource, ImageSizeBreakdown: r.ImageSizeBreakdown, VideoCount: r.VideoCount, VideoResolution: r.VideoResolution, VideoDurationSeconds: r.VideoDurationSeconds, WebSearchCalls: r.WebSearchCalls, AudioUsage: r.AudioUsage, RequestID: r.RequestID, ResponseID: r.ResponseID, Headers: r.UpstreamHeaders, Usage: r.Usage, Model: r.Model, BillingModel: r.BillingModel, UpstreamModel: r.UpstreamModel, UpstreamResponseServiceTier: r.UpstreamResponseServiceTier, ServiceTier: r.ServiceTier, ReasoningEffort: r.ReasoningEffort, Stream: r.Stream, Duration: r.Duration, FirstTokenMs: r.FirstTokenMs, ClientDisconnect: r.ClientDisconnect, SearchCount: r.SearchCount, ImageCount: r.ImageCount, ImageSize: r.ImageSize, ImageInputSize: r.ImageInputSize, ImageOutputSizes: r.ImageOutputSizes}
	if r.UpstreamWarning != nil {
		value.UpstreamWarning = &forwardcore.UpstreamWarning{StatusCode: r.UpstreamWarning.StatusCode, ResponseBody: r.UpstreamWarning.ResponseBody, Message: r.UpstreamWarning.Message}
	}
	return value
}
