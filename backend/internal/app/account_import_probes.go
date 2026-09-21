package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// provideAccountImportProbes 为账号/供应商导入绑定唯一按需队列，构造不会提前探测。
func provideAccountImportProbes(manager *lifecycle.Manager) *account.GrokImportProbeScheduler {
	queue := account.NewGrokImportProbeScheduler(account.GrokImportProbeOptions{Concurrency: 3, Timeout: 25 * time.Second, Debug: slog.Debug, Info: slog.Info, Warn: slog.Warn, Error: slog.Error})
	manager.Register(lifecycle.Hook{Name: "AccountImportProbes", StopOrder: 20, Stop: queue.StopContext})
	return queue
}

func provideGrokOAuthWithImports(
	queue *account.GrokImportProbeScheduler,
	grokOAuthService *account.GrokAuthorization,
	adminService *account.Admin,
	proxies *egress.ProxyAdmin,
	tasks *lifecycle.Tasks,
	quotaService *account.GrokQuotaService,
	reconciler account.GrokOAuthReconciler,
) *accounthttp.GrokOAuthHandler {
	proxyURL := func(ctx context.Context, id int64) (string, bool, error) {
		value, err := proxies.GetProxy(ctx, id)
		if err != nil || value == nil {
			return "", false, err
		}
		return value.URL(), true, nil
	}
	runTask := func(label string, work func()) { tasks.Go(label, work) }
	var auth *account.GrokAuthorization
	if grokOAuthService != nil {
		auth = grokOAuthService
	}
	var quota *account.GrokQuotaService
	if quotaService != nil {
		quota = quotaService
	}
	imports := account.NewGrokAccountImport(auth, account.GrokAccountImportOptions{Get: adminService.GetAccount, Create: adminService.CreateAccount, Update: adminService.UpdateAccount, NormalizeToken: grok.NormalizeSSOToken, LogError: slog.Error, RunTask: runTask, Schedule: func(value *account.Record) {
		snapshot := value.RoutingSnapshot()
		queue.Schedule(grokQuotaImportProbe{quota}, &snapshot)
	}})
	return accounthttp.NewGrokOAuthHandler(auth, imports, quota, accounthttp.GrokOAuthHTTPOptions{ProxyURL: proxyURL, RuntimeSanity: func() any { return grok.RuntimeSanity() }, Reconciler: reconciler})
}

// 导入队列只读取探测摘要，不暴露完整额度或凭据。
type grokQuotaImportProbe struct{ Source *account.GrokQuotaService }

func (p grokQuotaImportProbe) QueryQuota(ctx context.Context, id int64) (*account.GrokImportProbeResult, error) {
	v, err := p.Source.QueryQuota(ctx, id)
	if v == nil {
		return nil, err
	}
	return &account.GrokImportProbeResult{Model: v.Model, StatusCode: v.StatusCode, HeadersObserved: v.HeadersObserved}, err
}
