// 本文件只为旧入站链投影平台参数；调用者编排在 S11 继续迁移。
package service

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/gin-gonic/gin"
)

func (s *GatewayService) anthropicExchangeOptions(ctx context.Context, c *gin.Context, account *Account, token, tokenType, model string, stream, mimic bool, proxyURL string, tlsProfile *tlsfingerprint.Profile, replace func([]byte) error) claude.ExchangeOptions {
	o := claude.ExchangeOptions{AccountID: account.ID, AccountName: account.Name, Platform: account.Platform, Stream: stream, MaxAttempts: maxRetryAttempts, MaxElapsed: maxRetryElapsed, BudgetTokens: BudgetRectifyBudgetTokens, BudgetMaxTokens: BudgetRectifyMaxTokens, Context: detachStreamUpstreamContext, ReadErrorBody: s.readUpstreamErrorBody, Delay: retryBackoffDelay, SafeURL: safeUpstreamURL, Sanitize: sanitizeUpstreamErrorMessage, ErrorMessage: extractUpstreamErrorMessage, IsBudgetError: isThinkingBudgetConstraintError, RectifyBudget: RectifyThinkingBudget, ReplaceBody: replace}
	o.Build = func(ctx context.Context, body []byte) (*http.Request, []byte, error) {
		return s.buildUpstreamRequest(ctx, c, account, body, token, tokenType, model, stream, mimic)
	}
	o.Do = func(req *http.Request) (*http.Response, error) {
		return s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
	}
	o.ShouldRectify = func(ctx context.Context, body []byte) bool {
		return s.shouldRectifySignatureError(ctx, account, body, model)
	}
	o.IsSignatureError = func(ctx context.Context, body []byte) bool { return s.isSignatureErrorPattern(ctx, account, body) }
	o.BudgetEnabled = func(ctx context.Context) bool { return s.settingService.IsBudgetRectifierEnabled(ctx) }
	o.ShouldRetry = func(status int) bool { return s.shouldRetryUpstreamError(account, status) }
	o.FilterThinking = func(body []byte) []byte { return FilterThinkingBlocksForRetry(body, model) }
	o.FilterTools = func(body []byte) []byte { return FilterSignatureSensitiveBlocksForRetry(body, model) }
	o.Observe = func(e claude.ExchangeNotice) {
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{Platform: e.Platform, AccountID: e.AccountID, AccountName: e.AccountName, UpstreamStatusCode: e.UpstreamStatusCode, UpstreamRequestID: e.UpstreamRequestID, UpstreamURL: e.UpstreamURL, Kind: e.Kind, Message: e.Message, Detail: e.Detail})
	}
	o.Detail = func(body []byte) string {
		if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
			return truncateString(string(body), s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
		}
		return ""
	}
	o.TransportError = func(ctx context.Context, err error, url string) error {
		return s.handleUpstreamTransportError(ctx, c, account, err, OpsUpstreamErrorEvent{UpstreamURL: safeUpstreamURL(url)})
	}
	o.DebugHeaders = account.Platform == PlatformGemini && s.cfg != nil && s.cfg.Gateway.GeminiDebugResponseHeaders
	return o
}
