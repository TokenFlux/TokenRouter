// 旧网关只提供账号策略、HTTP 错误与 Ops 观察，重试状态属于原生平台。
package service

import (
	"context"
	"net/http"
	"strings"

	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	"github.com/gin-gonic/gin"
)

type geminiExchangeMode int

const (
	geminiExchangeMessages geminiExchangeMode = iota
	geminiExchangeNative
	geminiExchangeOpenAI
)

func (s *GeminiMessagesCompatService) geminiExchangeOptions(c *gin.Context, ctx context.Context, account *Account, model string, mode geminiExchangeMode, protocol geminiOpenAICompatProtocol) gemininative.ExchangeOptions {
	options := gemininative.ExchangeOptions{AccountID: account.ID, AccountName: account.Name, Platform: account.Platform, MaxRetries: geminiMaxRetries, ReadError: s.readUpstreamErrorBody, Sanitize: sanitizeUpstreamErrorMessage, Message: extractUpstreamErrorMessage, CheckPolicy: func(ctx context.Context, resp *http.Response) (bool, *http.Response) {
		return s.checkErrorPolicyInLoop(ctx, account, resp, model)
	}, ShouldRetry: func(code int) bool { return s.shouldRetryGeminiUpstreamError(account, code) }, OnStatus: func(ctx context.Context, status int, header http.Header, body []byte) {
		s.handleGeminiUpstreamError(ctx, account, status, header, body)
	}, Observe: func(value gemininative.ExchangeNotice) {
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: value.Platform, AccountID: value.AccountID, AccountName: value.AccountName, UpstreamStatusCode: value.UpstreamStatusCode, UpstreamRequestID: value.UpstreamRequestID, Kind: value.Kind, Message: value.Message, Detail: value.Detail})
	}, SetError: func(code int, message, detail string) { setOpsUpstreamError(c, code, message, detail) }, Detail: s.upstreamErrorDetail}
	options.BuildError = func(err error) error {
		if mode == geminiExchangeOpenAI {
			return s.writeGeminiOpenAICompatError(c, protocol, http.StatusBadGateway, "upstream_error", err.Error())
		}
		status := http.StatusBadGateway
		kind := "upstream_error"
		if strings.Contains(err.Error(), "missing project_id") {
			status = http.StatusBadRequest
			kind = "invalid_request_error"
		}
		if mode == geminiExchangeNative {
			return s.writeGoogleError(c, status, err.Error())
		}
		return s.writeClaudeError(c, status, kind, err.Error())
	}
	options.FinalError = func(message string) error {
		if mode == geminiExchangeNative {
			return s.writeGoogleError(c, http.StatusBadGateway, message)
		}
		if mode == geminiExchangeOpenAI {
			return s.writeGeminiOpenAICompatError(c, protocol, http.StatusBadGateway, "upstream_error", message)
		}
		return s.writeClaudeError(c, http.StatusBadGateway, "upstream_error", message)
	}
	return options
}
