// @project-doc docs/interfaces/upstream_usage.md#native_usage_adapters
// 供应商注册与技术请求装配归账号 Adapter；查询缓存、身份复核及生命周期归账号核心。
package provider

import (
	"context"
	"strings"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/deepseek"
	"github.com/TokenFlux/TokenRouter/internal/upstream/kimi"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usagecontract"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usageprovider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/zhipu"
)

type UpstreamUsageExecutionOptions struct {
	Available func() bool
	BaseURL   func(*account.Record) string
	Request   func(*account.Record, account.UpstreamUsageQueryConfig) (*usagecontract.Request, error)
}
type UpstreamUsageExecution struct {
	Options  UpstreamUsageExecutionOptions
	mu       sync.RWMutex
	adapters map[string]usagecontract.Adapter
}

func NewUpstreamUsageExecution(options UpstreamUsageExecutionOptions) *UpstreamUsageExecution {
	source := &UpstreamUsageExecution{Options: options, adapters: make(map[string]usagecontract.Adapter)}
	factories := map[string]func() usagecontract.Adapter{
		account.UpstreamUsageAdapterSub2API:         func() usagecontract.Adapter { return &usageprovider.Sub2APIUsageAdapter{} },
		account.UpstreamUsageAdapterNewAPI:          func() usagecontract.Adapter { return &usageprovider.NewAPIUsageAdapter{} },
		account.UpstreamUsageAdapterZivv:            func() usagecontract.Adapter { return &usageprovider.ZivvUsageAdapter{} },
		account.UpstreamUsageAdapterKimiCoding:      func() usagecontract.Adapter { return &kimi.KimiCodingUsageAdapter{} },
		account.UpstreamUsageAdapterKimiBalance:     func() usagecontract.Adapter { return &kimi.KimiBalanceUsageAdapter{} },
		account.UpstreamUsageAdapterZhipuCoding:     func() usagecontract.Adapter { return &zhipu.ZhipuCodingUsageAdapter{} },
		account.UpstreamUsageAdapterDeepseekBalance: func() usagecontract.Adapter { return &deepseek.DeepseekBalanceUsageAdapter{} },
	}
	for _, spec := range account.UpstreamUsageAdapterCatalog() {
		source.RegisterAdapter(factories[spec.Name]())
	}
	return source
}
func (s *UpstreamUsageExecution) RegisterAdapter(adapter usagecontract.Adapter) {
	if s == nil || adapter == nil || strings.TrimSpace(adapter.Name()) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.adapters == nil {
		s.adapters = make(map[string]usagecontract.Adapter)
	}
	s.adapters[strings.TrimSpace(adapter.Name())] = adapter
}
func (s *UpstreamUsageExecution) adapter(name string) usagecontract.Adapter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.adapters[name]
}
func (s *UpstreamUsageExecution) Available() bool {
	return s != nil && s.Options.Available != nil && s.Options.Available()
}
func (s *UpstreamUsageExecution) Supports(name string) bool { return s.adapter(name) != nil }
func (s *UpstreamUsageExecution) BaseURL(value *account.Record) string {
	return s.Options.BaseURL(value)
}
func (s *UpstreamUsageExecution) Query(ctx context.Context, value *account.Record, config account.UpstreamUsageQueryConfig) (*account.UpstreamUsageInfo, error) {
	adapter := s.adapter(config.Adapter)
	if adapter == nil {
		return nil, account.ErrUpstreamUsageUnsupported
	}
	input, err := s.Options.Request(value, config)
	if err != nil {
		return nil, err
	}
	return adapter.Query(ctx, input)
}
