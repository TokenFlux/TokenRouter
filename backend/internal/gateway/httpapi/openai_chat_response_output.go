package httpapi

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	requeststate "github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	moderationflow "github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

func (p *OpenAIResponseOutput) chatBufferedFailure(c *gin.Context, account *gatewayprovider.ExecutionAccount, resp *http.Response, requestID, upstreamModel string, finalResponse *wire.ResponsesResponse, usage wire.ForwardUsage) error {

	payload, _ := json.Marshal(map[string]any{"type": "response.failed", "response": finalResponse})
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
		WriteForwardChatError(c, http.StatusBadRequest, "invalid_request_error", clientMsg)
		return fmt.Errorf("openai cyber_policy: %s", msg)
	}
	message := openAICompatFailedResponseMessage(finalResponse)
	policyStatus, decision := p.ApplyStreamFailurePolicy(
		c.Request.Context(), account, upstreamModel, resp.Header, payload, message,
	)
	if decision.ShouldReturnGenericError() {
		WriteForwardChatError(c, http.StatusInternalServerError, "api_error", "Upstream gateway error")
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
	// response.failed 到达在 HTTP 200 SSE 流上，无真实 HTTP 错误码；统一走语义
	// 状态推断 + body 归一化（与 /v1/responses 路径一致），使按错误码配置的规则可命中。
	if status, errType, errMsg, matched := ApplyOpenAIStreamFailedErrorRule(
		c, account.Record.Platform, payload, message,
	); matched {
		if errMsg == "" {
			errMsg = message
		}
		MarkResponseCommitted(c)
		WriteForwardChatError(c, status, errType, errMsg)
		return fmt.Errorf("upstream response failed (passthrough): %s", errMsg)
	}
	WriteForwardChatError(c, http.StatusBadGateway, "upstream_error", message)
	return fmt.Errorf("upstream response failed: %s", message)

}

func (p *OpenAIResponseOutput) chatStreamFailure(c *gin.Context, account *gatewayprovider.ExecutionAccount, resp *http.Response, requestID, upstreamModel string, payloadBytes []byte, message string, clientOutputStarted bool) openai.ChatFailure {
	policyStatus, decision := p.ApplyStreamFailurePolicy(
		c.Request.Context(), account, upstreamModel, resp.Header, payloadBytes, message,
	)
	policyGeneric := decision.ShouldReturnGenericError()
	if !clientOutputStarted && decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(account), policyStatus, openai.OpenAIStreamFailedEventShouldFailover(payloadBytes, message)) {
		MarkOpenAIResponseFailureEffects(c, policyStatus, decision.StopScheduling)
		failure := p.NewStreamPolicyFailureWithModel(
			c, account, false, requestID, resp.Header, policyStatus, payloadBytes, message,
			gatewayprovider.OpenAIStreamFailureRetryable(account, payloadBytes, message), upstreamModel,
		)
		return openai.ChatFailure{Failover: failure}
	}
	message = p.RecordStreamError(c, account, false, requestID, "http_error", payloadBytes, message)
	defaultStatus, defaultErrType, defaultMsg := http.StatusBadGateway, "upstream_error", message
	if policyGeneric {
		defaultStatus, defaultErrType, defaultMsg = http.StatusInternalServerError, "upstream_error", "Upstream gateway error"
	}
	// 统一走语义状态推断 + body 归一化（与 /v1/responses 路径一致），
	// 使按错误码配置的透传规则可命中。
	if status, errType, errMsg, matched := ApplyOpenAIStreamFailedErrorRule(
		c, account.Record.Platform, payloadBytes, message,
	); matched && !policyGeneric {
		if errMsg == "" {
			errMsg = defaultMsg
		}
		defaultStatus, defaultErrType, defaultMsg = status, errType, errMsg
		MarkResponseCommitted(c)
	}
	return openai.ChatFailure{Status: defaultStatus, Type: defaultErrType, Message: defaultMsg}

}

func (p *OpenAIResponseOutput) ChatOptions(c *gin.Context, account *gatewayprovider.ExecutionAccount, resp *http.Response, originalModel, billingModel, upstreamModel string) openai.ChatResponseOptions {
	options := openai.ChatResponseOptions{
		Runtime:        bridge.Runtime{Now: time.Now, ReadRandom: rand.Read},
		RequestContext: c.Request.Context(),
		ReadBuffered: func() (*wire.ResponsesResponse, wire.ForwardUsage, *bridge.BufferedResponseAccumulator, error) {
			return p.ReadBufferedTerminal(resp, c, "openai chat_completions buffered", resp.Header.Get("x-request-id"))
		},
		BufferedReadFailure: func(err error) error {
			return p.BufferedReadFailure(c, account, resp, resp.Header.Get("x-request-id"), err)
		},
		BufferedFailure: func(final *wire.ResponsesResponse, usage wire.ForwardUsage) error {
			return p.chatBufferedFailure(c, account, resp, resp.Header.Get("x-request-id"), upstreamModel, final, usage)
		},
		ObserveFinal: func(final *wire.ResponsesResponse) {
			observer := UpstreamResponseModelObserverFromContext(c)
			if observer == nil {
				observer = BeginUpstreamResponseModelObservation(c)
			}
			observer.Observe(final.Model, true)
			observer.ObserveServiceTier(final.ServiceTier, true)
		},
		MissingUsage: func(final *wire.ResponsesResponse, usage wire.ForwardUsage) error {
			if gatewayprovider.RequiresBillableGrokChatUsage(account, billingModel, upstreamModel, final.Model) && !gatewayprovider.HasBillableGrokChatUsage(usage) {
				return NewGrokMissingUsageFailure(c, account, requeststate.FirstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")))
			}
			return nil
		},
		Headers: func(dst, src http.Header) {
			if p.Headers != nil {
				provider.WriteFilteredHeaders(dst, src, p.Headers)
			}
		},
		WriteError:   func(status int, kind, message string) { WriteForwardChatError(c, status, kind, message) },
		ServiceTier:  func() string { return ObservedUpstreamResponseServiceTier(c) },
		CountSearch:  account != nil && account.View().IsGrok(),
		JSONSearch:   grok.CountGrokNativeSearchCallsFromJSONBytes,
		StreamSearch: grok.CountGrokNativeSearchCallsInSSEDataDedup,
		Scanner:      func(r io.Reader) *bufio.Scanner { return p.Scanner(r) },
		StreamInterval: func() time.Duration {
			if p.Options.Configured && p.Options.StreamDataIntervalTimeout > 0 {
				return time.Duration(p.Options.StreamDataIntervalTimeout) * time.Second
			}
			return 0
		},
		KeepaliveInterval: func() time.Duration {
			if p.Options.Configured && p.Options.StreamKeepaliveInterval > 0 {
				return time.Duration(p.Options.StreamKeepaliveInterval) * time.Second
			}
			return 0
		},
		RestoreToolNames: func(body []byte) []byte { return RestoreCodexToolNamesFromContext(c, body) },
		Observe:          func(body []byte, event string) { ObserveOpenAIServiceTierInContext(c, body, event) },
		CyberForwarded:   forwardcore.ErrCyberPolicyForwarded,
		MarkCyber: func(value openai.CyberObservation) {
			MarkOpsCyberPolicy(c, moderationflow.Mark{Code: value.Code, Message: value.Message, Body: value.Body, UpstreamStatus: value.UpstreamStatus, UpstreamInTok: value.UpstreamInTok, UpstreamOutTok: value.UpstreamOutTok})
		},
		Truncate:         logredact.TruncateUTF8,
		BuildStreamError: BuildForwardChatStreamError,
		StreamFailure: func(body []byte, message string, started bool) openai.ChatFailure {
			return p.chatStreamFailure(c, account, resp, resp.Header.Get("x-request-id"), upstreamModel, body, message, started)
		},
		SilentRefusal: func() error {
			return NewOpenAISilentRefusalFailoverError(c, ExecutionErrorAccount(account), resp.Header.Get("x-request-id"))
		},
		ReadFailure: openai.NewUpstreamStreamReadError,
	}
	return options
}
