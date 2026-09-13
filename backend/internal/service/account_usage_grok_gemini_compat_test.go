//go:build unit

// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	xai "github.com/TokenFlux/TokenRouter/internal/pkg/xai"
	slog "log/slog"
	time "time"
)

// 旧断言委托新账号用例，不在生产保留无调用包装。
func (s *AccountUsageService) getGeminiUsage(ctx context.Context, account *Account) (*UsageInfo, error) {
	return s.Core().GetGeminiUsage(ctx, AccountRecordView(account))
}
func (s *AccountUsageService) getGrokUsage(ctx context.Context, account *Account, force bool) (*UsageInfo, error) {
	return s.Core().GetGrokUsage(ctx, AccountRecordView(account), force)
}
func grokLocalUsage24h(ctx context.Context, repo UsageLogRepository, accountID int64, now time.Time) *WindowStats {
	return accountcore.GrokLocalUsage24h(ctx, legacyGrokUsageReader(repo), accountID, now, slog.Warn)
}
func grokLocalUsageForBilling(
	ctx context.Context,
	repo UsageLogRepository,
	accountID int64,
	billing *xai.BillingSummary,
	now time.Time,
) (*WindowStats, *WindowStats) {
	return accountcore.GrokLocalUsageForBilling(ctx, legacyGrokUsageReader(repo), accountID, billing, now, slog.Warn)
}
