// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	maps "maps"
)

// ModelMappingDefaults 按需投影平台默认目录，不建立第二份别名缓存。
type ModelMappingDefaults struct {
	Antigravity           func() map[string]string
	GoogleOne             func() map[string]string
	AntigravityAgentModel string
}

// ResolveModelMapping 读取不可变配置并返回独立映射，不在共享账号内写入派生缓存。
func ResolveModelMapping(a *Record, defaults ModelMappingDefaults) map[string]string {
	rawMapping, _ := a.Credentials["model_mapping"].(map[string]any)
	if a.Credentials == nil {
		// Antigravity 平台使用默认映射
		if a.Platform == PlatformAntigravity {
			return maps.Clone(defaults.Antigravity())
		}
		// Bedrock 默认映射由 forwardBedrock 统一处理（需配合 region prefix 调整）
		return nil
	}
	if len(rawMapping) == 0 {
		if a.IsGeminiGoogleOne() {
			return maps.Clone(defaults.GoogleOne())
		}
		// Antigravity 平台使用默认映射
		if a.Platform == PlatformAntigravity {
			return maps.Clone(defaults.Antigravity())
		}
		return nil
	}

	result := make(map[string]string)
	for k, v := range rawMapping {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	if len(result) > 0 {
		if a.Platform == PlatformAntigravity {
			EnsureAntigravityDefaultPassthroughs(result, []string{
				"gemini-3-flash",
				"gemini-3.1-pro-high",
				"gemini-3.1-pro-low",
				"gemini-3.6-flash",
				"gemini-3.6-flash-high",
				"gemini-3.6-flash-low",
				"gemini-3.6-flash-medium",
				"gemini-3.6-flash-tiered",
			})
			ApplyAntigravityGemini31ProAliases(result, defaults.AntigravityAgentModel)
		}
		return result
	}

	// Antigravity 平台使用默认映射
	if a.IsGeminiGoogleOne() {
		return maps.Clone(defaults.GoogleOne())
	}
	if a.Platform == PlatformAntigravity {
		return maps.Clone(defaults.Antigravity())
	}
	return nil
}
