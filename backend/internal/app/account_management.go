package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	timezone "github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"log/slog"
)

// provideAccountManagement 将管理用例直接接入新 HTTP Adapter，展示投影在本阶段继续拆分。
func provideAccountManagement(admin *account.Admin, presenter *accounthttp.RuntimePresenter, ollama *account.OllamaCloudUsageService, privacy *account.PrivacyService, probes *account.GrokImportProbeScheduler, quota *service.GrokQuotaService, managed *account.ManagedRefreshService, tasks *lifecycle.Tasks, recovery *account.RecoveryService, listing *account.ManagementList, catalog *routing.AdminCatalog, tier *account.TierManagement, models *account.ModelSyncService, usage *service.AccountUsageService, diagnostics *service.AdvancedSchedulerScoreDiagnosticService) *accounthttp.ManagementHandler {
	return accounthttp.NewManagementHandler(admin, accounthttp.ManagementOptions{Diagnostics: legacybridge.AccountSchedulerDiagnostics(diagnostics), Models: models, Reports: accounthttp.AccountReportOptions{Now: timezone.NewCalendar(timezone.Location()).Now, StartOfDay: timezone.NewCalendar(timezone.Location()).StartOfDay, Query: legacybridge.AccountUsageReportQuery(usage)}, Tier: tier, Catalog: catalog, ModelDefaults: legacybridge.AccountAdminModelDefaults(), List: listing, RuntimePresenter: presenter, Recovery: recovery, Batch: account.NewManagementBatch(admin, managed, account.ManagementCreationOptions{Privacy: privacy, Background: tasks.Go, AfterCreate: func(v *account.Record) {
		if v == nil {
			return
		}
		snapshot := v.RoutingSnapshot()
		probes.Schedule(legacybridge.AccountImportQuotaProbe{Source: quota}, &snapshot)
	}, Error: slog.Error}), Managed: managed, Presenter: presenter, Ollama: ollama, Privacy: privacy, AfterCreate: func(v *account.Record) {
		if v == nil {
			return
		}
		snapshot := v.RoutingSnapshot()
		probes.Schedule(legacybridge.AccountImportQuotaProbe{Source: quota}, &snapshot)
	}})
}

// provideAdminModelCatalog 每次读取平台快照，不构造第二份目录缓存。
func provideAdminModelCatalog() *routing.AdminCatalog {
	return routing.NewAdminCatalog(legacybridge.AdminCatalogOptions())
}
