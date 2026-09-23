package provider

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// NormalizeMessagesDispatchModel 保留分组配置的旧型号规范化。
func NormalizeMessagesDispatchModel(model string) string {
	return strings.TrimSpace(NormalizeOpenAICompatRequestedModel(strings.TrimSpace(model)))
}

// ResolveMessagesDispatchModel 在真正命中跨客户端系列时读取一次动态目录。
func ResolveMessagesDispatchModel(group *routing.Group, model string) string {
	options := routing.MessagesDispatchOptions{
		NormalizeModel: NormalizeMessagesDispatchModel,
		CrossClientModel: func() string {
			options := grok.RuntimeModelMappingOptions()
			if !options.EnableCrossClientMap {
				return ""
			}
			return grok.ModelMappingWithOptions(options)["claude-*"]
		},
	}
	if group != nil {
		options.SkipGroupMapping = account.IsCNProvider(group.Platform)
	}
	return routing.ResolveMessagesDispatchModel(group, model, options)
}
