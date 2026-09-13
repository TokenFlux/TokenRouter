package admin

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	timezone "github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"log/slog"
)

// 旧 HTTP 入口只转换数据并委托新 Adapter，不再次实现协议输出。
type legacyAccountManagement struct{ legacyManagedAdmin }

func (a legacyAccountManagement) DeleteAccount(ctx context.Context, id int64) error {
	return a.source.DeleteAccount(ctx, id)
}
func (a legacyAccountManagement) CheckMixedChannelRisk(ctx context.Context, id int64, platform string, groups []int64) error {
	return a.source.CheckMixedChannelRisk(ctx, id, platform, groups)
}
func (h *AccountHandler) managementHTTP() *accounthttp.ManagementHandler {
	var ollama *account.OllamaCloudUsageService
	if h.ollamaCloudUsage != nil {
		ollama = h.ollamaCloudUsage.Core()
	}
	managed := h.managedRefresh
	if managed == nil {
		managed = h.legacyManagedRefresh(nil)
	}
	var diagnostics accounthttp.AccountSchedulerDiagnostics
	if h.advancedSchedulerScores != nil {
		diagnostics = h.advancedSchedulerScores
	}
	presenter := h.runtimePresenter()
	return accounthttp.NewManagementHandler(legacyAccountManagement{legacyManagedAdmin{h.adminService}}, accounthttp.ManagementOptions{Diagnostics: diagnostics, Models: account.NewModelSyncService(service.AccountModelSyncFetch(h.accountTestService)), Reports: accounthttp.AccountReportOptions{Now: timezone.Now, StartOfDay: timezone.StartOfDay, Query: h.accountUsageService.GetAccountUsageStats}, Tier: account.NewTierManagement(legacyAccountManagement{legacyManagedAdmin{h.adminService}}, service.AccountTierManagementOptions(h.geminiOAuthService)), Catalog: routing.NewAdminCatalog(service.AdminCatalogOptions()), ModelDefaults: service.AccountAdminModelDefaults(), List: h.managementList(ollama), RuntimePresenter: presenter, Recovery: h.rateLimitService.RecoveryCore(), Batch: account.NewManagementBatch(legacyAccountManagement{legacyManagedAdmin{h.adminService}}, managed, account.ManagementCreationOptions{Privacy: legacyManagedAdmin{h.adminService}, Background: service.RunBackgroundTask, AfterCreate: func(v *account.Record) { h.scheduleGrokImportProbe(service.AccountFromRecord(v)) }, Error: slog.Error}), Managed: managed, Presenter: presenter, Ollama: ollama, Privacy: legacyManagedAdmin{h.adminService}, AfterCreate: func(v *account.Record) { h.scheduleGrokImportProbe(service.AccountFromRecord(v)) }})
}

func (a legacyAccountManagement) CreateAccount(ctx context.Context, input *account.CreateAccountInput) (*account.Record, error) {
	v, err := a.source.CreateAccount(ctx, input)
	return service.AccountRecordView(v), err
}
func (a legacyAccountManagement) DuplicateAccount(ctx context.Context, id int64, scope, key string) (*account.Record, error) {
	v, err := a.source.DuplicateAccount(ctx, id, scope, key)
	return service.AccountRecordView(v), err
}
func (a legacyAccountManagement) RecoverDuplicateAccount(ctx context.Context, id int64, scope, key string) (*account.Record, error) {
	v, err := a.source.RecoverDuplicateAccount(ctx, id, scope, key)
	return service.AccountRecordView(v), err
}
func (a legacyManagedAdmin) ForceOpenAIPrivacy(ctx context.Context, v *account.Record) string {
	old := service.AccountFromRecord(v)
	mode := a.source.ForceOpenAIPrivacy(ctx, old)
	if old != nil {
		v.Extra = account.CloneValues(old.Extra)
	}
	return mode
}
func (a legacyManagedAdmin) ForceAntigravityPrivacy(ctx context.Context, v *account.Record) string {
	old := service.AccountFromRecord(v)
	mode := a.source.ForceAntigravityPrivacy(ctx, old)
	if old != nil {
		v.Extra = account.CloneValues(old.Extra)
	}
	return mode
}

func (a legacyAccountManagement) GetAccountsByIDs(ctx context.Context, ids []int64) ([]*account.Record, error) {
	values, err := a.source.GetAccountsByIDs(ctx, ids)
	if values == nil {
		return nil, err
	}
	out := make([]*account.Record, len(values))
	for i, v := range values {
		out[i] = service.AccountRecordView(v)
	}
	return out, err
}

func (a legacyAccountManagement) SetAccountSchedulable(ctx context.Context, id int64, value bool) (*account.Record, error) {
	v, err := a.source.SetAccountSchedulable(ctx, id, value)
	return service.AccountRecordView(v), err
}
func (a legacyAccountManagement) ResetAccountQuota(ctx context.Context, id int64) error {
	return a.source.ResetAccountQuota(ctx, id)
}
func (a legacyAccountManagement) RevertAccountProxyFallback(ctx context.Context, id int64) error {
	return a.source.RevertAccountProxyFallback(ctx, id)
}

func (a legacyAccountManagement) BulkUpdateAccounts(ctx context.Context, input *account.BulkUpdateAccountsInput) (*account.BulkUpdateAccountsResult, error) {
	return a.source.BulkUpdateAccounts(ctx, input)
}

// runtimePresenter 只投影旧独立构造的依赖，生产由 app 持有实例。
func (h *AccountHandler) runtimePresenter() *accounthttp.RuntimePresenter {
	var ollama *account.OllamaCloudUsageService
	if h.ollamaCloudUsage != nil {
		ollama = h.ollamaCloudUsage.Core()
	}
	return accounthttp.NewRuntimePresenter(account.NewRuntimeStatusReader(service.AccountRuntimeStatusOptions(h.concurrencyService, h.accountUsageService, h.sessionLimitCache, h.rpmCache, h.settingService)), legacyAccountManagement{legacyManagedAdmin{h.adminService}}, ollama)
}

func (h *AccountHandler) managementList(ollama *account.OllamaCloudUsageService) *account.ManagementList {
	admin := legacyAccountManagement{legacyManagedAdmin{h.adminService}}
	return account.NewManagementList(admin, account.NewRuntimeStatusReader(service.AccountRuntimeStatusOptions(h.concurrencyService, h.accountUsageService, h.sessionLimitCache, h.rpmCache, h.settingService)), account.NewSchedulerScoreView(admin, service.AccountSchedulerScoreOptions(h.concurrencyService, h.rateLimitService)), ollama)
}
func (a legacyAccountManagement) ListAccounts(ctx context.Context, page, size int, platform, kind, status, search string, groupID int64, privacy, sortBy, sortOrder string) ([]account.Record, int64, error) {
	v, total, err := a.source.ListAccounts(ctx, page, size, platform, kind, status, search, groupID, privacy, sortBy, sortOrder)
	return service.AccountRecordsView(v), total, err
}
func (a legacyAccountManagement) ListAccountsForSchedulerScoreFilter(ctx context.Context, platform, kind, status, search string, groupID int64, privacy string) ([]account.Record, error) {
	v, err := a.source.ListAccountsForSchedulerScoreFilter(ctx, platform, kind, status, search, groupID, privacy)
	return service.AccountRecordsView(v), err
}
func (a legacyAccountManagement) ListSchedulableAccountsForAdvancedSchedulerScore(ctx context.Context, groupID *int64, platform string) ([]account.Record, error) {
	v, err := a.source.ListSchedulableAccountsForAdvancedSchedulerScore(ctx, groupID, platform)
	return service.AccountRecordsView(v), err
}
