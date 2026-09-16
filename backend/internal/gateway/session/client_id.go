// 客户端会话字段是关联值，不是登录凭据或调度会话对象。
package session

import (
	"strings"
	"unicode/utf8"
)

// MaxPersistedSessionIDLength 保留既有数据库列宽；超长整值拒绝，不截断。
const MaxPersistedSessionIDLength = 255

// SanitizeClientSessionID 规范化客户端提供的原始会话标识：去除首尾空白，包含控制字符
// （CR、LF、制表符、NUL 等）或超过数据库列上限时整值拒绝，防止日志或请求头
// 注入内容进入关联数据。缺失或无效输入返回空字符串。
func SanitizeClientSessionID(raw string) string {
	if !utf8.ValidString(raw) {
		return ""
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	count := 0
	for _, r := range trimmed {
		if r < 0x20 || r == 0x7f {
			// 显式关联标识不应包含控制字符；整值丢弃，避免持久化被篡改或部分注入的内容。
			return ""
		}
		count++
		if count > MaxPersistedSessionIDLength {
			return ""
		}
	}
	return trimmed
}

// ExtractClientSessionID 保留调用方声明的头部优先级，Grok 扩展只在原适用入口启用。
func ExtractClientSessionID(header func(string) string, names []string, grok bool) string {
	for _, name := range names {
		if value := SanitizeClientSessionID(header(name)); value != "" {
			return value
		}
	}
	if grok {
		return SanitizeClientSessionID(header("X-Grok-Conv-Id"))
	}
	return ""
}
