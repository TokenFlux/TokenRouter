package service

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
)

// lookupQoderModelAlias 仅解析当前公开 alias，未命中的模型由调用方原样透传。
func lookupQoderModelAlias(model string) (qoderModelInfo, bool) {
	model = normalizeQoderAliasModel(model)
	info, ok := defaultQoderModelAliases[model]
	return info, ok
}

// lookupQoderModelAliasForSite 只解析账号站点实际支持的公开 alias。
func lookupQoderModelAliasForSite(site qoder.Site, model string) (qoderModelInfo, bool) {
	model = normalizeQoderAliasModel(model)
	if _, ok := qoder.AliasForSite(site, model); !ok {
		return qoderModelInfo{}, false
	}
	info, ok := defaultQoderModelAliases[model]
	return info, ok
}

func normalizeQoderAliasModel(model string) string {
	return strings.ToLower(strings.TrimSpace(model))
}
