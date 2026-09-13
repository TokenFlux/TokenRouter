// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// 原 HEAD 缓存回归断言只投影并调用新查询实现。
// getAntigravityUsage 为旧内部调用保留模型投影，规则与状态均由账号核心持有。
func (s *AccountUsageService) getAntigravityUsage(ctx context.Context, account *Account) (*UsageInfo, error) {
	return s.Core().GetAntigravityUsage(ctx, AccountRecordView(account))
}
func (s *AccountUsageService) getQoderUsage(ctx context.Context, account *Account, force bool) (*UsageInfo, error) {
	return s.Core().GetQoderUsage(ctx, AccountRecordView(account), force)
}

// 旧私有测试入口只构造最小投影，生产查询传递交换前完整身份。
func (s *AccountUsageService) syncActiveToPassive(ctx context.Context, id int64, usage *UsageInfo) {
	s.Core().SyncActiveToPassive(ctx, &accountcore.Record{ID: id}, usage)
}
