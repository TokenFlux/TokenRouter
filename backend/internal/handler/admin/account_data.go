// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	context "context"
	account "github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	transfer "github.com/TokenFlux/TokenRouter/internal/account/transfer"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	gin "github.com/gin-gonic/gin"
	slog "log/slog"
	time "time"
)

const (
	dataType       = transfer.DataType
	legacyDataType = transfer.LegacyDataType
	dataVersion    = transfer.DataVersion
)

type DataPayload = transfer.DataPayload
type DataProxy = egress.TransferProxy
type DataAccount = transfer.DataAccount
type DataImportRequest = transfer.DataImportRequest
type DataImportResult = transfer.DataImportResult
type DataImportError = transfer.DataImportError

func (h *AccountHandler) ExportData(c *gin.Context) {
	accounthttp.NewArchiveHandler(h.legacyArchive()).ExportData(c)
}
func (h *AccountHandler) ImportData(c *gin.Context) {
	accounthttp.NewArchiveHandler(h.legacyArchive()).ImportData(c)
}

// legacyArchive 为旧独立构造提供投影，没有模板、缓存或导入算法副本。
func (h *AccountHandler) legacyArchive() *account.Archive {
	options := account.ArchiveOptions{Now: time.Now, Info: slog.Info, Error: slog.Error, Debug: slog.Debug, DecodeIDToken: accountprovider.DecodeArchiveIDToken, Background: service.RunBackgroundTask,
		ForcePrivacy: func(ctx context.Context, value *account.Record) string {
			return h.adminService.ForceAntigravityPrivacy(ctx, service.AccountFromRecord(value))
		},
		Probe: func(value account.AccountSnapshot) {
			if h.grokImportProber != nil {
				h.importProbes.Schedule(legacyImportQuotaProbe{h.grokImportProber}, &value)
			}
		},
	}
	if h.settingService != nil {
		options.Defaults = h.settingService.GetOpenAIOAuthImportDefaults
	}
	return account.NewArchive(legacyArchiveAccounts{h.adminService}, egress.NewProxyTransfer(h.adminService, nil, time.Now), options)
}

type legacyArchiveAccounts struct{ source service.AdminService }

func (a legacyArchiveAccounts) ListAccounts(ctx context.Context, page, size int, platform, kind, status, search string, group int64, privacy, sortBy, sortOrder string) ([]account.Record, int64, error) {
	v, total, err := a.source.ListAccounts(ctx, page, size, platform, kind, status, search, group, privacy, sortBy, sortOrder)
	return service.AccountRecordsView(v), total, err
}
func (a legacyArchiveAccounts) GetAccountsByIDs(ctx context.Context, ids []int64) ([]*account.Record, error) {
	v, err := a.source.GetAccountsByIDs(ctx, ids)
	if v == nil {
		return nil, err
	}
	out := make([]*account.Record, len(v))
	for i := range v {
		out[i] = service.AccountRecordView(v[i])
	}
	return out, err
}
func (a legacyArchiveAccounts) CreateAccount(ctx context.Context, input *account.CreateAccountInput) (*account.Record, error) {
	v, err := a.source.CreateAccount(ctx, input)
	return service.AccountRecordView(v), err
}
