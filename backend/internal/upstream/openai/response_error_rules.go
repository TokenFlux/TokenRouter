// OpenAI 错误与输出判定只依赖原生报文；账号健康写入与全局重试由外层拥有。
package openai

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const OpenAIPropertyNameAboveMaxLengthCode = "property_name_above_max_length"
const OpenAICapacityShedRetryableClientCode = "server_error"

func OpenAIStreamEventIsPreamble(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "response.created", "response.in_progress":
		return true
	default:
		return false
	}
}

func OpenAIStreamAddedEventStartsClientOutput(payload []byte, eventType string) bool {
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return true
	}

	switch strings.TrimSpace(eventType) {
	case "response.output_item.added":
		item := gjson.GetBytes(payload, "item")
		if !item.Exists() || !item.IsObject() {
			return true
		}
		switch strings.TrimSpace(item.Get("type").String()) {
		case "reasoning":
			if item.Get("encrypted_content").String() != "" {
				return true
			}
			summary := item.Get("summary")
			if !summary.IsArray() {
				return false
			}
			for _, part := range summary.Array() {
				if strings.TrimSpace(part.Get("type").String()) != "summary_text" || part.Get("text").String() != "" {
					return true
				}
			}
			return false
		case "message":
			content := item.Get("content")
			if !content.IsArray() {
				return false
			}
			for _, part := range content.Array() {
				switch strings.TrimSpace(part.Get("type").String()) {
				case "output_text":
					if part.Get("text").String() != "" {
						return true
					}
				case "refusal":
					if part.Get("refusal").String() != "" {
						return true
					}
				default:
					return true
				}
			}
			return false
		case "function_call":
			return item.Get("arguments").String() != ""
		case "custom_tool_call":
			return item.Get("input").String() != ""
		case "compaction":
			return item.Get("encrypted_content").String() != ""
		default:
			return true
		}
	case "response.content_part.added":
		part := gjson.GetBytes(payload, "part")
		if !part.Exists() || !part.IsObject() {
			return true
		}
		switch strings.TrimSpace(part.Get("type").String()) {
		case "output_text":
			return part.Get("text").String() != ""
		case "refusal":
			return part.Get("refusal").String() != ""
		default:
			return true
		}
	case "response.reasoning_summary_part.added":
		part := gjson.GetBytes(payload, "part")
		if !part.Exists() || !part.IsObject() || strings.TrimSpace(part.Get("type").String()) != "summary_text" {
			return true
		}
		return part.Get("text").String() != ""
	default:
		return true
	}
}

func OpenAIStreamDataStartsClientOutput(data, eventType string) bool {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return false
	}
	switch strings.TrimSpace(eventType) {
	case "response.failed":
		return false
	case "error":
		// 上游降载/瞬时故障会先推 {"type":"error"} 帧、再以 response.failed 收尾。
		// 可重试类错误帧不能算客户端输出：一旦把它当首输出 flush，
		// clientOutputStarted 即被固化，随后的 failed 事件永远进不了 pre-output
		// failover 分支，只能把致命错误原样转发给客户端。不可重试类
		// （content_policy / invalid_request 等）维持原样转发，保留上游错误细节。
		payload := []byte(trimmed)
		return !OpenAIStreamFailedEventShouldFailover(payload, ExtractOpenAISSEErrorMessage(payload))
	case "response.output_item.added", "response.content_part.added", "response.reasoning_summary_part.added":
		return OpenAIStreamAddedEventStartsClientOutput([]byte(trimmed), eventType)
	}
	return !OpenAIStreamEventIsPreamble(eventType)
}

// OpenAIStreamDataStartsSemanticTTFT 使用客户端语义事件作为 TTFT 起点；空的
// reasoning/content 结构只表示协议进度，不能提交首输出阶段或阻断故障转移。
func OpenAIStreamDataStartsSemanticTTFT(data, eventType string) bool {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" || trimmed == "[DONE]" {
		return false
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "" && gjson.Valid(trimmed) {
		eventType = strings.TrimSpace(gjson.Get(trimmed, "type").String())
	}
	switch eventType {
	case "response.failed":
		return false
	case "response.completed", "response.done":
		// 仅携带 usage 的终态不是语义输出，不能虚构首 token。
		return wire.StreamDataStartsVisibleOutput(trimmed, eventType)
	case "error":
		payload := []byte(trimmed)
		return !OpenAIStreamFailedEventShouldFailover(payload, ExtractOpenAISSEErrorMessage(payload))
	default:
		return OpenAIStreamDataStartsClientOutput(trimmed, eventType)
	}
}

// OpenAIStreamFailedEventErrorCode 提取流内 failed 事件的错误码（小写），
// 兼容 response.failed 的嵌套形态与裸 error 形态。
func OpenAIStreamFailedEventErrorCode(payload []byte) string {
	code := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "response.error.code").String()))
	if code == "" {
		code = strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "error.code").String()))
	}
	return code
}

// IsOpenAIUpstreamCapacityShedEvent 判断流内 failed 事件是否为上游容量降载信号。
// 上游在容量紧张时会把请求丢进降载路径：HTTP 200 之后立刻推 event: error
// （code=server_is_overloaded / slow_down）并以 response.failed 收尾。
func IsOpenAIUpstreamCapacityShedEvent(payload []byte) bool {
	switch OpenAIStreamFailedEventErrorCode(payload) {
	case "server_is_overloaded", "slow_down":
		return true
	}
	for _, path := range []string{"response.error.message", "error.message", "message"} {
		if IsOpenAICapacityShedMessage(gjson.GetBytes(payload, path).String()) {
			return true
		}
	}
	return false
}

// SanitizeOpenAICapacityShedErrorCodeForClient 把即将写给下游客户端的
// error / response.failed 事件中的容量降载错误码改写为客户端可重试的错误码。
// 走到转发这一步说明网关侧 failover 已不可用（流中途）或已用尽；保留原始降载码
// 只会让客户端就地终止会话。错误消息原样保留；监控与账号状态判定都基于改写前
// 的原始 payload，不受影响。rate_limit 等其他错误码一律不动（客户端依赖
// rate_limit_exceeded 原码解析重试延时）。
func SanitizeOpenAICapacityShedErrorCodeForClient(payload []byte) ([]byte, bool) {
	if len(payload) == 0 || !gjson.ValidBytes(payload) || !IsOpenAIUpstreamCapacityShedEvent(payload) {
		return payload, false
	}
	updated := payload
	changed := false
	for _, path := range []string{"response.error.code", "error.code"} {
		parent := strings.TrimSuffix(path, ".code")
		if !gjson.GetBytes(updated, parent).Exists() {
			continue
		}
		code := strings.ToLower(strings.TrimSpace(gjson.GetBytes(updated, path).String()))
		if code != "" && code != "server_is_overloaded" && code != "slow_down" {
			continue
		}
		next, err := sjson.SetBytes(updated, path, OpenAICapacityShedRetryableClientCode)
		if err != nil {
			return payload, false
		}
		updated = next
		changed = true
	}
	return updated, changed
}

func OpenAIStreamFailedEventSemanticStatus(payload []byte, message string) int {
	if IsOpenAIContextWindowError(message, payload) {
		return http.StatusBadRequest
	}
	// 聚合上游可能在 HTTP 200 的 response.failed 中携带真实状态码；
	// 必须优先保留，才能让任意自定义错误码命中统一账号策略。
	for _, path := range []string{
		"response.error.status_code",
		"response.error.status",
		"error.status_code",
		"error.status",
	} {
		status := int(gjson.GetBytes(payload, path).Int())
		if status >= http.StatusBadRequest && status <= 599 {
			return status
		}
	}

	code := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "response.error.code").String()))
	if code == "" {
		code = strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "error.code").String()))
	}
	errType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "response.error.type").String()))
	if errType == "" {
		errType = strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "error.type").String()))
	}
	combined := strings.TrimSpace(errType + " " + code + " " + strings.ToLower(strings.TrimSpace(message)))
	for _, path := range []string{"response.error.status_code", "error.status_code", "status_code"} {
		if status := int(gjson.GetBytes(payload, path).Int()); status == http.StatusUnauthorized ||
			status == http.StatusForbidden || status == http.StatusTooManyRequests || status == 529 {
			return status
		}
	}
	switch {
	// 上游可能用泛化 invalid_request 类型包装真实限流代码，限流信号优先。
	case strings.Contains(combined, "rate_limit"):
		return http.StatusTooManyRequests
	case strings.Contains(errType, "invalid_request"):
		return http.StatusBadRequest
	case strings.Contains(combined, "authentication") || strings.Contains(combined, "unauthorized") || strings.Contains(combined, "invalid_api_key"):
		return http.StatusUnauthorized
	case strings.Contains(combined, "permission") || strings.Contains(combined, "forbidden") || strings.Contains(combined, "access denied"):
		return http.StatusForbidden
	case IsOpenAIUpstreamAccessStateError(message, payload):
		return http.StatusForbidden
	case IsOpenAIUpstreamCapacityShedEvent(payload):
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}

func OpenAIStreamFailureStatus(payload []byte, message string) int {
	if len(bytes.TrimSpace(payload)) == 0 || !gjson.ValidBytes(payload) {
		return http.StatusBadGateway
	}
	semanticStatus := OpenAIStreamFailedEventSemanticStatus(payload, message)
	// response.failed 可能携带任意明确的 HTTP 状态码（例如 422 自定义策略），
	// 该状态必须传递给统一策略与故障转移错误，而不能退化为 502。
	for _, path := range []string{"response.error.status_code", "response.error.status", "error.status_code", "error.status", "status_code"} {
		status := int(gjson.GetBytes(payload, path).Int())
		if status >= http.StatusBadRequest && status <= 599 {
			return status
		}
	}
	switch semanticStatus {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, 529:
		return semanticStatus
	case http.StatusServiceUnavailable:
		if IsOpenAIUpstreamCapacityShedEvent(payload) {
			return semanticStatus
		}
	}
	return http.StatusBadGateway
}

// OpenAIStreamCredentialAuthFailure distinguishes credential failures from
// request/content permission denials carried inside an HTTP 200 stream. Do not
// infer credential health from free-form 403 messages: providers also use
// permission_error/forbidden/access denied for request-scoped policy failures.
func OpenAIStreamCredentialAuthFailure(payload []byte) bool {
	if len(bytes.TrimSpace(payload)) == 0 || !gjson.ValidBytes(payload) {
		return false
	}
	for _, path := range []string{"response.error.status_code", "error.status_code", "status_code"} {
		if int(gjson.GetBytes(payload, path).Int()) == http.StatusUnauthorized {
			return true
		}
	}
	for _, path := range []string{"response.error.type", "error.type", "type"} {
		errType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, path).String()))
		if errType == "authentication_error" || errType == "authentication_failed" || errType == "unauthorized_error" {
			return true
		}
	}
	for _, path := range []string{"response.error.code", "error.code", "code"} {
		switch strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, path).String())) {
		case "invalid_api_key", "api_key_disabled", "unauthorized", "authentication_error",
			"invalid_token", "access_token_invalid", "token_revoked", "token_invalidated",
			"invalid_credentials", "credential_invalid":
			return true
		}
	}
	return false
}

func OpenAIStream403AccountFailure(payload []byte, message string) bool {
	return IsOpenAIUpstreamAccessStateError(message, payload) || OpenAIStreamCredentialAuthFailure(payload)
}

func OpenAIStreamFailedEventShouldFailover(payload []byte, message string) bool {
	if hit, _, _ := DetectOpenAICyberPolicy(payload); hit {
		return false
	}
	if IsOpenAIContextWindowError(message, payload) {
		return false
	}
	if IsOpenAIUpstreamAccessStateError(message, payload) {
		return true
	}
	semanticStatus := OpenAIStreamFailureStatus(payload, message)
	if semanticStatus == http.StatusForbidden {
		return OpenAIStream403AccountFailure(payload, message)
	}
	// A response.failed event is transported over HTTP 200. Prefer its semantic
	// rate-limit status over a generic/invalid_request error type so it can enter
	// the same 429 retry policy as a regular upstream HTTP response.
	if semanticStatus == http.StatusTooManyRequests {
		return true
	}
	if IsOpenAITransientProcessingError(http.StatusBadRequest, message, payload) {
		return true
	}
	if IsOpenAICyberWarningText(message) || IsOpenAICyberWarningText(string(payload)) {
		return false
	}
	code := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "response.error.code").String()))
	if code == "" {
		code = strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "error.code").String()))
	}
	errType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "response.error.type").String()))
	if errType == "" {
		errType = strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "error.type").String()))
	}
	combined := strings.ToLower(strings.TrimSpace(message + " " + code + " " + errType))
	if combined == "" {
		return true
	}
	nonRetryableMarkers := []string{
		"invalid_request",
		"content_policy",
		"policy",
		"safety",
		"high-risk cyber",
		"not allowed",
		"violat",
	}
	for _, marker := range nonRetryableMarkers {
		if strings.Contains(combined, marker) {
			return false
		}
	}
	return true
}

// OpenAIStreamErrorEventShouldFailover 只处理尚未写出语义内容的裸 error 事件。
// response.failed 仍由统一账号策略处理，避免重复记录或重复切号。
func OpenAIStreamErrorEventShouldFailover(payload []byte, message string) bool {
	if hit, _, _ := DetectOpenAICyberPolicy(payload); hit {
		return false
	}
	if IsOpenAIContextWindowError(message, payload) {
		return false
	}
	if IsOpenAIUpstreamAccessStateError(message, payload) {
		return true
	}
	switch OpenAIStreamFailedEventSemanticStatus(payload, message) {
	case http.StatusForbidden:
		return OpenAIStream403AccountFailure(payload, message)
	case http.StatusUnauthorized, http.StatusTooManyRequests, 529:
		return true
	}
	if IsOpenAITransientProcessingError(http.StatusBadRequest, message, payload) {
		return true
	}
	combined := strings.ToLower(strings.TrimSpace(message + " " +
		gjson.GetBytes(payload, "error.message").String() + " " +
		gjson.GetBytes(payload, "response.error.message").String()))
	return strings.Contains(combined, "temporary") ||
		strings.Contains(combined, "try again") ||
		strings.Contains(combined, "please retry")
}

func IsOpenAITransientProcessingError(upstreamStatusCode int, upstreamMsg string, upstreamBody []byte) bool {
	if upstreamStatusCode < http.StatusBadRequest {
		return false
	}

	hasOpenAIServerOverloadedCode := func(payload []byte) bool {
		code := strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "error.code").String()))
		if code == "" {
			code = strings.ToLower(strings.TrimSpace(gjson.GetBytes(payload, "response.error.code").String()))
		}
		return code == "server_is_overloaded" || code == "slow_down"
	}

	if len(upstreamBody) > 0 && hasOpenAIServerOverloadedCode(upstreamBody) {
		return true
	}
	if IsOpenAICapacityShedMessage(upstreamMsg) ||
		IsOpenAICapacityShedMessage(gjson.GetBytes(upstreamBody, "error.message").String()) ||
		IsOpenAICapacityShedMessage(gjson.GetBytes(upstreamBody, "response.error.message").String()) ||
		(!gjson.ValidBytes(upstreamBody) && IsOpenAICapacityShedMessage(string(upstreamBody))) {
		return true
	}
	if upstreamStatusCode != http.StatusBadRequest && upstreamStatusCode != http.StatusServiceUnavailable {
		return false
	}
	if upstreamStatusCode != http.StatusBadRequest {
		return false
	}

	match := func(text string) bool {
		lower := strings.ToLower(strings.TrimSpace(text))
		if lower == "" {
			return false
		}
		if strings.Contains(lower, "an error occurred while processing your request") {
			return true
		}
		if strings.Contains(lower, "selected model is at capacity") {
			return true
		}
		return strings.Contains(lower, "you can retry your request") &&
			strings.Contains(lower, "help.openai.com") &&
			strings.Contains(lower, "request id")
	}

	if match(upstreamMsg) {
		return true
	}
	if len(upstreamBody) == 0 {
		return false
	}
	if match(gjson.GetBytes(upstreamBody, "error.message").String()) {
		return true
	}
	if match(gjson.GetBytes(upstreamBody, "response.error.message").String()) ||
		match(gjson.GetBytes(upstreamBody, "message").String()) {
		return true
	}
	// A valid JSON error may echo arbitrary request content. Only its explicit
	// error fields are authoritative; scan the whole body only for non-JSON
	// providers that return a plain-text error response.
	return !gjson.ValidBytes(upstreamBody) && match(string(upstreamBody))
}

// IsOpenAIClientInvalidRequestError 仅识别已确认由客户端参数触发的 OpenAI 400。
// 不能只判断 invalid_request_error，否则会把网关字段转换错误也排除出 SLA。
func IsOpenAIClientInvalidRequestError(upstreamStatusCode int, upstreamMsg string, upstreamBody []byte) bool {
	if upstreamStatusCode != http.StatusBadRequest || len(upstreamBody) == 0 {
		return false
	}
	if IsOpenAITransientProcessingError(upstreamStatusCode, upstreamMsg, upstreamBody) {
		return false
	}
	errType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(upstreamBody, "error.type").String()))
	errCode := strings.ToLower(strings.TrimSpace(gjson.GetBytes(upstreamBody, "error.code").String()))
	return errType == "invalid_request_error" && errCode == OpenAIPropertyNameAboveMaxLengthCode
}

// IsOpenAICapacityShedMessage 识别没有稳定错误码时的上游容量降载文案。
func IsOpenAICapacityShedMessage(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.Contains(lower, "server is overloaded") ||
		strings.Contains(lower, "servers are overloaded") ||
		strings.Contains(lower, "servers are currently overloaded")
}

func IsOpenAIRequestScopedCapacityShed(upstreamMsg string, upstreamBody []byte) bool {
	return IsOpenAIUpstreamCapacityShedEvent(upstreamBody) ||
		IsOpenAICapacityShedMessage(upstreamMsg) ||
		(!gjson.ValidBytes(upstreamBody) && IsOpenAICapacityShedMessage(string(upstreamBody)))
}

func IsOpenAIContextWindowError(upstreamMsg string, upstreamBody []byte) bool {
	match := func(text string) bool {
		lower := strings.ToLower(strings.TrimSpace(text))
		if lower == "" {
			return false
		}
		if strings.Contains(lower, "context_too_large") || strings.Contains(lower, "context_length_exceeded") {
			return true
		}
		if strings.Contains(lower, "maximum context length") || strings.Contains(lower, "max context length") {
			return true
		}
		hasExceeded := strings.Contains(lower, "exceed") || strings.Contains(lower, "too large") || strings.Contains(lower, "too long")
		if strings.Contains(lower, "context window") && hasExceeded {
			return true
		}
		if strings.Contains(lower, "context length") && hasExceeded {
			return true
		}
		return strings.Contains(lower, "token limit") &&
			strings.Contains(lower, "context") &&
			hasExceeded
	}

	if match(upstreamMsg) {
		return true
	}
	if len(upstreamBody) == 0 {
		return false
	}
	for _, path := range []string{
		"error.message",
		"response.error.message",
		"message",
		"error.code",
		"response.error.code",
		"code",
	} {
		if match(gjson.GetBytes(upstreamBody, path).String()) {
			return true
		}
	}
	// Do not let echoed request content in a structured JSON error change the
	// retry/client-status classification. Plain-text upstream errors remain
	// supported by scanning the whole body only when it is not valid JSON.
	return !gjson.ValidBytes(upstreamBody) && match(string(upstreamBody))
}

// IsOpenAIUpstreamAccessStateError recognizes provider-side credential state
// failures only from explicit structured codes. Free-form messages may contain
// echoed user input, including inside stream terminal error.message fields.
func IsOpenAIUpstreamAccessStateError(_ string, body []byte) bool {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false
	}
	for _, path := range []string{"error.code", "response.error.code", "detail.code", "code"} {
		if IsOpenAIUpstreamAccessStateCode(gjson.GetBytes(body, path).String()) {
			return true
		}
	}
	return false
}

func IsOpenAIUpstreamAccessStateCode(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "deactivated_workspace" {
		return true
	}
	for _, subject := range []string{"workspace", "account", "organization", "org"} {
		for _, state := range []string{"deactivated", "disabled", "suspended"} {
			if value == subject+"_"+state || value == state+"_"+subject {
				return true
			}
		}
	}
	return false
}

// IsOpenAIHTTPUpstreamAccessStateError is deliberately status-independent:
// known provider codes are durable evidence, while 401/403 messages without
// such a code must flow through the existing authentication/403 policies.
func IsOpenAIHTTPUpstreamAccessStateError(_ int, _ string, body []byte) bool {
	return IsOpenAIUpstreamAccessStateError("", body)
}

func ExtractOpenAISSEErrorMessage(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	for _, path := range []string{"response.error.message", "error.message", "message"} {
		if msg := strings.TrimSpace(gjson.GetBytes(payload, path).String()); msg != "" {
			return logredact.SanitizeUpstreamQueries(msg)
		}
	}
	return logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(payload)))
}

func SanitizeOpenAIResponseFailedEventForClient(payload []byte, eventType string, clientOutputStarted bool) ([]byte, bool) {
	eventType = strings.TrimSpace(eventType)
	isFailedEvent := eventType == "response.failed"
	if (!isFailedEvent && eventType != "error") || len(payload) == 0 || !gjson.ValidBytes(payload) {
		return payload, false
	}
	updated := payload
	// 容量降载码对 Codex CLI 是致命错误；事件既然要写给客户端（failover 已不可用），
	// 就改写为客户端可重试的错误码。error 帧与 response.failed 都要改：上游降载
	// 总是先推 error 帧再收 failed，两帧携带同一个错误。
	if rewritten, changed := SanitizeOpenAICapacityShedErrorCodeForClient(updated); changed {
		updated = rewritten
	}
	if !isFailedEvent {
		return updated, !bytes.Equal(updated, payload)
	}
	if clientOutputStarted && IsOpenAIContextWindowError(ExtractOpenAISSEErrorMessage(payload), payload) {
		errorPath := ""
		switch {
		case gjson.GetBytes(updated, "response.error").Exists():
			errorPath = "response.error"
		case gjson.GetBytes(updated, "error").Exists():
			errorPath = "error"
		}
		if errorPath != "" {
			next, err := sjson.SetBytes(updated, errorPath+".type", "invalid_request_error")
			if err != nil {
				return payload, false
			}
			updated = next
			next, err = sjson.SetBytes(updated, errorPath+".code", "context_length_exceeded")
			if err != nil {
				return payload, false
			}
			updated = next
		}
	}
	if !gjson.GetBytes(updated, "response").Exists() {
		return updated, !bytes.Equal(updated, payload)
	}
	// response.failed 的 response 可能回显完整 instructions/output/tools 等大字段；
	// 客户端只需要错误信息，先保留内部计费/告警使用的原始 payload，再裁剪下发内容。
	for _, path := range []string{
		"response.instructions",
		"response.output",
		"response.usage",
		"response.metadata",
		"response.reasoning",
		"response.tools",
		"response.tool_choice",
		"response.parallel_tool_calls",
		"response.text",
		"response.truncation",
		"response.max_output_tokens",
		"response.incomplete_details",
	} {
		next, err := sjson.DeleteBytes(updated, path)
		if err != nil {
			return payload, false
		}
		updated = next
	}
	return updated, !bytes.Equal(updated, payload)
}

// DetectOpenAICyberPolicy 精确识别 error.code 或 response.error.code 为 cyber_policy 的响应。
func DetectOpenAICyberPolicy(payload []byte) (bool, string, string) {
	code := gjson.GetBytes(payload, "error.code").String()
	if code == "" {
		code = gjson.GetBytes(payload, "response.error.code").String()
	}
	if !strings.EqualFold(strings.TrimSpace(code), "cyber_policy") {
		return false, "", ""
	}
	msg := gjson.GetBytes(payload, "error.message").String()
	if msg == "" {
		msg = gjson.GetBytes(payload, "response.error.message").String()
	}
	return true, "cyber_policy", strings.TrimSpace(msg)
}

// IsOpenAICyberWarningText 判断上游错误文本是否属于 OpenAI cyber 风控拒绝。
func IsOpenAICyberWarningText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	// 只用明确的 cyber 风控锚点命中，避免普通 usage policy/flagged 错误被当成 cyber 拒绝。
	if strings.Contains(lower, "cybersecurity risk") ||
		strings.Contains(lower, "chatgpt.com/cyber") ||
		strings.Contains(lower, "cyber abuse") ||
		strings.Contains(lower, "trusted access for cyber") {
		return true
	}
	return strings.Contains(lower, "cyber") &&
		(strings.Contains(lower, "risk") ||
			strings.Contains(lower, "abuse") ||
			strings.Contains(lower, "security work"))
}
