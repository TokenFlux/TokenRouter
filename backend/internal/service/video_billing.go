package service

import (
	"strings"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// 内置视频默认价格使用的标准模型族。
const (
	VideoPriceFamilyGrokImagineVideo   = "grok-imagine-video"
	VideoPriceFamilyGrokImagineVideo15 = "grok-imagine-video-1.5"
)

// CanonicalGrokImagineVideoPriceFamily 将模型别名、预览版与旧版 ID
// 归一化为内置默认价格使用的模型族。
func CanonicalGrokImagineVideoPriceFamily(model string) string {
	if model == "" {
		return ""
	}
	// 已知别名优先使用共享 xAI 辅助函数；未来新增的原生 Imagine 模型保持独立，
	// 便于运营方分别配置价格。
	if c := xai.CanonicalImagineVideoModel(model); c != "" {
		switch c {
		case xai.DefaultImagineVideo15Model:
			return VideoPriceFamilyGrokImagineVideo15
		case xai.DefaultImagineVideoModel:
			return VideoPriceFamilyGrokImagineVideo
		}
		if strings.HasPrefix(c, "grok-imagine-video-") {
			return c
		}
	}
	m := strings.ToLower(strings.TrimSpace(model))
	for _, prefix := range []string{"xai/", "x-ai/", "grok/"} {
		if strings.HasPrefix(m, prefix) {
			m = strings.TrimPrefix(m, prefix)
			break
		}
	}
	switch {
	case m == "grok-imagine-video-1.5" || m == "grok-imagine-video-1.5-preview" ||
		m == "grok-video-1.5" || strings.Contains(m, "video-1.5"):
		return VideoPriceFamilyGrokImagineVideo15
	case m == "grok-imagine-video" || m == "grok-imagine-video-preview" ||
		m == "grok-video" || m == "grok-video-latest":
		return VideoPriceFamilyGrokImagineVideo
	default:
		return ""
	}
}
