package httpapi

import (
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/egress/provider"
)

// WriteAnthropicPassthroughHeaders 保留显式过滤器与默认双 Header 透传的区别。
func WriteAnthropicPassthroughHeaders(dst, src http.Header, filter *egress.CompiledHeaderFilter) {
	if dst == nil || src == nil {
		return
	}
	if filter != nil {
		provider.WriteFilteredHeaders(dst, src, filter)
		return
	}
	if value := strings.TrimSpace(src.Get("Content-Type")); value != "" {
		dst.Set("Content-Type", value)
	}
	if value := strings.TrimSpace(src.Get("x-request-id")); value != "" {
		dst.Set("x-request-id", value)
	}
}
