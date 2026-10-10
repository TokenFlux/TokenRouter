package provider

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	billingpricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

const (
	hotReloadCatalogJSON = `{"providers":{"openai":{"models":{"remote-model":{"cost":{"input":1,"output":2}}}}}}`

	fallbackOriginFixture = `{"providers":{
	"anthropic":{"models":{
		"claude-opus-4-6":{"cost":{"input":5,"output":25}},
		"claude-opus-4-6-20260101":{"cost":{"input":6,"output":30}}
	}},
	"openai":{"models":{"gpt-5.4":{"cost":{"input":2.5,"output":15}}}},
	"relay":{"models":{
		"claude-opus-4-6":{"cost":{"input":1,"output":2}},
		"claude-opus-4-6-discount":{"cost":{"input":0,"output":0}},
		"gpt-5.4":{"cost":{"input":1,"output":3}}
	}}
}}`
)

var structuredLogCaptureMu sync.Mutex

type inMemoryLogSink struct {
	mu     sync.Mutex
	events []*logging.LogEvent
}

type stubCatalogRemoteClient struct{ body string }

type lifecyclePricingRemote struct{ calls atomic.Int64 }

func TestParsePricingData_WarnsOrphanCacheTierFields(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()

	service := newHotReloadCatalog(t, `{
		"gemini-orphan": {"provider": "vertex_ai-language-models", "mode": "chat",
			"input_cost_per_token": 1.25e-06, "output_cost_per_token": 1e-05,
			"input_cost_per_token_above_200k_tokens": 2.5e-06,
			"output_cost_per_token_above_200k_tokens": 1.5e-05,
			"cache_creation_input_token_cost_above_200k_tokens": 2.5e-07},
		"gemini-complete": {"provider": "vertex_ai-language-models", "mode": "chat",
			"input_cost_per_token": 1.25e-06, "output_cost_per_token": 1e-05,
			"cache_creation_input_token_cost": 1.25e-06,
			"input_cost_per_token_above_200k_tokens": 2.5e-06,
			"output_cost_per_token_above_200k_tokens": 1.5e-05,
			"cache_creation_input_token_cost_above_200k_tokens": 2.5e-06},
		"priority-orphan": {"provider": "openai", "mode": "chat",
			"input_cost_per_token": 5e-06, "output_cost_per_token": 3e-05,
			"cache_creation_input_token_cost_above_272k_tokens_priority": 2.5e-05}
	}`)
	data := service.Snapshot().Data
	require.Equal(t, 200000, data["gemini-orphan"].LongContextInputTokenThreshold)
	require.True(t, logSink.ContainsMessageAtLevel("gemini-orphan(cache_creation_input_token_cost_above_200k_tokens)", "warn"))
	require.True(t, logSink.ContainsMessage("priority-orphan(cache_creation_input_token_cost_above_272k_tokens_priority)"))
	require.False(t, logSink.ContainsMessage("gemini-complete"))
}

func TestParsePricingData_WarnsLopsidedLongContextLadder(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()

	service := newHotReloadCatalog(t, `{
		"mixed-versions": {"provider": "openai", "mode": "chat",
			"input_cost_per_token": 5e-06, "output_cost_per_token": 3e-05,
			"input_cost_per_token_above_272k_tokens": 8e-06,
			"output_cost_per_token_above_272k_tokens": 3e-05},
		"consistent": {"provider": "openai", "mode": "chat",
			"input_cost_per_token": 4e-06, "output_cost_per_token": 2e-05,
			"input_cost_per_token_above_272k_tokens": 8e-06,
			"output_cost_per_token_above_272k_tokens": 3e-05}
	}`)
	data := service.Snapshot().Data
	require.Equal(t, 272000, data["mixed-versions"].LongContextInputTokenThreshold)
	require.True(t, logSink.ContainsMessageAtLevel("mixed-versions(input x1.60, output x1.00)", "warn"))
	require.False(t, logSink.ContainsMessage("consistent"))
}

// TestDefaultCatalogSnapshot_CacheTierContract 检查发布目录的上下文阶梯和缓存报价。
func TestDefaultCatalogSnapshot_CacheTierContract(t *testing.T) {
	service := newOfflinePricingFixture(t)
	price := service.GetModelPricing("gpt-5.6-sol")
	require.NotNil(t, price)
	require.Len(t, price.ContextPrices, 1)
	require.Equal(t, 272000, price.ContextPrices[0].Threshold)
	require.InDelta(t, 0.4e-6, price.CacheReadInputTokenCost, 1e-12)
	require.InDelta(t, 0.8e-6, price.ContextPrices[0].Pricing.CacheReadInputTokenCost, 1e-12)
}

func TestCatalogLookupGeminiThinkingTiers(t *testing.T) {
	for _, base := range []string{
		"gemini-3.1-pro", "gemini-3.5-flash", "gemini-3.6-flash",
		"gemini-3.7-flash", "gemini-3.8-flash", "gemini-17-pro", "gemini-17.2-flash",
	} {
		t.Run(base, func(t *testing.T) {
			pricing := catalogLookupTestPricing(2e-6, "text", "image", "audio", "video")
			svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{base: pricing}})
			for _, suffix := range []string{"", "-high", "-low", "-medium", "-tiered"} {
				for _, prefix := range []string{"", "models/", "publishers/google/models/", "projects/demo/locations/global/publishers/google/models/"} {
					model := " " + strings.ToUpper(prefix+base+suffix) + " "
					if suffix != "" {
						require.Nil(t, svc.GetModelPricing(model), model)
						input, output := svc.GetModelModalities(model)
						require.Nil(t, input)
						require.Nil(t, output)
						continue
					}
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
			tierPricing := &billingpricing.CatalogModelPricing{InputCostPerToken: 9e-6, Mode: "chat"}
			svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
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
	svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
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
			svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{names[i]: pricing}})
			model := "models/" + names[1-i]
			require.Nil(t, svc.GetModelPricing(model))
			input, _ := svc.GetModelModalities(model)
			require.Nil(t, input)
			exact := catalogLookupTestPricing(9e-6, "text")
			mutatePricingFixture(svc, func(data map[string]*billingpricing.CatalogModelPricing) { data[names[1-i]] = exact })
			require.Same(t, exact, svc.GetModelPricing(model))
			input, _ = svc.GetModelModalities(model)
			require.Equal(t, []string{"text"}, input)
		}
	}
	// 日期后缀代表独立型号，价格和能力按完整版本查询。
	svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
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
			svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
				"gpt-5.4":       catalogLookupTestPricing(1e-6, "text"),
				"gpt-5.6":       catalogLookupTestPricing(2e-6, "text"),
				"gpt-5.1-codex": catalogLookupTestPricing(3e-6, "text"),
			}})
			mutatePricingFixture(svc, func(data map[string]*billingpricing.CatalogModelPricing) { data[model] = own })
			for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"} {
				if !capability.OpenAIModelSupportsReasoningEffort(model, effort) {
					continue
				}
				alias := "openai/" + model + "-" + effort
				require.Nil(t, svc.GetModelPricing(alias), alias)
				input, _ := svc.GetModelModalities(alias)
				require.Nil(t, input, alias)
			}
			for _, suffix := range []string{"-20260905", "-2026-09-05", "-openai-compact"} {
				require.Nil(t, svc.GetModelPricing(model+suffix), model+suffix)
				input, output := svc.GetModelModalities(model + suffix)
				require.Nil(t, input)
				require.Nil(t, output)
			}
		})
	}
}

func TestCatalogLookupOpenAIDedicatedFallbackBeforeGenericBase(t *testing.T) {
	svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
		"gpt-5.4": catalogLookupTestPricing(99e-6, "text"),
		"gpt-5.5": catalogLookupTestPricing(99e-6, "text"),
		"gpt-5.6": catalogLookupTestPricing(99e-6, "text"),
		"gpt-6":   catalogLookupTestPricing(99e-6, "text"),
	}})
	for _, model := range []string{"gpt-5.4-mini", "gpt-5.4-nano", "gpt-5.5-pro", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-6-astra"} {
		for _, suffix := range []string{"", "-high", "-20260905", "-2026-09-05"} {
			require.Nil(t, svc.GetModelPricing(model+suffix))
			input, output := svc.GetModelModalities(model + suffix)
			require.Nil(t, input)
			require.Nil(t, output)
		}
	}
}

// TestCatalogLookupOpenAIPriceFallbackKeepsDynamicProduct 验证 preview 和 latest 后缀不会继承基础型号的价格或能力。
func TestCatalogLookupOpenAIPriceFallbackKeepsDynamicProduct(t *testing.T) {
	for _, model := range []string{"gpt-5.4", "gpt-5.5", "gpt-5.5-pro", "gpt-5.6-terra", "gpt-6-astra"} {
		pricing := catalogLookupTestPricing(17e-6, "text", "image")
		svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{model: pricing}})
		for _, suffix := range []string{"-preview", "-chat-latest"} {
			require.Nil(t, svc.GetModelPricing(model+suffix), model+suffix)
			input, output := svc.GetModelModalities(model + suffix)
			require.Nil(t, input)
			require.Nil(t, output)
		}
	}
}

func TestCatalogLookupBareGPT56DoesNotBorrowOtherPrices(t *testing.T) {
	svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
		"gpt-5.6-sol":   catalogLookupTestPricing(5e-6, "text", "image"),
		"gpt-5.4":       catalogLookupTestPricing(2.5e-6, "text"),
		"gpt-5.1-codex": catalogLookupTestPricing(1.25e-6, "text"),
	}})
	for _, pricingSvc := range []*Service{svc, nil} {
		billing := newBillingFixture(pricingSvc)
		for _, model := range []string{"gpt-5.6", "gpt5.6", "openai/gpt-5.6-high", "gpt-5.6-max", "gpt-5.6-20260905", "gpt-5.6-2026-09-05", "gpt-5.6-openai-compact"} {
			price, err := billing.GetModelPricing(model)
			require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable, model)
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
	svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
		"gpt-5.1-codex": legacy, "gpt-5.3-codex-spark": spark,
	}})
	require.Same(t, spark, svc.GetModelPricing("gpt-5.3-codex-spark"))
	require.Nil(t, svc.GetModelPricing("gpt-5.3-codex-spark-high"))
	input, output := svc.GetModelModalities("gpt-5.3-codex-spark-high")
	require.Nil(t, input)
	require.Nil(t, output)
}

func TestCatalogLookupGrokUsesKnownRuntimeAliases(t *testing.T) {
	previous := xai.RuntimeDefaultTextModel()
	t.Cleanup(func() { xai.SetRuntimeDefaultTextModel(previous) })
	text := catalogLookupTestPricing(1e-6, "text")
	vision := catalogLookupTestPricing(2e-6, "text", "image")
	svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
		"grok-4.5": text, "grok-4.6": vision, "grok-4.20-0309-reasoning": vision,
	}})
	require.Nil(t, svc.GetModelPricing("x-ai/grok-latest"))
	xai.SetRuntimeDefaultTextModel("")
	for _, alias := range []string{"grok", "xai/grok-latest", "grok-4.6-latest", "grok-4.20-reasoning"} {
		require.Nil(t, svc.GetModelPricing(alias))
		input, _ := svc.GetModelModalities(alias)
		require.Nil(t, input)
	}
	// 默认模型配置只去空白，返回的名称也要兼容前缀、大小写和已知固定别名。
	for _, target := range []string{"GROK-4.6", "xai/grok-4.6", "X-AI/GROK-4.6", "grok-4.6-latest"} {
		xai.SetRuntimeDefaultTextModel(target)
		for _, alias := range []string{"grok", "grok-latest"} {
			require.Nil(t, svc.GetModelPricing(alias), target)
			input, _ := svc.GetModelModalities(alias)
			require.Nil(t, input, target)
		}
	}
	// 完整别名条目仍可独立配价，未知模型和跨客户端名称不会继承 Grok 能力。
	mutatePricingFixture(svc, func(data map[string]*billingpricing.CatalogModelPricing) { data["grok-latest"] = text })
	require.Same(t, text, svc.GetModelPricing("grok-latest"))
	for _, model := range []string{"grok-unknown", "gpt-5.4", "claude-sonnet-4"} {
		input, output := svc.GetModelModalities(model)
		require.Nil(t, input)
		require.Nil(t, output)
	}
}

// TestCatalogLookupGrokDefaultAliasCycle 检查默认配置出现别名循环时返回未知模型。
func TestCatalogLookupGrokDefaultAliasCycle(t *testing.T) {
	previous := xai.RuntimeDefaultTextModel()
	t.Cleanup(func() { xai.SetRuntimeDefaultTextModel(previous) })
	svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{}})
	for _, target := range []string{"grok", "grok-latest", "xai/grok-latest"} {
		xai.SetRuntimeDefaultTextModel(target)
		require.Nil(t, svc.GetModelPricing("grok"))
		input, output := svc.GetModelModalities("grok-latest")
		require.Nil(t, input)
		require.Nil(t, output)
	}
}

func TestCatalogLookupModalityFieldCompatibility(t *testing.T) {
	svc := newStubCatalogFromJSON(t, `{
		"legacy-input": {"input_cost_per_token": 1, "mode": "chat", "supported_modalities": [],
			"supported_input_modalities": ["audio", "text", "audio", "file"], "supports_video_input": true},
		"primary-input": {"input_cost_per_token": 1, "mode": "chat", "supported_modalities": ["text"],
			"supported_input_modalities": ["video"]},
		"video-flag": {"input_cost_per_token": 1, "mode": "chat", "supports_video_input": true}
	}`)
	for model, want := range map[string][]string{
		"legacy-input": {"text", "audio", "video"}, "primary-input": {"text"}, "video-flag": {"text", "video"},
	} {
		for range 3 {
			input, output := svc.GetModelModalities(model)
			require.Equal(t, want, input)
			require.Equal(t, []string{"text"}, output)
		}
	}
	require.Equal(t, []string{"audio", "text", "audio", "file"}, svc.Snapshot().Data["legacy-input"].SupportedModalities)
}

func TestPricingHotReload_FallbackChangeRebuildsWithoutTouchingSyncAnchor(t *testing.T) {
	svc := newHotReloadCatalog(t, `{`+hotReloadModelJSON("custom-a", 4e-6, 8e-6)+`}`)
	require.InDelta(t, 4e-6, svc.Snapshot().Data["custom-a"].InputCostPerToken, 1e-12)
	require.Nil(t, svc.Snapshot().Data["custom-b"])
	require.NotEmpty(t, svc.Snapshot().CustomFilesHash)
	anchor, updated := svc.Snapshot().LocalHash, svc.Snapshot().LastUpdated

	require.NoError(t, os.WriteFile(svc.options.FallbackFile, []byte(`{`+
		hotReloadModelJSON("custom-a", 5e-6, 8e-6)+`,`+
		hotReloadModelJSON("custom-b", 1e-6, 3e-6)+`}`), 0o644))
	svc.reloadIfCustomFilesChanged()

	require.InDelta(t, 5e-6, svc.Snapshot().Data["custom-a"].InputCostPerToken, 1e-12, "改价即时生效")
	require.NotNil(t, svc.Snapshot().Data["custom-b"], "新模型即时并入")
	require.InDelta(t, 1e-6, svc.Snapshot().Data["remote-model"].InputCostPerToken, 1e-12, "目录条目不受影响")
	require.Equal(t, anchor, svc.Snapshot().LocalHash, "热重载不得改动远程同步锚点")
	require.Equal(t, updated, svc.Snapshot().LastUpdated)
	require.Equal(t, svc.customPricingFilesFingerprint(), svc.Snapshot().CustomFilesHash)
}

func TestPricingHotReload_InvalidFileKeepsCurrentDataUntilFixed(t *testing.T) {
	svc := newHotReloadCatalog(t, `{`+hotReloadModelJSON("custom-a", 4e-6, 8e-6)+`}`)
	before := svc.Snapshot().CustomFilesHash

	require.NoError(t, os.WriteFile(svc.options.FallbackFile, []byte(`{"custom-a": {"input_cost_per_token": `), 0o644))
	svc.reloadIfCustomFilesChanged()
	require.InDelta(t, 4e-6, svc.Snapshot().Data["custom-a"].InputCostPerToken, 1e-12, "半写文件不得替换数据")
	require.Equal(t, before, svc.Snapshot().CustomFilesHash, "指纹不更新，下一轮继续尝试")

	require.NoError(t, os.WriteFile(svc.options.FallbackFile, []byte(`{`+hotReloadModelJSON("custom-a", 6e-6, 8e-6)+`}`), 0o644))
	svc.reloadIfCustomFilesChanged()
	require.InDelta(t, 6e-6, svc.Snapshot().Data["custom-a"].InputCostPerToken, 1e-12, "文件修好后正常重建")
	require.NotEqual(t, before, svc.Snapshot().CustomFilesHash)
}

// TestPricingHotReload_DownloadRefreshesFingerprint 检查远程下载重建后指纹与当前文件内容一致，下一轮定时比对跳过重载。
func TestPricingHotReload_DownloadRefreshesFingerprint(t *testing.T) {
	svc := newHotReloadCatalog(t, `{`+hotReloadModelJSON("custom-a", 4e-6, 8e-6)+`}`)
	svc.options.RemoteURL = "https://example.com/pricing.json"
	setPricingFixtureRemote(svc, stubCatalogRemoteClient{body: hotReloadCatalogJSON})
	require.NoError(t, os.WriteFile(svc.options.FallbackFile, []byte(`{`+hotReloadModelJSON("custom-b", 1e-6, 3e-6)+`}`), 0o644))

	require.NoError(t, svc.ForceUpdate())

	require.NotNil(t, svc.Snapshot().Data["custom-b"])
	require.Nil(t, svc.Snapshot().Data["custom-a"])
	require.Equal(t, svc.customPricingFilesFingerprint(), svc.Snapshot().CustomFilesHash)

	mutatePricingFixture(svc, func(data map[string]*billingpricing.CatalogModelPricing) {
		data["sentinel"] = &billingpricing.CatalogModelPricing{}
	})
	svc.reloadIfCustomFilesChanged()
	require.Contains(t, svc.Snapshot().Data, "sentinel", "下载已消化文件变化，不得再次重建")
}

// TestPricingSchedulerStartsForCustomFilesWithoutRemoteURL 检查未配置 remote_url 时调度器仍比对补充文件的改动。
func TestPricingSchedulerStartsForCustomFilesWithoutRemoteURL(t *testing.T) {
	svc := NewService(Options{
		RemoteURL:    "",
		FallbackFile: filepath.Join(t.TempDir(), "fallback.json"),
	}, nil)

	svc.startUpdateScheduler()
	done := make(chan struct{})
	go func() {
		svc.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("custom file watch must keep the scheduler running")
	case <-time.After(50 * time.Millisecond):
	}

	svc.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler must exit after Stop")
	}
}

// TestSupplementRemovalAndRecreation 检查补充文件更新、删除和重建后，目录与条件请求标识保持有效。
func TestSupplementRemovalAndRecreation(t *testing.T) {
	svc := newHotReloadCatalog(t, `{"custom-model":{"input_cost_per_token":0.000004,"output_cost_per_token":0.000008}}`)
	before := svc.Snapshot()
	require.NoError(t, os.WriteFile(svc.options.FallbackFile, []byte(`{"custom-model":{"input_cost_per_token":0.000007,"output_cost_per_token":0.000008}}`), 0o600))
	require.NoError(t, svc.ForceUpdate())
	require.InDelta(t, 7e-6, svc.GetModelPricing("custom-model").InputCostPerToken, 1e-12)
	require.NoError(t, os.Remove(svc.options.FallbackFile))
	svc.reloadIfCustomFilesChanged()
	require.Nil(t, svc.GetModelPricing("custom-model"))
	require.Equal(t, before.LocalHash, svc.Snapshot().LocalHash)
	require.Equal(t, before.LastUpdated, svc.Snapshot().LastUpdated)
	hash := svc.Snapshot().CustomFilesHash
	svc.reloadIfCustomFilesChanged()
	require.Equal(t, hash, svc.Snapshot().CustomFilesHash)
	require.NoError(t, os.WriteFile(svc.options.FallbackFile, []byte(`{"custom-model":{"input_cost_per_token":0,"output_cost_per_token":0}}`), 0o600))
	svc.reloadIfCustomFilesChanged()
	require.NotNil(t, svc.GetModelPricing("custom-model"))
	require.Zero(t, svc.GetModelPricing("custom-model").InputCostPerToken)
}

func TestPricingSchedulerBlankRemoteURLDoesNotStart(t *testing.T) {
	svc := NewService(Options{RemoteURL: "  \t  "}, nil)
	defer svc.Stop()

	svc.startUpdateScheduler()
	done := make(chan struct{})
	go func() {
		svc.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("blank remote URL must not start scheduler")
	}
}

func TestPricingNonEmptyInvalidRemoteURLStillReturnsValidationError(t *testing.T) {
	svc := NewService(Options{
		RemoteURL: "://invalid",
		DataDir:   t.TempDir(),
	}, nil)

	err := svc.ForceUpdate()

	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid pricing url")
}

// TestCatalogService_ExplicitCatalogEntryDoesNotRedirectToSol 检查自定义目录条目按完整模型名读取价格和产品规则。
func TestCatalogService_ExplicitCatalogEntryDoesNotRedirectToSol(t *testing.T) {
	pricingSvc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
		"gpt-5.6":       {InputCostPerToken: 4e-6},
		"gpt-5.6-sol":   {InputCostPerToken: 5e-6},
		"gpt-5.6-terra": {InputCostPerToken: 2e-6},
		"gpt-5.6-luna":  {InputCostPerToken: 0.2e-6},
		"gpt-5.4":       {InputCostPerToken: 2.5e-6},
	}})

	for i := range 100 {
		for _, alias := range []string{"gpt-5.6"} {
			pricing := pricingSvc.GetModelPricing(alias)
			require.NotNil(t, pricing)
			require.InDelta(t, 4e-6, pricing.InputCostPerToken, 1e-12, "iteration=%d alias=%s", i, alias)
		}
	}

	billingSvc := newBillingFixture(pricingSvc)
	for _, alias := range []string{"gpt-5.6"} {
		pricing, err := billingSvc.GetModelPricing(alias)
		require.NoError(t, err)
		require.InDelta(t, 4e-6, pricing.InputPricePerToken, 1e-12)
		require.Zero(t, pricing.CacheCreationPricePerToken)
	}
}

// TestCatalogService_MergesFallbackOnlyModels 检查补充文件中的新模型可查询，远程 token 报价优先。
func TestCatalogService_MergesFallbackOnlyModels(t *testing.T) {
	svc := newHotReloadCatalog(t, `{"remote-model":{"input_cost_per_token":9,"output_cost_per_token":9},"local-model":{"input_cost_per_token":0.000004,"output_cost_per_token":0.000008}}`)
	require.InDelta(t, 1e-6, svc.GetModelPricing("remote-model").InputCostPerToken, 1e-12)
	require.InDelta(t, 4e-6, svc.GetModelPricing("local-model").InputCostPerToken, 1e-12)
}

func TestGetModelPricing_Gpt53CodexSparkUsesGpt51CodexPricing(t *testing.T) {
	sparkPricing := &billingpricing.CatalogModelPricing{InputCostPerToken: 1}
	gpt53Pricing := &billingpricing.CatalogModelPricing{InputCostPerToken: 9}

	svc := newModelCatalogFixture(modelCatalogFixture{
		pricingData: map[string]*billingpricing.CatalogModelPricing{
			"gpt-5.1-codex": sparkPricing,
			"gpt-5.3":       gpt53Pricing,
		},
	})

	got := svc.GetModelPricing("gpt-5.3-codex-spark")
	require.Nil(t, got)
}

func TestGetModelPricing_Gpt53CodexFallbackStillUsesGpt52Codex(t *testing.T) {
	gpt52CodexPricing := &billingpricing.CatalogModelPricing{InputCostPerToken: 2}

	svc := newModelCatalogFixture(modelCatalogFixture{
		pricingData: map[string]*billingpricing.CatalogModelPricing{
			"gpt-5.2-codex": gpt52CodexPricing,
		},
	})

	got := svc.GetModelPricing("gpt-5.3-codex")
	require.Nil(t, got)
}

func TestGetModelPricing_OpenAIFallbackMatchedLoggedAsInfo(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()

	gpt52CodexPricing := &billingpricing.CatalogModelPricing{InputCostPerToken: 2}
	svc := newModelCatalogFixture(modelCatalogFixture{
		pricingData: map[string]*billingpricing.CatalogModelPricing{
			"gpt-5.2-codex": gpt52CodexPricing,
		},
	})

	got := svc.GetModelPricing("gpt-5.3-codex")
	require.Nil(t, got)

	require.False(t, logSink.ContainsMessageAtLevel("[Pricing] OpenAI fallback matched gpt-5.3-codex -> gpt-5.2-codex", "info"))
	require.False(t, logSink.ContainsMessageAtLevel("[Pricing] OpenAI fallback matched gpt-5.3-codex -> gpt-5.2-codex", "warn"))
}

func TestGetModelPricing_UnknownCompactAliasIsUnpriced(t *testing.T) {
	svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{}})
	for _, model := range []string{"openai/gpt5.5", "gpt-5.5-openai-compact", "gpt-5.5-preview"} {
		require.Nil(t, svc.GetModelPricing(model))
	}
}

func TestCatalogService_Gemini36FlashThinkingTiersUseBasePricing(t *testing.T) {
	basePricing := &billingpricing.CatalogModelPricing{
		InputCostPerToken:       1.5e-6,
		OutputCostPerToken:      7.5e-6,
		CacheReadInputTokenCost: 0.15e-6,
	}
	svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
		"gemini-3.6-flash": basePricing,
	}})

	for _, model := range []string{
		"gemini-3.6-flash",
		"gemini-3.6-flash-high",
		"gemini-3.6-flash-low",
		"gemini-3.6-flash-medium",
		"gemini-3.6-flash-tiered",
	} {
		t.Run(model, func(t *testing.T) {
			if model == "gemini-3.6-flash" || model == "gemini-3.5-flash" {
				require.Same(t, basePricing, svc.GetModelPricing(model))
			} else {
				require.Nil(t, svc.GetModelPricing(model))
			}
		})
	}
}

// TestCatalogService_Gemini35FlashThinkingTiersUseBasePricing 验证后缀型号不会复用基础模型价格。
func TestCatalogService_Gemini35FlashThinkingTiersUseBasePricing(t *testing.T) {
	basePricing := &billingpricing.CatalogModelPricing{
		InputCostPerToken:       1.5e-6,
		OutputCostPerToken:      9e-6,
		CacheReadInputTokenCost: 0.15e-6,
	}
	svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
		"gemini-3.5-flash": basePricing,
	}})

	for _, model := range []string{
		"gemini-3.5-flash",
		"gemini-3.5-flash-high",
		"gemini-3.5-flash-low",
		"gemini-3.5-flash-medium",
		"gemini-3.5-flash-tiered",
	} {
		t.Run(model, func(t *testing.T) {
			if model == "gemini-3.6-flash" || model == "gemini-3.5-flash" {
				require.Same(t, basePricing, svc.GetModelPricing(model))
			} else {
				require.Nil(t, svc.GetModelPricing(model))
			}
		})
	}
}

func TestCatalogService_Gemini36FlashTierSpecificPricingTakesPrecedence(t *testing.T) {
	basePricing := &billingpricing.CatalogModelPricing{InputCostPerToken: 1.5e-6}
	tierPricing := &billingpricing.CatalogModelPricing{InputCostPerToken: 2e-6}
	svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
		"gemini-3.6-flash":     basePricing,
		"gemini-3.6-flash-low": tierPricing,
	}})

	require.Same(t, tierPricing, svc.GetModelPricing("models/gemini-3.6-flash-low"))
}

// TestCatalogService_Gemini35FlashTierSpecificPricingTakesPrecedence 验证未来出现档位专属价格时优先精确匹配。
func TestCatalogService_Gemini35FlashTierSpecificPricingTakesPrecedence(t *testing.T) {
	basePricing := &billingpricing.CatalogModelPricing{InputCostPerToken: 1.5e-6}
	tierPricing := &billingpricing.CatalogModelPricing{InputCostPerToken: 2e-6}
	svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
		"gemini-3.5-flash":     basePricing,
		"gemini-3.5-flash-low": tierPricing,
	}})

	require.Same(t, tierPricing, svc.GetModelPricing("models/gemini-3.5-flash-low"))
}

func TestDefaultPricingIncludesGemini36FlashRates(t *testing.T) {
	svc := NewService(Options{DataDir: t.TempDir()}, nil)
	require.NoError(t, svc.Initialize())
	require.NotNil(t, svc.GetModelPricing("gemini-3.6-flash"))
	// 后缀型号按完整 ID 查询目录报价。
	for _, suffix := range []string{"high", "low", "medium", "tiered"} {
		model := "gemini-3.6-flash-" + suffix
		if _, exists := svc.pricingData[model]; !exists {
			require.Nil(t, svc.GetModelPricing(model))
		}
	}
}

// TestDefaultPricingIncludesGemini35FlashRates 验证内置快照只为已登记的完整型号提供价格。
func TestDefaultPricingIncludesGemini35FlashRates(t *testing.T) {
	svc := NewService(Options{DataDir: t.TempDir()}, nil)
	require.NoError(t, svc.Initialize())
	require.NotNil(t, svc.GetModelPricing("gemini-3.5-flash"))
	// 后缀型号按完整 ID 查询目录报价。
	for _, suffix := range []string{"high", "low", "medium", "tiered"} {
		model := "gemini-3.5-flash-" + suffix
		if _, exists := svc.pricingData[model]; !exists {
			require.Nil(t, svc.GetModelPricing(model))
		}
	}
}

// TestDefaultCatalogRequiresConfiguredAutoReviewPricing 要求内部型号通过手动价卡或模型映射取得价格。
func TestDefaultCatalogRequiresConfiguredAutoReviewPricing(t *testing.T) {
	service := newOfflinePricingFixture(t)
	require.NotContains(t, service.Snapshot().Data, "codex-auto-review")
	require.Nil(t, service.GetModelPricing("codex-auto-review"))
}

func TestGetModelPricing_ImageModelDoesNotFallbackToTextModel(t *testing.T) {
	imagePricing := &billingpricing.CatalogModelPricing{InputCostPerToken: 3}
	textPricing := &billingpricing.CatalogModelPricing{InputCostPerToken: 9}

	svc := newModelCatalogFixture(modelCatalogFixture{
		pricingData: map[string]*billingpricing.CatalogModelPricing{
			"gpt-image-2": imagePricing,
			"gpt-5.4":     textPricing,
		},
	})

	got := svc.GetModelPricing("gpt-image-3")
	require.Nil(t, got)
}

func TestListModelNamesByProvider_ReturnsMatchingModels(t *testing.T) {
	svc := newModelCatalogFixture(modelCatalogFixture{
		pricingData: map[string]*billingpricing.CatalogModelPricing{
			"claude-opus-4-5-20251101": {Provider: "anthropic", InputCostPerToken: 1.5e-5},
			"claude-sonnet-4-5":        {Provider: "anthropic", InputCostPerToken: 3e-6},
			"gpt-4o":                   {Provider: "openai", InputCostPerToken: 5e-6},
			"gemini-2.5-pro":           {Provider: "google", InputCostPerToken: 1.25e-6},
		},
	})

	got := svc.ListModelNamesByProvider("anthropic")
	require.ElementsMatch(t, []string{"claude-opus-4-5-20251101", "claude-sonnet-4-5"}, got)
	// 模型名按字母序排列。
	require.Equal(t, "claude-opus-4-5-20251101", got[0])
	require.Equal(t, "claude-sonnet-4-5", got[1])
}

func TestListModelNamesByProvider_CaseInsensitive(t *testing.T) {
	svc := newModelCatalogFixture(modelCatalogFixture{
		pricingData: map[string]*billingpricing.CatalogModelPricing{
			"gpt-4o": {Provider: "OpenAI", InputCostPerToken: 5e-6},
		},
	})

	got := svc.ListModelNamesByProvider("openai")
	require.Equal(t, []string{"gpt-4o"}, got)

	got2 := svc.ListModelNamesByProvider("OPENAI")
	require.Equal(t, []string{"gpt-4o"}, got2)
}

func TestListModelNamesByProvider_NoMatch(t *testing.T) {
	svc := newModelCatalogFixture(modelCatalogFixture{
		pricingData: map[string]*billingpricing.CatalogModelPricing{
			"gpt-4o": {Provider: "openai", InputCostPerToken: 5e-6},
		},
	})

	got := svc.ListModelNamesByProvider("anthropic")
	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestListModelNamesByProvider_EmptyCatalog(t *testing.T) {
	svc := newModelCatalogFixture(modelCatalogFixture{
		pricingData: map[string]*billingpricing.CatalogModelPricing{},
	})

	got := svc.ListModelNamesByProvider("openai")
	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestGetModelModalities(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		data    map[string]*billingpricing.CatalogModelPricing
		wantIn  []string
		wantOut []string
	}{
		{
			name:  "supported_modalities 优先且按固定顺序输出",
			model: "gemini-2.5-flash",
			data: map[string]*billingpricing.CatalogModelPricing{
				"gemini-2.5-flash": {
					Mode:                      "chat",
					SupportedModalities:       []string{"video", "text", "image"},
					SupportedOutputModalities: []string{"image", "text"},
				},
			},
			wantIn:  []string{"text", "image", "video"},
			wantOut: []string{"text", "image"},
		},
		{
			name:  "模态缺失时用 mode 兜底并用 supports_vision 补充图片输入",
			model: "gpt-5.5",
			data: map[string]*billingpricing.CatalogModelPricing{
				"gpt-5.5": {Mode: "chat", SupportsVision: true},
			},
			wantIn:  []string{"text", "image"},
			wantOut: []string{"text"},
		},
		{
			name:  "生图模型用图片输入价识别图生图能力",
			model: "gpt-image-2",
			data: map[string]*billingpricing.CatalogModelPricing{
				"gpt-image-2": {Mode: "image_generation", InputCostPerImageToken: 8e-6},
			},
			wantIn:  []string{"text", "image"},
			wantOut: []string{"image"},
		},
		{
			name:  "纯生图模型只有文字输入",
			model: "flux-schnell",
			data: map[string]*billingpricing.CatalogModelPricing{
				"flux-schnell": {Mode: "image_generation"},
			},
			wantIn:  []string{"text"},
			wantOut: []string{"image"},
		},
		{
			name:  "音频输入输出标记合成音频模态",
			model: "gpt-realtime",
			data: map[string]*billingpricing.CatalogModelPricing{
				"gpt-realtime": {Mode: "realtime", SupportsAudioInput: true, SupportsAudioOutput: true},
			},
			wantIn:  []string{"text", "audio"},
			wantOut: []string{"text", "audio"},
		},
		{
			name:  "非模态取值被过滤",
			model: "file-model",
			data: map[string]*billingpricing.CatalogModelPricing{
				"file-model": {
					Mode:                      "chat",
					SupportedModalities:       []string{"text", "file"},
					SupportedOutputModalities: []string{"text"},
				},
			},
			wantIn:  []string{"text"},
			wantOut: []string{"text"},
		},
		{
			name:  "版本写法不同不命中",
			model: "claude-opus-4-5-20251101",
			data: map[string]*billingpricing.CatalogModelPricing{
				"claude-opus-4.5-20251101": {Mode: "chat", SupportsVision: true},
			},
			wantIn:  nil,
			wantOut: nil,
		},
		{
			name:  "查不到时返回 nil",
			model: "unknown-model",
			data: map[string]*billingpricing.CatalogModelPricing{
				"claude-opus-4.5": {Mode: "chat"},
			},
			wantIn:  nil,
			wantOut: nil,
		},
		{
			name:  "不做系列模糊回退，避免新模型继承旧模型能力",
			model: "claude-opus-4.6",
			data: map[string]*billingpricing.CatalogModelPricing{
				"claude-opus-4.5": {Mode: "chat", SupportsVision: true},
			},
			wantIn:  nil,
			wantOut: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newModelCatalogFixture(modelCatalogFixture{pricingData: tt.data})
			in, out := svc.GetModelModalities(tt.model)
			require.Equal(t, tt.wantIn, in)
			require.Equal(t, tt.wantOut, out)
		})
	}
}

// TestCatalogQueryFreezesOneCandidateFactory 检查每次查询生成一次完整型号候选。
func TestCatalogQueryFreezesOneCandidateFactory(t *testing.T) {
	factories, lookups := 0, 0
	options := Options{ModelLookupCandidates: func() func(string) []string {
		factories++
		return func(model string) []string {
			lookups++
			if model == "gpt-9.0" {
				return []string{"priced-model"}
			}
			return []string{model}
		}
	}}
	service := NewServiceFromSnapshot(options, nil, Snapshot{Data: map[string]*CatalogModelPricing{"priced-model": {InputCostPerToken: 0.001}}})
	price := service.GetModelPricing("gpt-9.0-20260101")
	require.Nil(t, price)
	require.Equal(t, 1, factories)
	require.Equal(t, 1, lookups)
}

// TestCatalogSnapshotIsIndependent 检查快照数据可独立修改，并区分 nil 和空切片。
func TestCatalogSnapshotIsIndependent(t *testing.T) {
	service := NewServiceFromSnapshot(Options{}, nil, Snapshot{Data: map[string]*CatalogModelPricing{"model": {InputCostPerToken: 1, SupportedModalities: []string{"text"}, SupportedOutputModalities: []string{}}}})
	snapshot := service.Snapshot()
	require.NotNil(t, snapshot.Data["model"].SupportedOutputModalities)
	snapshot.Data["model"].InputCostPerToken = 2
	snapshot.Data["model"].SupportedModalities[0] = "image"
	delete(snapshot.Data, "model")
	stored := service.Snapshot().Data["model"]
	require.Equal(t, 1.0, stored.InputCostPerToken)
	require.Equal(t, []string{"text"}, stored.SupportedModalities)
	empty := NewServiceFromSnapshot(Options{}, nil, Snapshot{})
	require.Nil(t, empty.Snapshot().Data)
}

// TestPricingConstructionAndLifecycle 检查初始化后启动更新任务，重复停止等待同一任务退出。
func TestPricingConstructionAndLifecycle(t *testing.T) {
	remote := &lifecyclePricingRemote{}
	service := NewService(Options{DataDir: t.TempDir(), RemoteURL: "https://pricing.invalid/catalog"}, remote)
	require.Zero(t, remote.calls.Load())
	require.NoError(t, service.Initialize())
	require.Zero(t, remote.calls.Load())
	service.Start()
	service.Start()
	service.Stop()
	service.Stop()
	require.Equal(t, int64(1), remote.calls.Load())
	require.NotNil(t, service.GetModelPricing("remote-model"))
}

// TestDeepseekDefaultCatalogUsesNativeEntries 验证默认目录原厂报价；中继的历史型号仍可独立存在。
func TestDeepseekDefaultCatalogUsesNativeEntries(t *testing.T) {
	pricingService := newOfflinePricingFixture(t)
	pricingData := pricingService.Snapshot().Data

	for _, tc := range []struct {
		model                 string
		input, output, cached float64
	}{
		{"deepseek-v4-flash", 0.15e-6, 0.6e-6, 0.003e-6},
		{"deepseek-v4-flash-vision-exp", 0.15e-6, 0.6e-6, 0.003e-6},
		{"deepseek-v4-pro", 0.66e-6, 1.98e-6, 0.022e-6},
	} {
		entry, exists := pricingData["deepseek/"+tc.model]
		require.True(t, exists, "%s 必须存在于原厂价格目录", tc.model)
		require.NotNil(t, entry)
		require.Equal(t, "models.dev", entry.Source)
		require.Equal(t, "deepseek", entry.Provider)
		require.InDelta(t, tc.input, entry.InputCostPerToken, 1e-15)
		require.InDelta(t, tc.output, entry.OutputCostPerToken, 1e-15)
		require.InDelta(t, tc.cached, entry.CacheReadInputTokenCost, 1e-15)
	}
}

// TestEmbeddedSupplementsCustomLayer 验证目录优先、自定义零价、官方缺项及删除后的恢复。
func TestEmbeddedSupplementsCustomLayer(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "custom.json")
	custom := []byte(`{"gemini-3-pro-image":{"input_cost_per_token":99,"output_cost_per_token":0,"image_prices":{"1K":0}},"_billing_defaults":{"web_search_price_per_call":0}}`)
	require.NoError(t, os.WriteFile(file, custom, 0o600))
	service := NewService(Options{DataDir: dir, FallbackFile: file}, nil)
	require.NoError(t, service.Initialize())
	for _, name := range []string{"gemini-3-pro-image", "google/gemini-3-pro-image"} {
		price := service.GetModelPricing(name)
		require.NotNil(t, price)
		require.NotEqual(t, 99.0, price.InputCostPerToken, "自定义补充不能覆盖目录已有报价")
		require.Zero(t, price.OutputCostPerToken)
		require.Zero(t, price.ImagePrices["1K"])
	}
	require.Zero(t, *service.BillingDefaults().WebSearchPricePerCall)
	require.Equal(t, 15.0, *service.BillingDefaults().AudioTTSPricePerMillionChars)
	saved, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, custom, saved, "加载不能改写自定义文件")

	before := service.Snapshot()
	require.NoError(t, os.WriteFile(file, []byte(`{"_billing_defaults":null}`), 0o600))
	require.Error(t, service.ForceUpdate())
	require.Equal(t, before.Data, service.Snapshot().Data)
	require.Equal(t, before.BillingDefaults, service.BillingDefaults())

	require.NoError(t, os.Remove(file))
	service.reloadIfCustomFilesChanged()
	for _, name := range []string{"gemini-3-pro-image", "google/gemini-3-pro-image"} {
		price := service.GetModelPricing(name)
		require.Equal(t, 12e-6, price.OutputCostPerToken)
		require.Equal(t, 0.134, price.ImagePrices["1K"])
	}
	require.Equal(t, 0.01, *service.BillingDefaults().WebSearchPricePerCall)
}

// TestEmbeddedSupplementsSurviveInvalidCustomFile 验证首次启动遇到损坏文件时仍保留官方补充。
func TestEmbeddedSupplementsSurviveInvalidCustomFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "custom.json")
	require.NoError(t, os.WriteFile(file, []byte(`{`), 0o600))
	service := NewService(Options{DataDir: dir, FallbackFile: file}, nil)
	require.NoError(t, service.Initialize())
	require.NotEmpty(t, service.AttributesSnapshot().LastError)
	require.Equal(t, 0.134, service.GetModelPricing("gemini-3-pro-image").ImagePrices["1K"])
	require.Equal(t, 0.01, *service.BillingDefaults().WebSearchPricePerCall)
}

// TestModelsCatalogFallbackIsStable 检查目录按原厂完整型号查询价格和属性。
func TestModelsCatalogFallbackIsStable(t *testing.T) {
	service := NewService(Options{DataDir: t.TempDir()}, nil)
	require.NoError(t, service.Initialize())
	for _, model := range []string{"claude-opus-4-5", "claude-opus-4-6"} {
		require.NotNil(t, service.GetModelPricing(model))
		require.Nil(t, service.GetModelPricing(model+"-20990101"))
		require.Nil(t, service.ModelAttributes(model+"-20990101").Context)
	}
}

func TestModelsCatalogFallbackOriginAndSupplements(t *testing.T) {
	dir := t.TempDir()
	patch := filepath.Join(dir, "supplement.json")
	service := NewService(Options{
		DataDir:      dir,
		RemoteURL:    "https://models.dev/catalog.json",
		FallbackFile: patch,
	}, &catalogRemoteFixture{body: []byte(fallbackOriginFixture)})
	require.NoError(t, service.ForceUpdate())
	check := func(reader *Service, input float64) {
		require.InDelta(t, input, reader.GetModelPricing("claude-opus-4-6").InputCostPerToken, 1e-12)
		for range 20 {
			for _, model := range []string{"claude-opus-4-6-thinking", "claude-opus-4-6-20990101"} {
				require.Nil(t, reader.GetModelPricing(model))
			}
		}
		require.InDelta(t, 1e-6, reader.GetModelPricing("relay/claude-opus-4-6").InputCostPerToken, 1e-12)
		require.Equal(t, "unpriced", reader.GetModelPricing("relay/claude-opus-4-6-thinking").Source)
		for _, model := range []string{"gpt-5.4-20990101", "gpt-5.4-openai-compact"} {
			require.Nil(t, reader.GetModelPricing(model))
		}
		require.InDelta(t, 1e-6, reader.GetModelPricing("relay/gpt-5.4").InputCostPerToken, 1e-12)
	}
	check(service, 5e-6)
	check(service.ReadOnlySnapshot(), 5e-6)
	require.NoError(t, os.WriteFile(patch, []byte(`{
		"claude-opus-4-6":{"input_cost_per_token":0},
		"claude-opus-4-7":{"input_cost_per_token":0.000008,"output_cost_per_token":0.00004}
	}`), 0o600))
	require.NoError(t, service.ForceUpdate())
	check(service, 5e-6)
	check(service.ReadOnlySnapshot(), 5e-6)
	// 本地补充可添加精确模型，目录已有报价优先。
	require.InDelta(t, 8e-6, service.GetModelPricing("claude-opus-4-7").InputCostPerToken, 1e-12)
}

func TestModelsCatalogFallbackDoesNotBorrowRelayOnlyModel(t *testing.T) {
	remote := &catalogRemoteFixture{body: []byte(`{"providers":{"relay":{"models":{"claude-opus-4-6":{"cost":{"input":1,"output":2}}}}}}`)}
	service := NewService(Options{DataDir: t.TempDir(), RemoteURL: "https://models.dev/catalog.json"}, remote)
	require.NoError(t, service.ForceUpdate())
	// 唯一来源的完整名称可查询中继报价，未知变体返回未定价。
	require.NotNil(t, service.GetModelPricing("claude-opus-4-6"))
	require.Nil(t, service.GetModelPricing("claude-opus-4-6-thinking"))
	require.Nil(t, service.ReadOnlySnapshot().GetModelPricing("claude-opus-4-6-thinking"))
}

func TestModelsCatalogExactMediaSupplementKeepsExplicitZero(t *testing.T) {
	dir := t.TempDir()
	supplement := filepath.Join(dir, "supplement.json")
	require.NoError(t, os.WriteFile(supplement, []byte(`{
		"gpt-image-2":{"input_cost_per_image_token":0.000008},
		"openai/gpt-image-2":{"input_cost_per_image_token":0}
	}`), 0o600))
	service := NewService(Options{DataDir: dir, RemoteURL: "https://models.dev/catalog.json", FallbackFile: supplement}, &catalogRemoteFixture{body: []byte(mediaAliasFixture)})
	require.NoError(t, service.ForceUpdate())
	require.InDelta(t, 8e-6, service.GetModelPricing("gpt-image-2").InputCostPerImageToken, 1e-12)
	qualified := service.GetModelPricing("openai/gpt-image-2")
	require.True(t, qualified.ImageInputPricePresent)
	require.Zero(t, qualified.InputCostPerImageToken)
}

func TestModelsCatalogInvalidPricePatchKeepsPublishedSnapshot(t *testing.T) {
	dir := t.TempDir()
	patch := filepath.Join(dir, "supplement.json")
	remote := &catalogRemoteFixture{body: []byte(modelsCatalogFixture)}
	service := NewService(Options{DataDir: dir, RemoteURL: "https://models.dev/catalog.json", FallbackFile: patch}, remote)
	require.NoError(t, service.ForceUpdate())
	before := service.AttributesSnapshot()
	beforePrices := service.Snapshot().Data
	beforeFile, err := os.ReadFile(service.catalogFilePath())
	require.NoError(t, err)
	remote.body = []byte(strings.ReplaceAll(modelsCatalogFixture, `"name":"Claude"`, `"name":"Changed"`))
	for _, tc := range []struct {
		name  string
		patch string
		field string
	}{
		{"base price", `{"input_cost_per_token":"invalid"}`, "input_cost_per_token"},
		{"tier price", `{"context_prices":[{"threshold":100,"pricing":{"input_cost_per_token":"invalid","output_cost_per_token":0.000015}}]}`, "input_cost_per_token"},
		{"threshold", `{"long_context_input_token_threshold":"invalid"}`, "long_context_input_token_threshold"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, os.WriteFile(patch, []byte(`{"claude-test":`+tc.patch+`}`), 0o600))
			err := service.ForceUpdate()
			require.Error(t, err)
			require.ErrorContains(t, err, "claude-test")
			require.ErrorContains(t, err, tc.field)
			after := service.AttributesSnapshot()
			require.Equal(t, before.Version, after.Version)
			require.Equal(t, before.LastUpdated, after.LastUpdated)
			require.Equal(t, before.Items, after.Items)
			require.Equal(t, beforePrices, service.Snapshot().Data)
			require.Contains(t, after.LastError, tc.field)
			afterFile, err := os.ReadFile(service.catalogFilePath())
			require.NoError(t, err)
			require.Equal(t, beforeFile, afterFile)
		})
	}
	// 合法的零价可以正常发布，并清除之前的错误。
	require.NoError(t, os.WriteFile(patch, []byte(`{"claude-test":{"input_cost_per_token":0}}`), 0o600))
	require.NoError(t, service.ForceUpdate())
	require.InDelta(t, 3e-6, service.GetModelPricing("claude-test").InputCostPerToken, 1e-12)
	require.Empty(t, service.AttributesSnapshot().LastError)
	require.NotNil(t, service.ModelAttributes("attributes-only").DisplayName)
}

func TestModelsCatalogInvalidLocalReloadReportsError(t *testing.T) {
	dir := t.TempDir()
	patch := filepath.Join(dir, "supplement.json")
	remote := &catalogRemoteFixture{body: []byte(modelsCatalogFixture), etag: "v1"}
	service := NewService(Options{DataDir: dir, RemoteURL: "https://models.dev/catalog.json", FallbackFile: patch}, remote)
	require.NoError(t, service.ForceUpdate())
	before := service.Snapshot().Data
	require.NoError(t, os.WriteFile(patch, []byte(`{"claude-test":{"input_cost_per_token":"invalid"}}`), 0o600))
	// 304 表示远程内容未变，同步入口仍报告本地补充的校验错误。
	remote.unchanged = true
	require.ErrorContains(t, service.syncWithRemote(), "input_cost_per_token")
	service.reloadIfCustomFilesChanged()
	require.Contains(t, service.AttributesSnapshot().LastError, "input_cost_per_token")
	require.Equal(t, before, service.Snapshot().Data)
}

func TestModelsCatalogMistralNativeAlias(t *testing.T) {
	service := NewService(Options{DataDir: t.TempDir()}, nil)
	require.NoError(t, service.Initialize())
	plain := catalogPriceForTest(t, service, "devstral-latest")
	qualified := catalogPriceForTest(t, service, "mistral/devstral-latest")
	require.InDelta(t, 0.4e-6, plain.InputPricePerToken, 1e-12)
	require.Equal(t, qualified, plain)
	require.Equal(t, service.ModelAttributes("mistral/devstral-latest"), service.ModelAttributes("devstral-latest"))
	require.NotNil(t, service.ModelAttributes("devstral-latest").Context)
	// 中继记录保持自己的价格与属性。
	require.InDelta(t, 0.44e-6, catalogPriceForTest(t, service, "requesty/devstral-latest").InputPricePerToken, 1e-12)
}

// TestModelsCatalogOfflineManualUpdate 验证磁盘缓存不存在时，手动更新仍能使用已加载的内存目录。
func TestModelsCatalogOfflineManualUpdate(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	patch := filepath.Join(dir, "supplement.json")
	// 用不可作为目录的路径稳定模拟缓存不可写，测试不依赖进程的文件权限。
	require.NoError(t, os.WriteFile(cache, []byte("unwritable cache"), 0o600))
	require.NoError(t, os.WriteFile(patch, []byte(`{"custom-model":{"input_cost_per_token":0.000007}}`), 0o600))
	service := NewService(Options{DataDir: cache, FallbackFile: patch}, nil)
	require.NoError(t, service.Initialize())
	before := service.AttributesSnapshot()
	require.NoError(t, os.WriteFile(patch, []byte(`{"custom-model":{"input_cost_per_token":0.000009}}`), 0o600))
	require.NoError(t, service.ForceUpdate())
	require.InDelta(t, 9e-6, service.GetModelPricing("custom-model").InputCostPerToken, 1e-12)
	require.Equal(t, before.Version, service.AttributesSnapshot().Version)
	require.Empty(t, service.AttributesSnapshot().LastError)
	// 本地更新同样完整校验，失败时保留上一次成功价格和属性。
	require.NoError(t, os.WriteFile(patch, []byte(`{"custom-model":{"input_cost_per_token":"invalid"}}`), 0o600))
	require.Error(t, service.ForceUpdate())
	require.InDelta(t, 9e-6, service.GetModelPricing("custom-model").InputCostPerToken, 1e-12)
	require.Equal(t, before.Items, service.AttributesSnapshot().Items)
	require.NotEmpty(t, service.AttributesSnapshot().LastError)
	require.NoError(t, os.WriteFile(patch, []byte(`{"custom-model":{"input_cost_per_token":0}}`), 0o600))
	require.NoError(t, service.ForceUpdate())
	require.Zero(t, service.GetModelPricing("custom-model").InputCostPerToken)
	require.Empty(t, service.AttributesSnapshot().LastError)
}

func TestModelsCatalogAtomicUpdateAndUnpricedAttributes(t *testing.T) {
	remote := &catalogRemoteFixture{body: []byte(modelsCatalogFixture), etag: "v1"}
	s := NewService(Options{RemoteURL: "https://models.dev/catalog.json", DataDir: t.TempDir()}, remote)
	require.NoError(t, s.ForceUpdate())
	require.Equal(t, "No price", *s.ModelAttributes("attributes-only").DisplayName)
	require.False(t, *s.ModelAttributes("attributes-only").Temperature)
	price := s.GetModelPricing("claude-test")
	require.False(t, price.CacheCreation1hPricePresent)
	require.Zero(t, price.CacheCreationInputTokenCostAbove1hr)
	require.Len(t, price.ContextPrices, 2)
	before := s.AttributesSnapshot()
	remote.body = []byte(`{"providers":{}}`)
	require.Error(t, s.ForceUpdate())
	after := s.AttributesSnapshot()
	require.Equal(t, before.Version, after.Version)
	require.Equal(t, before.LastUpdated, after.LastUpdated)
	require.NotEmpty(t, after.LastError)
	require.Equal(t, price.InputCostPerToken, s.GetModelPricing("claude-test").InputCostPerToken)
	remote.unchanged = true
	require.NoError(t, s.syncWithRemote())
	require.Equal(t, "v1", remote.validators[len(remote.validators)-1])
	require.Empty(t, s.AttributesSnapshot().LastError)
}

func TestModelsCatalogSupplementAndConcurrentReaders(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "supplement.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"claude-test":{"cache_creation_input_token_cost_above_1hr":0.000012}}`), 0o600))
	remote := &catalogRemoteFixture{body: []byte(modelsCatalogFixture)}
	s := NewService(Options{RemoteURL: "https://models.dev/catalog.json", DataDir: dir, FallbackFile: file}, remote)
	require.NoError(t, s.ForceUpdate())
	require.InDelta(t, 12e-6, s.GetModelPricing("claude-test").CacheCreationInputTokenCostAbove1hr, 1e-12)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 10 {
				_ = s.ModelAttributes("claude-test")
				_ = s.AttributesSnapshot()
				_ = s.ReadOnlySnapshot()
			}
		})
	}
	for range 3 {
		require.NoError(t, s.ForceUpdate())
	}
	wg.Wait()
}

func TestModelsCatalogOfflineFirstStart(t *testing.T) {
	s := NewService(Options{DataDir: t.TempDir()}, nil)
	require.NoError(t, s.Initialize())
	require.NotNil(t, s.ModelAttributes("claude-sonnet-4-5").Context)
	require.FileExists(t, s.catalogFilePath())
}

func TestModelsCatalogBrokenSupplementBootAndRecovery(t *testing.T) {
	dir := t.TempDir()
	patch := filepath.Join(dir, "supplement.json")
	require.NoError(t, os.WriteFile(patch, []byte("broken"), 0o600))
	s := NewService(Options{DataDir: dir, FallbackFile: patch}, nil)
	require.NoError(t, s.Initialize())
	require.NotEmpty(t, s.AttributesSnapshot().LastError)
	require.NotNil(t, s.ModelAttributes("claude-sonnet-4-5").Context)
	require.Nil(t, s.GetModelPricing("claude-opus-4-6-thinking"))
	require.NoError(t, os.WriteFile(patch, []byte(`{"custom-model":{"input_cost_per_token":0.000007}}`), 0o600))
	require.NoError(t, s.reloadCustomPricingLayers())
	require.Empty(t, s.AttributesSnapshot().LastError)
	require.InDelta(t, 7e-6, s.GetModelPricing("custom-model").InputCostPerToken, 1e-12)
}

func TestModelsCatalogReadOnlyCacheStillServesOfflineData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(path, []byte("file"), 0o600))
	s := NewService(Options{DataDir: path}, nil)
	require.NoError(t, s.Initialize())
	require.NotNil(t, s.ModelAttributes("claude-sonnet-4-5").Context)
}

// TestModelsCatalogConditionalUpdate 验证普通更新使用 ETag，强制更新清空条件，304 仍应用本地补充。
func TestModelsCatalogConditionalUpdate(t *testing.T) {
	dir := t.TempDir()
	patch := filepath.Join(dir, "supplement.json")
	remote := &catalogRemoteFixture{body: []byte(modelsCatalogFixture), etag: "v1"}
	service := NewService(Options{RemoteURL: "https://models.dev/catalog.json", DataDir: dir, FallbackFile: patch}, remote)
	require.NoError(t, service.ForceUpdate())
	require.NoError(t, service.syncWithRemote())
	require.Equal(t, []string{"", "v1"}, remote.validators)
	require.NoError(t, service.ForceUpdate())
	require.Equal(t, "", remote.validators[2])
	before := service.Snapshot()
	remote.unchanged = true
	require.NoError(t, os.WriteFile(patch, []byte(`{"custom-model":{"input_cost_per_token":0}}`), 0o600))
	require.NoError(t, service.syncWithRemote())
	require.Zero(t, service.GetModelPricing("custom-model").InputCostPerToken)
	require.Equal(t, before.LocalHash, service.Snapshot().LocalHash)
	require.Equal(t, before.LastUpdated, service.Snapshot().LastUpdated)
	require.NoError(t, os.Remove(patch))
	require.NoError(t, service.syncWithRemote())
	require.Nil(t, service.GetModelPricing("custom-model"))
}

// TestModelsCatalogRejectsLegacyRemote 验证自定义旧远程文件报迁移错误，内存和磁盘版本均保持不变。
func TestModelsCatalogRejectsLegacyRemote(t *testing.T) {
	remote := &catalogRemoteFixture{body: []byte(modelsCatalogFixture)}
	service := NewService(Options{RemoteURL: "https://custom.example/prices.json", DataDir: t.TempDir()}, remote)
	require.NoError(t, service.ForceUpdate())
	before := service.Snapshot()
	attrs := service.AttributesSnapshot()
	disk := readCatalogTestFile(t, service.catalogFilePath())
	remote.body = []byte(`{"gpt-test":{"input_cost_per_token":1}}`)
	require.ErrorContains(t, service.ForceUpdate(), "legacy pricing JSON")
	require.Equal(t, before.Data, service.Snapshot().Data)
	require.Equal(t, attrs.Items, service.AttributesSnapshot().Items)
	require.Equal(t, before.LocalHash, service.Snapshot().LocalHash)
	require.Equal(t, before.LastUpdated, service.Snapshot().LastUpdated)
	require.Equal(t, disk, readCatalogTestFile(t, service.catalogFilePath()))
}

// TestModelsCatalogDroppedAbsoluteTierWarns 检查更新后绝对阶梯消失时记录告警。
func TestModelsCatalogDroppedAbsoluteTierWarns(t *testing.T) {
	sink, restore := captureStructuredLog(t)
	defer restore()
	remote := &catalogRemoteFixture{body: []byte(modelsCatalogFixture)}
	service := NewService(Options{RemoteURL: "https://models.dev/catalog.json", DataDir: t.TempDir()}, remote)
	require.NoError(t, service.ForceUpdate())
	remote.body = []byte(`{"providers":{"anthropic":{"models":{"claude-test":{"cost":{"input":3,"output":15}}}}}}`)
	require.NoError(t, service.ForceUpdate())
	require.True(t, sink.ContainsMessageAtLevel("Long-context ladder dropped", "warn"))
}

// TestModelsCatalogAttributesOnlyWithUnknownPatch 验证没有价格的合法目录仍能发布属性，无价格补充不生成价格。
func TestModelsCatalogAttributesOnlyWithUnknownPatch(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "supplement.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"typo":{"long_context_input_token_threshold":0}}`), 0o600))
	remote := &catalogRemoteFixture{body: []byte(`{"providers":{"openai":{"models":{"attributes-only":{"name":"No prices"}}}}}`)}
	service := NewService(Options{RemoteURL: "https://models.dev/catalog.json", DataDir: dir, FallbackFile: file}, remote)
	require.NoError(t, service.ForceUpdate())
	require.NotContains(t, service.Snapshot().Data, "typo")
	require.Nil(t, service.GetModelPricing("attributes-only"))
	require.Equal(t, "No prices", *service.ModelAttributes("attributes-only").DisplayName)
}

// TestIncompleteCatalogFieldsAreNotReplaced 验证缺一侧报价时也保留目录已有字段。
func TestIncompleteCatalogFieldsAreNotReplaced(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "prices.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"partial":{"input_cost_per_token":99,"output_cost_per_token":0}}`), 0o600))
	remote := &catalogRemoteFixture{body: []byte(`{"providers":{"openai":{"models":{"partial":{"cost":{"input":2},"reasoning":false,"modalities":{"output":[]}}}}}}`)}
	service := NewService(Options{DataDir: dir, RemoteURL: "https://models.dev/catalog.json", FallbackFile: file}, remote)
	require.NoError(t, service.ForceUpdate())
	price := service.GetModelPricing("partial")
	require.InDelta(t, 2e-6, price.InputCostPerToken, 1e-12)
	require.Zero(t, price.OutputCostPerToken)
	require.False(t, price.TokenPricingAbsent)
	require.False(t, *service.ModelAttributes("partial").Reasoning)
	require.Empty(t, *service.ModelAttributes("partial").OutputModalities)
}

// TestSupplementRejectsInvalidOperationAndRule 检查无基础价或已有目录报价的补充条目仍需通过校验。
func TestSupplementRejectsInvalidOperationAndRule(t *testing.T) {
	for _, body := range []string{
		`{"_billing_defaults":{"web_search_price_per_call":-1}}`,
		`{"_billing_defaults":{"audio_tts_price_per_million_chars":"bad"}}`,
		`{"_billing_defaults":null}`,
		`{"claude-test":{"input_cost_per_token":-1}}`,
		`{"claude-test":{"fast_multiplier":0}}`,
		`{"claude-test":{"input_cost_per_token":1e308,"cache_write_multiplier":1e308}}`,
		`{"claude-test":{"video_prices":{"4K":1}}}`,
		`{"claude-test":{"image_prices":{"1K":null}}}`,
		`{"claude-test":{"time_pricing":{"timezone":"Local","periods":[]}}}`,
	} {
		t.Run(body, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "supplement.json")
			require.NoError(t, os.WriteFile(file, []byte(body), 0o600))
			_, err := loadLocalPricingEntries(file)
			require.Error(t, err)
		})
	}
}

func mutatePricingFixture(service *Service, change func(map[string]*billingpricing.CatalogModelPricing)) {
	data := service.Snapshot().Data
	change(data)
	setPricingFixtureData(service, data)
}

func setPricingFixtureRemote(service *Service, remote RemoteClient) {
	service.remoteClient = remote
}

func (s *inMemoryLogSink) WriteLogEvent(event *logging.LogEvent) {
	if event == nil {
		return
	}
	cloned := *event
	if event.Fields != nil {
		cloned.Fields = make(map[string]any, len(event.Fields))
		maps.Copy(cloned.Fields, event.Fields)
	}
	s.mu.Lock()
	s.events = append(s.events, &cloned)
	s.mu.Unlock()
}

func (s *inMemoryLogSink) ContainsMessage(substr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range s.events {
		if ev != nil && strings.Contains(ev.Message, substr) {
			return true
		}
	}
	return false
}

func (s *inMemoryLogSink) ContainsMessageAtLevel(substr, level string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	wantLevel := strings.ToLower(strings.TrimSpace(level))
	for _, ev := range s.events {
		if ev == nil {
			continue
		}
		if strings.Contains(ev.Message, substr) && strings.ToLower(strings.TrimSpace(ev.Level)) == wantLevel {
			return true
		}
	}
	return false
}

func captureStructuredLog(t *testing.T) (*inMemoryLogSink, func()) {
	t.Helper()
	structuredLogCaptureMu.Lock()

	err := logging.Init(logging.InitOptions{
		Level:       "debug",
		Format:      "json",
		ServiceName: "tokenrouter",
		Environment: "test",
		Output: logging.OutputOptions{
			ToStdout: true,
			ToFile:   false,
		},
		Sampling: logging.SamplingOptions{Enabled: false},
	})
	require.NoError(t, err)

	sink := &inMemoryLogSink{}
	logging.SetSink(sink)
	return sink, func() {
		logging.SetSink(nil)
		structuredLogCaptureMu.Unlock()
	}
}

// catalogLookupTestPricing 为不同型号设置不同单价，供测试识别命中的条目。
func catalogLookupTestPricing(input float64, modalities ...string) *billingpricing.CatalogModelPricing {
	return &billingpricing.CatalogModelPricing{
		InputCostPerToken: input, OutputCostPerToken: input * 5,
		CacheCreationInputTokenCost: input * 1.25, CacheReadInputTokenCost: input / 10,
		LongContextInputTokenThreshold: 200000, LongContextInputCostMultiplier: 2,
		LongContextOutputCostMultiplier: 1.5, Mode: "chat",
		SupportedModalities: modalities, SupportedOutputModalities: []string{"text"},
	}
}

func hotReloadModelJSON(name string, input, output float64) string {
	return `"` + name + `": {"provider": "test", "mode": "chat",
		"input_cost_per_token": ` + formatFloat(input) + `, "output_cost_per_token": ` + formatFloat(output) + `}`
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// newHotReloadCatalog 在数据目录里放好目录缓存与补充文件并完成首次加载。
// 传空串表示不配置补充文件。
func newHotReloadCatalog(t *testing.T, fallbackJSON string) *Service {
	t.Helper()
	dir := t.TempDir()
	svc := newModelCatalogFixture(modelCatalogFixture{options: Options{}})
	svc.options.DataDir = dir
	require.NoError(t, os.WriteFile(svc.catalogFilePath(), []byte(hotReloadCatalogJSON), 0o644))
	if fallbackJSON != "" {
		svc.options.FallbackFile = filepath.Join(dir, "fallback.json")
		require.NoError(t, os.WriteFile(svc.options.FallbackFile, []byte(fallbackJSON), 0o644))
	}
	require.NoError(t, svc.Initialize())
	return svc
}

func (c stubCatalogRemoteClient) FetchCatalog(context.Context, string, string) ([]byte, string, bool, error) {
	return []byte(c.body), "", false, nil
}

func (r *lifecyclePricingRemote) FetchCatalog(context.Context, string, string) ([]byte, string, bool, error) {
	r.calls.Add(1)
	return []byte(hotReloadCatalogJSON), "", false, nil
}
