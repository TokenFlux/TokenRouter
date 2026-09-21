package provider

import (
	"strings"
	"testing"

	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	"github.com/stretchr/testify/require"
)

// 使用不同价卡验证命中身份，避免只比较恰好相同的公开 token 单价。
func catalogLookupTestPricing(input float64, modalities ...string) *purepricing.LiteLLMModelPricing {
	return &purepricing.LiteLLMModelPricing{
		InputCostPerToken: input, OutputCostPerToken: input * 5,
		CacheCreationInputTokenCost: input * 1.25, CacheReadInputTokenCost: input / 10,
		LongContextInputTokenThreshold: 200000, LongContextInputCostMultiplier: 2,
		LongContextOutputCostMultiplier: 1.5, Mode: "chat",
		SupportedModalities: modalities, SupportedOutputModalities: []string{"text"},
	}
}

func TestCatalogLookupGeminiThinkingTiers(t *testing.T) {
	for _, base := range []string{
		"gemini-3.1-pro", "gemini-3.5-flash", "gemini-3.6-flash",
		"gemini-3.7-flash", "gemini-3.8-flash", "gemini-17-pro", "gemini-17.2-flash",
	} {
		t.Run(base, func(t *testing.T) {
			pricing := catalogLookupTestPricing(2e-6, "text", "image", "audio", "video")
			svc := newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*purepricing.LiteLLMModelPricing{base: pricing}})
			for _, suffix := range []string{"", "-high", "-low", "-medium", "-tiered"} {
				for _, prefix := range []string{"", "models/", "publishers/google/models/", "projects/demo/locations/global/publishers/google/models/"} {
					model := " " + strings.ToUpper(prefix+base+suffix) + " "
					require.Same(t, pricing, svc.GetModelPricing(model), model)
					input, output := svc.GetModelModalities(model)
					require.Equal(t, []string{"text", "image", "audio", "video"}, input, model)
					require.Equal(t, []string{"text"}, output, model)
				}
			}
		})
	}
}

func TestCatalogLookupExactEntryWinsWithoutMerging(t *testing.T) {
	for _, base := range []string{"gemini-3.1-pro", "gemini-3.8-flash", "gpt-5.6-terra"} {
		t.Run(base, func(t *testing.T) {
			basePricing := catalogLookupTestPricing(1e-6, "text", "image", "audio", "video")
			tierPricing := &purepricing.LiteLLMModelPricing{InputCostPerToken: 9e-6, Mode: "chat"}
			svc := newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*purepricing.LiteLLMModelPricing{
				base: basePricing, base + "-high": tierPricing,
			}})
			for _, model := range []string{base + "-high", "models/" + base + "-high"} {
				require.Same(t, tierPricing, svc.GetModelPricing(model))
				input, output := svc.GetModelModalities(model)
				require.Equal(t, []string{"text"}, input)
				require.Equal(t, []string{"text"}, output)
			}
		})
	}
}

func TestCatalogLookupGeminiRejectsUnknownAliases(t *testing.T) {
	svc := newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*purepricing.LiteLLMModelPricing{
		"gemini-3.8-flash":       catalogLookupTestPricing(1e-6, "text", "video"),
		"gemini-3.8-flash-image": catalogLookupTestPricing(2e-6, "image"),
		"gemini-3.8-flash-lite":  catalogLookupTestPricing(3e-6, "text"),
		"gemini-3.1-pro-high":    catalogLookupTestPricing(4e-6, "text"),
	}})
	for _, model := range []string{
		"gemini-3.9-flash-tiered", "gemini-3.8-flash-high-high", "gemini-3.8-flash-ultra",
		"gemini-3.8-flash-image-high", "gemini-3.8-flash-lite-high", "gemini-3.1-pro-low", "gemini-3.1-pro",
	} {
		require.Nil(t, svc.GetModelPricing(model), model)
		input, output := svc.GetModelModalities(model)
		require.Nil(t, input, model)
		require.Nil(t, output, model)
	}
}

func TestCatalogLookupClaudeEquivalentVersions(t *testing.T) {
	for _, names := range [][2]string{
		{"claude-opus-4.6", "claude-opus-4-6"},
		{"claude-opus-4.7-20260416", "claude-opus-4-7-20260416"},
		{"claude-sonnet-4.6-thinking", "claude-sonnet-4-6-thinking"},
		{"claude-3.5-sonnet-20241022-v1:0", "claude-3-5-sonnet-20241022-v1:0"},
	} {
		for i := range names {
			pricing := catalogLookupTestPricing(3e-6, "text", "image")
			svc := newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*purepricing.LiteLLMModelPricing{names[i]: pricing}})
			model := "models/" + names[1-i]
			require.Same(t, pricing, svc.GetModelPricing(model))
			input, _ := svc.GetModelModalities(model)
			require.Equal(t, []string{"text", "image"}, input)
			exact := catalogLookupTestPricing(9e-6, "text")
			mutatePricingFixture(svc, func(data map[string]*purepricing.LiteLLMModelPricing) { data[names[1-i]] = exact })
			require.Same(t, exact, svc.GetModelPricing(model))
			input, _ = svc.GetModelModalities(model)
			require.Equal(t, []string{"text"}, input)
		}
	}
	// 不把日期当作次版本，也不让能力查询跨版本回退。
	svc := newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*purepricing.LiteLLMModelPricing{
		"claude-sonnet-4.20250514": catalogLookupTestPricing(1e-6, "video"),
		"claude-opus-4-6":          catalogLookupTestPricing(2e-6, "image"),
	}})
	for _, model := range []string{"claude-sonnet-4-20250514", "claude-opus-4.7"} {
		input, output := svc.GetModelModalities(model)
		require.Nil(t, input)
		require.Nil(t, output)
	}
}

func TestCatalogLookupOpenAIProductIdentity(t *testing.T) {
	for _, model := range []string{
		"gpt-5.4-mini", "gpt-5.4-nano", "gpt-5.5-pro", "gpt-5.5",
		"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-6-astra", "gpt-5.3-codex",
	} {
		t.Run(model, func(t *testing.T) {
			own := catalogLookupTestPricing(7e-6, "text", "image")
			svc := newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*purepricing.LiteLLMModelPricing{
				"gpt-5.4":       catalogLookupTestPricing(1e-6, "text"),
				"gpt-5.6":       catalogLookupTestPricing(2e-6, "text"),
				"gpt-5.1-codex": catalogLookupTestPricing(3e-6, "text"),
			}})
			mutatePricingFixture(svc, func(data map[string]*purepricing.LiteLLMModelPricing) { data[model] = own })
			for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"} {
				if !capability.OpenAIModelSupportsReasoningEffort(model, effort) {
					continue
				}
				alias := "openai/" + model + "-" + effort
				require.Same(t, own, svc.GetModelPricing(alias), alias)
				input, _ := svc.GetModelModalities(alias)
				require.Equal(t, []string{"text", "image"}, input, alias)
			}
			for _, suffix := range []string{"-20260905", "-2026-09-05", "-openai-compact"} {
				require.Same(t, own, svc.GetModelPricing(model+suffix), model+suffix)
				input, output := svc.GetModelModalities(model + suffix)
				require.Nil(t, input)
				require.Nil(t, output)
			}
		})
	}
}

func TestCatalogLookupOpenAIDedicatedFallbackBeforeGenericBase(t *testing.T) {
	svc := newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*purepricing.LiteLLMModelPricing{
		"gpt-5.4": catalogLookupTestPricing(99e-6, "text"),
		"gpt-5.5": catalogLookupTestPricing(99e-6, "text"),
		"gpt-5.6": catalogLookupTestPricing(99e-6, "text"),
		"gpt-6":   catalogLookupTestPricing(99e-6, "text"),
	}})
	for model, want := range map[string]*purepricing.LiteLLMModelPricing{
		"gpt-5.4-mini": purepricing.OpenAIGPT54MiniFallbackPricing, "gpt-5.4-nano": purepricing.OpenAIGPT54NanoFallbackPricing,
		"gpt-5.5-pro": purepricing.OpenAIGPT55ProFallbackPricing, "gpt-5.6-sol": purepricing.OpenAIGPT56SolPricing,
		"gpt-5.6-terra": purepricing.OpenAIGPT56TerraPricing, "gpt-5.6-luna": purepricing.OpenAIGPT56LunaPricing,
		"gpt-6-astra": purepricing.OpenAIGPT6AstraPricing,
	} {
		for _, suffix := range []string{"", "-high", "-20260905", "-2026-09-05"} {
			require.Same(t, want, svc.GetModelPricing(model+suffix), model+suffix)
			input, output := svc.GetModelModalities(model + suffix)
			require.Nil(t, input)
			require.Nil(t, output)
		}
	}
}

// 原有价格后缀兼容仍优先同产品目录；这些非明确身份别名不能生成能力。
func TestCatalogLookupOpenAIPriceFallbackKeepsDynamicProduct(t *testing.T) {
	for _, model := range []string{"gpt-5.4", "gpt-5.5", "gpt-5.5-pro", "gpt-5.6-terra", "gpt-6-astra"} {
		pricing := catalogLookupTestPricing(17e-6, "text", "image")
		svc := newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*purepricing.LiteLLMModelPricing{model: pricing}})
		for _, suffix := range []string{"-preview", "-chat-latest"} {
			require.Same(t, pricing, svc.GetModelPricing(model+suffix), model+suffix)
			input, output := svc.GetModelModalities(model + suffix)
			require.Nil(t, input)
			require.Nil(t, output)
		}
	}
}

func TestCatalogLookupBareGPT56DoesNotBorrowOtherPrices(t *testing.T) {
	svc := newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*purepricing.LiteLLMModelPricing{
		"gpt-5.6-sol":   catalogLookupTestPricing(5e-6, "text", "image"),
		"gpt-5.4":       catalogLookupTestPricing(2.5e-6, "text"),
		"gpt-5.1-codex": catalogLookupTestPricing(1.25e-6, "text"),
	}})
	for _, pricingSvc := range []*PricingService{svc, nil} {
		billing := newBillingFixture(pricingSvc)
		for _, model := range []string{"gpt-5.6", "gpt5.6", "openai/gpt-5.6-high", "gpt-5.6-max", "gpt-5.6-20260905", "gpt-5.6-2026-09-05", "gpt-5.6-openai-compact"} {
			price, err := billing.GetModelPricing(model)
			require.ErrorIs(t, err, purepricing.ErrModelPricingUnavailable, model)
			require.Nil(t, price)
		}
	}
	input, output := svc.GetModelModalities("gpt-5.6")
	require.Nil(t, input)
	require.Nil(t, output)
}

func TestCatalogLookupSparkBillingPolicyDoesNotSupplyCapabilities(t *testing.T) {
	legacy := catalogLookupTestPricing(1e-6, "audio")
	spark := catalogLookupTestPricing(9e-6, "text")
	svc := newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*purepricing.LiteLLMModelPricing{
		"gpt-5.1-codex": legacy, "gpt-5.3-codex-spark": spark,
	}})
	require.Same(t, spark, svc.GetModelPricing("gpt-5.3-codex-spark"))
	require.Same(t, legacy, svc.GetModelPricing("gpt-5.3-codex-spark-high"))
	input, output := svc.GetModelModalities("gpt-5.3-codex-spark-high")
	require.Nil(t, input)
	require.Nil(t, output)
}

func TestCatalogLookupGrokUsesKnownRuntimeAliases(t *testing.T) {
	previous := xai.RuntimeModelMappingOptions()
	t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(previous) })
	text := catalogLookupTestPricing(1e-6, "text")
	vision := catalogLookupTestPricing(2e-6, "text", "image")
	svc := newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*purepricing.LiteLLMModelPricing{
		"grok-4.5": text, "grok-4.6": vision, "grok-4.20-0309-reasoning": vision,
	}})
	xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{DefaultText: "grok-4.5", EnableCrossClientMap: true})
	require.Same(t, text, svc.GetModelPricing("x-ai/grok-latest"))
	xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{})
	for _, alias := range []string{"grok", "xai/grok-latest", "grok-4.6-latest", "grok-4.20-reasoning"} {
		require.Same(t, vision, svc.GetModelPricing(alias))
		input, _ := svc.GetModelModalities(alias)
		require.Equal(t, []string{"text", "image"}, input)
	}
	// 默认模型配置只去空白，返回的名称也要兼容前缀、大小写和已知固定别名。
	for _, target := range []string{"GROK-4.6", "xai/grok-4.6", "X-AI/GROK-4.6", "grok-4.6-latest"} {
		xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{DefaultText: target})
		for _, alias := range []string{"grok", "grok-latest"} {
			require.Same(t, vision, svc.GetModelPricing(alias), target)
			input, _ := svc.GetModelModalities(alias)
			require.Equal(t, []string{"text", "image"}, input, target)
		}
	}
	// 完整别名条目仍可独立配价，未知模型和跨客户端名称不会继承 Grok 能力。
	mutatePricingFixture(svc, func(data map[string]*purepricing.LiteLLMModelPricing) { data["grok-latest"] = text })
	require.Same(t, text, svc.GetModelPricing("grok-latest"))
	for _, model := range []string{"grok-unknown", "gpt-5.4", "claude-sonnet-4"} {
		input, output := svc.GetModelModalities(model)
		require.Nil(t, input)
		require.Nil(t, output)
	}
}

// 非法默认配置形成循环时保持未知，不无限展开候选。
func TestCatalogLookupGrokDefaultAliasCycle(t *testing.T) {
	previous := xai.RuntimeModelMappingOptions()
	t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(previous) })
	svc := newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*purepricing.LiteLLMModelPricing{}})
	for _, target := range []string{"grok", "grok-latest", "xai/grok-latest"} {
		xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{DefaultText: target})
		require.Nil(t, svc.GetModelPricing("grok"))
		input, output := svc.GetModelModalities("grok-latest")
		require.Nil(t, input)
		require.Nil(t, output)
	}
}

func TestCatalogLookupModalityFieldCompatibility(t *testing.T) {
	svc := newStubPricingServiceFromJSON(t, `{
		"legacy-input": {"input_cost_per_token": 1, "mode": "chat", "supported_modalities": [],
			"supported_input_modalities": ["audio", "text", "audio", "file"], "supports_video_input": true},
		"primary-input": {"input_cost_per_token": 1, "mode": "chat", "supported_modalities": ["text"],
			"supported_input_modalities": ["video"]},
		"video-flag": {"input_cost_per_token": 1, "mode": "chat", "supports_video_input": true}
	}`)
	for model, want := range map[string][]string{
		"legacy-input": {"text", "audio", "video"}, "primary-input": {"text"}, "video-flag": {"text", "video"},
	} {
		for i := 0; i < 3; i++ {
			input, output := svc.GetModelModalities(model)
			require.Equal(t, want, input)
			require.Equal(t, []string{"text"}, output)
		}
	}
	require.Equal(t, []string{"audio", "text", "audio", "file"}, svc.Snapshot().Data["legacy-input"].SupportedModalities)
}
