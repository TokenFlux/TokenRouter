// 旧设置及网关入口只持有新 search 实例的引用。
package service

import (
	"context"
	"sync/atomic"

	"github.com/TokenFlux/TokenRouter/internal/search"
)

type WebSearchEmulationConfig = search.WebSearchEmulationConfig
type WebSearchProviderConfig = search.WebSearchProviderConfig
type WebSearchTestResult = search.WebSearchTestResult

var searchRegistry atomic.Pointer[search.Registry]

func WebSearchRegistry() *search.Registry {
	if r := searchRegistry.Load(); r != nil {
		return r
	}
	next := search.NewRegistry()
	if searchRegistry.CompareAndSwap(nil, next) {
		return next
	}
	return searchRegistry.Load()
}
func SetWebSearchRegistry(r *search.Registry) { searchRegistry.Store(r) }
func (s *SettingService) SetSearchRuntime(runtime *search.ConfigService) {
	s.runtimeSettingsMu.Lock()
	s.searchConfig = runtime
	s.runtimeSettingsMu.Unlock()
}
func (s *SettingService) SearchRuntime() *search.ConfigService {
	s.runtimeSettingsMu.Lock()
	defer s.runtimeSettingsMu.Unlock()
	if s.searchConfig == nil {
		s.searchConfig = search.NewConfigService(s.settingRepo, nil, nil, WebSearchRegistry())
	}
	return s.searchConfig
}
func (s *SettingService) GetWebSearchEmulationConfig(ctx context.Context) (*WebSearchEmulationConfig, error) {
	return s.SearchRuntime().GetWebSearchEmulationConfig(ctx)
}
func (s *SettingService) SaveWebSearchEmulationConfig(ctx context.Context, cfg *WebSearchEmulationConfig) error {
	return s.SearchRuntime().SaveWebSearchEmulationConfig(ctx, cfg)
}
func (s *SettingService) IsWebSearchEmulationEnabled(ctx context.Context) bool {
	return s.SearchRuntime().IsWebSearchEmulationEnabled(ctx)
}

func PopulateWebSearchUsage(ctx context.Context, cfg *WebSearchEmulationConfig) *WebSearchEmulationConfig {
	return search.PopulateWebSearchUsage(ctx, cfg, WebSearchRegistry())
}
func SanitizeWebSearchConfig(ctx context.Context, cfg *WebSearchEmulationConfig) *WebSearchEmulationConfig {
	return search.SanitizeWebSearchConfig(ctx, cfg, WebSearchRegistry())
}
func ResetWebSearchUsage(ctx context.Context, provider string) error {
	return search.ResetWebSearchUsage(ctx, provider, WebSearchRegistry())
}
func TestWebSearch(ctx context.Context, query string) (*WebSearchTestResult, error) {
	return search.TestWebSearch(ctx, query, WebSearchRegistry())
}
