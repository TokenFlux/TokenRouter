// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	slog "log/slog"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// legacyRecoveryStore 只转换旧账号形状，不复制恢复规则。
type legacyRecoveryStore struct{ AccountRepository }

func (s legacyRecoveryStore) GetByID(ctx context.Context, id int64) (*accountcore.Record, error) {
	v, err := s.AccountRepository.GetByID(ctx, id)
	return AccountRecordView(v), err
}

// RecoveryOptions 保留装配后设置的技术缓存与调度端口。
func (s *RateLimitService) RecoveryOptions() accountcore.RecoveryOptions {
	return accountcore.RecoveryOptions{Now: time.Now, Warn: slog.Warn, ResetCounter: s.ResetOpenAI403Counter, ClearSchedulingBlock: s.notifyAccountSchedulingBlockCleared, InvalidateToken: func(ctx context.Context, v *accountcore.Record) error {
		if s.tokenCacheInvalidator == nil {
			return nil
		}
		return s.tokenCacheInvalidator.InvalidateToken(ctx, v)
	}}
}

// BindRecovery 在启动前绑定 app 持有的唯一生产用例。
func (s *RateLimitService) BindRecovery(v *accountcore.RecoveryService) { s.recovery = v }

// RecoveryCore 的独立构造兼容只投影依赖，不拥有额外状态或缓存。
func (s *RateLimitService) RecoveryCore() *accountcore.RecoveryService {
	if s == nil {
		return nil
	}
	if s.recovery != nil {
		return s.recovery
	}
	return accountcore.NewRecoveryService(legacyRecoveryStore{s.accountRepo}, s.tempUnschedCache, s.RecoveryOptions())
}
