package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	schedulerhttp "github.com/TokenFlux/TokenRouter/internal/scheduler/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// provideAccountDiagnostics 保留原诊断装配与唯一调度反馈，执行算法留 S07。
func provideAccountDiagnostics(admin *account.Admin, groups *routing.GroupAdmin, concurrency *scheduler.ConcurrencyService, limits *service.RateLimitService, gateway *service.GatewayService, openai *service.OpenAIGatewayService) *service.AdvancedSchedulerScoreDiagnosticService {
	core := service.NewAdvancedSchedulerScoreDiagnosticService(accountDiagnosticSource{accounts: admin, groups: groups}, concurrency, limits)
	core.SetSchedulingServices(gateway, openai)
	return core
}

// provideSchedulerDiagnosticsHTTP 直接将只读诊断用例装配到 scheduler HTTP。
func provideSchedulerDiagnosticsHTTP(core *service.AdvancedSchedulerScoreDiagnosticService) *schedulerhttp.DiagnosticsHandler {
	return schedulerhttp.NewDiagnosticsHandler(core)
}

// accountDiagnosticSource 只投影已迁管理读取；调度诊断仍复用当前唯一的资格和反馈实例。
// 此旧账号形状随网关诊断端口清理一起删除，不持有缓存、锁或评分规则。
type accountDiagnosticSource struct {
	accounts *account.Admin
	groups   *routing.GroupAdmin
}

func (s accountDiagnosticSource) GetAccount(ctx context.Context, id int64) (*service.Account, error) {
	value, err := s.accounts.GetAccount(ctx, id)
	return service.AccountFromRecord(value), err
}
func (s accountDiagnosticSource) GetGroup(ctx context.Context, id int64) (*routing.Group, error) {
	return s.groups.GetGroup(ctx, id)
}
func (s accountDiagnosticSource) ListAccountsForSchedulerScoreFilter(ctx context.Context, platform, kind, status, search string, gid int64, privacy string) ([]service.Account, error) {
	values, err := s.accounts.ListAccountsForSchedulerScoreFilter(ctx, platform, kind, status, search, gid, privacy)
	return service.AccountsFromRecords(values), err
}
func (s accountDiagnosticSource) ListSchedulableAccountsForAdvancedSchedulerScore(ctx context.Context, gid *int64, platform string) ([]service.Account, error) {
	values, err := s.accounts.ListSchedulableAccountsForAdvancedSchedulerScore(ctx, gid, platform)
	return service.AccountsFromRecords(values), err
}
