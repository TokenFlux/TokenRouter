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
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// Complete catalog identities keep their own prices and modality declarations
// through resource-path lookup, supplement reload, and failed candidates.
// Fixtures compare returned values with retained source data and capture the
// warnings generated when a candidate's context rules are incomplete.
//
// Scheduled-loop tests join Wait after the configured lifecycle stops, while
// concurrent query tests join their readers before fixture teardown. Captured
// logging uses a separate global mutex because its sink is replaced for each
// capture. The sink copies event records and top-level field maps, leaving
// nested field values shared with their producer, so assertions use retained
// messages and levels rather than mutating those values.

// hotReloadCatalogJSON and fallbackOriginFixture supply base cache records and
// competing source prices for reload and complete-identity assertions.
const (
	hotReloadCatalogJSON = `{"openai":{"id":"openai","models":{"remote-model":{"id":"remote-model","type":"chat","capabilities":{},"cost":{"text":{"input":{"price":1},"output":{"price":2}}}}}}}`

	fallbackOriginFixture = `{
	"anthropic":{"id":"anthropic","models":{"claude-opus-4-6":{"id":"claude-opus-4-6","type":"chat","capabilities":{},"cost":{"text":{"input":{"price":5},"output":{"price":25}}}},"claude-opus-4-6-20260101":{"id":"claude-opus-4-6-20260101","type":"chat","capabilities":{},"cost":{"text":{"input":{"price":6},"output":{"price":30}}}}}},
	"openai":{"id":"openai","models":{"gpt-5.4":{"id":"gpt-5.4","type":"chat","capabilities":{},"cost":{"text":{"input":{"price":2.5},"output":{"price":15}}}}}},
	"ollama-cloud":{"id":"ollama-cloud","models":{"claude-opus-4-6":{"id":"claude-opus-4-6","type":"chat","capabilities":{},"cost":{"text":{"input":{"price":1},"output":{"price":2}}}},"claude-opus-4-6-discount":{"id":"claude-opus-4-6-discount","type":"chat","capabilities":{},"cost":{"text":{"input":{"price":0},"output":{"price":0}}}},"gpt-5.4":{"id":"gpt-5.4","type":"chat","capabilities":{},"cost":{"text":{"input":{"price":1},"output":{"price":3}}}}}}
}`
)

// structuredLogCaptureMu serializes captures that install and clear the global
// logging sink across these fixtures.
var structuredLogCaptureMu sync.Mutex

// inMemoryLogSink retains event copies for message and level assertions.
type inMemoryLogSink struct {
	// mu guards events while writers append and assertion helpers scan them.
	// Stored records own their top-level Fields maps, while referenced values
	// inside those maps remain shared with the logging producer.
	mu     sync.Mutex          // captured event access lock
	events []*logging.LogEvent // retained log message records
}

// stubCatalogRemoteClient supplies configured catalog JSON for forced download
// fixtures.
type stubCatalogRemoteClient struct {
	body string // configured catalog fixture JSON
}

// lifecyclePricingRemote counts scheduler fetches while returning a fixed
// catalog body.
type lifecyclePricingRemote struct {
	calls atomic.Int64 // observed catalog fetch count
}

// TestParsePricingData_WarnsOrphanCacheTierFields loads cache tier fields with
// and without their required base prices. Orphan fields must produce warning
// messages, while the complete cache record remains absent from those
// diagnostics.
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

// TestParsePricingData_WarnsLopsidedLongContextLadder compares a one-sided
// surcharge with a model whose input and output both increase. The asymmetric
// model must produce the asserted warning, while the consistent record does
// not.
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

// TestDefaultCatalogSnapshot_CacheTierContract reads GPT-5.6 Sol from the
// embedded publication. Its single context tier must retain the asserted
// threshold and distinct base and above-tier cache-read prices.
func TestDefaultCatalogSnapshot_CacheTierContract(t *testing.T) {
	service := newOfflinePricingFixture(t)
	price := service.GetModelPricing("gpt-5.6-sol")
	require.NotNil(t, price)
	require.Len(t, price.ContextPrices, 1)
	require.Equal(t, 272000, price.ContextPrices[0].Threshold)
	require.InDelta(t, 0.4e-6, price.CacheReadInputTokenCost, 1e-12)
	require.InDelta(t, 0.8e-6, price.ContextPrices[0].Pricing.CacheReadInputTokenCost, 1e-12)
}

// TestCatalogLookupGeminiThinkingTiers queries complete Gemini IDs through
// recognized resource paths, case folding, and suffix variants. Base identities
// must retain their configured price and modalities; missing suffix entries
// remain unresolved.
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

// TestCatalogLookupExactEntryWinsWithoutMerging configures a base entry and a
// separately priced high identity. Exact high lookups must return that record
// and derive its text modalities instead of borrowing the base capabilities.
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

// TestCatalogLookupGeminiRejectsUnknownAliases queries unconfigured Gemini
// suffixes around several known identities. Each unsupported complete name must
// return missing prices and modality lists even when a nearby base record
// exists.
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

// TestCatalogLookupClaudeEquivalentVersions compares dot, hyphen, thinking, and
// dated Claude spellings. A different spelling remains unresolved until
// separately configured, after which its exact price and input modalities take
// precedence.
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
	// Different date and version spellings leave input and output modalities
	// unknown when only the nearby complete identities have configured records.
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

// TestCatalogLookupOpenAIProductIdentity configures concrete OpenAI products
// alongside generic entries. Effort suffixes must have missing prices and input
// modalities; date and Compact suffixes must also have missing output modalities.
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

// TestCatalogLookupOpenAIDedicatedFallbackBeforeGenericBase supplies generic
// GPT family prices and queries concrete products and versions. Every
// unconfigured identity must remain unresolved for prices and modalities,
// preventing generic family inheritance.
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

// TestCatalogLookupOpenAIPriceFallbackKeepsDynamicProduct configures an exact
// product then queries preview and chat-latest variants. Those complete variant
// names must return missing prices and modalities despite the configured base
// record.
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

// TestCatalogLookupBareGPT56DoesNotBorrowOtherPrices queries bare GPT-5.6
// variants with concrete Sol and older products available. Billing must return
// pricing-unavailable with either source fixture, and bare modality lookup
// remains unknown.
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

// TestCatalogLookupSparkBillingPolicyDoesNotSupplyCapabilities configures an
// exact Spark price alongside an older audio-capable Codex record. Exact Spark
// selects its own entry, while an unconfigured high suffix retains missing
// prices and modalities.
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

// TestCatalogLookupGrokUsesKnownRuntimeAliases changes the runtime text default
// across case and prefix forms. Catalog aliases remain unresolved until given
// their own price, and unrelated names acquire no Grok modalities.
func TestCatalogLookupGrokUsesKnownRuntimeAliases(t *testing.T) {
	// Runtime defaults are process state shared with other request fixtures.
	// Save and restore this value around the alias checks so subsequent tests
	// retain their configured default.
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
	// Vary the process default's prefix and case while keeping catalog entries
	// fixed, isolating whether runtime model selection can supply an unconfigured
	// catalog price.
	for _, target := range []string{"GROK-4.6", "xai/grok-4.6", "X-AI/GROK-4.6", "grok-4.6-latest"} {
		xai.SetRuntimeDefaultTextModel(target)
		for _, alias := range []string{"grok", "grok-latest"} {
			require.Nil(t, svc.GetModelPricing(alias), target)
			input, _ := svc.GetModelModalities(alias)
			require.Nil(t, input, target)
		}
	}
	// Supply the alias as its own exact record after the unresolved checks.
	// Its price then resolves while unrelated identities retain their unknown
	// modality results.
	mutatePricingFixture(svc, func(data map[string]*billingpricing.CatalogModelPricing) { data["grok-latest"] = text })
	require.Same(t, text, svc.GetModelPricing("grok-latest"))
	for _, model := range []string{"grok-unknown", "gpt-5.4", "claude-sonnet-4"} {
		input, output := svc.GetModelModalities(model)
		require.Nil(t, input)
		require.Nil(t, output)
	}
}

// TestCatalogLookupGrokDefaultAliasCycle sets runtime defaults to alias-like
// Grok identities with an empty price map. Catalog lookup must remain
// unresolved for both prices and modalities as those defaults change.
func TestCatalogLookupGrokDefaultAliasCycle(t *testing.T) {
	// Restore the process default after alias-like values have been exercised.
	// The catalog fixture stays empty throughout, making missing identity results
	// independent of later tests' defaults.
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

// TestCatalogLookupModalityFieldCompatibility combines legacy input fields,
// primary declarations, and video support flags. Repeated queries must return
// sanitized modality lists while the stored source list keeps its declared
// contents.
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

// TestPricingHotReload_FallbackChangeRebuildsWithoutTouchingSyncAnchor edits an
// existing custom rate and adds another model. Reload must publish those prices
// and a new supplement fingerprint while retaining remote prices, raw hash, and
// update time.
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

// TestPricingHotReload_InvalidFileKeepsCurrentDataUntilFixed writes incomplete
// custom JSON and then repairs it. Rejection must retain prices and the
// successful fingerprint, while the repaired candidate replaces both on the
// next reload.
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

// TestPricingHotReload_DownloadRefreshesFingerprint downloads a catalog after
// replacing its custom supplement content. Publication must record the new
// fingerprint, and an unchanged local check must leave an inserted sentinel
// price intact.
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

// TestPricingSchedulerStartsForCustomFilesWithoutRemoteURL configures a
// supplement watch with remote downloads disabled. Wait must remain blocked
// while the scheduler is active, then complete within the asserted deadline
// after Stop.
func TestPricingSchedulerStartsForCustomFilesWithoutRemoteURL(t *testing.T) {
	svc := NewService(Options{
		RemoteURL:    "",
		FallbackFile: filepath.Join(t.TempDir(), "fallback.json"),
	}, nil)

	svc.startUpdateScheduler()
	done := make(chan struct{})
	// Join the scheduler in another goroutine so the test can observe it
	// staying active before Stop, then use the same completion signal after
	// its registered work ends.
	go func() {
		svc.Wait()
		close(done)
	}()

	// A custom watch keeps the loop registered even with no remote fetch.
	// The observation window distinguishes that active lifetime from an
	// immediate Wait completion.
	select {
	case <-done:
		t.Fatal("custom file watch must keep the scheduler running")
	case <-time.After(50 * time.Millisecond):
	}

	svc.Stop()
	// Stop has joined the scheduler; allow the separate waiter to publish its
	// completion signal before treating a missing signal as a failed lifecycle
	// observation.
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler must exit after Stop")
	}
}

// TestSupplementRemovalAndRecreation updates, deletes, and recreates a custom
// model with zero prices. Reload must follow those layers while retaining the
// remote hash and timestamp and accepting the explicit free record.
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

// TestPricingSchedulerBlankRemoteURLDoesNotStart uses a blank remote URL with
// no custom watch. Wait must return within the asserted observation window
// because no scheduled loop was registered for that configuration.
func TestPricingSchedulerBlankRemoteURLDoesNotStart(t *testing.T) {
	svc := NewService(Options{RemoteURL: "  \t  "}, nil)
	defer svc.Stop()

	svc.startUpdateScheduler()
	done := make(chan struct{})
	// Observe Wait from another goroutine so an incorrectly registered
	// scheduler becomes a bounded assertion failure, with the deferred Stop
	// available for fixture cleanup.
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

// TestPricingNonEmptyInvalidRemoteURLStillReturnsValidationError configures a
// malformed populated remote URL. ForceUpdate must return an error containing
// the pricing URL diagnostic before the absent downloader can supply a
// candidate.
func TestPricingNonEmptyInvalidRemoteURLStillReturnsValidationError(t *testing.T) {
	svc := NewService(Options{
		RemoteURL: "://invalid",
		DataDir:   t.TempDir(),
	}, nil)

	err := svc.ForceUpdate()

	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid pricing url")
}

// TestCatalogService_ExplicitCatalogEntryDoesNotRedirectToSol configures bare
// GPT-5.6 and its separate concrete products. Repeated catalog and billing
// queries must retain the bare entry price and its asserted cache price rather
// than selecting Sol.
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

// TestCatalogService_MergesFallbackOnlyModels supplements an existing remote
// identity and introduces another exact model. Catalog pricing must win for the
// existing record, while the newly supplied model retains its custom price.
func TestCatalogService_MergesFallbackOnlyModels(t *testing.T) {
	svc := newHotReloadCatalog(t, `{"remote-model":{"input_cost_per_token":9,"output_cost_per_token":9},"local-model":{"input_cost_per_token":0.000004,"output_cost_per_token":0.000008}}`)
	require.InDelta(t, 1e-6, svc.GetModelPricing("remote-model").InputCostPerToken, 1e-12)
	require.InDelta(t, 4e-6, svc.GetModelPricing("local-model").InputCostPerToken, 1e-12)
}

// TestGetModelPricing_Gpt53CodexSparkUsesGpt51CodexPricing provides older Codex
// and generic GPT-5.3 prices while requesting Spark. The unconfigured complete
// Spark identity must remain unresolved instead of selecting either supplied
// record.
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

// TestGetModelPricing_Gpt53CodexFallbackStillUsesGpt52Codex supplies only a
// GPT-5.2 Codex price and requests GPT-5.3 Codex. The lookup must return nil
// because that distinct complete identity has no configured price.
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

// TestGetModelPricing_OpenAIFallbackMatchedLoggedAsInfo queries an unconfigured
// GPT-5.3 Codex identity while capturing logs. Lookup must remain nil, and the
// asserted cross-model match message must be absent at both checked levels.
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

// TestGetModelPricing_UnknownCompactAliasIsUnpriced queries unconfigured
// spelling, Compact, and preview identities against an empty map. Every
// complete requested name must return nil rather than acquire an implicit
// product price.
func TestGetModelPricing_UnknownCompactAliasIsUnpriced(t *testing.T) {
	svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{}})
	for _, model := range []string{"openai/gpt5.5", "gpt-5.5-openai-compact", "gpt-5.5-preview"} {
		require.Nil(t, svc.GetModelPricing(model))
	}
}

// TestCatalogService_Gemini36FlashThinkingTiersUseBasePricing supplies the
// exact Gemini 3.6 Flash base price and queries its thinking suffixes. The base
// entry must resolve, while each missing suffixed identity returns nil.
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

// TestCatalogService_Gemini35FlashThinkingTiersUseBasePricing supplies the
// exact Gemini 3.5 Flash base price and queries its thinking suffixes. Base
// lookup must retain the supplied record, and unconfigured suffixed identities
// remain unresolved.
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

// TestCatalogService_Gemini36FlashTierSpecificPricingTakesPrecedence configures
// separate base and low identities for Gemini 3.6 Flash. A resource-prefixed
// low request must return that exact tier record with its independently
// supplied price.
func TestCatalogService_Gemini36FlashTierSpecificPricingTakesPrecedence(t *testing.T) {
	basePricing := &billingpricing.CatalogModelPricing{InputCostPerToken: 1.5e-6}
	tierPricing := &billingpricing.CatalogModelPricing{InputCostPerToken: 2e-6}
	svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
		"gemini-3.6-flash":     basePricing,
		"gemini-3.6-flash-low": tierPricing,
	}})

	require.Same(t, tierPricing, svc.GetModelPricing("models/gemini-3.6-flash-low"))
}

// TestCatalogService_Gemini35FlashTierSpecificPricingTakesPrecedence configures
// separate base and low identities for Gemini 3.5 Flash. Its resource-prefixed
// low request must select the independently configured tier entry rather than
// the base record.
func TestCatalogService_Gemini35FlashTierSpecificPricingTakesPrecedence(t *testing.T) {
	basePricing := &billingpricing.CatalogModelPricing{InputCostPerToken: 1.5e-6}
	tierPricing := &billingpricing.CatalogModelPricing{InputCostPerToken: 2e-6}
	svc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
		"gemini-3.5-flash":     basePricing,
		"gemini-3.5-flash-low": tierPricing,
	}})

	require.Same(t, tierPricing, svc.GetModelPricing("models/gemini-3.5-flash-low"))
}

// TestDefaultPricingIncludesGemini36FlashRates loads the embedded Gemini 3.6
// Flash price. Suffixed identities absent from the published map must also
// remain absent from lookup, preserving the complete-name distinction.
func TestDefaultPricingIncludesGemini36FlashRates(t *testing.T) {
	svc := NewService(Options{DataDir: t.TempDir()}, nil)
	require.NoError(t, svc.Initialize())
	require.NotNil(t, svc.GetModelPricing("gemini-3.6-flash"))
	for _, suffix := range []string{"high", "low", "medium", "tiered"} {
		model := "gemini-3.6-flash-" + suffix
		if _, exists := svc.pricingData[model]; !exists {
			require.Nil(t, svc.GetModelPricing(model))
		}
	}
}

// TestDefaultPricingIncludesGemini35FlashRates loads the embedded Gemini 3.5
// Flash price. Lookup must not supply prices for thinking suffixes that have no
// complete entry in the retained price map.
func TestDefaultPricingIncludesGemini35FlashRates(t *testing.T) {
	svc := NewService(Options{DataDir: t.TempDir()}, nil)
	require.NoError(t, svc.Initialize())
	require.NotNil(t, svc.GetModelPricing("gemini-3.5-flash"))
	for _, suffix := range []string{"high", "low", "medium", "tiered"} {
		model := "gemini-3.5-flash-" + suffix
		if _, exists := svc.pricingData[model]; !exists {
			require.Nil(t, svc.GetModelPricing(model))
		}
	}
}

// TestDefaultCatalogRequiresConfiguredAutoReviewPricing loads the embedded
// publication and queries the internal auto-review identity. That name must be
// absent from both the copied price map and direct price lookup.
func TestDefaultCatalogRequiresConfiguredAutoReviewPricing(t *testing.T) {
	service := newOfflinePricingFixture(t)
	require.NotContains(t, service.Snapshot().Data, "codex-auto-review")
	require.Nil(t, service.GetModelPricing("codex-auto-review"))
}

// TestGetModelPricing_ImageModelDoesNotFallbackToTextModel provides a known
// image price and a text product price. A different unconfigured image identity
// must return nil rather than selecting either of those records.
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

// TestListModelNamesByProvider_ReturnsMatchingModels queries one provider from
// mixed-provider fixture records. The result must contain exactly its two model
// identities and retain their asserted alphabetical order for administrative
// listing.
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
	require.Equal(t, "claude-opus-4-5-20251101", got[0])
	require.Equal(t, "claude-sonnet-4-5", got[1])
}

// TestListModelNamesByProvider_CaseInsensitive stores a mixed-case provider
// identity and queries lower and upper case. Both queries must return the same
// model list, protecting provider matching across those spellings.
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

// TestListModelNamesByProvider_NoMatch queries another provider from a
// populated fixture catalog. The unmatched result must be a nonnil empty list,
// allowing callers to enumerate it without a missing-result distinction.
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

// TestListModelNamesByProvider_EmptyCatalog queries a provider from an empty
// price map. The result must remain a nonnil empty list, matching the
// collection shape returned for populated maps with no matches.
func TestListModelNamesByProvider_EmptyCatalog(t *testing.T) {
	svc := newModelCatalogFixture(modelCatalogFixture{
		pricingData: map[string]*billingpricing.CatalogModelPricing{},
	})

	got := svc.ListModelNamesByProvider("openai")
	require.NotNil(t, got)
	require.Empty(t, got)
}

// TestGetModelModalities combines explicit modality fields, compatibility
// modes, media prices, and support flags. Sanitized results must match each
// fixture, while unknown identities and different version spellings retain nil
// lists.
func TestGetModelModalities(t *testing.T) {
	tests := []struct {
		name    string                                         // named modality lookup scenario
		model   string                                         // complete requested model identity
		data    map[string]*billingpricing.CatalogModelPricing // configured catalog price entries
		wantIn  []string                                       // expected input modality list
		wantOut []string                                       // expected output modality list
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

// TestCatalogQueryFreezesOneCandidateFactory injects counters into identity
// factory construction and lookup with a price-only snapshot. One price query
// must call each once and leave the unconfigured dated identity unresolved
// despite another base-like candidate rule.
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

// TestCatalogSnapshotIsIndependent mutates and removes copied price data after
// taking a snapshot. Stored scalar and modality values must remain intact, with
// empty output modalities and a nil price map retaining their shapes.
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

// TestPricingConstructionAndLifecycle observes the remote call count before
// initialization and after repeated Start and Stop. Construction and
// initialization perform no fetch, and one scheduled fetch leaves its prices
// queryable after both Stop calls.
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

// TestDeepseekDefaultCatalogUsesNativeEntries reads complete DeepSeek provider
// records from the embedded publication. Each asserted rate must retain its
// models.dev source, DeepSeek provider identity, and separate input, output,
// and cached values.
func TestDeepseekDefaultCatalogUsesNativeEntries(t *testing.T) {
	pricingService := newOfflinePricingFixture(t)
	pricingData := pricingService.Snapshot().Data

	for _, tc := range []struct {
		model                 string  // complete original model identity
		input, output, cached float64 // USD/token
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

// TestEmbeddedSupplementsCustomLayer overrides missing model fields and
// operation defaults with explicit custom zero prices. Catalog values remain
// authoritative, invalid defaults retain the publication, and deletion restores
// embedded model and operation values.
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
		require.Equal(t, 12e-6, price.OutputCostPerToken)
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

// TestEmbeddedSupplementsSurviveInvalidCustomFile initializes with malformed
// custom supplement JSON. Startup must retain embedded media and operation
// prices while exposing the configured candidate failure in its attribute
// status.
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

// TestModelsCatalogFallbackIsStable queries complete embedded Claude identities
// and unconfigured future dates. Exact identities retain prices, while dated
// variants acquire neither a price nor an inferred context limit.
func TestModelsCatalogFallbackIsStable(t *testing.T) {
	service := NewService(Options{DataDir: t.TempDir()}, nil)
	require.NoError(t, service.Initialize())
	for _, model := range []string{"claude-opus-4-5", "claude-opus-4-6"} {
		require.NotNil(t, service.GetModelPricing(model))
		require.Nil(t, service.GetModelPricing(model+"-20990101"))
		require.Nil(t, service.ModelAttributes(model+"-20990101").Context)
	}
}

// TestModelsCatalogFallbackOriginAndSupplements compares original-provider and
// hosted service prices through live and copied query services. Unknown
// variants remain unresolved, catalog fields resist supplement replacement,
// and an exact new supplement model becomes priced.
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
		require.InDelta(t, 1e-6, reader.GetModelPricing("ollama-cloud/claude-opus-4-6").InputCostPerToken, 1e-12)
		require.Equal(t, "unpriced", reader.GetModelPricing("ollama-cloud/claude-opus-4-6-thinking").Source)
		for _, model := range []string{"gpt-5.4-20990101", "gpt-5.4-openai-compact"} {
			require.Nil(t, reader.GetModelPricing(model))
		}
		require.InDelta(t, 1e-6, reader.GetModelPricing("ollama-cloud/gpt-5.4").InputCostPerToken, 1e-12)
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
	require.InDelta(t, 8e-6, service.GetModelPricing("claude-opus-4-7").InputCostPerToken, 1e-12)
}

// TestModelsCatalogFallbackDoesNotBorrowRelayOnlyModel publishes public Claude
// metadata alongside a filtered relay quote. Public attributes remain available,
// while bare and qualified prices stay unpriced and unknown suffixes stay absent.
func TestModelsCatalogFallbackDoesNotBorrowRelayOnlyModel(t *testing.T) {
	remote := &catalogRemoteFixture{body: []byte(`{"relay":{"id":"relay","models":{"claude-opus-4-6":{"id":"claude-opus-4-6","type":"chat","capabilities":{},"cost":{"text":{"input":{"price":1},"output":{"price":2}}}}}},"anthropic":{"id":"anthropic","models":{"claude-opus-4-6":{"id":"claude-opus-4-6","type":"chat","capabilities":{},"canonical_id":"anthropic/claude-opus-4-6","limit":{"context":200000}}}}}`)}
	service := NewService(Options{DataDir: t.TempDir(), RemoteURL: "https://models.dev/catalog.json"}, remote)
	require.NoError(t, service.ForceUpdate())
	for _, reader := range []*Service{service, service.ReadOnlySnapshot()} {
		require.Nil(t, reader.GetModelPricing("claude-opus-4-6"))
		price := reader.GetModelPricing("relay/claude-opus-4-6")
		require.NotNil(t, price)
		require.Equal(t, "unpriced", price.Source)
		require.True(t, price.TokenPricingAbsent)
		require.Equal(t, 200000, *reader.ModelAttributes("claude-opus-4-6").Context)
		require.Nil(t, reader.ModelAttributes("relay/claude-opus-4-6").Context)
		require.Nil(t, reader.GetModelPricing("claude-opus-4-6-thinking"))
	}
}

// TestModelsCatalogExactMediaSupplementKeepsExplicitZero supplies different
// media prices at bare and qualified first-party keys. Both exact supplements
// must retain their own values, including the qualified explicit-zero presence
// flag.
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

// TestModelsCatalogInvalidPricePatchKeepsPublishedSnapshot submits malformed
// base, tier, and threshold fields with changed remote metadata. Rejection
// retains prices, attributes, version, timestamp, and disk body; a valid
// follow-up clears the diagnostic.
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
		name  string // named invalid price scenario
		patch string // malformed supplement object JSON
		field string // expected invalid field diagnostic
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
	// Repair the invalid field with a zero input rate. The catalog's populated
	// input rate still wins, while accepting the supplement clears the prior
	// update diagnostic.
	require.NoError(t, os.WriteFile(patch, []byte(`{"claude-test":{"input_cost_per_token":0}}`), 0o600))
	require.NoError(t, service.ForceUpdate())
	require.InDelta(t, 3e-6, service.GetModelPricing("claude-test").InputCostPerToken, 1e-12)
	require.Empty(t, service.AttributesSnapshot().LastError)
	require.NotNil(t, service.ModelAttributes("attributes-only").DisplayName)
}

// TestModelsCatalogInvalidLocalReloadReportsError changes a custom price field
// to an invalid type before an unchanged remote response. Both conditional
// refresh and local reload must report the field error while retaining
// published prices.
func TestModelsCatalogInvalidLocalReloadReportsError(t *testing.T) {
	dir := t.TempDir()
	patch := filepath.Join(dir, "supplement.json")
	remote := &catalogRemoteFixture{body: []byte(modelsCatalogFixture), etag: "v1"}
	service := NewService(Options{DataDir: dir, RemoteURL: "https://models.dev/catalog.json", FallbackFile: patch}, remote)
	require.NoError(t, service.ForceUpdate())
	before := service.Snapshot().Data
	require.NoError(t, os.WriteFile(patch, []byte(`{"claude-test":{"input_cost_per_token":"invalid"}}`), 0o600))
	// Hold the remote body unchanged while rejecting the modified local layer.
	// This makes the following failure depend on supplement validation during
	// the 304 path.
	remote.unchanged = true
	require.ErrorContains(t, service.syncWithRemote(), "input_cost_per_token")
	service.reloadIfCustomFilesChanged()
	require.Contains(t, service.AttributesSnapshot().LastError, "input_cost_per_token")
	require.Equal(t, before, service.Snapshot().Data)
}

// TestModelsCatalogMistralNativeAlias compares bare and qualified original
// Mistral identities with a filtered relay record. Original metadata and prices
// must agree across its aliases, while the relay is absent from default prices.
func TestModelsCatalogMistralNativeAlias(t *testing.T) {
	service := NewService(Options{DataDir: t.TempDir()}, nil)
	require.NoError(t, service.Initialize())
	plain := catalogPriceForTest(t, service, "devstral-latest")
	qualified := catalogPriceForTest(t, service, "mistral/devstral-latest")
	require.InDelta(t, 0.4e-6, plain.InputPricePerToken, 1e-12)
	require.Equal(t, qualified, plain)
	require.Equal(t, service.ModelAttributes("mistral/devstral-latest"), service.ModelAttributes("devstral-latest"))
	require.NotNil(t, service.ModelAttributes("devstral-latest").Context)
	removed := service.GetModelPricing("requesty/devstral-latest")
	require.Equal(t, "unpriced", removed.Source)
	require.True(t, removed.TokenPricingAbsent)
	require.NotContains(t, service.ModelIDs(), "requesty/devstral-latest")
}

// TestModelsCatalogOfflineManualUpdate uses a regular file where the cache
// directory should be and edits a custom rate. Memory-backed reload must
// publish valid changes, retain data on invalid input, and clear errors after
// repair.
func TestModelsCatalogOfflineManualUpdate(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	patch := filepath.Join(dir, "supplement.json")
	// Use a regular file at the cache directory path to make persistence fail
	// regardless of process permissions, exercising memory-backed reload even
	// for privileged test runs.
	require.NoError(t, os.WriteFile(cache, []byte("unwritable cache"), 0o600))
	require.NoError(t, os.WriteFile(patch, []byte(`{"custom-model":{"input_cost_per_token":0.000007}}`), 0o600))
	service := NewService(Options{DataDir: cache, FallbackFile: patch}, nil)
	require.NoError(t, service.Initialize())
	before := service.AttributesSnapshot()
	require.NoError(t, os.WriteFile(patch, []byte(`{"custom-model":{"input_cost_per_token":0.000009}}`), 0o600))
	require.NoError(t, service.ForceUpdate())
	require.InDelta(t, 9e-6, service.GetModelPricing("custom-model").InputCostPerToken, 1e-12)
	require.NotEqual(t, before.Version, service.AttributesSnapshot().Version)
	require.Empty(t, service.AttributesSnapshot().LastError)
	// Reject this changed local price type after a successful memory-backed
	// reload. The invalid candidate must leave the retained price and metadata
	// available for the following assertions.
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

// TestModelsCatalogAtomicUpdateAndUnpricedAttributes publishes metadata-only
// attributes and tiered prices before returning invalid catalog bytes.
// Rejection retains version, timestamp, and price; a subsequent 304 uses the
// saved validator and clears the error.
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

// TestModelsCatalogSupplementAndConcurrentReaders registers metadata and
// snapshot queries before forcing supplemented publications. Each update must
// succeed, the supplement price must be present, and every reader finishes
// before the test returns.
func TestModelsCatalogSupplementAndConcurrentReaders(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "supplement.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"claude-test":{"cache_creation_input_token_cost_above_1hr":0.000012}}`), 0o600))
	remote := &catalogRemoteFixture{body: []byte(modelsCatalogFixture)}
	s := NewService(Options{RemoteURL: "https://models.dev/catalog.json", DataDir: dir, FallbackFile: file}, remote)
	require.NoError(t, s.ForceUpdate())
	require.InDelta(t, 12e-6, s.GetModelPricing("claude-test").CacheCreationInputTokenCostAbove1hr, 1e-12)
	var wg sync.WaitGroup
	// Register metadata reader work before the forced replacements. Their
	// queries can overlap publication, and the join below keeps the fixture
	// alive until every registered reader finishes.
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
	// Join all metadata readers after the updates finish, preventing
	// unfinished fixture queries from extending into the temporary directory's
	// cleanup or the next test.
	wg.Wait()
}

// TestModelsCatalogOfflineFirstStart initializes an empty cache directory from
// the embedded catalog. The retained Claude context must be available, and
// initialization must create the configured disk catalog file.
func TestModelsCatalogOfflineFirstStart(t *testing.T) {
	s := NewService(Options{DataDir: t.TempDir()}, nil)
	require.NoError(t, s.Initialize())
	require.NotNil(t, s.ModelAttributes("claude-sonnet-4-5").Context)
	require.FileExists(t, s.catalogFilePath())
}

// TestModelsCatalogBrokenSupplementBootAndRecovery starts with broken custom
// bytes and then supplies a valid model price. Embedded metadata remains usable
// with an error, and successful reload clears it while publishing the custom
// rate.
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

// TestModelsCatalogReadOnlyCacheStillServesOfflineData places a regular file at
// the configured cache directory path. Initialization must still succeed and
// retain an embedded model context through the memory publication path.
func TestModelsCatalogReadOnlyCacheStillServesOfflineData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(path, []byte("file"), 0o600))
	s := NewService(Options{DataDir: path}, nil)
	require.NoError(t, s.Initialize())
	require.NotNil(t, s.ModelAttributes("claude-sonnet-4-5").Context)
}

// TestModelsCatalogConditionalUpdate records forced and conditional request
// validators before editing a supplement under 304 responses. Custom zero
// prices and deletion must apply while the raw hash and update time remain
// fixed.
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

// TestModelsCatalogRejectsLegacyRemote downloads legacy price JSON after
// publishing a valid catalog. Rejection must report the legacy-format
// diagnostic and retain prices, metadata, raw hash, update time, and cached
// body bytes.
func TestModelsCatalogRejectsLegacyRemote(t *testing.T) {
	remote := &catalogRemoteFixture{body: []byte(modelsCatalogFixture)}
	service := NewService(Options{RemoteURL: "https://custom.example/prices.json", DataDir: t.TempDir()}, remote)
	require.NoError(t, service.ForceUpdate())
	before := service.Snapshot()
	attrs := service.AttributesSnapshot()
	disk := readCatalogTestFile(t, service.catalogFilePath())
	remote.body = []byte(`{"gpt-test":{"input_cost_per_token":1}}`)
	require.ErrorContains(t, service.ForceUpdate(), "invalid models.dev v2 provider")
	require.Equal(t, before.Data, service.Snapshot().Data)
	require.Equal(t, attrs.Items, service.AttributesSnapshot().Items)
	require.Equal(t, before.LocalHash, service.Snapshot().LocalHash)
	require.Equal(t, before.LastUpdated, service.Snapshot().LastUpdated)
	require.Equal(t, disk, readCatalogTestFile(t, service.catalogFilePath()))
}

// TestModelsCatalogDroppedAbsoluteTierWarns publishes context tiers and then
// replaces the same model with base prices alone. The retained-key comparison
// must emit the asserted warning about lost context surcharge data.
func TestModelsCatalogDroppedAbsoluteTierWarns(t *testing.T) {
	sink, restore := captureStructuredLog(t)
	defer restore()
	remote := &catalogRemoteFixture{body: []byte(modelsCatalogFixture)}
	service := NewService(Options{RemoteURL: "https://models.dev/catalog.json", DataDir: t.TempDir()}, remote)
	require.NoError(t, service.ForceUpdate())
	remote.body = []byte(`{"anthropic":{"id":"anthropic","models":{"claude-test":{"id":"claude-test","type":"chat","capabilities":{},"cost":{"text":{"input":{"price":3},"output":{"price":15}}}}}}}`)
	require.NoError(t, service.ForceUpdate())
	require.True(t, sink.ContainsMessageAtLevel("Long-context ladder dropped", "warn"))
}

// TestModelsCatalogAttributesOnlyWithUnknownPatch adds a threshold-only
// supplement to an attribute-only catalog. The unknown patch must produce no
// price record, while the declared model remains displayable and unpriced.
func TestModelsCatalogAttributesOnlyWithUnknownPatch(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "supplement.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"typo":{"long_context_input_token_threshold":0}}`), 0o600))
	remote := &catalogRemoteFixture{body: []byte(`{"openai":{"id":"openai","models":{"attributes-only":{"id":"attributes-only","type":"chat","capabilities":{},"name":"No prices"}}}}`)}
	service := NewService(Options{RemoteURL: "https://models.dev/catalog.json", DataDir: dir, FallbackFile: file}, remote)
	require.NoError(t, service.ForceUpdate())
	require.NotContains(t, service.Snapshot().Data, "typo")
	require.Nil(t, service.GetModelPricing("attributes-only"))
	require.Equal(t, "No prices", *service.ModelAttributes("attributes-only").DisplayName)
}

// TestIncompleteCatalogFieldsAreNotReplaced supplements a partial catalog price
// with another input price and free output. The existing input rate, false
// reasoning, and empty output modalities remain authoritative while missing
// output pricing is filled.
func TestIncompleteCatalogFieldsAreNotReplaced(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "prices.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"partial":{"input_cost_per_token":99,"output_cost_per_token":0}}`), 0o600))
	remote := &catalogRemoteFixture{body: []byte(`{"openai":{"id":"openai","models":{"partial":{"id":"partial","type":"chat","capabilities":{"reasoning":{"supported":false}},"modalities":{"output":[]},"cost":{"text":{"input":{"price":2}}}}}}}`)}
	service := NewService(Options{DataDir: dir, RemoteURL: "https://models.dev/catalog.json", FallbackFile: file}, remote)
	require.NoError(t, service.ForceUpdate())
	price := service.GetModelPricing("partial")
	require.InDelta(t, 2e-6, price.InputCostPerToken, 1e-12)
	require.Zero(t, price.OutputCostPerToken)
	require.False(t, price.TokenPricingAbsent)
	require.False(t, *service.ModelAttributes("partial").Reasoning)
	require.Empty(t, *service.ModelAttributes("partial").OutputModalities)
}

// TestSupplementRejectsInvalidOperationAndRule supplies invalid operation
// defaults, multipliers, media dimensions, and time rules. Local supplement
// validation must reject every asserted candidate before its values can
// participate in publication.
func TestSupplementRejectsInvalidOperationAndRule(t *testing.T) {
	for _, body := range []string{
		`{"_billing_defaults":{"web_search_price_per_call":-1}}`,
		`{"_billing_defaults":{"audio_tts_price_per_million_chars":"bad"}}`,
		`{"_billing_defaults":null}`,
		`{"claude-test":{"input_cost_per_token":-1}}`,
		`{"claude-test":{"fast_multiplier":0}}`,
		`{"claude-test":{"input_cost_per_token":1e308,"cache_write_multiplier":1e308}}`,
		`{"claude-test":{"video_prices":{"HD":1,"720p":2}}}`,
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

// mutatePricingFixture copies a fixture price map, applies the supplied
// mutation, and installs it for later queries. Callers use this helper before
// concurrent readers or scheduled updates start accessing the fixture.
func mutatePricingFixture(service *Service, change func(map[string]*billingpricing.CatalogModelPricing)) {
	data := service.Snapshot().Data
	change(data)
	setPricingFixtureData(service, data)
}

// setPricingFixtureRemote replaces a fixture downloader before its update work
// runs. Callers serialize that setup with fixture operations because the
// service field assignment itself uses no lifecycle or update lock.
func setPricingFixtureRemote(service *Service, remote RemoteClient) {
	service.remoteClient = remote
}

// WriteLogEvent records a shallow event copy and a copied top-level Fields map
// under the sink mutex. Nested field values remain shared; nil events are
// ignored, and callers keep referenced nested values read-only.
func (s *inMemoryLogSink) WriteLogEvent(event *logging.LogEvent) {
	if event == nil {
		return
	}
	cloned := *event
	if event.Fields != nil {
		cloned.Fields = make(map[string]any, len(event.Fields))
		maps.Copy(cloned.Fields, event.Fields)
	}
	// Append the copied record under the same mutex used by message scans so
	// assertions cannot traverse a slice while another logging callback
	// changes its length.
	s.mu.Lock()
	s.events = append(s.events, &cloned)
	s.mu.Unlock()
}

// ContainsMessage reports whether a captured event message contains the
// supplied substring. It locks the event list during scanning, skips nil
// events, and treats an empty substring as matching any nonnil event.
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

// ContainsMessageAtLevel matches a message substring and a normalized level
// within captured events. Event-list reads use the sink mutex, while level case
// and surrounding whitespace are ignored for the comparison.
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

// captureStructuredLog configures global debug JSON logging and installs a test
// sink while holding the capture mutex. The caller invokes the returned cleanup
// exactly once after its observations.
//
// The mutex serializes captures that replace the global sink. Cleanup clears
// that sink and releases the mutex; the initialized logger configuration
// remains installed. Initialization failures fail the test before cleanup is
// returned, so this helper's successful callers own the cleanup lifetime
// through their deferred function.
func captureStructuredLog(t *testing.T) (*inMemoryLogSink, func()) {
	t.Helper()
	// Serialize logger configuration and sink replacement until the returned
	// cleanup clears this capture, keeping another capture from replacing its
	// sink during assertions.
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
		// Clear this sink while its capture lock is still held, then release
		// that lock so the next capture installs its own sink before observing
		// new events.
		logging.SetSink(nil)
		structuredLogCaptureMu.Unlock()
	}
}

// catalogLookupTestPricing constructs distinct fixture rates and text output
// modalities for identity selection assertions. The supplied input modality
// slice remains shared, so callers complete its mutations before using the
// retained fixture concurrently.
func catalogLookupTestPricing(input float64, modalities ...string) *billingpricing.CatalogModelPricing {
	// Distinct output and cache ratios identify the selected fixture record.
	// The fixed context threshold also exposes accidental inheritance of that
	// record's surcharge rules.
	return &billingpricing.CatalogModelPricing{
		InputCostPerToken: input, OutputCostPerToken: input * 5,
		CacheCreationInputTokenCost: input * 1.25, CacheReadInputTokenCost: input / 10,
		LongContextInputTokenThreshold: 200000, LongContextInputCostMultiplier: 2,
		LongContextOutputCostMultiplier: 1.5, Mode: "chat",
		SupportedModalities: modalities, SupportedOutputModalities: []string{"text"},
	}
}

// hotReloadModelJSON builds one fixture object member containing input and
// output rates. Callers supply a JSON-safe model key and finite numbers because
// the helper inserts the key literally into the object text.
func hotReloadModelJSON(name string, input, output float64) string {
	return `"` + name + `": {"provider": "test", "mode": "chat",
		"input_cost_per_token": ` + formatFloat(input) + `, "output_cost_per_token": ` + formatFloat(output) + `}`
}

// formatFloat formats a fixture float using the shortest float64
// representation. Callers supply finite values when inserting the result into
// JSON, since nonfinite spellings are not valid JSON numbers.
func formatFloat(v float64) string {
	// Keep fixture prices round-trippable so parser assertions can compare
	// their initial float64 values after the shortest number spelling is
	// decoded.
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// newHotReloadCatalog prepares a cached base catalog and optional custom
// supplement in a temporary directory, then initializes the service. An empty
// supplement body leaves the path unconfigured, and initialization failures
// fail the test.
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

// FetchCatalog returns fresh bytes from the configured string body with no ETag
// or failure. Request URL, validator, and context are ignored because this
// fixture supplies unconditional test responses.
func (c stubCatalogRemoteClient) FetchCatalog(context.Context, string, string) ([]byte, string, bool, error) {
	return []byte(c.body), "", false, nil
}

// FetchCatalog atomically counts each fixture fetch and returns fresh catalog
// bytes for lifecycle assertions. Request inputs are ignored, so the test
// observes constructor and scheduler dispatches without network work.
func (r *lifecyclePricingRemote) FetchCatalog(context.Context, string, string) ([]byte, string, bool, error) {
	// Count the dispatch before returning its candidate so Stop's joined
	// scheduler result can be compared with the atomic call count after
	// lifecycle completion.
	r.calls.Add(1)
	return []byte(hotReloadCatalogJSON), "", false, nil
}
