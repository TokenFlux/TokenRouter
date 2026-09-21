package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	modelidentity "github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/gin-gonic/gin"
)

// isClaudeCodeClient 判断请求是否来自真正的 Claude Code 客户端。
// 判定条件：
//  1. User-Agent 匹配 claude-cli/X.Y.Z（大小写不敏感）
//  2. metadata.user_id 符合 Claude Code 格式（legacy 或 JSON 格式）
//
// 只检查 metadata.user_id 非空不够严格：第三方工具（opencode 等）可能伪造 UA
// 并附带任意 metadata.user_id 字符串，从而绕过 mimicry。必须通过 ParseMetadataUserID
// 验证格式才能确认是真正的 Claude Code 客户端。
func isClaudeCodeClient(userAgent string, metadataUserID string) bool {
	if !claudeCliUserAgentRe.MatchString(userAgent) {
		return false
	}
	return claude.ParseMetadataUserID(metadataUserID) != nil
}

// shouldRectifySignatureError 统一判断是否应触发签名整流（strip thinking blocks 并重试）。
// 根据账号类型检查对应的开关和匹配模式。
func (s *GatewayService) shouldRectifySignatureError(ctx context.Context, account *Account, respBody []byte, mappedModel ...string) bool {
	if len(mappedModel) > 0 && !modelidentity.ShouldRectifyThinkingSignatureError(mappedModel[0]) {
		return false
	}
	if account.Type == capability.AccountTypeAPIKey {
		// API Key 账号：独立开关，一次读取配置
		settings, err := s.settingService.Gateway.GetRectifierSettings(ctx)
		if err != nil || !settings.Enabled || !settings.APIKeySignatureEnabled {
			return false
		}
		// 先检查内置模式（同 OAuth），再检查自定义关键词
		if s.isThinkingBlockSignatureError(respBody) {
			return true
		}
		return claude.MatchSignaturePatterns(respBody, settings.APIKeySignaturePatterns)
	}
	// OAuth/SetupToken/Upstream/Bedrock 等：保持原有行为（内置模式 + 原开关）
	return s.isThinkingBlockSignatureError(respBody) && s.settingService.Gateway.IsSignatureRectifierEnabled(ctx)
}

// isSignatureErrorPattern 仅做模式匹配，不检查开关。
// 用于已进入重试流程后的二阶段检测（此时开关已在首次调用时验证过）。
func (s *GatewayService) isSignatureErrorPattern(ctx context.Context, account *Account, respBody []byte) bool {
	if s.isThinkingBlockSignatureError(respBody) {
		return true
	}
	if account.Type == capability.AccountTypeAPIKey {
		settings, err := s.settingService.Gateway.GetRectifierSettings(ctx)
		if err != nil {
			return false
		}
		return claude.MatchSignaturePatterns(respBody, settings.APIKeySignaturePatterns)
	}
	return false
}

func (s *GatewayService) isThinkingBlockSignatureError(respBody []byte) bool {
	matched, diagnostic := claude.IsThinkingBlockSignatureError(upstream.ExtractErrorMessage(respBody))
	if diagnostic != "" {
		logging.LegacyPrintf("service.gateway", "%s", diagnostic)
	}
	return matched
}

func (s *GatewayService) shouldFailoverOn400(respBody []byte) bool {
	// 只对"可能是兼容性差异导致"的 400 允许切换，避免无意义重试。
	// 默认保守：无法识别则不切换。
	msg := strings.ToLower(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
	if msg == "" {
		return false
	}

	// 缺少/错误的 beta header：换账号/链路可能成功（尤其是混合调度时）。
	// 更精确匹配 beta 相关的兼容性问题，避免误触发切换。
	if strings.Contains(msg, "anthropic-beta") ||
		strings.Contains(msg, "beta feature") ||
		strings.Contains(msg, "requires beta") {
		return true
	}

	// thinking/tool streaming 等兼容性约束（常见于中间转换链路）
	if strings.Contains(msg, "thinking") || strings.Contains(msg, "thought_signature") || strings.Contains(msg, "signature") {
		return true
	}
	if strings.Contains(msg, "tool_use") || strings.Contains(msg, "tool_result") || strings.Contains(msg, "tools") {
		return true
	}

	return false
}

func isCountTokensUnsupported404(statusCode int, body []byte) bool {
	if statusCode != http.StatusNotFound {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(upstream.ExtractErrorMessage(body)))
	if msg == "" {
		return false
	}
	if strings.Contains(msg, "/v1/messages/count_tokens") {
		return true
	}
	return strings.Contains(msg, "count_tokens") && strings.Contains(msg, "not found")
}

func (s *GatewayService) readUpstreamErrorBody(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	limit := gatewayUpstreamErrorBodyReadLimit
	if s != nil && s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody && s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes > int(limit) {
		limit = int64(s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func (s *GatewayService) handleErrorResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, requestedModel ...string) (*forwardcore.MessagesResult, error) {
	adapter := &anthropicErrorAdapter{s: s, c: c, account: account, resp: resp}
	result, err := forwardcore.AnthropicError(ctx, adapter, adapter.input(requestedModel))
	return legacyForwardExecutionResult(result), err
}

func (s *GatewayService) handleRetryExhaustedSideEffects(ctx context.Context, resp *http.Response, account *Account, requestedModel ...string) UpstreamErrorDecision {
	body, _ := s.readUpstreamErrorBody(resp)
	statusCode := resp.StatusCode
	if s.rateLimitService == nil {
		return upstreamErrorDecisionWithoutPersistence(account, statusCode)
	}
	policy := s.rateLimitService.ApplyExplicitErrorPolicy(ctx, account, statusCode, body, requestedModel...)
	decision := UpstreamErrorDecision{Policy: policy}
	switch policy {
	case accountcore.ErrorPolicyCustomMatched, accountcore.ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case accountcore.ErrorPolicyCustomSkipped, accountcore.ErrorPolicyPoolBypassed:
		return decision
	}

	// OAuth/Setup Token 账号的 403：按上游错误策略处理账号状态。
	if account.IsOAuth() && statusCode == 403 {
		decision = s.rateLimitService.ApplyUpstreamError(ctx, account, statusCode, resp.Header, body, requestedModel...)
		logging.LegacyPrintf("service.gateway", "Account %d: applied upstream error policy after %d retries for status %d", account.ID, maxRetryAttempts, statusCode)
	} else {
		// API Key 未配置错误码：不标记账号状态
		logging.LegacyPrintf("service.gateway", "Account %d: upstream error %d after %d retries (not marking account)", account.ID, statusCode, maxRetryAttempts)
	}
	return decision
}

func (s *GatewayService) handleFailoverSideEffects(ctx context.Context, resp *http.Response, account *Account, requestedModel ...string) UpstreamErrorDecision {
	body, _ := s.readUpstreamErrorBody(resp)
	if s.rateLimitService == nil {
		return upstreamErrorDecisionWithoutPersistence(account, resp.StatusCode)
	}
	if len(requestedModel) > 0 {
		return s.rateLimitService.ApplyUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, requestedModel[0])
	}
	return s.rateLimitService.ApplyUpstreamError(ctx, account, resp.StatusCode, resp.Header, body)
}

// handleRetryExhaustedError 处理重试耗尽后的错误
// OAuth 403：按错误策略处理账号状态
// API Key 未配置错误码：仅返回错误，不标记账号
func (s *GatewayService) handleRetryExhaustedError(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, requestedModel ...string) (*forwardcore.MessagesResult, error) {
	adapter := &anthropicErrorAdapter{s: s, c: c, account: account, resp: resp}
	result, err := forwardcore.AnthropicRetryError(ctx, adapter, adapter.input(requestedModel))
	return legacyForwardExecutionResult(result), err
}

// streamingResult 流式响应结果
type streamingResult struct {
	usage            *upstream.TokenUsage
	firstTokenMs     *int
	clientDisconnect bool // 客户端是否在流式传输过程中断开
}

func (s *GatewayService) handleStreamingResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, startTime time.Time, originalModel, mappedModel string, mimicClaudeCode bool) (*streamingResult, error) {
	options := s.anthropicStreamOptions(c, account)
	result, err := claude.StreamResponse(ctx, resp, upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), options, startTime, originalModel, mappedModel, mimicClaudeCode)
	if result == nil {
		return nil, err
	}
	return &streamingResult{usage: result.Usage, firstTokenMs: result.FirstTokenMs, clientDisconnect: result.ClientDisconnect}, err
}

func (s *GatewayService) parseSSEUsage(data string, usage *upstream.TokenUsage) {
	protocolanthropic.ParseSSEUsage(data, usage)
}

func (s *GatewayService) resolveCacheTTLUsageOverrideTarget(ctx context.Context, account *Account) (string, bool) {
	if account == nil {
		return "", false
	}
	if account.IsCacheTTLOverrideEnabled() {
		return account.GetCacheTTLOverrideTarget(), true
	}
	if account.IsAnthropicOAuthOrSetupToken() && s != nil && s.settingService != nil && s.settingService.Gateway.IsAnthropicCacheTTL1hInjectionEnabled(ctx) {
		return cacheTTLTarget5m, true
	}
	return "", false
}

func (s *GatewayService) handleNonStreamingResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, originalModel, mappedModel string) (*upstream.TokenUsage, error) {
	options := s.anthropicResponseOptions(ctx, c, account, mappedModel, false)
	return claude.NonStreamResponse(ctx, resp, upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), options, originalModel, mappedModel)
}

// anthropicStreamOptions 保留逐事件动态设置读取和旧账号观测的时机。
func (s *GatewayService) anthropicStreamOptions(c *gin.Context, account *Account) claude.StreamOptions {
	o := claude.StreamOptions{AccountID: account.ID, ToolNames: toolNameRewriteFromContext(c), UpdateWindow: func(ctx context.Context, h http.Header) { s.rateLimitService.UpdateSessionWindow(ctx, account, h) }, OverrideCache: func(ctx context.Context) (string, bool) { return s.resolveCacheTTLUsageOverrideTarget(ctx, account) }, Failover: func(body []byte) error {
		return &forwardcore.UpstreamFailoverError{StatusCode: 502, ResponseBody: body, RetryableOnSameAccount: true}
	}}
	if c != nil && c.Request != nil {
		o.UserAgent = c.GetHeader("User-Agent")
	}
	if s.cfg != nil {
		o.MaxLineSize = s.cfg.Gateway.MaxLineSize
		if s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
			o.Interval = time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
		}
		if s.cfg.Gateway.StreamKeepaliveInterval > 0 {
			o.Keepalive = time.Duration(s.cfg.Gateway.StreamKeepaliveInterval) * time.Second
		}
	}
	if s.responseHeaderFilter != nil {
		o.WriteHeaders = func(dst, src http.Header) { provider.WriteFilteredHeaders(dst, src, s.responseHeaderFilter) }
	}
	if s.rateLimitService != nil {
		o.OnTimeout = func(ctx context.Context, model string) { s.rateLimitService.HandleStreamTimeout(ctx, account, model) }
	}
	return o
}

// anthropicResponseOptions 只读取当前调用所需的配置，保留原错误与过滤策略的调用位置。
func (s *GatewayService) anthropicResponseOptions(ctx context.Context, c *gin.Context, account *Account, model string, passthrough bool) claude.ResponseOptions {
	o := claude.ResponseOptions{StreamOptions: s.anthropicStreamOptions(c, account), ReadBody: func(r io.Reader) ([]byte, error) {
		return ReadUpstreamResponseBody(r, s.cfg, c, anthropicTooLargeError)
	}, PreserveContentType: s.cfg != nil && !s.cfg.Security.ResponseHeaders.Enabled, ForceCacheBilling: passthrough && IsForceCacheBilling(ctx)}
	o.InvalidJSON = func(ctx context.Context, resp *http.Response, body []byte, err error) error {
		if passthrough {
			return invalidNonStreamingJSONFailoverError(ctx, s.rateLimitService, resp, account, body, err)
		}
		return invalidNonStreamingJSONFailoverError(ctx, s.rateLimitService, resp, account, body, err, model)
	}
	o.WriteHeaders = func(dst, src http.Header) { provider.WriteFilteredHeaders(dst, src, s.responseHeaderFilter) }
	if passthrough {
		if s.rateLimitService == nil {
			o.UpdateWindow = nil
		}
		o.WriteHeaders = func(dst, src http.Header) {
			gatewayhttp.WriteAnthropicPassthroughHeaders(dst, src, s.responseHeaderFilter)
		}
	}
	return o
}
