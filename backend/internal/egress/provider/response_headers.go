// 本文件维护 provider 的所属能力；兼容入口复用唯一实现。
package provider

import (
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	http "net/http"
)

func FilterHeaders(src http.Header, filter *egress.CompiledHeaderFilter) http.Header {

	filtered := make(http.Header, len(src))
	for key, values := range src {
		if !filter.Allows(key) {
			continue
		}
		for _, value := range values {
			filtered.Add(key, value)
		}
	}
	return filtered
}

func WriteFilteredHeaders(dst http.Header, src http.Header, filter *egress.CompiledHeaderFilter) {
	filtered := FilterHeaders(src, filter)
	for key, values := range filtered {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
