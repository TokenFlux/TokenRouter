package search

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/stretchr/testify/require"
)

// configFixture 固定规划阶段的旧回源/新保存交错，写入可注入失败。
type configFixture struct {
	mu        sync.Mutex
	raw       string
	fail      error
	firstRead atomic.Bool
	entered   chan struct{}
	release   chan struct{}
}

func (r *configFixture) GetValue(_ context.Context, _ string) (string, error) {
	r.mu.Lock()
	raw := r.raw
	r.mu.Unlock()
	if r.entered != nil && r.firstRead.CompareAndSwap(false, true) {
		close(r.entered)
		<-r.release
	}
	if raw == "" {
		return "", settings.ErrSettingNotFound
	}
	return raw, nil
}
func (r *configFixture) Set(_ context.Context, _, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	r.raw = value
	return nil
}
func TestS10ConfigOldLoadCannotOverwriteSave(t *testing.T) {
	repo := &configFixture{raw: `{"enabled":false,"providers":[]}`, entered: make(chan struct{}), release: make(chan struct{})}
	service := NewConfigService(repo, nil, nil, nil)
	done := make(chan error, 1)
	go func() { _, err := service.GetWebSearchEmulationConfig(context.Background()); done <- err }()
	<-repo.entered
	require.NoError(t, service.SaveWebSearchEmulationConfig(context.Background(), &WebSearchEmulationConfig{Enabled: true, Providers: []WebSearchProviderConfig{{Type: ProviderTypeBrave, APIKey: "fixture"}}}))
	close(repo.release)
	require.NoError(t, <-done)
	value, err := service.GetWebSearchEmulationConfig(context.Background())
	require.NoError(t, err)
	require.True(t, value.Enabled)
}
func TestS10ConfigCopiesAndWriteFailure(t *testing.T) {
	repo := &configFixture{}
	service := NewConfigService(repo, nil, nil, nil)
	limit, sub, proxy, expiry := int64(10), int64(20), int64(30), int64(40)
	input := &WebSearchEmulationConfig{Enabled: true, Providers: []WebSearchProviderConfig{{Type: ProviderTypeBrave, APIKey: "fixture", QuotaLimit: &limit, SubscribedAt: &sub, ProxyID: &proxy, ExpiresAt: &expiry}}}
	require.NoError(t, service.SaveWebSearchEmulationConfig(context.Background(), input))
	input.Enabled = false
	input.Providers[0].APIKey = "changed"
	limit, sub, proxy, expiry = 1, 2, 3, 4
	value, err := service.GetWebSearchEmulationConfig(context.Background())
	require.NoError(t, err)
	require.True(t, value.Enabled)
	require.Equal(t, "fixture", value.Providers[0].APIKey)
	require.Equal(t, int64(10), *value.Providers[0].QuotaLimit)
	require.Equal(t, int64(20), *value.Providers[0].SubscribedAt)
	require.Equal(t, int64(30), *value.Providers[0].ProxyID)
	require.Equal(t, int64(40), *value.Providers[0].ExpiresAt)
	view := SanitizeWebSearchConfig(context.Background(), value, service.Registry())
	require.Empty(t, view.Providers[0].APIKey)
	*view.Providers[0].QuotaLimit = 99
	*value.Providers[0].ExpiresAt = 99
	current, err := service.GetWebSearchEmulationConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(10), *current.Providers[0].QuotaLimit)
	require.Equal(t, int64(40), *current.Providers[0].ExpiresAt)
	repo.mu.Lock()
	repo.fail = errors.New("write failed")
	repo.mu.Unlock()
	require.Error(t, service.SaveWebSearchEmulationConfig(context.Background(), &WebSearchEmulationConfig{}))
	current, err = service.GetWebSearchEmulationConfig(context.Background())
	require.NoError(t, err)
	require.True(t, current.Enabled)
}

type configProxyFixture struct {
	first            atomic.Bool
	entered, release chan struct{}
}

func (p *configProxyFixture) URLs(context.Context, []int64) (map[int64]string, error) {
	if p.first.CompareAndSwap(false, true) {
		close(p.entered)
		<-p.release
	}
	return map[int64]string{1: "http://fixture.invalid"}, nil
}

type noSearchExecutor struct{}

func (noSearchExecutor) Search(context.Context, ProviderConfig, SearchRequest) (*SearchResponse, error) {
	return &SearchResponse{}, nil
}
func (noSearchExecutor) IsProxyError(error) bool { return false }
func (noSearchExecutor) CloseIdle()              {}
func TestS10OldManagerBuildCannotOverwriteSavedGeneration(t *testing.T) {
	repo := &configFixture{raw: `{"enabled":true,"providers":[{"type":"brave","api_key":"old","proxy_id":1}]}`}
	proxy := &configProxyFixture{entered: make(chan struct{}), release: make(chan struct{})}
	service := NewConfigService(repo, proxy, func(c []ProviderConfig, g *WorkGroup) *Manager { return NewManager(c, nil, noSearchExecutor{}, g) }, nil)
	done := make(chan error, 1)
	go func() { done <- service.Initialize(context.Background()) }()
	<-proxy.entered
	id := int64(1)
	require.NoError(t, service.SaveWebSearchEmulationConfig(context.Background(), &WebSearchEmulationConfig{Enabled: true, Providers: []WebSearchProviderConfig{{Type: ProviderTypeBrave, APIKey: "new", ProxyID: &id}}}))
	close(proxy.release)
	require.NoError(t, <-done)
	require.Equal(t, "new", service.Registry().Get().ProviderConfigs()[0].APIKey)
	require.NoError(t, service.StopContext(context.Background()))
	require.Error(t, service.Initialize(context.Background()))
}

func TestS10LoadedConfigurationThenSavedReplacement(t *testing.T) {
	repo := &configFixture{raw: `{"enabled":false,"providers":[]}`}
	service := NewConfigService(repo, nil, func(c []ProviderConfig, g *WorkGroup) *Manager { return NewManager(c, nil, noSearchExecutor{}, g) }, nil)
	prior, err := service.GetWebSearchEmulationConfig(context.Background())
	require.NoError(t, err)
	require.False(t, prior.Enabled)
	require.NoError(t, service.SaveWebSearchEmulationConfig(context.Background(), &WebSearchEmulationConfig{Enabled: true, Providers: []WebSearchProviderConfig{{Type: ProviderTypeBrave, APIKey: "replacement"}}}))
	current, err := service.GetWebSearchEmulationConfig(context.Background())
	require.NoError(t, err)
	require.True(t, current.Enabled)
	require.Equal(t, "replacement", service.Registry().Get().ProviderConfigs()[0].APIKey)
}
func TestS10ConsecutiveSavesKeepLastPublication(t *testing.T) {
	repo := &configFixture{}
	proxies := &configProxyFixture{entered: make(chan struct{}), release: make(chan struct{})}
	service := NewConfigService(repo, proxies, func(c []ProviderConfig, g *WorkGroup) *Manager { return NewManager(c, nil, noSearchExecutor{}, g) }, nil)
	id := int64(1)
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() {
		first <- service.SaveWebSearchEmulationConfig(context.Background(), &WebSearchEmulationConfig{Enabled: true, Providers: []WebSearchProviderConfig{{Type: ProviderTypeBrave, APIKey: "first", ProxyID: &id}}})
	}()
	<-proxies.entered
	go func() {
		second <- service.SaveWebSearchEmulationConfig(context.Background(), &WebSearchEmulationConfig{Enabled: true, Providers: []WebSearchProviderConfig{{Type: ProviderTypeBrave, APIKey: "second", ProxyID: &id}}})
	}()
	close(proxies.release)
	require.NoError(t, <-first)
	require.NoError(t, <-second)
	require.Equal(t, "second", service.Registry().Get().ProviderConfigs()[0].APIKey)
}
