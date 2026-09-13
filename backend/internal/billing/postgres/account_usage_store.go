package postgres

import (
	"context"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
)

// AccountUsageOptions 绑定原提交后事件和观察端口，不复制调度缓存。
type AccountUsageOptions struct {
	Changed func(context.Context, int64) error
	Sync    func(context.Context, int64)
	Observe func(string, ...any)
}

// AccountUsageStore 拥有账号消费写入及原提交后顺序；事务参与使用 AccountUsageInTx。
type AccountUsageStore struct {
	exec    postgresinfra.Executor
	options AccountUsageOptions
}

func NewAccountUsageStore(exec postgresinfra.Executor, options AccountUsageOptions) *AccountUsageStore {
	if options.Observe == nil {
		options.Observe = func(string, ...any) {}
	}
	return &AccountUsageStore{exec: exec, options: options}
}
func (s *AccountUsageStore) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) error {
	crossed, err := AccountUsageInTx(s.exec).Increment(ctx, id, amount)
	if err != nil {
		return err
	}
	if crossed && s.options.Changed != nil {
		if err := s.options.Changed(ctx, id); err != nil {
			s.options.Observe("[SchedulerOutbox] enqueue quota exceeded failed: account=%d err=%v", id, err)
		}
	}
	return nil
}
func (s *AccountUsageStore) ResetQuotaUsedAndClearRateLimitCooldown(ctx context.Context, id int64) error {
	if err := AccountUsageInTx(s.exec).ResetAndClearRateLimitCooldown(ctx, id); err != nil {
		return err
	}
	if s.options.Changed != nil {
		if err := s.options.Changed(ctx, id); err != nil {
			s.options.Observe("[SchedulerOutbox] enqueue quota reset failed: account=%d err=%v", id, err)
		}
	}
	if s.options.Sync != nil {
		s.options.Sync(ctx, id)
	}
	return nil
}
