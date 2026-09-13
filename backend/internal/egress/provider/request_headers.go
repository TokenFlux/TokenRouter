// 本文件维护 provider 的所属能力；兼容入口复用唯一实现。
package provider

import (
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	http "net/http"
	strings "strings"
)

// ApplyRequestHeaders 在调用方原定时机应用允许覆写，不碰其它请求头或共享客户端。
func ApplyRequestHeaders(headers http.Header, policy egress.EgressPolicy, wireCasing func(string) string) {
	if headers == nil {
		return
	}
	for name, value := range policy.Headers {
		for existing := range headers {
			if strings.EqualFold(existing, name) {
				delete(headers, existing)
			}
		}
		headers[wireCasing(name)] = []string{value}
	}
}
