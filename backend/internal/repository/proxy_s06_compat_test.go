// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	context "context"
	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
)

func sortedUniqueAccountIDs(ids []int64) []int64 { return egresspostgres.SortedUniqueAccountIDs(ids) }
func enqueueProxyAccountChanges(ctx context.Context, exec sqlExecutor, ids []int64) error {
	return newProxyRepositoryWithSQL(nil, exec).EnqueueAccountChanges(ctx, exec, ids)
}
