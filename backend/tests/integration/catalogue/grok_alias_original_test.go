package catalogue_test

import (
	"context"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
)

// 原默认目录与账号显式别名断言直接组合原生解析器。
func TestGrokRequestableModelsExcludeBuiltinAliases(t *testing.T) {
	groupID := int64(4510)
	account := accountcore.Record{ID: 1, Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{}}
	repo := &modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{groupID: {account}}}
	service := newCatalogueFixture(repo, nil, nil)

	result := service.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformGrok)
	require.Equal(t, xai.DefaultModelIDs(), routing.RequestableModelIDs(result.Models))
	require.NotContains(t, routing.RequestableModelIDs(result.Models), "grok")
	require.NotContains(t, routing.RequestableModelIDs(result.Models), "grok-latest")

	account.Credentials["model_mapping"] = map[string]any{"grok": "grok-4.3"}
	repo.byGroup = map[int64][]accountcore.Record{groupID: {account}}
	result = service.ResolveRequestableModels(context.Background(), &groupID, capability.PlatformGrok)
	require.Contains(t, routing.RequestableModelIDs(result.Models), "grok")
}
