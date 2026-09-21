package provider

import "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

// sparkModelVariants 返回所有归一到 spark 的模型 ID（含推理强度别名）。
// 从 codexModelMap 派生，使集合与别名表单一来源、不漂移；若上游将来新增 spark 变体，
// 在 codexModelMap 注册后此处自动跟随。
func sparkModelVariants() []string {
	out := make([]string, 0, 1)
	for alias, target := range openai.CodexModelMap {
		if target == "gpt-5.3-codex-spark" {
			out = append(out, alias)
		}
	}
	return out
}

// DefaultSparkShadowModels 返回 spark 影子账号的默认 model_mapping。
//
// 恒等映射（key 映射到自身）把「只接 spark」限制落在 key 白名单上，模型零改写、
// 与空 mapping 透传行为一致。推理强度别名由 Codex OAuth 转换层统一归一到 base。
func DefaultSparkShadowModels() map[string]any {
	variants := sparkModelVariants()
	mapping := make(map[string]any, len(variants))
	for _, m := range variants {
		mapping[m] = m
	}
	return mapping
}
