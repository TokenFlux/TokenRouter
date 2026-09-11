package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"

	"github.com/stretchr/testify/require"
)

// gpt55OverrideCatalogJSON 模拟真实目录中用 above_272k 绝对价表达阶梯的条目。
const gpt55OverrideCatalogJSON = `{
	"gpt-5.5": {"litellm_provider": "openai", "mode": "chat",
		"input_cost_per_token": 5e-06, "input_cost_per_token_priority": 1.25e-05,
		"output_cost_per_token": 3e-05, "output_cost_per_token_priority": 7.5e-05,
		"cache_read_input_token_cost": 5e-07,
		"input_cost_per_token_above_272k_tokens": 1e-05,
		"output_cost_per_token_above_272k_tokens": 4.5e-05,
		"cache_read_input_token_cost_above_272k_tokens": 1e-06},
	"gpt-5.4": {"litellm_provider": "openai", "mode": "chat",
		"input_cost_per_token": 2.5e-06, "output_cost_per_token": 1.5e-05,
		"cache_read_input_token_cost": 2.5e-07,
		"input_cost_per_token_above_272k_tokens": 5e-06,
		"output_cost_per_token_above_272k_tokens": 2.25e-05}
}`

func newPricingServiceWithOverride(t *testing.T, overrideJSON string) *PricingService {
	t.Helper()
	path := filepath.Join(t.TempDir(), "overrides.json")
	require.NoError(t, os.WriteFile(path, []byte(overrideJSON), 0644))
	service := newPricingServiceFixture(pricingServiceFixture{cfg: &config.Config{}})
	service.cfg.Pricing.OverrideFile = path
	return service
}

func TestPricingOverride_ExplicitZeroThresholdDisablesCatalogLadder(t *testing.T) {
	service := newPricingServiceWithOverride(t, `{"gpt-5.5": {"long_context_input_token_threshold": 0}}`)
	data, err := service.parsePricingData([]byte(gpt55OverrideCatalogJSON))
	require.NoError(t, err)

	patched := data["gpt-5.5"]
	require.NotNil(t, patched)
	require.Zero(t, patched.LongContextInputTokenThreshold)
	require.Zero(t, patched.LongContextInputCostMultiplier)
	require.InDelta(t, 5e-6, patched.InputCostPerToken, 1e-12)
	require.Equal(t, 272000, data["gpt-5.4"].LongContextInputTokenThreshold)
}

func TestPricingOverride_FieldLevelMergeKeepsOtherFields(t *testing.T) {
	service := newPricingServiceWithOverride(t, `{"gpt-5.4": {"input_cost_per_token": 3e-06}}`)
	data, err := service.parsePricingData([]byte(gpt55OverrideCatalogJSON))
	require.NoError(t, err)

	patched := data["gpt-5.4"]
	require.InDelta(t, 3e-6, patched.InputCostPerToken, 1e-12)
	require.InDelta(t, 1.5e-5, patched.OutputCostPerToken, 1e-12)
	require.Equal(t, "openai", patched.LiteLLMProvider)
	require.Equal(t, 272000, patched.LongContextInputTokenThreshold)
	require.InDelta(t, 5.0/3.0, patched.LongContextInputCostMultiplier, 1e-9)
}

func TestPricingOverride_NullFieldValueRemovesField(t *testing.T) {
	service := newPricingServiceWithOverride(t, `{"gpt-5.5": {
		"input_cost_per_token_above_272k_tokens": null,
		"output_cost_per_token_above_272k_tokens": null,
		"cache_read_input_token_cost_above_272k_tokens": null}}`)
	data, err := service.parsePricingData([]byte(gpt55OverrideCatalogJSON))
	require.NoError(t, err)
	require.Zero(t, data["gpt-5.5"].LongContextInputTokenThreshold)
}

func TestPricingOverride_LoadPipelineAddsNewModelAndPatchesFallbackOnly(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.json")
	require.NoError(t, os.WriteFile(catalogPath, []byte(`{
		"remote-model": {"litellm_provider": "test", "mode": "chat",
			"input_cost_per_token": 1e-06, "output_cost_per_token": 2e-06}
	}`), 0644))
	fallbackPath := filepath.Join(dir, "fallback.json")
	require.NoError(t, os.WriteFile(fallbackPath, []byte(`{
		"fallback-only-model": {"litellm_provider": "test", "mode": "chat",
			"input_cost_per_token": 4e-06, "output_cost_per_token": 8e-06,
			"cache_read_input_token_cost": 4e-07}
	}`), 0644))
	overridePath := filepath.Join(dir, "overrides.json")
	require.NoError(t, os.WriteFile(overridePath, []byte(`{
		"fallback-only-model": {"input_cost_per_token": 9e-06},
		"override-new-model": {"litellm_provider": "test", "mode": "chat",
			"input_cost_per_token": 5e-06, "output_cost_per_token": 1e-05}
	}`), 0644))

	service := newPricingServiceFixture(pricingServiceFixture{cfg: &config.Config{}})
	service.cfg.Pricing.FallbackFile = fallbackPath
	service.cfg.Pricing.OverrideFile = overridePath
	require.NoError(t, service.loadPricingData(catalogPath))

	patched := service.runtime.Snapshot().Data["fallback-only-model"]
	require.NotNil(t, patched)
	require.InDelta(t, 9e-6, patched.InputCostPerToken, 1e-12)
	require.InDelta(t, 8e-6, patched.OutputCostPerToken, 1e-12)
	require.InDelta(t, 4e-7, patched.CacheReadInputTokenCost, 1e-12)
	added := service.runtime.Snapshot().Data["override-new-model"]
	require.NotNil(t, added)
	require.InDelta(t, 5e-6, added.InputCostPerToken, 1e-12)
	require.InDelta(t, 1e-5, added.OutputCostPerToken, 1e-12)
	require.InDelta(t, 1e-6, service.runtime.Snapshot().Data["remote-model"].InputCostPerToken, 1e-12)
}

func TestPricingOverride_IneffectiveEntryWarns(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()

	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.json")
	require.NoError(t, os.WriteFile(catalogPath, []byte(`{
		"remote-model": {"litellm_provider": "test", "mode": "chat", "input_cost_per_token": 1e-06}
	}`), 0644))
	overridePath := filepath.Join(dir, "overrides.json")
	require.NoError(t, os.WriteFile(overridePath, []byte(`{
		"typo-model": {"long_context_input_token_threshold": 0}
	}`), 0644))

	service := newPricingServiceFixture(pricingServiceFixture{cfg: &config.Config{}})
	service.cfg.Pricing.OverrideFile = overridePath
	require.NoError(t, service.loadPricingData(catalogPath))
	require.NotContains(t, service.runtime.Snapshot().Data, "typo-model")
	require.True(t, logSink.ContainsMessageAtLevel("override had no effect for 1 model(s): typo-model", "warn"))
}

func TestPricingOverride_NonObjectEntryKeepsCatalogEntry(t *testing.T) {
	service := newPricingServiceWithOverride(t, `{"gpt-5.5": "oops"}`)
	data, err := service.parsePricingData([]byte(gpt55OverrideCatalogJSON))
	require.NoError(t, err)
	require.Equal(t, 272000, data["gpt-5.5"].LongContextInputTokenThreshold)
}

func TestPricingOverride_MissingOrInvalidFileIsIgnored(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		service := newPricingServiceFixture(pricingServiceFixture{cfg: &config.Config{}})
		service.cfg.Pricing.OverrideFile = filepath.Join(t.TempDir(), "absent.json")
		data, err := service.parsePricingData([]byte(gpt55OverrideCatalogJSON))
		require.NoError(t, err)
		require.Equal(t, 272000, data["gpt-5.5"].LongContextInputTokenThreshold)
	})

	t.Run("invalid json", func(t *testing.T) {
		service := newPricingServiceWithOverride(t, `{invalid`)
		data, err := service.parsePricingData([]byte(gpt55OverrideCatalogJSON))
		require.NoError(t, err)
		require.Equal(t, 272000, data["gpt-5.5"].LongContextInputTokenThreshold)
	})
}

func TestPricingOverride_DisablesGPT55LadderOnDefaultCatalog(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)

	service := newPricingServiceWithOverride(t, `{
		"gpt-5.5": {"long_context_input_token_threshold": 0},
		"gpt-5.5-2026-04-23": {"long_context_input_token_threshold": 0}
	}`)
	data, err := service.parsePricingData(body)
	require.NoError(t, err)
	setPricingFixtureData(service, data)
	billing := NewBillingService(&config.Config{}, service)

	for _, model := range []string{"gpt-5.5", "gpt-5.5-2026-04-23"} {
		pricing, err := billing.GetModelPricing(model)
		require.NoError(t, err)
		require.Zero(t, pricing.LongContextInputThreshold, model)
		require.InDelta(t, 5e-6, pricing.InputPricePerToken, 1e-12, model)
	}
	pricing, err := billing.GetModelPricing("gpt-5.4")
	require.NoError(t, err)
	require.Equal(t, 272000, pricing.LongContextInputThreshold)
}
