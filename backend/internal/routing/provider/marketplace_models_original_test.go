package provider

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
)

// TestGrokMarketplaceDefaultsReuseXAICatalog 验证模型广场直接复用 xAI 默认目录及展示名。
func TestGrokMarketplaceDefaultsReuseXAICatalog(t *testing.T) {
	definitions := MarketplaceModelDefs(capability.PlatformGrok)
	ids := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		ids = append(ids, definition.ID)
	}
	require.Equal(t, xai.DefaultModelIDs(), ids)
	require.NotContains(t, ids, "grok")

	displayNames := MarketplaceDisplayNames(capability.PlatformGrok)
	for _, model := range xai.DefaultModels() {
		require.Equal(t, model.DisplayName, displayNames[model.ID])
	}
}
