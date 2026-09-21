// 搜索配置、当前 Manager 与全部代次的在途计数由组合根统一持有。
package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	egresspg "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	"github.com/TokenFlux/TokenRouter/internal/search"
	searchhttp "github.com/TokenFlux/TokenRouter/internal/search/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/search/provider"
	"github.com/TokenFlux/TokenRouter/internal/search/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/redis/go-redis/v9"
)

func provideSearchRegistry() *search.Registry {
	registry := search.NewRegistry()
	return registry
}
func provideSearchRuntime(store *settings.Store, proxies *egresspg.ProxyStore, r *redis.Client, registry *search.Registry, manager *lifecycle.Manager) *search.ConfigService {
	var state search.QuotaState
	if r != nil {
		state = rediscache.New(r)
	}
	runtime := search.NewConfigService(store, searchProxies{proxies}, func(configs []search.ProviderConfig, work *search.WorkGroup) *search.Manager {
		return search.NewManager(configs, state, provider.NewExecutor(), work)
	}, registry)
	manager.Register(lifecycle.Hook{Name: "WebSearchRuntime", StartOrder: 181, StopOrder: 30, Start: runtime.Initialize, Stop: runtime.StopContext})
	return runtime
}
func provideSearchHTTP(runtime *search.ConfigService) *searchhttp.Handler {
	return searchhttp.New(runtime)
}

type searchProxies struct{ proxies *egresspg.ProxyStore }

func (s searchProxies) URLs(ctx context.Context, ids []int64) (map[int64]string, error) {
	values, err := s.proxies.ListByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(values))
	for _, v := range values {
		out[v.ID] = v.URL()
	}
	return out, nil
}
