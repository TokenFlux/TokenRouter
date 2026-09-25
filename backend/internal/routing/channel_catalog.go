// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	"fmt"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

type ModelPriceReader interface {
	GetModelPricing(string) (*ModelPricing, error)
}

// ChannelCatalog 只组合现有价格目录和平台默认列表，不构建第二份目录缓存。
type ChannelCatalog struct {
	Prices          ModelPriceReader
	NamesByProvider func(string) []string
	QoderModels     func() []string
}

func (c *ChannelCatalog) DefaultPricing(model string) (*ModelPricing, error) {
	return c.Prices.GetModelPricing(model)
}
func (c *ChannelCatalog) ModelNames(platform string) ([]string, error) {
	if platform == PlatformQoder {
		return c.QoderModels(), nil
	}
	provider, ok := platformToLiteLLMProvider[platform]
	if !ok {
		return nil, infraerrors.BadRequest("UNSUPPORTED_PLATFORM", fmt.Sprintf("unsupported platform: %s", platform)).WithMetadata(map[string]string{"param": platform})
	}
	return c.NamesByProvider(provider), nil
}

// platformToLiteLLMProvider 将渠道平台名映射为 LiteLLM 定价目录中的 provider
var platformToLiteLLMProvider = map[string]string{
	PlatformAnthropic:   "anthropic",
	PlatformOpenAI:      "openai",
	PlatformGemini:      "gemini",
	PlatformAntigravity: "anthropic",
	PlatformGrok:        "xai",
	PlatformKimi:        "moonshot",
	PlatformZhipu:       "zhipu",
	PlatformDeepseek:    "deepseek",
}
