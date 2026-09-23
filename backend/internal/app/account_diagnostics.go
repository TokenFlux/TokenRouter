package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	schedulerhttp "github.com/TokenFlux/TokenRouter/internal/scheduler/httpapi"
)

// provideAccountDiagnostics 直接组合只读诊断与真实选择共享的资格、参数和反馈。
func provideAccountDiagnostics(admin *account.Admin, groups *routing.GroupAdmin, concurrency *scheduler.ConcurrencyService,

	gateway *selection.Generic, openai *selection.Compatible, shared *schedulerSharedState) *selection.Diagnostics {
	return selection.NewDiagnostics(accountDiagnosticSource{accounts: admin, groups: groups}, selection.Shared{Concurrency: concurrency, Parameters: shared.Parameters, Feedback: shared.Feedback}, gateway, openai)
}

// provideSchedulerDiagnosticsHTTP 直接将只读诊断用例装配到 scheduler HTTP。
func provideSchedulerDiagnosticsHTTP(core *selection.Diagnostics) *schedulerhttp.DiagnosticsHandler {
	return schedulerhttp.NewDiagnosticsHandler(core)
}

// accountDiagnosticSource 只投影已迁管理读取；调度诊断仍复用当前唯一的资格和反馈实例。
// 此旧账号形状随网关诊断端口清理一起删除，不持有缓存、锁或评分规则。
type accountDiagnosticSource struct {
	accounts *account.Admin
	groups   *routing.GroupAdmin
}

func (s accountDiagnosticSource) GetAccount(ctx context.Context, id int64) (*gatewayprovider.ExecutionAccount, error) {
	value, err := s.accounts.GetAccount(ctx, id)
	return gatewayprovider.NewExecutionAccount(value), err
}
func (s accountDiagnosticSource) GetGroup(ctx context.Context, id int64) (*routing.Group, error) {
	return s.groups.GetGroup(ctx, id)
}
func (s accountDiagnosticSource) ListAccountsForSchedulerScoreFilter(ctx context.Context, platform, kind, status, search string, gid int64, privacy string) ([]gatewayprovider.ExecutionAccount, error) {
	values, err := s.accounts.ListAccountsForSchedulerScoreFilter(ctx, platform, kind, status, search, gid, privacy)
	return gatewayprovider.ExecutionAccounts(values), err
}
func (s accountDiagnosticSource) ListSchedulableAccountsForAdvancedSchedulerScore(ctx context.Context, gid *int64, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	values, err := s.accounts.ListSchedulableAccountsForAdvancedSchedulerScore(ctx, gid, platform)
	return gatewayprovider.ExecutionAccounts(values), err
}
