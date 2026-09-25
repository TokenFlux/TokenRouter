package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	routingprovider "github.com/TokenFlux/TokenRouter/internal/routing/provider"

	usagetypes "github.com/TokenFlux/TokenRouter/internal/usage"
	usagepostgres "github.com/TokenFlux/TokenRouter/internal/usage/postgres"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	timezone "github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// provideAccountManagement 为账号管理 HTTP 接口绑定管理用例和展示参数。
func provideAccountManagement(admin *account.Admin, presenter *accounthttp.RuntimePresenter, ollama *account.OllamaCloudUsageService, privacy *account.PrivacyService, probes *account.GrokImportProbeScheduler, quota *account.GrokQuotaService, managed *account.ManagedRefreshService, tasks *lifecycle.Tasks, recovery *account.RecoveryService, listing *account.ManagementList, catalog *routing.AdminCatalog, tier *account.TierManagement, models *account.ModelSyncService, usage *usagepostgres.Store, calendar timezone.Calendar) *accounthttp.ManagementHandler {
	query := func(ctx context.Context, id int64, start, end time.Time) (*usagetypes.AccountUsageStatsResponse, error) {
		value, err := usage.GetAccountUsageStats(ctx, id, start, end)
		if err != nil {
			return nil, fmt.Errorf("get account usage stats failed: %w", err)
		}
		return value, nil
	}
	return accounthttp.NewManagementHandler(admin, accounthttp.ManagementOptions{Models: models, Reports: accounthttp.AccountReportOptions{Now: calendar.Now, StartOfDay: calendar.StartOfDay, Query: query}, Tier: tier, Catalog: catalog, ModelDefaults: accountprovider.ModelDefaults(), List: listing, RuntimePresenter: presenter, Recovery: recovery, Batch: account.NewManagementBatch(admin, managed, account.ManagementCreationOptions{Privacy: privacy, Background: tasks.Go, AfterCreate: func(v *account.Record) {
		if v == nil {
			return
		}
		snapshot := v.RoutingSnapshot()
		probes.Schedule(grokImportQuotaProbe{source: quota}, &snapshot)
	}, Error: slog.Error}), Managed: managed, Presenter: presenter, Ollama: ollama, Privacy: privacy, AfterCreate: func(v *account.Record) {
		if v == nil {
			return
		}
		snapshot := v.RoutingSnapshot()
		probes.Schedule(grokImportQuotaProbe{source: quota}, &snapshot)
	}})
}

// provideAdminModelCatalog 每次读取平台快照，不构造第二份目录缓存。
func provideAdminModelCatalog() *routing.AdminCatalog {
	return routing.NewAdminCatalog(routingprovider.AdminCatalogOptions())
}
