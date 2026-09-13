// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"
	account "github.com/TokenFlux/TokenRouter/internal/account"
)

// UpdatePrivacyModeIfUnchanged 在原 Extra/outbox 事务内比较请求身份，不能写到管理员更换后的账号。
func (r *AccountStore) UpdatePrivacyModeIfUnchanged(ctx context.Context, v account.UsageObservationVersion, mode string) (bool, error) {
	return r.UpdateUsageExtraIfUnchanged(ctx, v, map[string]any{"privacy_mode": mode})
}
