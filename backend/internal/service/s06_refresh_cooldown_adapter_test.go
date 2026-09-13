package service

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"reflect"
)

// 交错夹具模拟条件操作，真实 SQL 行锁/JSONB 条件另由集成测试验证。
func (r *refreshSuccessCooldownRepo) ClearRefreshCooldownIfUnchanged(ctx context.Context, v account.RefreshCooldownVersion) (bool, error) {
	if !reflect.DeepEqual(account.ObserveRefreshCooldown(AccountRecordView(r.current)), v) {
		return false, nil
	}
	return true, r.ClearTempUnschedulable(ctx, v.ID)
}

func (r *tokenRefreshCandidateRepo) ClearRefreshCooldownIfUnchanged(_ context.Context, v account.RefreshCooldownVersion) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.accounts {
		if r.accounts[i].ID == v.ID {
			if !reflect.DeepEqual(account.ObserveRefreshCooldown(AccountRecordView(&r.accounts[i])), v) {
				return false, nil
			}
			r.clearTempCalls++
			r.accounts[i].TempUnschedulableUntil = nil
			r.accounts[i].TempUnschedulableReason = ""
			return true, nil
		}
	}
	return false, nil
}
