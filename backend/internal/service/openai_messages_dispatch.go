package service

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

func normalizeOpenAIMessagesDispatchMappedModel(model string) string {
	model = gatewayprovider.NormalizeOpenAICompatRequestedModel(strings.TrimSpace(model))
	return strings.TrimSpace(model)
}

func normalizeOpenAIMessagesDispatchModelConfig(cfg routing.OpenAIMessagesDispatchModelConfig) routing.OpenAIMessagesDispatchModelConfig {
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

// ResolveMessagesDispatchModel 只适配尚未迁出的平台动态型号来源，不再扩展分组实体。
func ResolveMessagesDispatchModel(g *routing.Group, requestedModel string) string {
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
		opts := xai.RuntimeModelMappingOptions()
		if !opts.EnableCrossClientMap {
			return ""
		}
		return xai.ModelMappingWithOptions(opts)["claude-*"]
	}

	// 国产供应商不使用 OpenAI Messages 的分组级模型映射；模型改写由渠道与
	// 账号 model_mapping 完成，避免历史脏配置把 GPT 模型发送给 CN 上游。
	if account.IsCNProvider(g.Platform) {
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

// 旧同包测试的调用随后随平台适配迁移，规则只保留上方一份实现。

func sanitizeGroupMessagesDispatchFields(g *routing.Group) {
	view := g
	routing.SanitizeGroupMessagesDispatchFields(view)
	if g != nil && view != nil {
		*g = *routing.CloneGroup(view)
	}
}
