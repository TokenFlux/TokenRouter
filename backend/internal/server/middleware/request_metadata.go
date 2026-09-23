package middleware

import (
	"strings"
)

const (
	maxPersistentRequestIDBytes = 64
)

func normalizeCorrelationID(value string) (string, bool) {
	value = strings.TrimSpace(strings.ToValidUTF8(value, ""))
	if value == "" || len(value) > maxPersistentRequestIDBytes {
		return "", false
	}
	// 关联 ID 可能来自不可信请求头，只允许可安全放入日志和 HTTP Header 的 ASCII 字符。
	for _, ch := range []byte(value) {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.' || ch == ':' {
			continue
		}
		return "", false
	}
	return value, true
}
