package policy

import (
	"fmt"
	"net/textproto"
	"strings"

	"golang.org/x/net/http/httpguts"
)

// MaxForwardedClientIPHeaders 保留原自定义客户端地址 Header 数量上限。
const MaxForwardedClientIPHeaders = 16

// NormalizeForwardedClientIPHeaders 只处理名称规范化、去重和边界，不访问 HTTP 请求或配置。
func NormalizeForwardedClientIPHeaders(headers []string) ([]string, error) {
	normalized := make([]string, 0, len(headers))
	seen := make(map[string]struct{}, len(headers))
	for _, header := range headers {
		header = strings.TrimSpace(header)
		if !httpguts.ValidHeaderFieldName(header) {
			return nil, fmt.Errorf("invalid HTTP header field name %q", header)
		}
		canonical := textproto.CanonicalMIMEHeaderKey(header)
		key := strings.ToLower(canonical)
		if _, exists := seen[key]; exists {
			continue
		}
		if len(normalized) == MaxForwardedClientIPHeaders {
			return nil, fmt.Errorf("forwarded client IP headers must contain at most %d unique names", MaxForwardedClientIPHeaders)
		}
		seen[key] = struct{}{}
		normalized = append(normalized, canonical)
	}
	return normalized, nil
}
