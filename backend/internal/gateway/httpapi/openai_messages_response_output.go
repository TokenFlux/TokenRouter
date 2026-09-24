package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	moderationflow "github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytelemetry "github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

func (p *OpenAIResponseOutput) messagesBufferedFailure(c *gin.Context, account *gatewayprovider.ExecutionAccount, resp *http.Response, requestID, upstreamModel string, finalResponse *wire.ResponsesResponse, usage wire.ForwardUsage) error {

	payload, _ := json.Marshal(gin.H{"type": "response.failed", "response": finalResponse})
	if hit, code, msg := openai.DetectOpenAICyberPolicy(payload); hit {
		MarkOpsCyberPolicy(c, moderationflow.Mark{
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
		WriteForwardAnthropicError(c, http.StatusBadRequest, "invalid_request_error", clientMsg)
		return fmt.Errorf("openai cyber_policy: %s", msg)
	}
	message := openAICompatFailedResponseMessage(finalResponse)
	policyStatus, decision := p.ApplyStreamFailurePolicy(
		c.Request.Context(), account, upstreamModel, resp.Header, payload, message,
	)
	if decision.ShouldReturnGenericError() {
		WriteForwardAnthropicError(c, http.StatusInternalServerError, "api_error", "Upstream gateway error")
		return fmt.Errorf("upstream response failed: status=%d (not in custom error codes)", policyStatus)
	}
	if decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), policyStatus, openai.OpenAIStreamFailedEventShouldFailover(payload, message)) {
		MarkOpenAIResponseFailureEffects(c, policyStatus, decision.StopScheduling)
		return p.NewStreamPolicyFailureWithModel(
			c, account, false, requestID, resp.Header, policyStatus, payload, message,
			gatewayprovider.OpenAIStreamFailureRetryable(account, payload, message), upstreamModel,
		)
	}
	message = p.RecordStreamError(c, account, false, requestID, "http_error", payload, message)
	// 统一走语义状态推断 + body 归一化（与 /v1/responses 路径一致），
	// 使按错误码配置的透传规则可命中。
	if status, errType, errMsg, matched := ApplyOpenAIStreamFailedErrorRule(
		c, account.Record.Platform, payload, message,
	); matched {
		if errMsg == "" {
			errMsg = message
		}
		MarkResponseCommitted(c)
		WriteForwardAnthropicError(c, status, errType, errMsg)
		return fmt.Errorf("upstream response failed (passthrough): %s", errMsg)
	}
	WriteForwardAnthropicError(c, http.StatusBadGateway, "api_error", message)
	return fmt.Errorf("upstream response failed: %s", message)

}

func (p *OpenAIResponseOutput) messagesStreamFailure(c *gin.Context, account *gatewayprovider.ExecutionAccount, resp *http.Response, requestID, upstreamModel string, payloadBytes []byte, message string, clientOutputStarted, isBareErrorEvent bool) openai.ChatFailure {
	policyStatus, decision := p.ApplyStreamFailurePolicy(
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
		MarkOpenAIResponseFailureEffects(c, policyStatus, decision.StopScheduling)
		failure := p.NewStreamPolicyFailureWithModel(
			c, account, false, requestID, resp.Header, policyStatus, payloadBytes, message,
			gatewayprovider.OpenAIStreamFailureRetryable(account, payloadBytes, message), upstreamModel,
		)
		return openai.ChatFailure{Failover: failure}
	}
	message = p.RecordStreamError(c, account, false, requestID, "http_error", payloadBytes, message)
	errStatus, errType, errMsg := http.StatusBadGateway, "api_error", message
	if policyGeneric {
		errStatus, errType, errMsg = http.StatusInternalServerError, "api_error", "Upstream gateway error"
	}
	// 统一走语义状态推断 + body 归一化（与 /v1/responses 路径一致），
	// 使按错误码配置的透传规则可命中。
	if status, passthroughType, passthroughMsg, matched := ApplyOpenAIStreamFailedErrorRule(
		c, account.Record.Platform, payloadBytes, message,
	); matched && !policyGeneric {
		if passthroughMsg == "" {
			passthroughMsg = errMsg
		}
		errStatus, errType, errMsg = status, passthroughType, passthroughMsg
		MarkResponseCommitted(c)
	}
	return openai.ChatFailure{Status: errStatus, Type: errType, Message: errMsg}

}

func (p *OpenAIResponseOutput) MessagesOptions(c *gin.Context, account *gatewayprovider.ExecutionAccount, resp *http.Response, originalModel, billingModel, upstreamModel string) openai.MessagesResponseOptions {
	options := p.ChatOptions(c, account, resp, originalModel, billingModel, upstreamModel)
	options.ReadBuffered = func() (*wire.ResponsesResponse, wire.ForwardUsage, *bridge.BufferedResponseAccumulator, error) {
		return p.ReadBufferedTerminal(resp, c, "openai messages buffered", resp.Header.Get("x-request-id"))
	}
	options.BufferedFailure = func(final *wire.ResponsesResponse, usage wire.ForwardUsage) error {
		return p.messagesBufferedFailure(c, account, resp, resp.Header.Get("x-request-id"), upstreamModel, final, usage)
	}
	options.WriteError = func(status int, kind, message string) { WriteForwardAnthropicError(c, status, kind, message) }
	options.BuildStreamError = BuildForwardAnthropicStreamError
	return openai.MessagesResponseOptions{
		ChatResponseOptions: options,
		MessagesFailure: func(body []byte, message string, started, bare bool) openai.ChatFailure {
			return p.messagesStreamFailure(c, account, resp, resp.Header.Get("x-request-id"), upstreamModel, body, message, started, bare)
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
			return p.NewStreamFailure(c, account, false, resp.Header.Get("x-request-id"), nil, message)
		},
		RecordMissingTerminal: func(message string) {
			p.RecordMessagesStreamError(c, account, resp.Header.Get("x-request-id"), "stream_missing_terminal", message)
		},
	}
}
