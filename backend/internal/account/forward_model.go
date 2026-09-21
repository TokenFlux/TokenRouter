package account

import "strings"

// ResolveForwardMappedModel 保留账号映射的一跳与空映射回退，调用方仍决定平台规范化。
func ResolveForwardMappedModel(value *Record, requested string, defaults ModelMappingDefaults) string {
	if value == nil {
		return ""
	}
	mapped, matched := ResolveMappedModel(value.Platform, ResolveModelMapping(value, defaults), requested)
	if !matched || strings.TrimSpace(mapped) == "" {
		return requested
	}
	return strings.TrimSpace(mapped)
}
