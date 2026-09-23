package app

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/search"

	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/stretchr/testify/require"
)

// readerStoreProbe 确认装配不提前读取动态值，也不在缓存命中时增加查询。
type readerStoreProbe struct {
	settings.Repository
	reads int
}

func (s *readerStoreProbe) GetValue(context.Context, string) (string, error) {
	s.reads++
	return "", settings.ErrSettingNotFound
}

func TestSettingsReadersSharePublishedState(t *testing.T) {
	repo := &readerStoreProbe{}
	store := settings.New(repo)
	gatewayRuntime := provideGatewaySettings(store)
	account := provideAccountSettings(store)
	quota := provideQuotaSettings(store)
	routing := provideRoutingSettings(store)
	moderation := provideModerationSettings(store)
	searchRuntime := search.NewConfigService(store, nil, nil, search.NewRegistry())
	readers := provideGatewayRuntimeReaders(store, nil, gatewayRuntime, account, quota, routing, moderation, searchRuntime)
	t.Cleanup(func() {
		// 清除本测试安装的默认 UA 读取器，避免影响同进程的其他装配契约。
		openai.SetCodexCanonicalUserAgentResolver(nil)
		antigravity.SetUserAgentVersionResolver(nil)
	})
	require.Zero(t, repo.reads)
	require.Same(t, gatewayRuntime, readers.Gateway)
	require.Same(t, account, readers.Account)
	require.Same(t, quota, readers.Quota)
	require.Same(t, routing, readers.Routing)
	require.Same(t, moderation, readers.Moderation)
	require.Same(t, searchRuntime, readers.Search)
	require.Same(t, store, readers.Scheduler)
	account.ApplySchedulingThresholds(map[string]int{"openai": 73})
	require.Equal(t, 73, readers.Account.GetAccountSchedulingThresholds(context.Background())["openai"])
	require.Zero(t, repo.reads, "提交后发布必须命中执行消费者共用的缓存")
}
