// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	time "time"
)

// 旧行为测试的入口仅投影和委托，不保留状态或算法。
func (s *AccountUsageService) getOpenAIUsage(ctx context.Context, value *Account, force bool) (*UsageInfo, error) {
	record := AccountRecordView(value)
	out, err := s.Core().GetOpenAIUsage(ctx, record, force)
	if value != nil && record != nil {
		ApplyAccountRecord(value, record)
	}
	return out, err
}
func shouldRefreshOpenAICodexSnapshot(value *Account, usage *UsageInfo, now time.Time) bool {
	return accountcore.ShouldRefreshOpenAICodexSnapshot(AccountRecordView(value), usage, now)
}
func (s *AccountUsageService) shouldProbeOpenAICodexSnapshot(id int64, now time.Time, force ...bool) bool {
	return s.Core().ShouldProbeOpenAICodexSnapshot(id, now, force...)
}
func (s *AccountUsageService) probeOpenAICodexSnapshot(ctx context.Context, value *Account) (map[string]any, error) {
	return s.Core().ProbeOpenAICodexSnapshot(ctx, AccountRecordView(value))
}
func (s *AccountUsageService) persistOpenAICodexProbeSnapshot(id int64, updates map[string]any) {
	s.Core().PersistOpenAICodexProbeSnapshot(&accountcore.Record{ID: id}, updates)
}
