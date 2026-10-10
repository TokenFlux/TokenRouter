package catalogue_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// TestGrokRequestableModelsExcludeBuiltinAliases 空配置目录为空，具体映射可以加入自定义别名。
func TestGrokRequestableModelsExcludeBuiltinAliases(t *testing.T) {
	groupID := int64(4510)
	provider := providercore.Record{ID: 1, Platform: capability.PlatformGrok, Type: capability.ProviderTypeAPIKey, Credentials: map[string]any{}}
	repo := &modelsListProviderRepoStub{byGroup: map[int64][]providercore.Record{groupID: {provider}}}
	service := newCatalogueFixture(repo, nil, nil)

	result := service.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformGrok)
	require.Empty(t, result.Models)
	require.NotContains(t, routing.RequestableModelIDs(result.Models), "grok")
	require.NotContains(t, routing.RequestableModelIDs(result.Models), "grok-latest")

	provider.Credentials["model_mapping"] = map[string]any{"grok": "grok-4.3"}
	repo.byGroup = map[int64][]providercore.Record{groupID: {provider}}
	result = service.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformGrok)
	require.Contains(t, routing.RequestableModelIDs(result.Models), "grok")
}
