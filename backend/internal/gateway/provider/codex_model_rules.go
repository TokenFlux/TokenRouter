package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// NormalizeCodexModel 在原有平台规则下规范化模型，不创建第二份目录。
func NormalizeCodexModel(model string) string {
	return openai.NormalizeCodexModel(model, CodexModelRules())
}

// CodexModelRules 组合已有模型能力与动态别名入口，按调用时点读取。
func CodexModelRules() openai.CodexModelRules {
	return openai.CodexModelRules{
		ImageOnly:      media.IsImageGenerationModel,
		LastSegment:    capability.LastOpenAIModelSegment,
		CanonicalAlias: capability.CanonicalizeOpenAIModelAliasSpelling,
		KnownModel:     modelidentity.NormalizeOpenAI,
		SupportsEffort: capability.OpenAIModelSupportsReasoningEffort,
	}
}
