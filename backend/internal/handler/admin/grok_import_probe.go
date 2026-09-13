// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	context "context"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	slog "log/slog"
	time "time"
)

type grokImportProber interface {
	QueryQuota(context.Context, int64) (*service.GrokQuotaProbeResult, error)
}
type legacyImportQuotaProbe struct{ source grokImportProber }

func (p legacyImportQuotaProbe) QueryQuota(ctx context.Context, id int64) (*accountcore.GrokImportProbeResult, error) {
	v, err := p.source.QueryQuota(ctx, id)
	if v == nil {
		return nil, err
	}
	return &accountcore.GrokImportProbeResult{Model: v.Model, StatusCode: v.StatusCode, HeadersObserved: v.HeadersObserved}, err
}
func newStandaloneImportProbes() *accountcore.GrokImportProbeScheduler {
	return accountcore.NewGrokImportProbeScheduler(accountcore.GrokImportProbeOptions{Concurrency: 3, Timeout: 25 * time.Second, Info: slog.Info, Warn: slog.Warn, Debug: slog.Debug, Error: slog.Error})
}
func (h *AccountHandler) scheduleGrokImportProbe(value *service.Account) {
	if h == nil || h.grokImportProber == nil {
		return
	}
	snapshot := service.AccountSnapshotView(value)
	h.importProbes.Schedule(legacyImportQuotaProbe{h.grokImportProber}, &snapshot)
}
func (h *GrokOAuthHandler) scheduleGrokImportProbe(value *service.Account) {
	if h == nil || h.importProber == nil {
		return
	}
	snapshot := service.AccountSnapshotView(value)
	h.importProbes.Schedule(legacyImportQuotaProbe{h.importProber}, &snapshot)
}
