//go:build unit

package creative_test

import (
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// TestCreativeOperationsForPlatform 平台能力矩阵。
func TestCreativeOperationsForPlatform(t *testing.T) {
	require.Equal(t, []string{"generate", "edit"}, creative.CreativeOperationsForPlatform(capability.PlatformGemini))
	require.Equal(t, []string{"generate", "edit", "inpaint"}, creative.CreativeOperationsForPlatform(capability.PlatformOpenAI))
	require.Equal(t, []string{"generate", "edit"}, creative.CreativeOperationsForPlatform(capability.PlatformGrok))
	require.Nil(t, creative.CreativeOperationsForPlatform(capability.PlatformAnthropic))
}

// TestCreativeGrokDefaultImageCandidates 校验无映射账号包含 Grok Imagine 图片候选，尤其是画质模型。
func TestCreativeGrokDefaultImageCandidates(t *testing.T) {
	account := &accountcore.Record{Platform: capability.PlatformGrok, Credentials: map[string]any{}}
	models := creative.CreativeExpandAccountModels(creativeprovider.CatalogAccount(account), creative.DefaultCreativeGrokModelCandidates(), media.IsGrokImageGenerationModel)
	require.Contains(t, models, "grok-imagine-image")
	require.Contains(t, models, "grok-imagine-image-quality")
	require.Contains(t, models, "grok-imagine-image-2.0")
}

// TestCreativeGrokDefaultImageCandidatesWithQualityWhitelist 校验精确白名单不会漏掉画质模型。
func TestCreativeGrokDefaultImageCandidatesWithQualityWhitelist(t *testing.T) {
	account := &accountcore.Record{
		Platform: capability.PlatformGrok,
		Credentials: map[string]any{
			"model_whitelist": []string{"grok-imagine-image-quality"},
		},
	}
	models := creative.CreativeExpandAccountModels(creativeprovider.CatalogAccount(account), creative.DefaultCreativeGrokModelCandidates(), media.IsGrokImageGenerationModel)
	require.Equal(t, []string{"grok-imagine-image-quality"}, models)
}

// TestCreativeMappedFinalModelCapability 确保文本请求别名映射到图片模型时按最终模型校验。
func TestCreativeMappedFinalModelCapability(t *testing.T) {
	account := &accountcore.Record{
		Platform: capability.PlatformOpenAI,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"draw-alias": "gpt-image-2", "text-alias": "gpt-5.4"},
			"model_whitelist": []string{"gpt-image-2", "gpt-5.4"},
		},
	}
	models := creative.CreativeExpandAccountModels(creativeprovider.CatalogAccount(account), []string{"draw-alias", "text-alias"}, media.IsGPTImageGenerationModel)
	// 候选集合没有映射 key 时仍应纳入显式映射的 requested alias。
	require.NotContains(t, models, "text-alias")
	// 反向映射目标为图片模型的别名必须被保留。
	account.Credentials["model_mapping"] = map[string]any{"draw-alias": "gpt-image-2"}
	models = creative.CreativeExpandAccountModels(creativeprovider.CatalogAccount(account), []string{"draw-alias"}, media.IsGPTImageGenerationModel)
	require.Equal(t, []string{"draw-alias"}, models)
}

// TestCreativeGrokConfiguredImageWhitelistCandidates 校验代理侧图片模型变体能从显式白名单进入候选。
func TestCreativeGrokConfiguredImageWhitelistCandidates(t *testing.T) {
	account := &accountcore.Record{
		Platform: capability.PlatformGrok,
		Credentials: map[string]any{
			"model_whitelist": []string{"grok-imagine-image-lite"},
		},
	}
	models := creative.CreativeExpandAccountModels(creativeprovider.CatalogAccount(account), creative.DefaultCreativeGrokModelCandidates(), media.IsGrokImageGenerationModel)
	require.Equal(t, []string{"grok-imagine-image-lite"}, models)
}

// TestCreativeGeminiConfiguredImageWhitelistCandidates 校验 Gemini 无映射账号不会漏掉图片模型变体。
func TestCreativeGeminiConfiguredImageWhitelistCandidates(t *testing.T) {
	account := &accountcore.Record{
		Platform: capability.PlatformGemini,
		Credentials: map[string]any{
			"model_whitelist": []string{"gemini-3-pro-image-quality", "gemini-2.5-flash"},
		},
	}
	models := creative.CreativeGeminiModelsForAccount(creativeprovider.CatalogAccount(account))
	require.Equal(t, []string{"gemini-3-pro-image-quality"}, models)
}
