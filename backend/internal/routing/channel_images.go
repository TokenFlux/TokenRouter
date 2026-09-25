// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	"strings"
)

// CodexImageGenerationBridgeOverride 返回渠道级 Codex 图片桥接覆盖配置。
// nil 表示继续跟随账号级或全局配置。
func (c *Channel) CodexImageGenerationBridgeOverride(platform string) *bool {
	if c == nil {
		return nil
	}
	return PlatformBoolOverride(c.FeaturesConfig, featureKeyCodexImageGenerationBridge, platform)
}

func PlatformBoolOverride(values map[string]any, key string, platform string) *bool {
	if values == nil {
		return nil
	}
	if v, ok := values[key].(bool); ok {
		return BoolOverridePtr(v)
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

const featureKeyCodexImageGenerationBridge = "codex_image_generation_bridge"
