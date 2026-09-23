package selection

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// 以下边界只借用已绑定拥有者；缓存解码、健康状态和窗口规则不在本包重建。
func readSnapshotAccount(ctx context.Context, source Snapshots, id int64) (*provider.ExecutionAccount, error) {
	value, err := source.GetAccount(ctx, id)
	return provider.NewExecutionAccount(value), err
}
func readSnapshotAccounts(ctx context.Context, source Snapshots, group *int64, platform string, forced bool) ([]provider.ExecutionAccount, bool, error) {
	values, mixed, err := source.ListAccounts(ctx, group, platform, forced)
	return provider.ExecutionAccounts(values), mixed, err
}
func (s *Generic) debugModelRoutingEnabled() bool                   { return s != nil && s.options.DebugRouting }
func (s *Generic) windowCostGuard() *billing.WindowCostGuard        { return s.window }
func (s *Compatible) runtimeBlockState() *account.RuntimeBlockState { return s.runtime }
func (s *Compatible) getOpenAIAccountModelTransientState() *account.ModelTransientState {
	if s == nil {
		return nil
	}
	return s.modelTransient
}
func (s *Compatible) getOpenAIProxyStreamCircuit() *egress.ProxyStreamCircuit {
	if s == nil {
		return nil
	}
	return s.proxyCircuit
}
func (s *Compatible) ResponseStateStore() session.OpenAIWSStateStore {
	if s == nil {
		return nil
	}
	return s.responseState
}
func (s *Compatible) OpenAIHTTPResponseStickyTTL() time.Duration {
	if s != nil && s.options.ResponseTTL > 0 {
		return s.options.ResponseTTL
	}
	return time.Hour
}
func (s *Compatible) stickyStats() *scheduler.StickyStats {
	if s == nil {
		return &scheduler.StickyStats{}
	}
	return s.stickyMetrics
}
func resolveAccountUpstreamModel(ctx context.Context, value *provider.ExecutionAccount, model string) string {
	return provider.ExecutionModelPolicy(value).UpstreamModel(ctx, model)
}
func mapAntigravityModel(value *provider.ExecutionAccount, model string) string {
	return accountprovider.MapAntigravityModel(provider.ExecutionRecord(value), model)
}

// defaultWindowCostGuard 保留未装配来源时的原窗口边界及全局观测，不复制窗口算法。
func defaultWindowCostGuard() *billing.WindowCostGuard {
	return billing.NewWindowCostGuard(nil, nil, billing.WindowCostGuardOptions{Now: time.Now, Stats: billing.SharedWindowCostMetrics(), Log: func(format string, args ...any) { logging.LegacyPrintf("service.gateway", format, args...) }, Debug: slog.Debug})
}
