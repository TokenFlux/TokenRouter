// Messages 保留与 Chat 不同的错误、取消及用量回传，不合并其账号策略。
package service

import (
	"encoding/json"
	"fmt"
	"net/http"

	moderationflow "github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytelemetry "github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

func (s *OpenAIGatewayService) nativeMessagesBufferedFailure(c *gin.Context, account *gatewayprovider.ExecutionAccount, resp *http.Response, requestID, upstreamModel string, finalResponse *wire.ResponsesResponse, usage wire.ForwardUsage) error {

	payload, _ := json.Marshal(gin.H{"type": "response.failed", "response": finalResponse})
	if hit, code, msg := openai.DetectOpenAICyberPolicy(payload); hit {
		httpapi.MarkOpsCyberPolicy(c, moderationflow.Mark{
			Code:           code,
			Message:        msg,
			Body:           logredact.TruncateUTF8(string(payload), 4096),
			UpstreamStatus: http.StatusOK,
			UpstreamInTok:  usage.InputTokens,
			UpstreamOutTok: usage.OutputTokens,
		})
		clientMsg := msg
		if clientMsg == "" {
			clientMsg = "Request blocked by upstream cyber-security policy"
		}
		httpapi.WriteForwardAnthropicError(c, http.StatusBadRequest, "invalid_request_error", clientMsg)
		return fmt.Errorf("openai cyber_policy: %s", msg)
	}
	message := openAICompatFailedResponseMessage(finalResponse)
	policyStatus, decision := s.applyOpenAIStreamFailedAccountPolicy(
		c.Request.Context(), account, upstreamModel, resp.Header, payload, message,
	)
	if decision.ShouldReturnGenericError() {
		httpapi.WriteForwardAnthropicError(c, http.StatusInternalServerError, "api_error", "Upstream gateway error")
		return fmt.Errorf("upstream response failed: status=%d (not in custom error codes)", policyStatus)
	}
	if decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), policyStatus, openai.OpenAIStreamFailedEventShouldFailover(payload, message)) {
		markOpenAIWSFailureSideEffectsApplied(c, policyStatus, decision.StopScheduling)
		return s.newOpenAIStreamPolicyFailoverErrorWithModel(
			c, account, false, requestID, resp.Header, policyStatus, payload, message,
			openAIStreamFailedEventRetryableOnSameAccount(account, payload, message), upstreamModel,
		)
	}
	message = s.recordOpenAIStreamUpstreamError(c, account, false, requestID, "http_error", payload, message)
	// 统一走语义状态推断 + body 归一化（与 /v1/responses 路径一致），
	// 使按错误码配置的透传规则可命中。
	if status, errType, errMsg, matched := applyOpenAIStreamFailedErrorPassthroughRule(
		c, account.Record.Platform, payload, message,
	); matched {
		if errMsg == "" {
			errMsg = message
		}
		httpapi.MarkResponseCommitted(c)
		httpapi.WriteForwardAnthropicError(c, status, errType, errMsg)
		return fmt.Errorf("upstream response failed (passthrough): %s", errMsg)
	}
	httpapi.WriteForwardAnthropicError(c, http.StatusBadGateway, "api_error", message)
	return fmt.Errorf("upstream response failed: %s", message)

}
func (s *OpenAIGatewayService) nativeMessagesStreamFailure(c *gin.Context, account *gatewayprovider.ExecutionAccount, resp *http.Response, requestID, upstreamModel string, payloadBytes []byte, message string, clientOutputStarted, isBareErrorEvent bool) openai.ChatFailure {
	policyStatus, decision := s.applyOpenAIStreamFailedAccountPolicy(
		c.Request.Context(), account, upstreamModel, resp.Header, payloadBytes, message,
	)
	policyGeneric := decision.ShouldReturnGenericError()
	// 客户端已有输出时切换账号会拼接两段模型流，此时必须回写 Anthropic error 事件，
	// 不能返回 handler 已无法安全重试的 failover 错误。
	shouldFailoverSignal := openai.OpenAIStreamFailedEventShouldFailover(payloadBytes, message)
	if isBareErrorEvent {
		shouldFailoverSignal = openai.OpenAIStreamErrorEventShouldFailover(payloadBytes, message)
	}
	if !clientOutputStarted && decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), policyStatus, shouldFailoverSignal) {
		markOpenAIWSFailureSideEffectsApplied(c, policyStatus, decision.StopScheduling)
		failure := s.newOpenAIStreamPolicyFailoverErrorWithModel(
			c, account, false, requestID, resp.Header, policyStatus, payloadBytes, message,
			openAIStreamFailedEventRetryableOnSameAccount(account, payloadBytes, message), upstreamModel,
		)
		return openai.ChatFailure{Failover: failure}
	}
	message = s.recordOpenAIStreamUpstreamError(c, account, false, requestID, "http_error", payloadBytes, message)
	errStatus, errType, errMsg := http.StatusBadGateway, "api_error", message
	if policyGeneric {
		errStatus, errType, errMsg = http.StatusInternalServerError, "api_error", "Upstream gateway error"
	}
	// 统一走语义状态推断 + body 归一化（与 /v1/responses 路径一致），
	// 使按错误码配置的透传规则可命中。
	if status, passthroughType, passthroughMsg, matched := applyOpenAIStreamFailedErrorPassthroughRule(
		c, account.Record.Platform, payloadBytes, message,
	); matched && !policyGeneric {
		if passthroughMsg == "" {
			passthroughMsg = errMsg
		}
		errStatus, errType, errMsg = status, passthroughType, passthroughMsg
		httpapi.MarkResponseCommitted(c)
	}
	return openai.ChatFailure{Status: errStatus, Type: errType, Message: errMsg}

}
func (s *OpenAIGatewayService) nativeMessagesResponseOptions(c *gin.Context, account *gatewayprovider.ExecutionAccount, resp *http.Response, originalModel, billingModel, upstreamModel string) openai.MessagesResponseOptions {
	options := s.nativeChatResponseOptions(c, account, resp, originalModel, billingModel, upstreamModel)
	options.ReadBuffered = func() (*wire.ResponsesResponse, wire.ForwardUsage, *bridge.BufferedResponseAccumulator, error) {
		return s.readOpenAICompatBufferedTerminal(resp, c, "openai messages buffered", resp.Header.Get("x-request-id"))
	}
	options.BufferedFailure = func(final *wire.ResponsesResponse, usage wire.ForwardUsage) error {
		return s.nativeMessagesBufferedFailure(c, account, resp, resp.Header.Get("x-request-id"), upstreamModel, final, usage)
	}
	options.WriteError = func(status int, kind, message string) { httpapi.WriteForwardAnthropicError(c, status, kind, message) }
	options.BuildStreamError = httpapi.BuildForwardAnthropicStreamError
	return openai.MessagesResponseOptions{
		ChatResponseOptions: options,
		MessagesFailure: func(body []byte, message string, started, bare bool) openai.ChatFailure {
			return s.nativeMessagesStreamFailure(c, account, resp, resp.Header.Get("x-request-id"), upstreamModel, body, message, started, bare)
		},
		MissingUsage: func(usage *wire.ForwardUsage, event string, disconnected bool) {
			if resp == nil {
				return
			}
			var id int64
			if account != nil {
				id = account.Record.ID
			}
			gatewaytelemetry.SuccessMissingUsage(c.Request.Context(), id, resp.StatusCode, usage, event, disconnected)
		},
		MissingTerminal: func(message string) error {
			return s.newOpenAIStreamFailoverError(c, account, false, resp.Header.Get("x-request-id"), nil, message)
		},
		RecordMissingTerminal: func(message string) {
			s.recordOpenAIMessagesStreamUpstreamError(c, account, resp.Header.Get("x-request-id"), "stream_missing_terminal", message)
		},
	}
}
