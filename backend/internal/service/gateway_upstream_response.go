package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/util/responseheaders"

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
	return ParseMetadataUserID(metadataUserID) != nil
}

// shouldRectifySignatureError 统一判断是否应触发签名整流（strip thinking blocks 并重试）。
// 根据账号类型检查对应的开关和匹配模式。
func (s *GatewayService) shouldRectifySignatureError(ctx context.Context, account *Account, respBody []byte, mappedModel ...string) bool {
	if len(mappedModel) > 0 && !ShouldRectifyThinkingSignatureError(mappedModel[0]) {
		return false
	}
	if account.Type == AccountTypeAPIKey {
		// API Key 账号：独立开关，一次读取配置
		settings, err := s.settingService.GetRectifierSettings(ctx)
		if err != nil || !settings.Enabled || !settings.APIKeySignatureEnabled {
			return false
		}
		// 先检查内置模式（同 OAuth），再检查自定义关键词
		if s.isThinkingBlockSignatureError(respBody) {
			return true
		}
		return matchSignaturePatterns(respBody, settings.APIKeySignaturePatterns)
	}
	// OAuth/SetupToken/Upstream/Bedrock 等：保持原有行为（内置模式 + 原开关）
	return s.isThinkingBlockSignatureError(respBody) && s.settingService.IsSignatureRectifierEnabled(ctx)
}

// isSignatureErrorPattern 仅做模式匹配，不检查开关。
// 用于已进入重试流程后的二阶段检测（此时开关已在首次调用时验证过）。
func (s *GatewayService) isSignatureErrorPattern(ctx context.Context, account *Account, respBody []byte) bool {
	if s.isThinkingBlockSignatureError(respBody) {
		return true
	}
	if account.Type == AccountTypeAPIKey {
		settings, err := s.settingService.GetRectifierSettings(ctx)
		if err != nil {
			return false
		}
		return matchSignaturePatterns(respBody, settings.APIKeySignaturePatterns)
	}
	return false
}

func matchSignaturePatterns(respBody []byte, patterns []string) bool {
	return claude.MatchSignaturePatterns(respBody, patterns)
}

func (s *GatewayService) isThinkingBlockSignatureError(respBody []byte) bool {
	matched, diagnostic := claude.IsThinkingBlockSignatureError(extractUpstreamErrorMessage(respBody))
	if diagnostic != "" {
		logger.LegacyPrintf("service.gateway", "%s", diagnostic)
	}
	return matched
}

func (s *GatewayService) shouldFailoverOn400(respBody []byte) bool {
	// 只对"可能是兼容性差异导致"的 400 允许切换，避免无意义重试。
	// 默认保守：无法识别则不切换。
	msg := strings.ToLower(strings.TrimSpace(extractUpstreamErrorMessage(respBody)))
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

// ExtractUpstreamErrorMessage 从上游响应体中提取错误消息
// 支持 Claude 风格的错误格式：{"type":"error","error":{"type":"...","message":"..."}}
func ExtractUpstreamErrorMessage(body []byte) string {
	return extractUpstreamErrorMessage(body)
}

func extractUpstreamErrorMessage(body []byte) string {
	return upstream.ExtractErrorMessage(body)
}

func extractUpstreamErrorCode(body []byte) string { return upstream.ExtractErrorCode(body) }

func isCountTokensUnsupported404(statusCode int, body []byte) bool {
	if statusCode != http.StatusNotFound {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(extractUpstreamErrorMessage(body)))
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

func (s *GatewayService) handleErrorResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, requestedModel ...string) (*ForwardResult, error) {
	// 上游返回非成功 HTTP 状态，仍应计入 Ollama Cloud 活动。
	scheduleOllamaCloudUsageActivity(s.deferredService, account)
	body, readErr := s.readUpstreamErrorBody(resp)
	if readErr != nil {
		// 读取失败时 body 可能被截断，错误分类会基于不完整数据；记录日志以便排查，
		// 避免静默吞掉导致误判。
		logger.LegacyPrintf("service.gateway", "[Forward] Failed to fully read upstream error body: Account=%d(%s) Status=%d err=%v",
			account.ID, account.Name, resp.StatusCode, readErr)
	}

	// 调试日志：打印上游错误响应
	logger.LegacyPrintf("service.gateway", "[Forward] Upstream error (non-retryable): Account=%d(%s) Status=%d RequestID=%s Body=%s",
		account.ID, account.Name, resp.StatusCode, resp.Header.Get("x-request-id"), truncateString(string(body), 1000))

	upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(body))
	upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)

	// Print a compact upstream request fingerprint when we hit the Claude Code OAuth
	// credential scope error. This avoids requiring env-var tweaks in a fixed deploy.
	if isClaudeCodeCredentialScopeError(upstreamMsg) && c != nil {
		if v, ok := c.Get(claudeMimicDebugInfoKey); ok {
			if line, ok := v.(string); ok && strings.TrimSpace(line) != "" {
				logger.LegacyPrintf("service.gateway", "[ClaudeMimicDebugOnError] status=%d request_id=%s %s",
					resp.StatusCode,
					resp.Header.Get("x-request-id"),
					line,
				)
			}
		}
	}

	// Enrich Ops error logs with upstream status + message, and optionally a truncated body snippet.
	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = truncateString(string(body), maxBytes)
	}
	setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Kind:               "http_error",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})

	// 处理上游错误，并区分显式策略、自定义未命中和池模式默认绕过。
	decision := upstreamErrorDecisionWithoutPersistence(account, resp.StatusCode)
	if s.rateLimitService != nil {
		if len(requestedModel) > 0 {
			decision = s.rateLimitService.ApplyUpstreamError(ctx, account, resp.StatusCode, resp.Header, body, requestedModel[0])
		} else {
			decision = s.rateLimitService.ApplyUpstreamError(ctx, account, resp.StatusCode, resp.Header, body)
		}
	}
	if decision.ShouldReturnGenericError() {
		MarkResponseCommitted(c)
		c.JSON(http.StatusInternalServerError, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "upstream_error",
				"message": "Upstream gateway error",
			},
		})
		return nil, fmt.Errorf("upstream error: %d (not in custom error codes)", resp.StatusCode)
	}
	if decision.ShouldFailover(account, resp.StatusCode, false) {
		return nil, &UpstreamFailoverError{
			StatusCode:             resp.StatusCode,
			ResponseBody:           body,
			RetryableOnSameAccount: decision.RetryableOnSameAccount(account, resp.StatusCode),
		}
	}

	MarkResponseCommitted(c)

	// 记录上游错误响应体摘要便于排障（可选：由配置控制；不回显到客户端）
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		logger.LegacyPrintf("service.gateway",
			"Upstream error %d (account=%d platform=%s type=%s): %s",
			resp.StatusCode,
			account.ID,
			account.Platform,
			account.Type,
			truncateForLog(body, s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes),
		)
	}

	// 非 failover 错误也支持错误透传规则匹配。
	if status, errType, errMsg, matched := applyErrorPassthroughRule(
		c,
		account.Platform,
		resp.StatusCode,
		body,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed",
	); matched {
		c.JSON(status, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    errType,
				"message": errMsg,
			},
		})

		summary := upstreamMsg
		if summary == "" {
			summary = errMsg
		}
		if summary == "" {
			return nil, fmt.Errorf("upstream error: %d (passthrough rule matched)", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d (passthrough rule matched) message=%s", resp.StatusCode, summary)
	}

	// 根据状态码返回适当的自定义错误响应（不透传上游详细信息）
	var errType, errMsg string
	var statusCode int

	switch resp.StatusCode {
	case 400:
		c.Data(http.StatusBadRequest, "application/json", body)
		summary := upstreamMsg
		if summary == "" {
			summary = truncateForLog(body, 512)
		}
		if summary == "" {
			return nil, fmt.Errorf("upstream error: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, summary)
	case 401:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream authentication failed, please contact administrator"
	case 403:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream access forbidden, please contact administrator"
	case 429:
		statusCode = http.StatusTooManyRequests
		errType = "rate_limit_error"
		errMsg = "Upstream rate limit exceeded, please retry later"
	case 529:
		statusCode = http.StatusServiceUnavailable
		errType = "overloaded_error"
		errMsg = "Upstream service overloaded, please retry later"
	case 500, 502, 503, 504:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream service temporarily unavailable"
	default:
		statusCode = http.StatusBadGateway
		errType = "upstream_error"
		errMsg = "Upstream request failed"
	}

	// 返回自定义错误响应
	c.JSON(statusCode, gin.H{
		"type": "error",
		"error": gin.H{
			"type":    errType,
			"message": errMsg,
		},
	})

	if upstreamMsg == "" {
		return nil, fmt.Errorf("upstream error: %d", resp.StatusCode)
	}
	return nil, fmt.Errorf("upstream error: %d message=%s", resp.StatusCode, upstreamMsg)
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
	case ErrorPolicyCustomMatched, ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case ErrorPolicyCustomSkipped, ErrorPolicyPoolBypassed:
		return decision
	}

	// OAuth/Setup Token 账号的 403：按上游错误策略处理账号状态。
	if account.IsOAuth() && statusCode == 403 {
		decision = s.rateLimitService.ApplyUpstreamError(ctx, account, statusCode, resp.Header, body, requestedModel...)
		logger.LegacyPrintf("service.gateway", "Account %d: applied upstream error policy after %d retries for status %d", account.ID, maxRetryAttempts, statusCode)
	} else {
		// API Key 未配置错误码：不标记账号状态
		logger.LegacyPrintf("service.gateway", "Account %d: upstream error %d after %d retries (not marking account)", account.ID, statusCode, maxRetryAttempts)
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
func (s *GatewayService) handleRetryExhaustedError(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, requestedModel ...string) (*ForwardResult, error) {
	// Capture upstream error body before side-effects consume the stream.
	respBody, _ := s.readUpstreamErrorBody(resp)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(respBody))

	decision := s.handleRetryExhaustedSideEffects(ctx, resp, account, requestedModel...)
	if decision.ShouldReturnGenericError() {
		return s.handleErrorResponse(ctx, resp, c, account, requestedModel...)
	}
	if decision.ShouldFailover(account, resp.StatusCode, false) {
		return nil, &UpstreamFailoverError{
			StatusCode:             resp.StatusCode,
			ResponseBody:           respBody,
			RetryableOnSameAccount: decision.RetryableOnSameAccount(account, resp.StatusCode),
		}
	}
	MarkResponseCommitted(c)

	upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
	upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)

	if isClaudeCodeCredentialScopeError(upstreamMsg) && c != nil {
		if v, ok := c.Get(claudeMimicDebugInfoKey); ok {
			if line, ok := v.(string); ok && strings.TrimSpace(line) != "" {
				logger.LegacyPrintf("service.gateway", "[ClaudeMimicDebugOnError] status=%d request_id=%s %s",
					resp.StatusCode,
					resp.Header.Get("x-request-id"),
					line,
				)
			}
		}
	}

	upstreamDetail := ""
	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		maxBytes := s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
		if maxBytes <= 0 {
			maxBytes = 2048
		}
		upstreamDetail = truncateString(string(respBody), maxBytes)
	}
	setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, upstreamDetail)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  resp.Header.Get("x-request-id"),
		Kind:               "retry_exhausted",
		Message:            upstreamMsg,
		Detail:             upstreamDetail,
	})

	if s.cfg != nil && s.cfg.Gateway.LogUpstreamErrorBody {
		logger.LegacyPrintf("service.gateway",
			"Upstream error %d retries_exhausted (account=%d platform=%s type=%s): %s",
			resp.StatusCode,
			account.ID,
			account.Platform,
			account.Type,
			truncateForLog(respBody, s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes),
		)
	}

	if status, errType, errMsg, matched := applyErrorPassthroughRule(
		c,
		account.Platform,
		resp.StatusCode,
		respBody,
		http.StatusBadGateway,
		"upstream_error",
		"Upstream request failed after retries",
	); matched {
		c.JSON(status, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    errType,
				"message": errMsg,
			},
		})

		summary := upstreamMsg
		if summary == "" {
			summary = errMsg
		}
		if summary == "" {
			return nil, fmt.Errorf("upstream error: %d (retries exhausted, passthrough rule matched)", resp.StatusCode)
		}
		return nil, fmt.Errorf("upstream error: %d (retries exhausted, passthrough rule matched) message=%s", resp.StatusCode, summary)
	}

	// 返回统一的重试耗尽错误响应
	c.JSON(http.StatusBadGateway, gin.H{
		"type": "error",
		"error": gin.H{
			"type":    "upstream_error",
			"message": "Upstream request failed after retries",
		},
	})

	if upstreamMsg == "" {
		return nil, fmt.Errorf("upstream error: %d (retries exhausted)", resp.StatusCode)
	}
	return nil, fmt.Errorf("upstream error: %d (retries exhausted) message=%s", resp.StatusCode, upstreamMsg)
}

// streamingResult 流式响应结果
type streamingResult struct {
	usage            *ClaudeUsage
	firstTokenMs     *int
	clientDisconnect bool // 客户端是否在流式传输过程中断开
}

// partialStreamUsageResult 在流式转发中途出错时，将已观测的 usage 包装为部分结果。
// 无已观测 token 时不生成记录；UpstreamFailoverError 也必须保持结果为 nil，
// 避免重试成功后对同一请求重复计费。
func partialStreamUsageResult(
	resp *http.Response,
	streamResult *streamingResult,
	model string,
	upstreamModel string,
	startTime time.Time,
	requestSpeed string,
	err error,
) *ForwardResult {
	if streamResult == nil || !streamResult.usage.HasObservedTokens() {
		return nil
	}
	var failoverErr *UpstreamFailoverError
	if errors.As(err, &failoverErr) {
		return nil
	}
	usage := *streamResult.usage
	if strings.TrimSpace(usage.Speed) == "" && strings.EqualFold(strings.TrimSpace(requestSpeed), "fast") {
		usage.Speed = "fast"
	}
	requestID := ""
	if resp != nil {
		requestID = resp.Header.Get("x-request-id")
	}
	return &ForwardResult{
		RequestID:        requestID,
		UpstreamHeaders:  resp.Header,
		Usage:            usage,
		Model:            model,
		UpstreamModel:    upstreamModel,
		Stream:           true,
		Duration:         time.Since(startTime),
		FirstTokenMs:     streamResult.firstTokenMs,
		ClientDisconnect: streamResult.clientDisconnect,
	}
}

func (s *GatewayService) handleStreamingResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, startTime time.Time, originalModel, mappedModel string, mimicClaudeCode bool) (*streamingResult, error) {
	options := s.anthropicStreamOptions(c, account)
	result, err := claude.StreamResponse(ctx, resp, upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), options, startTime, originalModel, mappedModel, mimicClaudeCode)
	if result == nil {
		return nil, err
	}
	return &streamingResult{usage: result.Usage, firstTokenMs: result.FirstTokenMs, clientDisconnect: result.ClientDisconnect}, err
}

func (s *GatewayService) parseSSEUsage(data string, usage *ClaudeUsage) {
	protocolanthropic.ParseSSEUsage(data, usage)
}

func applyCacheTTLOverride(usage *ClaudeUsage, target string) bool {
	return claude.ApplyCacheTTLOverride(usage, target)
}

func (s *GatewayService) resolveCacheTTLUsageOverrideTarget(ctx context.Context, account *Account) (string, bool) {
	if account == nil {
		return "", false
	}
	if account.IsCacheTTLOverrideEnabled() {
		return account.GetCacheTTLOverrideTarget(), true
	}
	if account.IsAnthropicOAuthOrSetupToken() && s != nil && s.settingService != nil && s.settingService.IsAnthropicCacheTTL1hInjectionEnabled(ctx) {
		return cacheTTLTarget5m, true
	}
	return "", false
}

func (s *GatewayService) handleNonStreamingResponse(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, originalModel, mappedModel string) (*ClaudeUsage, error) {
	options := s.anthropicResponseOptions(ctx, c, account, mappedModel, false)
	return claude.NonStreamResponse(ctx, resp, upstream.NewOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), options, originalModel, mappedModel)
}

func reconcileCachedTokens(usage map[string]any) bool { return claude.ReconcileCachedTokens(usage) }

// anthropicStreamOptions 保留逐事件动态设置读取和旧账号观测的时机。
func (s *GatewayService) anthropicStreamOptions(c *gin.Context, account *Account) claude.StreamOptions {
	o := claude.StreamOptions{AccountID: account.ID, ToolNames: toolNameRewriteFromContext(c), UpdateWindow: func(ctx context.Context, h http.Header) { s.rateLimitService.UpdateSessionWindow(ctx, account, h) }, OverrideCache: func(ctx context.Context) (string, bool) { return s.resolveCacheTTLUsageOverrideTarget(ctx, account) }, Failover: func(body []byte) error {
		return &UpstreamFailoverError{StatusCode: 502, ResponseBody: body, RetryableOnSameAccount: true}
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
		o.WriteHeaders = func(dst, src http.Header) { responseheaders.WriteFilteredHeaders(dst, src, s.responseHeaderFilter) }
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
	o.WriteHeaders = func(dst, src http.Header) { responseheaders.WriteFilteredHeaders(dst, src, s.responseHeaderFilter) }
	if passthrough {
		if s.rateLimitService == nil {
			o.UpdateWindow = nil
		}
		o.WriteHeaders = func(dst, src http.Header) { writeAnthropicPassthroughResponseHeaders(dst, src, s.responseHeaderFilter) }
	}
	return o
}
