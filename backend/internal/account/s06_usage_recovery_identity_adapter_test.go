package account

import (
	"context"
	"reflect"
)

// 原用例测试的条件存储替身按实际行比较，不把冲突改写为成功。
func (r *usageRecoveryRaceRepo) ClearUsageErrorIfUnchanged(ctx context.Context, v UsageRecoveryVersion) (bool, error) {
	c := r.current
	if c.ID != v.ID || c.Platform != v.Platform || c.Type != v.Type || c.Status != v.Status || c.ErrorMessage != v.ErrorMessage || !reflect.DeepEqual(c.Credentials, v.Credentials) || !reflect.DeepEqual(c.ProxyID, v.ProxyID) {
		return false, nil
	}
	return true, r.ClearError(ctx, v.ID)
}
