package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/handler/admin"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"log/slog"
	"time"
)

// provideAccountImportProbes 为账号/供应商导入绑定唯一按需队列，构造不会提前探测。
func provideAccountImportProbes(manager *lifecycle.Manager) *account.GrokImportProbeScheduler {
	queue := account.NewGrokImportProbeScheduler(account.GrokImportProbeOptions{Concurrency: 3, Timeout: 25 * time.Second, Debug: slog.Debug, Info: slog.Info, Warn: slog.Warn, Error: slog.Error})
	manager.Register(lifecycle.Hook{Name: "AccountImportProbes", StopOrder: 20, Stop: queue.StopContext})
	return queue
}

func provideGrokOAuthWithImports(
	queue *account.GrokImportProbeScheduler,
	grokOAuthService *service.GrokOAuthService,
	adminService service.AdminService,
	quotaService *service.GrokQuotaService,
	reconciler service.GrokOAuthReconciler,
) *admin.GrokOAuthHandler {
	return admin.NewGrokOAuthHandlerWithImportProbes(queue, grokOAuthService, adminService, quotaService, reconciler)
}
