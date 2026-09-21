//go:build unit

package catalogue_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
)

func TestGetAvailableModels_UsesShortCacheAndSupportsInvalidation(t *testing.T) {
	resetModelListMetrics()

	groupID := int64(9)
	repo := &modelsListAccountRepoStub{
		byGroup: map[int64][]account.Record{
			groupID: {
				{
					ID:       1,
					Platform: capability.PlatformAnthropic,
					Credentials: map[string]any{
						"model_mapping": map[string]any{
							"claude-3-5-sonnet": "claude-3-5-sonnet",
							"claude-3-5-haiku":  "claude-3-5-haiku",
						},
					},
				},
				{
					ID:       2,
					Platform: capability.PlatformGemini,
					Credentials: map[string]any{
						"model_mapping": map[string]any{
							"gemini-2.5-pro": "gemini-2.5-pro",
						},
					},
				},
			},
		},
	}

	svc := newModelListFixture(repo)

	models1 := svc.Available(context.Background(), &groupID, capability.PlatformAnthropic)
	require.Equal(t, []string{"claude-3-5-haiku", "claude-3-5-sonnet"}, models1)
	require.Equal(t, int64(1), repo.listByGroupCalls.Load())

	// TTL 内再次请求应命中缓存，不回源。
	models2 := svc.Available(context.Background(), &groupID, capability.PlatformAnthropic)
	require.Equal(t, models1, models2)
	require.Equal(t, int64(1), repo.listByGroupCalls.Load())

	// 更新仓储数据，但缓存未失效前应继续返回旧值。
	repo.byGroup[groupID] = []account.Record{
		{
			ID:       3,
			Platform: capability.PlatformAnthropic,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"claude-3-7-sonnet": "claude-3-7-sonnet",
				},
			},
		},
	}
	models3 := svc.Available(context.Background(), &groupID, capability.PlatformAnthropic)
	require.Equal(t, []string{"claude-3-5-haiku", "claude-3-5-sonnet"}, models3)
	require.Equal(t, int64(1), repo.listByGroupCalls.Load())

	svc.Invalidate(&groupID, capability.PlatformAnthropic)
	models4 := svc.Available(context.Background(), &groupID, capability.PlatformAnthropic)
	require.Equal(t, []string{"claude-3-7-sonnet"}, models4)
	require.Equal(t, int64(2), repo.listByGroupCalls.Load())

	metrics := routing.SharedModelListMetrics()
	hit, miss, store := metrics.Hit.Load(), metrics.Miss.Load(), metrics.Store.Load()
	require.Equal(t, int64(2), hit)
	require.Equal(t, int64(2), miss)
	require.Equal(t, int64(2), store)
}

func TestGetAvailableModels_ErrorAndGlobalListBranches(t *testing.T) {
	resetModelListMetrics()

	errRepo := &modelsListAccountRepoStub{
		err: errors.New("db error"),
	}
	svcErr := newModelListFixture(errRepo)
	require.Nil(t, svcErr.Available(context.Background(), nil, ""))

	okRepo := &modelsListAccountRepoStub{
		all: []account.Record{
			{
				ID:       1,
				Platform: capability.PlatformAnthropic,
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"claude-3-5-sonnet": "claude-3-5-sonnet",
					},
				},
			},
			{
				ID:       2,
				Platform: capability.PlatformGemini,
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"gemini-2.5-pro": "gemini-2.5-pro",
					},
				},
			},
		},
	}
	svcOK := newModelListFixture(okRepo)
	models := svcOK.Available(context.Background(), nil, "")
	require.Equal(t, []string{"claude-3-5-sonnet", "gemini-2.5-pro"}, models)
	require.Equal(t, int64(1), okRepo.listAllCalls.Load())
}

func TestGetAvailableModels_OpenAIPassthroughUsesDefaultFallback(t *testing.T) {
	groupID := int64(10)

	tests := []struct {
		name     string
		accounts []account.Record
		want     []string
	}{
		{
			name: "passthrough only ignores stale mapping",
			accounts: []account.Record{
				{
					ID:          1,
					Platform:    capability.PlatformOpenAI,
					Credentials: map[string]any{"model_mapping": map[string]any{"stale-model": "upstream-model"}},
					Extra:       map[string]any{"openai_passthrough": true},
				},
			},
			want: nil,
		},
		{
			name: "passthrough wins over ordinary account mapping",
			accounts: []account.Record{
				{
					ID:          2,
					Platform:    capability.PlatformOpenAI,
					Credentials: map[string]any{"model_mapping": map[string]any{"configured-model": "configured-upstream"}},
				},
				{
					ID:          3,
					Platform:    capability.PlatformOpenAI,
					Credentials: map[string]any{"model_mapping": map[string]any{"stale-model": "upstream-model"}},
					Extra:       map[string]any{"openai_passthrough": true},
				},
			},
			want: nil,
		},
		{
			name: "ordinary accounts preserve mapped whitelist",
			accounts: []account.Record{
				{
					ID:          4,
					Platform:    capability.PlatformOpenAI,
					Credentials: map[string]any{"model_mapping": map[string]any{"configured-model": "configured-model"}},
				},
			},
			want: []string{"configured-model"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &modelsListAccountRepoStub{byGroup: map[int64][]account.Record{groupID: tt.accounts}}
			svc := newModelListFixture(repo)

			require.Equal(t, tt.want, svc.Available(context.Background(), &groupID, capability.PlatformOpenAI))
		})
	}
}

func TestGetAvailableModels_GlobalListPreservesMappedModelsWithOpenAIPassthrough(t *testing.T) {
	groupID := int64(11)
	repo := &modelsListAccountRepoStub{
		byGroup: map[int64][]account.Record{
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
	svc := newModelListFixture(repo)

	require.Equal(t, []string{"claude-mapped"}, svc.Available(context.Background(), &groupID, ""))
}

func TestInvalidateAvailableModelsCache_ByDimensions(t *testing.T) {
	svc := &routing.ModelList{Cache: gocache.New(time.Minute, time.Minute)}
	group9 := int64(9)
	group10 := int64(10)
	svc.Cache.Set(routing.ModelListCacheKey(&group9, capability.PlatformAnthropic), []string{"a"}, time.Minute)
	svc.Cache.Set(routing.ModelListCacheKey(&group9, capability.PlatformGemini), []string{"b"}, time.Minute)
	svc.Cache.Set(routing.ModelListCacheKey(&group10, capability.PlatformAnthropic), []string{"c"}, time.Minute)
	svc.Cache.Set("invalid-key", []string{"d"}, time.Minute)

	t.Run("invalidate_group_and_platform", func(t *testing.T) {
		svc.Invalidate(&group9, capability.PlatformAnthropic)
		_, found := svc.Cache.Get(routing.ModelListCacheKey(&group9, capability.PlatformAnthropic))
		require.False(t, found)
		_, stillFound := svc.Cache.Get(routing.ModelListCacheKey(&group9, capability.PlatformGemini))
		require.True(t, stillFound)
	})

	t.Run("invalidate_group_only", func(t *testing.T) {
		svc.Invalidate(&group9, "")
		_, foundA := svc.Cache.Get(routing.ModelListCacheKey(&group9, capability.PlatformAnthropic))
		_, foundB := svc.Cache.Get(routing.ModelListCacheKey(&group9, capability.PlatformGemini))
		require.False(t, foundA)
		require.False(t, foundB)
		_, foundOtherGroup := svc.Cache.Get(routing.ModelListCacheKey(&group10, capability.PlatformAnthropic))
		require.True(t, foundOtherGroup)
	})

	t.Run("invalidate_platform_only", func(t *testing.T) {
		// 重建数据后仅按 platform 失效
		svc.Cache.Set(routing.ModelListCacheKey(&group9, capability.PlatformAnthropic), []string{"a"}, time.Minute)
		svc.Cache.Set(routing.ModelListCacheKey(&group9, capability.PlatformGemini), []string{"b"}, time.Minute)
		svc.Cache.Set(routing.ModelListCacheKey(&group10, capability.PlatformAnthropic), []string{"c"}, time.Minute)

		svc.Invalidate(nil, capability.PlatformAnthropic)
		_, found9Anthropic := svc.Cache.Get(routing.ModelListCacheKey(&group9, capability.PlatformAnthropic))
		_, found10Anthropic := svc.Cache.Get(routing.ModelListCacheKey(&group10, capability.PlatformAnthropic))
		_, found9Gemini := svc.Cache.Get(routing.ModelListCacheKey(&group9, capability.PlatformGemini))
		require.False(t, found9Anthropic)
		require.False(t, found10Anthropic)
		require.True(t, found9Gemini)
	})
}

// newModelListFixture 共用原生账号投影，只为原缓存断言指定一分钟 TTL。
func newModelListFixture(rows catalogueRows) *routing.ModelList {
	catalogue := newCatalogueFixture(rows, nil, nil)
	return routing.NewModelList(catalogue.Read, time.Minute)
}

// resetModelListMetrics 保留原测试对唯一进程指标的精确断言。
func resetModelListMetrics() {
	metrics := routing.SharedModelListMetrics()
	metrics.Hit.Store(0)
	metrics.Miss.Store(0)
	metrics.Store.Store(0)
}
