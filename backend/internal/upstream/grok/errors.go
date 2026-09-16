// 供应商报文分类与用量观测不改变账号资格、错误重写或资金处理。
package grok

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
)

// IsGrokContentPolicyRejection 识别 xAI 针对单次请求的内容安全拒绝。
// 这类失败由提示词或媒体内容引起，切换 OAuth 账号无法改变结果，反而会错误消耗账号池。
// 匹配条件必须保持严格：账号权益或封禁消息也可能提到策略，但仍应走正常的账号故障转移路径。
func IsGrokContentPolicyRejection(statusCode int, responseBody []byte) bool {
	if statusCode != http.StatusForbidden || len(responseBody) == 0 {
		return false
	}
	if GrokAccountAccessMessage(string(responseBody)) {
		return false
	}

	var payload any
	if json.Unmarshal(responseBody, &payload) == nil {
		if GrokStructuredAccountAccessMarker(payload) {
			return false
		}
		if GrokStructuredContentPolicyMarker(payload) {
			return true
		}
	}

	return GrokContentPolicyMessage(string(responseBody))
}
func GrokStructuredAccountAccessMarker(value any) bool {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			normalizedKey := NormalizeGrokErrorMarker(key)
			switch normalizedKey {
			case "code", "error_code", "type", "category", "reason":
				if marker, ok := child.(string); ok && IsGrokAccountAccessCode(marker) {
					return true
				}
			}
			if GrokStructuredAccountAccessMarker(child) {
				return true
			}
		}
	case []any:
		for _, child := range node {
			if GrokStructuredAccountAccessMarker(child) {
				return true
			}
		}
	}
	return false
}
func GrokStructuredContentPolicyMarker(value any) bool {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			normalizedKey := NormalizeGrokErrorMarker(key)
			switch normalizedKey {
			case "code", "error_code", "type", "category", "reason":
				if marker, ok := child.(string); ok && IsGrokContentPolicyCode(marker) {
					return true
				}
			}
			if GrokStructuredContentPolicyMarker(child) {
				return true
			}
		}
	case []any:
		for _, child := range node {
			if GrokStructuredContentPolicyMarker(child) {
				return true
			}
		}
	}
	return false
}
func NormalizeGrokErrorMarker(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}
func IsGrokContentPolicyCode(value string) bool {
	switch NormalizeGrokErrorMarker(value) {
	case "content_filter",
		"content_policy",
		"content_policy_violation",
		"content_moderation",
		"cyber_policy",
		"new_sensitive":
		return true
	default:
		return false
	}
}
func IsGrokAccountAccessCode(value string) bool {
	switch NormalizeGrokErrorMarker(value) {
	case "account_suspended",
		"account_disabled",
		"user_suspended",
		"user_disabled",
		"subscription_required",
		"entitlement_required",
		"not_entitled",
		"plan_required":
		// permission-denied 同时用于权益拒绝和请求级安全拦截，交由错误消息进一步判断。
		return true
	default:
		return false
	}
}
func GrokAccountAccessMessage(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, phrase := range []string{
		"account suspended",
		"account has been suspended",
		"account disabled",
		"account has been disabled",
		"user suspended",
		"user has been suspended",
		"subscription required",
		"entitlement required",
		"not entitled",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}
func GrokContentPolicyMessage(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	if lower == "" {
		return false
	}

	// xAI 媒体安全响应会使用这些明确短语，不会与普通账号策略或权益消息混淆。
	for _, phrase := range []string{
		"the moderation feature is not available",
		"image is sensitive",
		"text is sensitive",
		"prohibited content",
		"forbidden content",
		"content policy violation",
		"content policy rejection",
		"content policy rejected",
		"content moderation rejection",
		"content moderation rejected",
		"content moderation blocked",
		"request blocked by content moderation",
		"request rejected by content moderation",
		"request blocked by policy",
		"request rejected by policy",
		"request violates policy",
		"prompt violates content policy",
		"prompt violates policy",
		"input violates content policy",
		"input violates policy",
		"violates usage guidelines",
	} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}

	return false
}
func GrokContentPolicyClientMessage(message string) string {
	if message == "" {
		return "Request blocked by upstream content policy"
	}
	return message
}
func IsGrokDecoderCompatibilityError(statusCode int, responseBody []byte) bool {
	if statusCode != http.StatusUnprocessableEntity || len(responseBody) == 0 {
		return false
	}
	for _, candidate := range GrokStructuredErrorMessageCandidates(responseBody) {
		message := strings.ToLower(candidate)
		decoderSignal := strings.Contains(message, "untagged enum") ||
			strings.Contains(message, "decode") ||
			strings.Contains(message, "deserialize") ||
			strings.Contains(message, "deserializ") ||
			strings.Contains(message, "decoder")
		inputSignal := strings.Contains(message, "modelinput") ||
			strings.Contains(message, "model input") ||
			strings.Contains(message, "input[") ||
			strings.Contains(message, "input.")
		messageContentSignal := (strings.Contains(message, "messages[") ||
			strings.Contains(message, "messages.")) &&
			strings.Contains(message, "content") &&
			strings.Contains(message, "did not match any variant")
		if decoderSignal && (inputSignal || messageContentSignal) {
			return true
		}
	}
	return false
}
func GrokStructuredErrorMessageCandidates(body []byte) []string {
	candidates := make([]string, 0, 6)
	appendCandidate := func(result gjson.Result) {
		if !result.Exists() {
			return
		}
		value := strings.TrimSpace(result.String())
		if value != "" {
			candidates = append(candidates, value)
		}
	}
	appendCandidate(gjson.GetBytes(body, "error.message"))
	appendCandidate(gjson.GetBytes(body, "error.error"))
	errorNode := gjson.GetBytes(body, "error")
	if errorNode.Type == gjson.String {
		appendCandidate(errorNode)
	}
	appendCandidate(gjson.GetBytes(body, "message"))
	appendCandidate(gjson.GetBytes(body, "detail"))
	if !json.Valid(body) {
		if plaintext := strings.TrimSpace(string(body)); plaintext != "" {
			candidates = append(candidates, plaintext)
		}
	}
	return candidates
}
