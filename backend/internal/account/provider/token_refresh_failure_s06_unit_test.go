//go:build unit

package provider

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// 原独立计数替身未设置读取集时，视作调用版本存在；竞争场景必须明确提供当前行。
func (r *tokenRefreshAccountRepo) ApplyOAuthRefreshFailure(ctx context.Context, version account.RefreshFailureVersion, failure account.RefreshFailure) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if r.accountsByID != nil && !refreshFailureMatchesFixture(r.accountsByID[version.ID], version) {
		return false, nil
	}
	var err error
	if failure.Kind == account.RefreshFailurePermanent {
		err = r.SetError(ctx, version.ID, failure.Message)
	} else {
		err = r.SetTempUnschedulable(ctx, version.ID, failure.Until, failure.Message)
	}
	if err == nil && failure.Kind == account.RefreshFailurePermanent && r.accountsByID != nil {
		if value := r.accountsByID[version.ID]; value != nil {
			value.Status = account.StatusError
			value.Schedulable = false
			value.ErrorMessage = failure.Message
		}
	}
	return err == nil, err
}

// 旧观察替身复用相同计数，覆盖新凭据作用域发布入口，避免零调用断言因缺失端口而空通过。

func (b *tokenRefreshRuntimeBlocker) PrepareRefreshFailure(int64) func(account.RefreshFailureNotice) {
	return func(account.RefreshFailureNotice) { b.blockCalls++ }
}

func (r *tokenRefreshAccountRepo) ClearAntigravityRefreshRequest(ctx context.Context, version account.CredentialVersion) (bool, error) {
	if r.accountsByID != nil {
		value := r.accountsByID[version.ID]
		if value == nil || !refreshFailureMatchesFixture(value, account.RefreshFailureVersion{CredentialVersion: version, Schedulable: value.Schedulable}) {
			return false, nil
		}
	}
	err := r.UpdateExtra(ctx, version.ID, account.ClearedAntigravityRefreshRequest())
	return err == nil, err
}
