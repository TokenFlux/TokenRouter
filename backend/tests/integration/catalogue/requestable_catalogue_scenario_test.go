package catalogue_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// requestableListFixture 将完整解析结果转换为这些场景使用的 ID 列表。
type requestableListFixture struct{ *routing.RequestableCatalogue }

// TestRequestableCatalogueReadsCurrentProviders 每次查询读取一次提供商，并立即反映已保存配置。
func TestRequestableCatalogueReadsCurrentProviders(t *testing.T) {
	groupID := int64(9)
	record := provider.Record{ID: 1, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Credentials: map[string]any{"model_whitelist": []string{"first"}}}
	repo := &modelsListProviderRepoStub{byGroup: map[int64][]provider.Record{groupID: {record}}}
	catalogue := newCatalogueFixture(repo, nil, nil)
	first := catalogue.ResolveRequestableModels(context.Background(), &groupID, "")
	require.Equal(t, []string{"first"}, routing.RequestableModelIDs(first.Models))
	require.Equal(t, int64(1), repo.listByGroupCalls.Load())
	record.Credentials["model_whitelist"] = []string{"second"}
	repo.byGroup[groupID] = []provider.Record{record}
	second := catalogue.ResolveRequestableModels(context.Background(), &groupID, "")
	require.Equal(t, []string{"second"}, routing.RequestableModelIDs(second.Models))
	require.Equal(t, int64(2), repo.listByGroupCalls.Load())
}

func TestGetAvailableModels_ErrorAndGlobalListBranches(t *testing.T) {
	errRepo := &modelsListProviderRepoStub{
		err: errors.New("db error"),
	}
	svcErr := newRequestableListFixture(errRepo)
	require.Empty(t, svcErr.Available(context.Background(), nil, ""))

	okRepo := &modelsListProviderRepoStub{
		all: []provider.Record{
			{
				ID:       1,
				Platform: capability.PlatformAnthropic,
				Credentials: map[string]any{
					"model_whitelist": []string{"claude-3-5-sonnet"},
					"model_mapping": map[string]any{
						"claude-3-5-sonnet": "claude-3-5-sonnet",
					},
				},
			},
			{
				ID:       2,
				Platform: capability.PlatformGemini,
				Credentials: map[string]any{
					"model_whitelist": []string{"gemini-2.5-pro"},
					"model_mapping": map[string]any{
						"gemini-2.5-pro": "gemini-2.5-pro",
					},
				},
			},
		},
	}
	svcOK := newRequestableListFixture(okRepo)
	models := svcOK.Available(context.Background(), nil, "")
	require.Equal(t, []string{"claude-3-5-sonnet", "gemini-2.5-pro"}, models)
	require.Equal(t, int64(1), okRepo.listAllCalls.Load())
}

// TestGetAvailableModelsPassthroughPreservesExplicitScope 验证透传只改变传输，显式白名单及映射在目录聚合时仍生效。
func TestGetAvailableModelsPassthroughPreservesExplicitScope(t *testing.T) {
	groupID := int64(10)
	first := provider.Record{ID: 1, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Credentials: map[string]any{
		"model_mapping": map[string]any{"custom-alias": "custom-upstream"}, "model_whitelist": []string{"custom-upstream"},
	}, Extra: map[string]any{"openai_passthrough": true}}
	second := provider.Record{ID: 2, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey, Credentials: map[string]any{
		"model_mapping": map[string]any{"other-alias": "other-upstream"}, "model_whitelist": []string{"other-upstream"},
	}}
	repo := &modelsListProviderRepoStub{byGroup: map[int64][]provider.Record{groupID: {first, second}}}
	models := newRequestableListFixture(repo).Available(context.Background(), &groupID, capability.PlatformOpenAI)
	require.ElementsMatch(t, []string{"custom-alias", "custom-upstream", "other-alias", "other-upstream"}, models)
}

func TestGetAvailableModels_GlobalListPreservesMappedModelsWithOpenAIPassthrough(t *testing.T) {
	groupID := int64(11)
	repo := &modelsListProviderRepoStub{
		byGroup: map[int64][]provider.Record{
			groupID: {
				{
					ID:       1,
					Platform: capability.PlatformOpenAI,
					Extra:    map[string]any{"openai_passthrough": true},
				},
				{
					ID:          2,
					Platform:    capability.PlatformAnthropic,
					Credentials: map[string]any{"model_mapping": map[string]any{"claude-mapped": "claude-mapped"}},
				},
			},
		},
	}
	svc := newRequestableListFixture(repo)

	models := svc.Available(context.Background(), &groupID, "")
	require.Contains(t, models, "claude-mapped")
	require.NotContains(t, models, "gpt-5.6-sol", "目录只使用已配置的型号")
	require.NotContains(t, models, "unknown-model")
}

func newRequestableListFixture(rows catalogueRows) requestableListFixture {
	return requestableListFixture{newCatalogueFixture(rows, nil, nil).RequestableCatalogue}
}

func (f requestableListFixture) Available(ctx context.Context, groupID *int64, platform string) []string {
	return routing.RequestableModelIDs(f.ResolveRequestableModels(ctx, groupID, platform).Models)
}
