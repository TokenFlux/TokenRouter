// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
)

func shouldEnqueueSchedulerOutboxForExtraUpdates(updates map[string]any) bool {
	return accountpostgres.ShouldEnqueueSchedulerOutboxForExtraUpdates(updates)
}
func isSchedulerNeutralExtraKey(key string) bool {
	return accountpostgres.IsSchedulerNeutralExtraKey(key)
}
