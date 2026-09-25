package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func (s *OpenAIWebSocketExecutor) isOpenAIWSGeneratePrewarmEnabled() bool {
	return s != nil && s.Options != nil && s.Options.PrewarmGenerateEnabled
}

// performOpenAIWSGeneratePrewarm 在 WSv2 下执行可选的 generate=false 预热。
// 预热默认关闭，仅在配置开启后生效；失败时按可恢复错误回退到 HTTP。
func (s *OpenAIWebSocketExecutor) performOpenAIWSGeneratePrewarm(
	ctx context.Context,
	lease *upstreamopenai.WSConnLease,
	decision egress.OpenAIWSProtocolDecision,
	payload map[string]any,
	previousResponseID string,
	reqBody map[string]any,
	canonicalModel string,
	account *gatewayprovider.ExecutionAccount,
	stateStore session.OpenAIWSStateStore,
	groupID int64,
) error {
	if s == nil {
		return nil
	}
	if lease == nil || account == nil {
		gatewayprovider.LogOpenAIWSModeInfo("prewarm_skip reason=invalid_state has_lease=%v has_account=%v", lease != nil, account != nil)
		return nil
	}
	connID := strings.TrimSpace(lease.ConnID())
	if !s.isOpenAIWSGeneratePrewarmEnabled() {
		return nil
	}
	if decision.Transport != egress.OpenAIUpstreamTransportResponsesWebsocketV2 {
		gatewayprovider.LogOpenAIWSModeInfo(
			"prewarm_skip account_id=%d conn_id=%s reason=transport_not_v2 transport=%s",
			account.Record.ID,
			connID, gatewayprovider.NormalizeOpenAIWSLogValue(string(decision.Transport)),
		)
		return nil
	}
	if strings.TrimSpace(previousResponseID) != "" {
		gatewayprovider.LogOpenAIWSModeInfo(
			"prewarm_skip account_id=%d conn_id=%s reason=has_previous_response_id previous_response_id=%s",
			account.Record.ID,
			connID, gatewayprovider.TruncateOpenAIWSLogValue(previousResponseID, gatewayprovider.OpenAIWSIDValueMaxLen),
		)
		return nil
	}
	if lease.IsPrewarmed() {
		gatewayprovider.LogOpenAIWSModeInfo("prewarm_skip account_id=%d conn_id=%s reason=already_prewarmed", account.Record.ID, connID)
		return nil
	}
	if openai.NeedsToolContinuation(reqBody) {
		gatewayprovider.LogOpenAIWSModeInfo("prewarm_skip account_id=%d conn_id=%s reason=tool_continuation", account.Record.ID, connID)
		return nil
	}
	prewarmStart := time.Now()
	gatewayprovider.LogOpenAIWSModeInfo("prewarm_start account_id=%d conn_id=%s", account.Record.ID, connID)

	prewarmPayload := make(map[string]any, len(payload)+1)
	for k, v := range payload {
		prewarmPayload[k] = v
	}
	prewarmPayload["generate"] = false
	prewarmPayloadJSON := payloadAsJSONBytes(prewarmPayload)

	if err := lease.WriteJSONWithContextTimeout(ctx, prewarmPayload, s.openAIWSWriteTimeout()); err != nil {
		lease.MarkBroken()
		gatewayprovider.LogOpenAIWSModeInfo(
			"prewarm_write_fail account_id=%d conn_id=%s cause=%s",
			account.Record.ID,
			connID, gatewayprovider.TruncateOpenAIWSLogValue(err.Error(), gatewayprovider.OpenAIWSLogValueMaxLen),
		)
		return ws.WrapFallback("prewarm_write", err)
	}
	gatewayprovider.LogOpenAIWSModeInfo("prewarm_write_sent account_id=%d conn_id=%s payload_bytes=%d", account.Record.ID, connID, len(prewarmPayloadJSON))

	prewarmResponseID := ""
	prewarmEventCount := 0
	prewarmTerminalCount := 0
	for {
		message, readErr := lease.ReadMessageWithContextTimeout(ctx, s.openAIWSReadTimeout())
		if readErr != nil {
			lease.MarkBroken()
			closeStatus, closeReason := gatewayprovider.SummarizeOpenAIWSReadCloseError(readErr)
			gatewayprovider.LogOpenAIWSModeInfo(
				"prewarm_read_fail account_id=%d conn_id=%s close_status=%s close_reason=%s cause=%s events=%d",
				account.Record.ID,
				connID,
				closeStatus,
				closeReason, gatewayprovider.TruncateOpenAIWSLogValue(readErr.Error(), gatewayprovider.OpenAIWSLogValueMaxLen), prewarmEventCount,
			)
			return ws.WrapFallback("prewarm_"+gatewayprovider.ClassifyOpenAIWSReadFallbackReason(readErr), readErr)
		}

		eventType, eventResponseID, _ := openai.ParseWSEventEnvelope(message)
		if eventType == "" {
			continue
		}
		prewarmEventCount++
		if prewarmResponseID == "" && eventResponseID != "" {
			prewarmResponseID = eventResponseID
		}
		if prewarmEventCount <= openAIWSPrewarmEventLogHead || eventType == "error" || openai.IsWSTerminalEvent(eventType) {
			gatewayprovider.LogOpenAIWSModeInfo(
				"prewarm_event account_id=%d conn_id=%s idx=%d type=%s bytes=%d",
				account.Record.ID,
				connID,
				prewarmEventCount, gatewayprovider.TruncateOpenAIWSLogValue(eventType, gatewayprovider.OpenAIWSLogValueMaxLen), len(message),
			)
		}

		if eventType == "error" {
			errCodeRaw, errTypeRaw, errMsgRaw := openai.ParseWSErrorEventFields(message)
			errMsg := strings.TrimSpace(errMsgRaw)
			if errMsg == "" {
				errMsg = "OpenAI websocket prewarm error"
			}
			fallbackReason, canFallback := upstreamopenai.ClassifyWSErrorEventFromRaw(errCodeRaw, errTypeRaw, errMsgRaw)
			errCode, errType, errMessage := gatewayprovider.SummarizeOpenAIWSErrorEventFieldsFromRaw(errCodeRaw, errTypeRaw, errMsgRaw)
			gatewayprovider.LogOpenAIWSModeInfo(
				"prewarm_error_event account_id=%d conn_id=%s idx=%d fallback_reason=%s can_fallback=%v err_code=%s err_type=%s err_message=%s",
				account.Record.ID,
				connID,
				prewarmEventCount, gatewayprovider.TruncateOpenAIWSLogValue(fallbackReason, gatewayprovider.OpenAIWSLogValueMaxLen), canFallback,
				errCode,
				errType,
				errMessage,
			)
			lease.MarkBroken()
			statusCode := openAIWSErrorPolicyStatus(message)
			errorDecision := s.handleOpenAIWSErrorEventTransientFailure(
				ctx, account, canonicalModel, lease.HandshakeHeaders(), message,
			)
			if errorDecision.ShouldReturnGenericError() {
				return ws.NewGenericPolicyError(statusCode)
			}
			if errorDecision.ShouldFailoverWithDefaults(gatewayprovider.ExecutionErrorPolicy(account), statusCode, false, s.shouldFailoverOpenAIWSError(account, statusCode, message)) {
				return gatewayprovider.NewOpenAIUpstreamFailure(
					statusCode,
					lease.HandshakeHeaders(),
					message,
					errMsg,
					errorDecision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(account), statusCode),
				)
			}
			if canFallback {
				return ws.WrapFallback("prewarm_"+fallbackReason, errors.New(errMsg))
			}
			return ws.WrapFallback("prewarm_error_event", errors.New(errMsg))
		}

		if openai.IsWSTerminalEvent(eventType) {
			prewarmTerminalCount++
			break
		}
	}

	lease.MarkPrewarmed()
	if prewarmResponseID != "" && stateStore != nil {
		ttl := s.OpenAIHTTPResponseStickyTTL()
		gatewayprovider.LogOpenAIWSBindResponseAccountWarn(groupID, account.Record.ID, prewarmResponseID, stateStore.BindResponseAccount(ctx, groupID, prewarmResponseID, account.Record.ID, ttl))
		stateStore.BindResponseConn(prewarmResponseID, lease.ConnID(), ttl)
	}
	gatewayprovider.LogOpenAIWSModeInfo(
		"prewarm_done account_id=%d conn_id=%s response_id=%s events=%d terminal_events=%d duration_ms=%d",
		account.Record.ID,
		connID, gatewayprovider.TruncateOpenAIWSLogValue(prewarmResponseID, gatewayprovider.OpenAIWSIDValueMaxLen), prewarmEventCount,
		prewarmTerminalCount,
		time.Since(prewarmStart).Milliseconds(),
	)
	return nil
}

func payloadAsJSON(payload map[string]any) string {
	return string(payloadAsJSONBytes(payload))
}

func payloadAsJSONBytes(payload map[string]any) []byte {
	if len(payload) == 0 {
		return []byte("{}")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return []byte("{}")
	}
	return body
}

func normalizeOpenAIWSTerminalEvent(eventType string) string {
	switch strings.TrimSpace(eventType) {
	case "response.completed":
		return "response.completed"
	case "response.done":
		return "response.done"
	case "response.failed":
		return "response.failed"
	case "response.incomplete":
		return "response.incomplete"
	case "response.cancelled", "response.canceled":
		return "response.cancelled"
	default:
		return ""
	}
}

func openAIWSPayloadTransientStatus(payload []byte) int {
	if len(payload) == 0 {
		return 0
	}
	status := int(gjson.GetBytes(payload, "response.error.status_code").Int())
	if status == 0 {
		status = int(gjson.GetBytes(payload, "response.error.status").Int())
	}
	if status == 0 {
		status = int(gjson.GetBytes(payload, "error.status_code").Int())
	}
	if status == 0 {
		status = int(gjson.GetBytes(payload, "error.status").Int())
	}
	if gatewayprovider.IsTransientAccountFailure(status, payload) {
		return status
	}
	if status != 0 {
		return 0
	}
	code := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "response.error.code").String()))
	errType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "response.error.type").String()))
	if code == "" {
		code = strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "error.code").String()))
	}
	if errType == "" {
		errType = strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "error.type").String()))
	}
	switch {
	case code == "server_is_overloaded", code == "slow_down":
		return http.StatusServiceUnavailable
	case strings.Contains(code, "server_error"),
		strings.Contains(code, "internal_error"),
		strings.Contains(code, "upstream_error"),
		strings.Contains(errType, "server_error"),
		strings.Contains(errType, "internal_error"),
		strings.Contains(errType, "upstream_error"):
		return http.StatusInternalServerError
	default:
		return 0
	}
}

// openAIWSErrorPolicyStatus 解析 WS 错误事件用于账号策略的状态码。
// 事件显式携带状态码时必须原样保留，否则按既有 WS 错误类型映射，避免瞬态推断改变自定义规则的匹配值。
func openAIWSErrorPolicyStatus(payload []byte) int {
	if len(payload) == 0 {
		return 0
	}
	for _, path := range []string{
		"error.status_code",
		"error.status",
		"response.error.status_code",
		"response.error.status",
	} {
		status := int(gjson.GetBytes(payload, path).Int())
		if status >= http.StatusBadRequest && status <= 599 {
			return status
		}
	}
	codeRaw, errTypeRaw, _ := openai.ParseWSErrorEventFields(payload)
	if codeRaw == "" {
		codeRaw = strings.TrimSpace(gjson.GetBytes(payload, "response.error.code").String())
	}
	if errTypeRaw == "" {
		errTypeRaw = strings.TrimSpace(gjson.GetBytes(payload, "response.error.type").String())
	}
	return upstreamopenai.WSErrorHTTPStatusFromRaw(codeRaw, errTypeRaw)
}

// openAIWSTerminalPolicyDecision 保留终止事件类型及其账号策略结果，
// 调用方必须在写给客户端前判断通用错误和故障转移。
type openAIWSTerminalPolicyDecision struct {
	TerminalEvent string
	StatusCode    int
	Decision      accountcore.UpstreamErrorDecision
}

func (s *OpenAIWebSocketExecutor) handleOpenAIWSTerminalTransientFailure(ctx context.Context, account *gatewayprovider.ExecutionAccount, canonicalModel string, headers http.Header, payload []byte) openAIWSTerminalPolicyDecision {
	eventType, _, _ := openai.ParseWSEventEnvelope(payload)
	result := openAIWSTerminalPolicyDecision{
		TerminalEvent: normalizeOpenAIWSTerminalEvent(eventType),
		Decision:      accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone},
	}
	if result.TerminalEvent != "response.failed" {
		return result
	}
	result.StatusCode = openAIWSErrorPolicyStatus(payload)
	if result.StatusCode != 0 {
		if result.StatusCode == http.StatusTooManyRequests {
			headers = gatewayprovider.OpenAISemantic429Headers(account, canonicalModel, headers)
		}
		result.Decision = s.applyOpenAIWSEventErrorPolicy(ctx, account, canonicalModel, result.StatusCode, headers, payload)
	}
	return result
}

func (s *OpenAIWebSocketExecutor) handleOpenAIWSErrorEventTransientFailure(ctx context.Context, account *gatewayprovider.ExecutionAccount, canonicalModel string, headers http.Header, payload []byte) accountcore.UpstreamErrorDecision {
	eventType, _, _ := openai.ParseWSEventEnvelope(payload)
	if eventType != "error" {
		return accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
	}
	status := openAIWSErrorPolicyStatus(payload)
	if status == http.StatusTooManyRequests {
		headers = gatewayprovider.OpenAISemantic429Headers(account, canonicalModel, headers)
	}
	return s.applyOpenAIWSEventErrorPolicy(ctx, account, canonicalModel, status, headers, payload)
}

// markOpenAIWSClientVisibleFailure 记录已经写给客户端的 WS 错误事件，避免把
// 已经完成故障转移的内部错误重复计入 Ops。
func markOpenAIWSClientVisibleFailure(c *gin.Context, eventType string, payload []byte) {
	eventType = strings.TrimSpace(eventType)
	if eventType != "error" && eventType != "response.failed" {
		return
	}
	prefix := "error"
	if eventType == "response.failed" {
		prefix = "response.error"
	}
	code := strings.TrimSpace(gjson.GetBytes(payload, prefix+".code").String())
	errType := strings.TrimSpace(gjson.GetBytes(payload, prefix+".type").String())
	message := strings.TrimSpace(gjson.GetBytes(payload, prefix+".message").String())
	if eventType == "response.failed" && code == "" && errType == "" && message == "" {
		prefix = "error"
		code = strings.TrimSpace(gjson.GetBytes(payload, prefix+".code").String())
		errType = strings.TrimSpace(gjson.GetBytes(payload, prefix+".type").String())
		message = strings.TrimSpace(gjson.GetBytes(payload, prefix+".message").String())
	}
	status := int(gjson.GetBytes(payload, prefix+".status_code").Int())
	if status == 0 {
		status = int(gjson.GetBytes(payload, prefix+".status").Int())
	}
	if status == 0 && eventType == "error" {
		status = int(gjson.GetBytes(payload, "status").Int())
	}
	if status == 0 {
		status = upstreamopenai.WSErrorHTTPStatusFromRaw(code, errType)
	}
	if errType == "" {
		errType = "upstream_error"
	}
	if code == "" {
		code = strings.ReplaceAll(eventType, ".", "_")
	}
	if message == "" {
		message = "upstream websocket request failed"
	}
	MarkOpsStreamFailure(c, errType, code, message, status)
}

// handleOpenAIWSFailureAccountSideEffects 将 WS 错误事件映射到账号健康策略，
// 返回值用于成对的 error/response.failed 事件去重。
func (s *OpenAIWebSocketExecutor) handleOpenAIWSFailureAccountSideEffects(ctx context.Context, account *gatewayprovider.ExecutionAccount, canonicalModel string, headers http.Header, payload []byte) bool {
	message := upstreamopenai.ExtractOpenAISSEErrorMessage(payload)
	status := upstreamopenai.OpenAIStreamFailureStatus(payload, message)
	switch status {
	case http.StatusUnauthorized, http.StatusTooManyRequests, 529:
		s.Output.TerminalAccountEffects(nil, account, payload, message, headers, canonicalModel)
		return true
	case http.StatusForbidden:
		if !upstreamopenai.OpenAIStream403AccountFailure(payload, message) {
			return false
		}
		s.Output.TerminalAccountEffects(nil, account, payload, message, headers, canonicalModel)
		return true
	}
	status = openAIWSPayloadTransientStatus(payload)
	if status == 0 {
		return false
	}
	gatewayprovider.ApplyOpenAIResponseHealth(ctx, s.Output.Health, account, status, headers, payload, false, canonicalModel)
	return true
}

func (s *OpenAIWebSocketExecutor) handleOpenAIWSDialTransientFailure(ctx context.Context, account *gatewayprovider.ExecutionAccount, canonicalModel string, err error) accountcore.UpstreamErrorDecision {
	var dialErr *upstreamopenai.WSDialError
	if !errors.As(err, &dialErr) || dialErr == nil {
		return accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
	}
	return s.applyOpenAIWSEventErrorPolicy(ctx, account, canonicalModel, dialErr.StatusCode, dialErr.ResponseHeaders, dialErr.ResponseBody)
}

// applyOpenAIWSEventErrorPolicy 将握手和事件错误接入统一账号策略。
// 请求级错误保持原样，响应尚未输出时由调用方依据返回决策决定是否故障转移。
func (s *OpenAIWebSocketExecutor) applyOpenAIWSEventErrorPolicy(
	ctx context.Context,
	account *gatewayprovider.ExecutionAccount,
	canonicalModel string,
	statusCode int,
	headers http.Header,
	payload []byte,
) accountcore.UpstreamErrorDecision {
	if statusCode == 0 || gatewayprovider.OpenAIWSHTTPBridgeRequestScopedError(account, statusCode, upstream.ExtractErrorMessage(payload), payload) {
		return accountcore.UpstreamErrorDecision{Policy: accountcore.ErrorPolicyNone}
	}
	if account != nil && account.Record.Platform == capability.PlatformGrok {
		return gatewayprovider.ApplyGrokExecutionHealth(ctx, s.Output.GrokHealth, account, statusCode, headers, payload, "", canonicalModel)
	}
	return gatewayprovider.ApplyOpenAIResponseHealth(ctx, s.Output.Health, account, statusCode, headers, payload, false, canonicalModel)
}

// shouldFailoverOpenAIWSError 使用对应平台的 HTTP 错误分类作为 WS 握手和事件错误的默认切号规则。
func (s *OpenAIWebSocketExecutor) shouldFailoverOpenAIWSError(account *gatewayprovider.ExecutionAccount, statusCode int, payload []byte) bool {
	if statusCode == 0 {
		return false
	}
	if account != nil && account.Record.Platform == capability.PlatformGrok {
		return gatewayprovider.ShouldFailoverGrokResponse(statusCode, payload)
	}
	upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(payload)))
	return gatewayprovider.ShouldFailoverOpenAIResponse(statusCode, upstreamMsg, payload)
}

// openAIWSGenericPolicyCloseError 在 WS 入站尚未输出时用统一文案终止连接。
func openAIWSGenericPolicyCloseError(statusCode int) error {
	return NewOpenAIWSClientCloseError(
		coderws.StatusInternalError,
		"Upstream gateway error", ws.NewGenericPolicyError(statusCode),
	)
}

// newOpenAIWSRateLimitFailoverError 保留 WS 限流响应头并允许 OAuth 账号短暂原地重试。
func (s *OpenAIWebSocketExecutor) newOpenAIWSRateLimitFailoverError(account *gatewayprovider.ExecutionAccount, headers http.Header, responseBody []byte, message string) *forwardcore.UpstreamFailoverError {
	return (gatewayprovider.OpenAIFailoverPolicy{Health: s.Output.Health}).NewAccountFailure(
		account,
		http.StatusTooManyRequests,
		headers,
		responseBody,
		strings.TrimSpace(message),
		false,
		false,
	)
}

func (s *OpenAIWebSocketExecutor) openAIWSFallbackCooldown() time.Duration {
	if s == nil || s.Options == nil {
		return 30 * time.Second
	}
	seconds := s.Options.FallbackCooldownSeconds
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func (s *OpenAIWebSocketExecutor) isOpenAIWSFallbackCooling(accountID int64) bool {
	if s == nil || accountID <= 0 {
		return false
	}
	cooldown := s.openAIWSFallbackCooldown()
	if cooldown <= 0 {
		return false
	}
	rawUntil, ok := s.openaiWSFallbackUntil.Load(accountID)
	if !ok || rawUntil == nil {
		return false
	}
	until, ok := rawUntil.(time.Time)
	if !ok || until.IsZero() {
		s.openaiWSFallbackUntil.Delete(accountID)
		return false
	}
	if time.Now().Before(until) {
		return true
	}
	s.openaiWSFallbackUntil.Delete(accountID)
	return false
}

func (s *OpenAIWebSocketExecutor) markOpenAIWSFallbackCooling(accountID int64, _ string) {
	if s == nil || accountID <= 0 {
		return
	}
	cooldown := s.openAIWSFallbackCooldown()
	if cooldown <= 0 {
		return
	}
	s.openaiWSFallbackUntil.Store(accountID, time.Now().Add(cooldown))
}

func (s *OpenAIWebSocketExecutor) clearOpenAIWSFallbackCooling(accountID int64) {
	if s == nil || accountID <= 0 {
		return
	}
	s.openaiWSFallbackUntil.Delete(accountID)
}
