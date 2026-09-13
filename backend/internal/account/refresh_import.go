package account

import (
	"context"
	"errors"
	"fmt"
)

// RefreshImported 对已落库的导入身份进行一次尽力交换。它共享生产刷新锁和停止预算，
// 保留导入对禁用账号的显式刷新及原版本字段；不能套用后台 active/过期判断。
func (api *OAuthRefreshAPI) RefreshImported(ctx context.Context, value *Record, key string, exchange func(context.Context, *Record) map[string]any) error {
	if value == nil || value.IsCredentialShadow() || exchange == nil {
		return nil
	}
	if api == nil || api.accountRepo == nil {
		return errors.New("oauth refresh account repository is not configured")
	}
	ctx, finish, err := api.beginRefresh(ctx)
	if err != nil {
		return err
	}
	defer finish()
	release, held, err := api.acquireRefreshLock(ctx, value.ID, key)
	if err != nil {
		return err
	}
	if held {
		return nil
	}
	defer release()
	current, err := api.accountRepo.GetByID(ctx, value.ID)
	if err != nil {
		return err
	}
	if current == nil || current.ID != value.ID || current.Status != value.Status || RefreshCredentialIdentity(current) != RefreshCredentialIdentity(value) {
		return ErrRefreshAccountStateChanged
	}
	attempted := snapshotRefreshRecord(current)
	if err := ctx.Err(); err != nil {
		return err
	}
	credentials := exchange(ctx, current)
	if err := ctx.Err(); err != nil {
		return err
	}
	if credentials == nil {
		return nil
	}
	writer, ok := api.accountRepo.(CredentialRefreshWriter)
	if !ok {
		return fmt.Errorf("%w: conditional credential writer is not configured", ErrRefreshCredentialPersist)
	}
	applied, err := writer.UpdateOAuthCredentialsIfUnchanged(ctx, CredentialVersion{ID: attempted.ID, Platform: attempted.Platform, Type: attempted.Type, Status: attempted.Status, ProxyID: attempted.ProxyID, Credentials: attempted.Credentials}, CloneValues(credentials))
	if err != nil {
		return err
	}
	if !applied {
		// 比较失败只复核当前状态，不再次交换或用旧结果覆盖。
		latest, readErr := api.accountRepo.GetByID(ctx, value.ID)
		if readErr != nil {
			return readErr
		}
		if latest == nil || latest.ID != value.ID {
			return ErrRefreshAccountStateChanged
		}
		return nil
	}
	return nil
}
