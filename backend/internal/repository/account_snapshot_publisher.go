// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	context "context"
	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// syncSchedulerAccountSnapshot 在账号状态变更时主动同步快照到调度器缓存。
// 当账号被设置为错误、禁用、不可调度或临时不可调度时调用，
// 确保调度器和粘性会话逻辑能及时感知账号的最新状态，避免继续使用不可用账号。
//
// syncSchedulerAccountSnapshot proactively syncs account snapshot to scheduler cache
// when account status changes. Called when account is set to error, disabled,
// unschedulable, or temporarily unschedulable, ensuring scheduler and sticky session
// logic can promptly detect the latest account state and avoid using unavailable accounts.
func PublishAccountSnapshot(ctx context.Context, accountID int64, read func(context.Context, int64) (*service.Account, error), cache service.SchedulerCache) {
	if cache == nil || accountID <= 0 {
		return
	}
	account, err := read(ctx, accountID)
	if err != nil {
		logger.LegacyPrintf("repository.account", "[Scheduler] sync account snapshot read failed: id=%d err=%v", accountID, err)
		return
	}
	if err := cache.SetAccount(ctx, account); err != nil {
		logger.LegacyPrintf("repository.account", "[Scheduler] sync account snapshot write failed: id=%d err=%v", accountID, err)
	}
}

func PublishAccountSnapshots(ctx context.Context, accountIDs []int64, read func(context.Context, []int64) ([]*service.Account, error), cache service.SchedulerCache) {
	if cache == nil || len(accountIDs) == 0 {
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

	accounts, err := read(ctx, uniqueIDs)
	if err != nil {
		logger.LegacyPrintf("repository.account", "[Scheduler] batch sync account snapshot read failed: count=%d err=%v", len(uniqueIDs), err)
		return
	}

	for _, account := range accounts {
		if account == nil {
			continue
		}
		if err := cache.SetAccount(ctx, account); err != nil {
			logger.LegacyPrintf("repository.account", "[Scheduler] batch sync account snapshot write failed: id=%d err=%v", account.ID, err)
		}
	}
}

// deleteSchedulerAccountSnapshot 在账号删除后主动清理调度器缓存中的单账号快照。
func DropAccountSnapshot(ctx context.Context, accountID int64, cache service.SchedulerCache) {
	if cache == nil || accountID <= 0 {
		return
	}
	if err := cache.DeleteAccount(ctx, accountID); err != nil {
		logger.LegacyPrintf("repository.account", "[Scheduler] delete account snapshot failed: id=%d err=%v", accountID, err)
	}
}
