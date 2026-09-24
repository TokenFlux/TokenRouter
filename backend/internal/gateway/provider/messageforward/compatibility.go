package messageforward

import (
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"net/http"

	"strings"
)

func messageFailover400(respBody []byte) bool {
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
