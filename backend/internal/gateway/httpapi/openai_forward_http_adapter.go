// HTTP 执行参数复用原生健康、会话和恢复能力。
package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/compact"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	openaicore "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

// nativeForwardHTTPOptions 不预热读取器，保留响应到达后才读取运行设置及绑定输出观察。
func (s *OpenAIResponsesExecutor) nativeForwardHTTPOptions(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, input forward.HTTPInput, exchange openai.HTTPExchangeOptions, retryEncrypted func([]byte) ([]byte, bool, error), markLineage func([]byte)) forward.HTTPOptions {
	return forward.HTTPOptions{
		Exchange: exchange, Sink: ResponseSink{Writer: c.Writer},
		StreamOptions: func() openai.StreamOptions {
			return s.Output.StreamOptions(ctx, c, account, input.ReasoningEffortValue)
		},
		NonStreamOptions: func() openai.NonStreamOptions { return s.Output.NonStreamOptions(ctx, c, account) },
		ReadErrorBody:    s.Output.ReadErrorBody,
		IsAgentIdentity:  func(ctx context.Context) bool { return s.Requests.Identity.UsesAgentIdentity(ctx, account) },
		InvalidAgentTask: openai.IsAgentTaskInvalidHTTPResponse,
		RecoverAgentTask: func(ctx context.Context) error {
			return s.Requests.Identity.Recover(ctx, account, account.View().GetCredential("task_id"))
		},
		RedactErrorBody: func(ctx context.Context, body []byte) []byte {
			return s.Requests.Identity.Redact(ctx, account, body)
		},
		ErrorDetails: func(body []byte) (string, string) {
			return logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(body))), upstream.ExtractErrorCode(body)
		},
		RetryEncrypted: retryEncrypted, MarkInvalidLineage: markLineage,
		CompactRetry: func(body []byte, status int, message string, payload []byte, tried bool) ([]byte, string, bool) {
			return s.Text.Compact.Prepare(c, account, input.RequestedModel, body, status, message, payload, tried)
		},
		CompactRetryObserved: func(resp *http.Response, payload []byte, message string) {
			s.Text.Compact.Observe(c, account, resp, payload, message, false)
		},
		CompactSignal: func(err error) (forward.CompactFailure, bool) {
			signal, ok := compact.AsFailure(err)
			if !ok {
				return forward.CompactFailure{}, false
			}
			return forward.CompactFailure{Message: signal.Message, Payload: signal.Payload}, true
		},
		CompactErrorResponse: func(resp *http.Response, signal forward.CompactFailure) (*http.Response, []byte) {
			return CompactFallbackErrorResponse(resp, &compact.Failure{Message: signal.Message, Payload: signal.Payload})
		},
		ShouldFailover: gatewayprovider.ShouldFailoverOpenAIResponse,
		HTTPFailover: func(resp *http.Response, payload []byte, message, model string) error {
			detail := ""
			if s.Output.Options.LogUpstreamErrorBody {
				limit := s.Output.Options.LogUpstreamErrorBodyMaxBytes
				if limit <= 0 {
					limit = 2048
				}
				detail = logredact.TruncateUTF8(string(payload), limit)
			}
			AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{Platform: account.Record.Platform, AccountID: account.Record.ID, AccountName: account.Record.Name, UpstreamStatusCode: resp.StatusCode, UpstreamRequestID: resp.Header.Get("x-request-id"), Kind: "failover", Message: message, Detail: detail})
			decision := s.Output.ApplyHTTPFailure(ctx, resp, account, payload, model)
			if decision.ShouldReturnGenericError() {
				return nil
			}
			return gatewayprovider.NewOpenAIUpstreamFailure(resp.StatusCode, resp.Header, payload, message, decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), resp.StatusCode))
		},
		CompactFailover: func(resp *http.Response, payload []byte, message, model string) error {
			AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{Platform: account.Record.Platform, AccountID: account.Record.ID, AccountName: account.Record.Name, UpstreamStatusCode: resp.StatusCode, UpstreamRequestID: resp.Header.Get("x-request-id"), Kind: "failover", Message: message})
			disabled := s.Output.ApplyHTTPFailure(ctx, resp, account, payload, model).StopScheduling
			return (gatewayprovider.OpenAIFailoverPolicy{Health: s.Output.Health}).NewAccountFailure(account, resp.StatusCode, resp.Header, payload, message, disabled, !disabled && account.View().IsPoolMode() && (account.View().IsPoolModeRetryableStatus(resp.StatusCode) || openai.IsOpenAITransientProcessingError(resp.StatusCode, message, payload)))
		},
		ErrorResponse: func(resp *http.Response, body []byte, model string) error {
			_, err := s.Output.ResponseError(ctx, resp, c, account, body, model)
			return err
		},
		ErrorSchedulingModel: gatewayprovider.ErrorSchedulingModel,
		WrapResponseBody: func(resp *http.Response) {
			if mapping, ok := OpenAIResponsesClientToolMapping(c); ok && openai.IsEventStreamResponse(resp.Header) {
				limit := openAIResponseDefaultMaxLineSize
				if s.Output.Options.MaxLineSize > 0 {
					limit = s.Output.Options.MaxLineSize
				}
				resp.Body = upstream.NewResponsesClientToolStreamBody(resp.Body, mapping, limit)
			}
		},
		BindResponseOwner: func(ctx context.Context, id string) { s.Output.BindResponseAccount(ctx, c, account, id) },
		UpdateUsageSnapshot: func(ctx context.Context, headers http.Header) {
			if snapshot := openai.ParseCodexRateLimitHeaders(headers); snapshot != nil {
				s.Text.CodexUsage.Observe(ctx, account.Record.ID, snapshot)
			}
		},
		ObserveUpstreamModel: func(model string) { SetOpsUpstreamModel(c, model) },
		ObservedServiceTier:  func() string { return ObservedUpstreamResponseServiceTier(c) },
		ResolvedServiceTier:  func(tier *string) *string { return ResolvedOpenAIUpstreamServiceTier(c, tier) },
		ExtractServiceTier:   requeststate.ExtractOpenAIServiceTierFromBody,
		Log: func(format string, args ...any) {
			logging.LegacyPrintf("service.openai_gateway", format, args...)
		},
	}
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
