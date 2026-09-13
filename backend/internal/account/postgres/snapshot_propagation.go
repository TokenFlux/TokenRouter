// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"
	time "time"
)

// afterChangeDetached 在请求取消后仍以短超时传播最新账号快照。
func (r *AccountStore) afterChangeDetached(ctx context.Context, accountID int64) {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	propagationCtx, cancel := context.WithTimeout(base, 2*time.Second)
	defer cancel()
	r.afterChange(propagationCtx, accountID)
}
