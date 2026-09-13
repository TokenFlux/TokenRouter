package service

import (
	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"strings"
)

func normalizeOpenAIMessagesDispatchMappedModel(model string) string {
	model = NormalizeOpenAICompatRequestedModel(strings.TrimSpace(model))
	return strings.TrimSpace(model)
}

func normalizeOpenAIMessagesDispatchModelConfig(cfg OpenAIMessagesDispatchModelConfig) OpenAIMessagesDispatchModelConfig {
	return routing.NormalizeMessagesDispatchConfig(cfg, normalizeOpenAIMessagesDispatchMappedModel)
}

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

func (g *Group) ResolveMessagesDispatchModel(requestedModel string) string {
	if g == nil {
		return ""
	}
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return ""
	}

	if g.Platform == PlatformGrok {
		if claudeMessagesDispatchFamily(requestedModel) == "" {
			return ""
		}
		opts := xai.RuntimeModelMappingOptions()
		if !opts.EnableCrossClientMap {
			return ""
		}
		return xai.ModelMappingWithOptions(opts)["claude-*"]
	}

	// 国产供应商不使用 OpenAI Messages 的分组级模型映射；模型改写由渠道与
	// 账号 model_mapping 完成，避免历史脏配置把 GPT 模型发送给 CN 上游。
	if IsCNProvider(g.Platform) {
		return ""
	}

	cfg := normalizeOpenAIMessagesDispatchModelConfig(g.MessagesDispatchModelConfig)
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

func sanitizeGroupMessagesDispatchFields(g *Group) {
	view := groupRules(g)
	routing.SanitizeGroupMessagesDispatchFields(view)
	ApplyRoutingGroup(g, view)
}
