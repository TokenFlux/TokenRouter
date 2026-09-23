package routing

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

func claudeMessagesDispatchFamily(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if !strings.HasPrefix(normalized, "claude") {
		return ""
	}
	switch {
	case strings.Contains(normalized, "opus"):
		return "opus"
	case strings.Contains(normalized, "sonnet"):
		return "sonnet"
	case strings.Contains(normalized, "haiku"):
		return "haiku"
	default:
		return ""
	}
}

// ResolveMessagesDispatchModel 保持精确映射优先及平台隔离，动态来源按需调用。
func ResolveMessagesDispatchModel(g *Group, requestedModel string, options MessagesDispatchOptions) string {
	if g == nil {
		return ""
	}
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return ""
	}

	if g.Platform == capability.PlatformGrok {
		if claudeMessagesDispatchFamily(requestedModel) == "" {
			return ""
		}
		return options.CrossClientModel()
	}

	// 国产供应商不使用 OpenAI Messages 的分组级模型映射；模型改写由渠道与
	// 账号 model_mapping 完成，避免历史脏配置把 GPT 模型发送给 CN 上游。
	if options.SkipGroupMapping {
		return ""
	}

	cfg := NormalizeMessagesDispatchConfig(g.MessagesDispatchModelConfig, options.NormalizeModel)
	if mappedModel := strings.TrimSpace(cfg.ExactModelMappings[requestedModel]); mappedModel != "" {
		return mappedModel
	}

	// 系列映射只在管理员显式配置非空目标时生效，空值表示保持请求模型。
	switch claudeMessagesDispatchFamily(requestedModel) {
	case "opus":
		return strings.TrimSpace(cfg.OpusMappedModel)
	case "sonnet":
		return strings.TrimSpace(cfg.SonnetMappedModel)
	case "haiku":
		return strings.TrimSpace(cfg.HaikuMappedModel)
	default:
		return ""
	}
}

// MessagesDispatchOptions 保留平台型号的按需读取，核心不引用具体供应商。
type MessagesDispatchOptions struct {
	NormalizeModel   func(string) string
	CrossClientModel func() string
	SkipGroupMapping bool
}
