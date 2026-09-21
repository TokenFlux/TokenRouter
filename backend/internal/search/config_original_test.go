//go:build unit

package search_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/search"
	searchprovider "github.com/TokenFlux/TokenRouter/internal/search/provider"
	"github.com/stretchr/testify/require"
)

// --- validateWebSearchConfig ---

func TestValidateWebSearchConfig_Nil(t *testing.T) {
	require.NoError(t, search.ValidateConfig(nil))
}

func TestValidateWebSearchConfig_Valid(t *testing.T) {
	cfg := &search.WebSearchEmulationConfig{
		Enabled: true,
		Providers: []search.WebSearchProviderConfig{
			{Type: "brave", QuotaLimit: originalSearchQuota(1000)},
			{Type: "tavily", QuotaLimit: originalSearchQuota(500)},
		},
	}
	require.NoError(t, search.ValidateConfig(cfg))
}

func TestValidateWebSearchConfig_TooManyProviders(t *testing.T) {
	cfg := &search.WebSearchEmulationConfig{Providers: make([]search.WebSearchProviderConfig, 11)}
	for i := range cfg.Providers {
		cfg.Providers[i] = search.WebSearchProviderConfig{Type: "brave"}
	}
	err := search.ValidateConfig(cfg)
	require.ErrorContains(t, err, "too many providers")
}

func TestValidateWebSearchConfig_InvalidType(t *testing.T) {
	cfg := &search.WebSearchEmulationConfig{
		Providers: []search.WebSearchProviderConfig{{Type: "bing"}},
	}
	require.ErrorContains(t, search.ValidateConfig(cfg), "invalid type")
}

func TestValidateWebSearchConfig_NegativeQuotaLimit(t *testing.T) {
	cfg := &search.WebSearchEmulationConfig{
		Providers: []search.WebSearchProviderConfig{{Type: "brave", QuotaLimit: originalSearchQuota(-1)}},
	}
	require.ErrorContains(t, search.ValidateConfig(cfg), "quota_limit must be > 0 or null")
}

func TestValidateWebSearchConfig_DuplicateType(t *testing.T) {
	cfg := &search.WebSearchEmulationConfig{
		Providers: []search.WebSearchProviderConfig{
			{Type: "brave"},
			{Type: "brave"},
		},
	}
	require.ErrorContains(t, search.ValidateConfig(cfg), "duplicate type")
}

func TestValidateWebSearchConfig_NilQuotaLimit(t *testing.T) {
	cfg := &search.WebSearchEmulationConfig{
		Providers: []search.WebSearchProviderConfig{{Type: "brave", QuotaLimit: nil}},
	}
	require.NoError(t, search.ValidateConfig(cfg))
}

// --- parseWebSearchConfigJSON ---

func TestParseWebSearchConfigJSON_ValidJSON(t *testing.T) {
	raw := `{"enabled":true,"providers":[{"type":"brave","api_key":"sk-xxx"}]}`
	cfg := search.ParseConfig(raw)
	require.True(t, cfg.Enabled)
	require.Len(t, cfg.Providers, 1)
	require.Equal(t, "brave", cfg.Providers[0].Type)
}

func TestParseWebSearchConfigJSON_EmptyString(t *testing.T) {
	cfg := search.ParseConfig("")
	require.False(t, cfg.Enabled)
	require.Empty(t, cfg.Providers)
}

func TestParseWebSearchConfigJSON_InvalidJSON(t *testing.T) {
	cfg := search.ParseConfig("not{json")
	require.False(t, cfg.Enabled)
	require.Empty(t, cfg.Providers)
}

func TestParseWebSearchConfigJSON_BackwardCompatibility(t *testing.T) {
	// Old config with priority and quota_refresh_interval should parse without error
	raw := `{"enabled":true,"providers":[{"type":"brave","priority":1,"quota_refresh_interval":"monthly","quota_limit":1000}]}`
	cfg := search.ParseConfig(raw)
	require.True(t, cfg.Enabled)
	require.Len(t, cfg.Providers, 1)
	require.Equal(t, int64(1000), *cfg.Providers[0].QuotaLimit)
}

// --- SanitizeWebSearchConfig ---

func TestSanitizeWebSearchConfig_MaskAPIKey(t *testing.T) {
	registry := search.NewRegistry()

	cfg := &search.WebSearchEmulationConfig{
		Enabled: true,
		Providers: []search.WebSearchProviderConfig{
			{Type: "brave", APIKey: "sk-secret-xxx"},
		},
	}
	out := search.SanitizeWebSearchConfig(context.Background(), cfg, registry)
	require.Equal(t, "", out.Providers[0].APIKey)
	require.True(t, out.Providers[0].APIKeyConfigured)
}

func TestSanitizeWebSearchConfig_NoAPIKey(t *testing.T) {
	registry := search.NewRegistry()

	cfg := &search.WebSearchEmulationConfig{
		Providers: []search.WebSearchProviderConfig{{Type: "brave", APIKey: ""}},
	}
	out := search.SanitizeWebSearchConfig(context.Background(), cfg, registry)
	require.Equal(t, "", out.Providers[0].APIKey)
	require.False(t, out.Providers[0].APIKeyConfigured)
}

func TestSanitizeWebSearchConfig_Nil(t *testing.T) {
	registry := search.NewRegistry()

	require.Nil(t, search.SanitizeWebSearchConfig(context.Background(), nil, registry))
}

func TestSanitizeWebSearchConfig_PreservesOtherFields(t *testing.T) {
	registry := search.NewRegistry()

	cfg := &search.WebSearchEmulationConfig{
		Enabled: true,
		Providers: []search.WebSearchProviderConfig{
			{Type: "brave", APIKey: "secret", QuotaLimit: originalSearchQuota(1000)},
		},
	}
	out := search.SanitizeWebSearchConfig(context.Background(), cfg, registry)
	require.True(t, out.Enabled)
	require.Equal(t, int64(1000), *out.Providers[0].QuotaLimit)
}

func TestSanitizeWebSearchConfig_DoesNotMutateOriginal(t *testing.T) {
	registry := search.NewRegistry()

	cfg := &search.WebSearchEmulationConfig{
		Providers: []search.WebSearchProviderConfig{{Type: "brave", APIKey: "secret"}},
	}
	_ = search.SanitizeWebSearchConfig(context.Background(), cfg, registry)
	require.Equal(t, "secret", cfg.Providers[0].APIKey)
}

// --- PopulateWebSearchUsage ---

func TestPopulateWebSearchUsage_NilInput(t *testing.T) {
	registry := search.NewRegistry()

	require.Nil(t, search.PopulateWebSearchUsage(context.Background(), nil, registry))
}

func TestPopulateWebSearchUsage_NoManager_QuotaUsedZero(t *testing.T) {
	registry := search.NewRegistry()

	// Ensure no global manager is set
	registry.Set(nil)
	defer registry.Set(nil)

	cfg := &search.WebSearchEmulationConfig{
		Enabled: true,
		Providers: []search.WebSearchProviderConfig{
			{Type: "brave", APIKey: "sk-key", QuotaLimit: originalSearchQuota(1000)},
		},
	}
	out := search.PopulateWebSearchUsage(context.Background(), cfg, registry)
	require.NotNil(t, out)
	require.Len(t, out.Providers, 1)
	require.Equal(t, int64(0), out.Providers[0].QuotaUsed)
}

func TestPopulateWebSearchUsage_APIKeyConfigured_True(t *testing.T) {
	registry := search.NewRegistry()

	registry.Set(nil)
	defer registry.Set(nil)

	cfg := &search.WebSearchEmulationConfig{
		Providers: []search.WebSearchProviderConfig{
			{Type: "brave", APIKey: "sk-key"},
		},
	}
	out := search.PopulateWebSearchUsage(context.Background(), cfg, registry)
	require.True(t, out.Providers[0].APIKeyConfigured)
}

func TestPopulateWebSearchUsage_APIKeyConfigured_False(t *testing.T) {
	registry := search.NewRegistry()

	registry.Set(nil)
	defer registry.Set(nil)

	cfg := &search.WebSearchEmulationConfig{
		Providers: []search.WebSearchProviderConfig{
			{Type: "brave", APIKey: ""},
		},
	}
	out := search.PopulateWebSearchUsage(context.Background(), cfg, registry)
	require.False(t, out.Providers[0].APIKeyConfigured)
}

func TestPopulateWebSearchUsage_NilQuotaLimit(t *testing.T) {
	registry := search.NewRegistry()

	registry.Set(nil)
	defer registry.Set(nil)

	cfg := &search.WebSearchEmulationConfig{
		Providers: []search.WebSearchProviderConfig{
			{Type: "brave", APIKey: "sk-key", QuotaLimit: nil},
		},
	}
	out := search.PopulateWebSearchUsage(context.Background(), cfg, registry)
	require.Nil(t, out.Providers[0].QuotaLimit)
}

func TestPopulateWebSearchUsage_NonNilQuotaLimit(t *testing.T) {
	registry := search.NewRegistry()

	registry.Set(nil)
	defer registry.Set(nil)

	cfg := &search.WebSearchEmulationConfig{
		Providers: []search.WebSearchProviderConfig{
			{Type: "brave", APIKey: "sk-key", QuotaLimit: originalSearchQuota(500)},
		},
	}
	out := search.PopulateWebSearchUsage(context.Background(), cfg, registry)
	require.NotNil(t, out.Providers[0].QuotaLimit)
	require.Equal(t, int64(500), *out.Providers[0].QuotaLimit)
}

func TestPopulateWebSearchUsage_WithManager_NilRedis(t *testing.T) {
	registry := search.NewRegistry()

	// Manager with nil Redis returns 0 usage without error
	mgr := search.NewManager([]search.ProviderConfig{
		{Type: "brave", APIKey: "k"},
	}, nil, searchprovider.NewExecutor(), nil)
	registry.Set(mgr)
	defer registry.Set(nil)

	cfg := &search.WebSearchEmulationConfig{
		Providers: []search.WebSearchProviderConfig{
			{Type: "brave", APIKey: "sk-key", QuotaLimit: originalSearchQuota(1000)},
		},
	}
	out := search.PopulateWebSearchUsage(context.Background(), cfg, registry)
	require.Equal(t, int64(0), out.Providers[0].QuotaUsed)
	require.True(t, out.Providers[0].APIKeyConfigured)
}

func TestPopulateWebSearchUsage_DoesNotMutateOriginal(t *testing.T) {
	registry := search.NewRegistry()

	registry.Set(nil)
	defer registry.Set(nil)

	cfg := &search.WebSearchEmulationConfig{
		Providers: []search.WebSearchProviderConfig{
			{Type: "brave", APIKey: "secret", QuotaLimit: originalSearchQuota(100)},
		},
	}
	_ = search.PopulateWebSearchUsage(context.Background(), cfg, registry)
	// Original should be unchanged
	require.Equal(t, "secret", cfg.Providers[0].APIKey)
	require.Equal(t, int64(0), cfg.Providers[0].QuotaUsed)
}

// --- ResetWebSearchUsage ---

func TestResetWebSearchUsage_NilManager(t *testing.T) {
	registry := search.NewRegistry()

	registry.Set(nil)
	defer registry.Set(nil)

	err := search.ResetWebSearchUsage(context.Background(), "brave", registry)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not initialized")
}

// originalSearchQuota 只构造原限额输入。
func originalSearchQuota(v int64) *int64 { return &v }
