// 本文件维护 responseheaders 的所属能力；兼容入口复用唯一实现。
package responseheaders

import (
	config "github.com/TokenFlux/TokenRouter/internal/config"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	provider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	http "net/http"
)

type CompiledHeaderFilter = egress.CompiledHeaderFilter

func CompileHeaderFilter(cfg config.ResponseHeaderConfig) *CompiledHeaderFilter {
	return egress.CompileHeaderFilter(egress.ResponseHeaderOptions{Enabled: cfg.Enabled, AdditionalAllowed: cfg.AdditionalAllowed, ForceRemove: cfg.ForceRemove})
}
func FilterHeaders(src http.Header, filter *CompiledHeaderFilter) http.Header {
	return provider.FilterHeaders(src, filter)
}
func WriteFilteredHeaders(dst http.Header, src http.Header, filter *CompiledHeaderFilter) {
	provider.WriteFilteredHeaders(dst, src, filter)
}
