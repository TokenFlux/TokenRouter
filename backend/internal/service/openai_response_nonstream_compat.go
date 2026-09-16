// 非流结果只通过原入站端口观察、恢复名称并执行原错误副作用。
package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	native "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
)

func (s *OpenAIGatewayService) nativeFailedResponseTerminal(ctx context.Context, c *gin.Context, account *Account, resp *http.Response, mappedModel string, terminalPayload []byte, msg string) error {
	policyStatus, decision := s.applyOpenAIStreamFailedAccountPolicy(
		ctx, account, mappedModel, resp.Header, terminalPayload, msg,
	)
	if decision.ShouldReturnGenericError() {
		MarkResponseCommitted(c)
		writeOpenAIPassthroughErrorEnvelope(c, http.StatusInternalServerError, resp.Header, "Upstream gateway error")
		return fmt.Errorf("upstream compact response failed: status=%d (not in custom error codes)", policyStatus)
	}
	if !IsResponseCommitted(c) && decision.ShouldFailover(account, policyStatus, openAIStreamFailedEventShouldFailover(terminalPayload, msg)) {
		markOpenAIWSFailureSideEffectsApplied(c, policyStatus, decision.StopScheduling)
		return s.newOpenAIStreamPolicyFailoverError(
			c, account, false, strings.TrimSpace(resp.Header.Get("x-request-id")), resp.Header,
			policyStatus, terminalPayload, msg,
			openAIStreamFailedEventRetryableOnSameAccount(account, terminalPayload, msg),
		)
	}
	err := s.writeOpenAINonStreamingProtocolError(resp, c, msg)
	return wrapOpenAIUpstreamWarningIfCyber(resp.StatusCode, terminalPayload, msg, err)
}
func (s *OpenAIGatewayService) nativeNonStreamOptions(ctx context.Context, c *gin.Context, account *Account) native.NonStreamOptions {
	return native.NonStreamOptions{
		OAuthAccount:        account != nil && account.Type == AccountTypeOAuth,
		GrokCompact:         account != nil && account.IsGrok() && isOpenAIResponsesCompactPath(c),
		PreserveContentType: s.cfg != nil && !s.cfg.Security.ResponseHeaders.Enabled,
		ReadBody: func(reader io.Reader) ([]byte, error) {
			return ReadUpstreamResponseBody(reader, s.cfg, c, openAITooLargeError)
		},
		ObserveTier:        func(body []byte) { observeOpenAIServiceTierInContext(c, body, "response.completed") },
		ObserveSSE:         func(body string) { observeOpenAISSEBody(c, body) },
		ConvertCompact:     convertGrokResponseToOpenAICompact,
		RestoreClientTools: func(body []byte) ([]byte, error) { return restoreGrokResponsesClientToolPayload(c, body) },
		RestoreOpenAITools: func(body []byte) ([]byte, error) { return restoreOpenAIResponsesClientToolPayload(c, body) },
		RestoreNamespace:   func(body []byte) ([]byte, error) { return restoreOpenAIResponsesNamespacePayload(c, body) },
		RestoreToolNames:   func(body []byte) []byte { return restoreCodexToolNamesFromContext(c, body) },
		CorrectToolCalls:   s.correctToolCallsInResponseBody,
		ResponseHeaders: func(output, input http.Header) {
			responseheaders.WriteFilteredHeaders(output, input, s.responseHeaderFilter)
			s.relayOpenAICodexTurnState(c, account, input)
		},
		WriteCompactBridge: func(status int, body []byte) bool { return writeOpenAICompactSSEBridge(c, status, body) },
		CountJSONSearch:    countGrokNativeSearchCallsFromJSONBytes,
		CountSSESearch:     countGrokNativeSearchCallsFromSSEBody,
		ExtractError:       extractOpenAISSEErrorMessage,
		CompactFallback:    func(body []byte, message string) error { return newOpenAICompactFallbackSignal(c, body, message) },
		TerminalFailover: func(resp *http.Response, event string, body []byte, message, model string) error {
			if failure := s.nonStreamingTerminalFailureFailover(c, resp, account, false, event, body, message, model); failure != nil {
				return failure
			}
			return nil
		},
		ProtocolError: func(resp *http.Response, message string) error {
			return s.writeOpenAINonStreamingProtocolError(resp, c, message)
		},
		FailedTerminal: func(resp *http.Response, model string, body []byte, message string) error {
			return s.nativeFailedResponseTerminal(ctx, c, account, resp, model, body, message)
		},
		SupplementCompaction: func(body []byte, stream string) []byte { return supplementCompactionItemFromSSE(c, body, stream) },
	}
}
