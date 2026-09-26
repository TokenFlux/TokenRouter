// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	"strings"
)

// PlatformBoolOverride 读取分组的布尔覆盖，兼容按平台保存的对象。
// 未配置或值类型不匹配时返回 nil，由调用方继续应用兜底策略。
func PlatformBoolOverride(values map[string]any, key string, platform string) *bool {
	if values == nil {
		return nil
	}
	if v, ok := values[key].(bool); ok {
		return BoolOverridePtr(v)
	}
	if typed, ok := values[key].(map[string]bool); ok {
		if value, found := typed[platform]; found {
			return BoolOverridePtr(value)
		}
	}
	raw, ok := values[key].(map[string]any)
	if !ok {
		return nil
	}
	platform = strings.TrimSpace(platform)
	if platform == "" {
		return nil
	}
	if v, ok := raw[platform].(bool); ok {
		return BoolOverridePtr(v)
	}
	return nil
}

func BoolOverridePtr(v bool) *bool {
	return &v
}
