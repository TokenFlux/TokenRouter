//go:build unit

package provider

import (
	"context"
	"reflect"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// 原稳定行夹具保留清理次数断言，存在当前行时同时核对条件写入输入。
func (r *tokenRefreshAccountRepo) ClearRefreshCooldownIfUnchanged(ctx context.Context, v account.RefreshCooldownVersion) (bool, error) {
	if current := r.accountsByID[v.ID]; current != nil && !reflect.DeepEqual(account.ObserveRefreshCooldown(current), v) {
		return false, nil
	}
	return true, r.ClearTempUnschedulable(ctx, v.ID)
}
