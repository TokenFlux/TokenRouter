package upstream

import (
	"strings"
)

// IsHTMLResponse 保留原 HTML 前缀识别，不把链路拦截当成结构化账号错误。
func IsHTMLResponse(body []byte) bool {
	trimmed := strings.TrimSpace(strings.ToLower(string(body)))
	return strings.HasPrefix(trimmed, "<!doctype html") ||
		strings.HasPrefix(trimmed, "<html")
}
