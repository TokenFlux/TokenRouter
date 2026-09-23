// Chat 兼容端口保留账号副作用、缺价/缺用量裁决与旧 HTTP 错误形状。
package service

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	moderationflow "github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

func (s *OpenAIGatewayService) nativeChatBufferedFailure(c *gin.Context, account *gatewayprovider.ExecutionAccount, resp *http.Response, requestID, upstreamModel string, finalResponse *wire.ResponsesResponse, usage wire.ForwardUsage) error {

	payload, _ := json.Marshal(map[string]any{"type": "response.failed", "response": finalResponse})
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
		writeChatCompletionsError(c, http.StatusBadRequest, "invalid_request_error", clientMsg)
		return fmt.Errorf("openai cyber_policy: %s", msg)
	}
	message := openAICompatFailedResponseMessage(finalResponse)
	policyStatus, decision := s.applyOpenAIStreamFailedAccountPolicy(
		c.Request.Context(), account, upstreamModel, resp.Header, payload, message,
	)
	if decision.ShouldReturnGenericError() {
		writeChatCompletionsError(c, http.StatusInternalServerError, "api_error", "Upstream gateway error")
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
	// response.failed 到达在 HTTP 200 SSE 流上，无真实 HTTP 错误码；统一走语义
	// 状态推断 + body 归一化（与 /v1/responses 路径一致），使按错误码配置的规则可命中。
	if status, errType, errMsg, matched := applyOpenAIStreamFailedErrorPassthroughRule(
		c, account.Record.Platform, payload, message,
	); matched {
		if errMsg == "" {
			errMsg = message
		}
		httpapi.MarkResponseCommitted(c)
		writeChatCompletionsError(c, status, errType, errMsg)
		return fmt.Errorf("upstream response failed (passthrough): %s", errMsg)
	}
	writeChatCompletionsError(c, http.StatusBadGateway, "upstream_error", message)
	return fmt.Errorf("upstream response failed: %s", message)

}
func (s *OpenAIGatewayService) nativeChatStreamFailure(c *gin.Context, account *gatewayprovider.ExecutionAccount, resp *http.Response, requestID, upstreamModel string, payloadBytes []byte, message string, clientOutputStarted bool) openai.ChatFailure {
	policyStatus, decision := s.applyOpenAIStreamFailedAccountPolicy(
		c.Request.Context(), account, upstreamModel, resp.Header, payloadBytes, message,
	)
	policyGeneric := decision.ShouldReturnGenericError()
	if !clientOutputStarted && decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), policyStatus, openai.OpenAIStreamFailedEventShouldFailover(payloadBytes, message)) {
		markOpenAIWSFailureSideEffectsApplied(c, policyStatus, decision.StopScheduling)
		failure := s.newOpenAIStreamPolicyFailoverErrorWithModel(
			c, account, false, requestID, resp.Header, policyStatus, payloadBytes, message,
			openAIStreamFailedEventRetryableOnSameAccount(account, payloadBytes, message), upstreamModel,
		)
		return openai.ChatFailure{Failover: failure}
	}
	message = s.recordOpenAIStreamUpstreamError(c, account, false, requestID, "http_error", payloadBytes, message)
	defaultStatus, defaultErrType, defaultMsg := http.StatusBadGateway, "upstream_error", message
	if policyGeneric {
		defaultStatus, defaultErrType, defaultMsg = http.StatusInternalServerError, "upstream_error", "Upstream gateway error"
	}
	// 统一走语义状态推断 + body 归一化（与 /v1/responses 路径一致），
	// 使按错误码配置的透传规则可命中。
	if status, errType, errMsg, matched := applyOpenAIStreamFailedErrorPassthroughRule(
		c, account.Record.Platform, payloadBytes, message,
	); matched && !policyGeneric {
		if errMsg == "" {
			errMsg = defaultMsg
		}
		defaultStatus, defaultErrType, defaultMsg = status, errType, errMsg
		httpapi.MarkResponseCommitted(c)
	}
	return openai.ChatFailure{Status: defaultStatus, Type: defaultErrType, Message: defaultMsg}

}
func (s *OpenAIGatewayService) nativeChatResponseOptions(c *gin.Context, account *gatewayprovider.ExecutionAccount, resp *http.Response, originalModel, billingModel, upstreamModel string) openai.ChatResponseOptions {
	options := openai.ChatResponseOptions{
		Runtime:        bridge.Runtime{Now: time.Now, ReadRandom: rand.Read},
		RequestContext: c.Request.Context(),
		ReadBuffered: func() (*wire.ResponsesResponse, wire.ForwardUsage, *bridge.BufferedResponseAccumulator, error) {
			return s.readOpenAICompatBufferedTerminal(resp, c, "openai chat_completions buffered", resp.Header.Get("x-request-id"))
		},
		BufferedReadFailure: func(err error) error {
			return s.newOpenAICompatBufferedReadFailoverError(c, account, resp, resp.Header.Get("x-request-id"), err)
		},
		BufferedFailure: func(final *wire.ResponsesResponse, usage wire.ForwardUsage) error {
			return s.nativeChatBufferedFailure(c, account, resp, resp.Header.Get("x-request-id"), upstreamModel, final, usage)
		},
		ObserveFinal: func(final *wire.ResponsesResponse) {
			observer := httpapi.UpstreamResponseModelObserverFromContext(c)
			if observer == nil {
				observer = httpapi.BeginUpstreamResponseModelObservation(c)
			}
			observer.Observe(final.Model, true)
			observer.ObserveServiceTier(final.ServiceTier, true)
		},
		MissingUsage: func(final *wire.ResponsesResponse, usage wire.ForwardUsage) error {
			if requiresBillableGrokChatUsage(account, billingModel, upstreamModel, final.Model) && !hasBillableGrokChatUsage(usage) {
				return newGrokMissingUsageFailoverError(c, account, firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")))
			}
			return nil
		},
		Headers: func(dst, src http.Header) {
			if s.responseHeaderFilter != nil {
				provider.WriteFilteredHeaders(dst, src, s.responseHeaderFilter)
			}
		},
		WriteError:   func(status int, kind, message string) { writeChatCompletionsError(c, status, kind, message) },
		ServiceTier:  func() string { return httpapi.ObservedUpstreamResponseServiceTier(c) },
		CountSearch:  account != nil && account.View().IsGrok(),
		JSONSearch:   grok.CountGrokNativeSearchCallsFromJSONBytes,
		StreamSearch: grok.CountGrokNativeSearchCallsInSSEDataDedup,
		Scanner:      func(r io.Reader) *bufio.Scanner { return s.newUpstreamSSEScanner(r) },
		StreamInterval: func() time.Duration {
			if s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
				return time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
			}
			return 0
		},
		KeepaliveInterval: func() time.Duration {
			if s.cfg != nil && s.cfg.Gateway.StreamKeepaliveInterval > 0 {
				return time.Duration(s.cfg.Gateway.StreamKeepaliveInterval) * time.Second
			}
			return 0
		},
		RestoreToolNames: func(body []byte) []byte { return restoreCodexToolNamesFromContext(c, body) },
		Observe:          func(body []byte, event string) { httpapi.ObserveOpenAIServiceTierInContext(c, body, event) },
		CyberForwarded:   forwardcore.ErrCyberPolicyForwarded,
		MarkCyber: func(value openai.CyberObservation) {
			httpapi.MarkOpsCyberPolicy(c, moderationflow.Mark{Code: value.Code, Message: value.Message, Body: value.Body, UpstreamStatus: value.UpstreamStatus, UpstreamInTok: value.UpstreamInTok, UpstreamOutTok: value.UpstreamOutTok})
		},
		Truncate:         logredact.TruncateUTF8,
		BuildStreamError: httpapi.BuildForwardChatStreamError,
		StreamFailure: func(body []byte, message string, started bool) openai.ChatFailure {
			return s.nativeChatStreamFailure(c, account, resp, resp.Header.Get("x-request-id"), upstreamModel, body, message, started)
		},
		SilentRefusal: func() error {
			return httpapi.NewOpenAISilentRefusalFailoverError(c, upstreamErrorAccount(account), resp.Header.Get("x-request-id"))
		},
		ReadFailure: openai.NewUpstreamStreamReadError,
	}
	return options
}

// chatForwardResult 仅把供应商用量和名称投影回尚未迁移的入站完成处理。
func chatForwardResult(result *openai.CompatResponseResult, billingModel string) *forwardcore.OpenAIResult {
	if result == nil {
		return nil
	}
	return &forwardcore.OpenAIResult{RequestID: result.RequestID, ReasoningEffort: result.ReasoningEffort, ServiceTier: result.ResolvedTier, ResponseID: result.ResponseID, ClientDisconnect: result.ClientDisconnect, UpstreamHeaders: result.UpstreamHeaders, Usage: result.Usage, Model: result.Model, BillingModel: billingModel, UpstreamModel: result.UpstreamModel, UpstreamResponseServiceTier: result.ServiceTier, Stream: result.Stream, Duration: result.Duration, FirstTokenMs: result.FirstTokenMs, SearchCount: result.SearchCount}
}
