// 旧构造只装配新实现，不保留第二份配额或选择状态。
package websearch

import (
	"github.com/TokenFlux/TokenRouter/internal/search"
	"github.com/TokenFlux/TokenRouter/internal/search/provider"
	"github.com/TokenFlux/TokenRouter/internal/search/rediscache"
	"github.com/redis/go-redis/v9"
)

type Manager = search.Manager

var ErrProxyUnavailable = search.ErrProxyUnavailable

func NewManager(configs []ProviderConfig, r *redis.Client) *Manager {
	var state search.QuotaState
	if r != nil {
		state = rediscache.New(r)
	}
	return search.NewManager(configs, state, provider.NewExecutor(), nil)
}
