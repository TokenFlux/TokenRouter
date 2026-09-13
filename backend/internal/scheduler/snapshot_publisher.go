package scheduler

import "context"

// SnapshotPublicationCache 保留同事务写 outbox、提交后尽力同步快照的独立边界。
type SnapshotPublicationCache interface {
	SetAccount(context.Context, SnapshotAccount) error
	DeleteAccount(context.Context, int64) error
}

// SnapshotPublisher 不持有额外状态，只执行原批量去重与逐项发布规则。
type SnapshotPublisher struct {
	Read        func(context.Context, int64) (SnapshotAccount, error)
	ReadMany    func(context.Context, []int64) ([]SnapshotAccount, error)
	Cache       SnapshotPublicationCache
	Diagnostics Diagnostics
}

func (p SnapshotPublisher) Publish(ctx context.Context, accountID int64) {
	if p.Cache == nil || accountID <= 0 {
		return
	}
	account, err := p.Read(ctx, accountID)
	if err != nil {
		p.Diagnostics.printf("repository.account", "[Scheduler] sync account snapshot read failed: id=%d err=%v", accountID, err)
		return
	}
	if err := p.Cache.SetAccount(ctx, account); err != nil {
		p.Diagnostics.printf("repository.account", "[Scheduler] sync account snapshot write failed: id=%d err=%v", accountID, err)
	}
}

func (p SnapshotPublisher) PublishMany(ctx context.Context, accountIDs []int64) {
	if p.Cache == nil || len(accountIDs) == 0 {
		return
	}

	uniqueIDs := make([]int64, 0, len(accountIDs))
	seen := make(map[int64]struct{}, len(accountIDs))
	for _, id := range accountIDs {
		if id <= 0 {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	if len(uniqueIDs) == 0 {
		return
	}

	accounts, err := p.ReadMany(ctx, uniqueIDs)
	if err != nil {
		p.Diagnostics.printf("repository.account", "[Scheduler] batch sync account snapshot read failed: count=%d err=%v", len(uniqueIDs), err)
		return
	}

	for _, account := range accounts {
		if account == nil {
			continue
		}
		if err := p.Cache.SetAccount(ctx, account); err != nil {
			p.Diagnostics.printf("repository.account", "[Scheduler] batch sync account snapshot write failed: id=%d err=%v", account.SnapshotMetadata().ID, err)
		}
	}
}

// deleteSchedulerAccountSnapshot 在账号删除后主动清理调度器缓存中的单账号快照。
func (p SnapshotPublisher) Drop(ctx context.Context, accountID int64) {
	if p.Cache == nil || accountID <= 0 {
		return
	}
	if err := p.Cache.DeleteAccount(ctx, accountID); err != nil {
		p.Diagnostics.printf("repository.account", "[Scheduler] delete account snapshot failed: id=%d err=%v", accountID, err)
	}
}
