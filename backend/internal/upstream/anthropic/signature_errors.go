// 本文件只识别平台错误与返回诊断，不读取账号或安装日志状态。
package anthropic

import "strings"

// matchSignaturePatterns 检查响应体是否匹配自定义关键词列表（不区分大小写）。
func MatchSignaturePatterns(respBody []byte, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	bodyLower := strings.ToLower(string(respBody))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(bodyLower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// isThinkingBlockSignatureError 检测是否是thinking block相关错误
// 这类错误可以通过过滤thinking blocks并重试来解决
func IsThinkingBlockSignatureError(message string) (bool, string) {
	msg := strings.ToLower(strings.TrimSpace(message))
	if msg == "" {
		return false, ""
	}

	// 检测signature相关的错误（更宽松的匹配）
	// 例如: "Invalid `signature` in `thinking` block", "***.signature" 等
	if strings.Contains(msg, "signature") {
		return true, ""
	}

	// 检测 thinking block 顺序/类型错误
	// 例如: "Expected `thinking` or `redacted_thinking`, but found `text`"
	if strings.Contains(msg, "expected") && (strings.Contains(msg, "thinking") || strings.Contains(msg, "redacted_thinking")) {
		return true, "[SignatureCheck] Detected thinking block type error"
	}

	// 检测 thinking block 被修改的错误
	// 例如: "thinking or redacted_thinking blocks in the latest assistant message cannot be modified"
	if strings.Contains(msg, "cannot be modified") && (strings.Contains(msg, "thinking") || strings.Contains(msg, "redacted_thinking")) {
		return true, "[SignatureCheck] Detected thinking block modification error"
	}

	// 检测空消息内容错误（可能是过滤 thinking blocks 后导致的，或客户端发送了空 text block）
	// 例如: "all messages must have non-empty content"
	//       "messages: text content blocks must be non-empty"
	if strings.Contains(msg, "non-empty content") || strings.Contains(msg, "empty content") ||
		strings.Contains(msg, "content blocks must be non-empty") {
		return true, "[SignatureCheck] Detected empty content error"
	}

	// 检测 thinking block 缺少 thinking 字段的错误（跨模型切换时常见：
	// 其他模型回过的 assistant 历史里有 type=thinking 但没有 thinking 文本，
	// 喂给开启 extended thinking 的 claude 时会被拒）
	// 例如: "messages.1.content.0.thinking: each thinking block must contain thinking"
	if strings.Contains(msg, "thinking block must contain") {
		return true, "[SignatureCheck] Detected thinking block missing content error"
	}

	return false, ""
}
