package provider

import (
	"net/http"
	"strings"
	"unicode/utf8"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

const (
	// maxUsageUpstreamRequestIDLen 与 usage_logs.upstream_request_id VARCHAR(128) 对齐。
	maxUsageUpstreamRequestIDLen = 128
)

// UpstreamRequestIDFromHeaders 从直接上游的响应头解析请求标识。
// 只读账户指定的头；账户未指定头名时恒为空串。
func UpstreamRequestIDFromHeaders(account *acctcore.Record, h http.Header) string {
	if len(h) == 0 {
		return ""
	}
	name := acctcore.UpstreamRequestIDHeaderName(account)
	if name == "" {
		return ""
	}
	return strings.TrimSpace(h.Get(name))
}

// usageUpstreamRequestIDPtr 生成落库到 usage_logs.upstream_request_id 的值。
// WS 轮次没有 HTTP 响应头，保持 nil；超长时截断到列宽而不是让整条用量行失败。
func usageUpstreamRequestIDPtr(account *acctcore.Record, h http.Header, wsMode bool) *string {
	if wsMode {
		return nil
	}
	id := UpstreamRequestIDFromHeaders(account, h)
	if id == "" {
		return nil
	}
	if len(id) > maxUsageUpstreamRequestIDLen {
		id = id[:maxUsageUpstreamRequestIDLen]
		for len(id) > 0 && !utf8.ValidString(id) {
			id = id[:len(id)-1]
		}
	}
	if id == "" {
		return nil
	}
	return &id
}
