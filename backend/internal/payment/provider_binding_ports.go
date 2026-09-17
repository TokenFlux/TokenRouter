// 渠道绑定仅接收实例查询与构造端口；应用持有唯一注册表与加载屏障。
package payment

import (
	"context"
	"sync"
)

type BindingStore interface {
	Instance(context.Context, int64) (*ProviderInstance, error)
	ListInstances(context.Context, InstanceFilter) ([]*ProviderInstance, error)
	CountEnabledInstances(context.Context, string) (int, error)
	OrderByTradeNumber(context.Context, string) (*Order, error)
	IsNotFound(error) bool
}
type BindingRuntime struct {
	Factory         func(string, string, map[string]string) (Provider, error)
	RegistryFactory func(string, string, map[string]string) (Provider, error)
	Warn            func(string, ...any)
}
type ProviderBindings struct {
	providerMu      sync.Mutex
	providersLoaded bool
	store           BindingStore
	registry        *Registry
	loadBalancer    LoadBalancer
	runtime         BindingRuntime
}

func NewProviderBindings(store BindingStore, registry *Registry, balancer LoadBalancer, runtime BindingRuntime, alreadyLoaded bool) *ProviderBindings {
	return &ProviderBindings{store: store, registry: registry, loadBalancer: balancer, runtime: runtime, providersLoaded: alreadyLoaded}
}
func (s *ProviderBindings) warn(message string, attrs ...any) {
	if s.runtime.Warn != nil {
		s.runtime.Warn(message, attrs...)
	}
}
