package legacybridge

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// AccountUpstreamUsage 只将只读查询交给尚未迁移的供应商 Adapter；不持有缓存或执行健康规则。
type AccountUpstreamUsage struct{ Source *service.UpstreamUsageService }

func (s AccountUpstreamUsage) Available() bool                      { return s.Source.Available() }
func (s AccountUpstreamUsage) Supports(name string) bool            { return s.Source.Supports(name) }
func (s AccountUpstreamUsage) BaseURL(value *account.Record) string { return s.Source.BaseURL(value) }
func (s AccountUpstreamUsage) Query(ctx context.Context, value *account.Record, config account.UpstreamUsageQueryConfig) (*account.UpstreamUsageInfo, error) {
	return s.Source.Query(ctx, value, config)
}
