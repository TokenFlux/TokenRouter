package service

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
)

// ClearManagedRefreshError 仅投影旧入口；生产实例与显式管理恢复共用新账号用例。
func (s *adminServiceImpl) ClearManagedRefreshError(ctx context.Context, value *Account) (*Account, bool, error) {
	v, applied, err := s.accountAdministration().ClearManagedRefreshError(ctx, AccountRecordView(value))
	return AccountFromRecord(v), applied, err
}

func (r legacyAccountAdminStore) ApplyManagedRecoveryStep(ctx context.Context, step account.ManagedRecoveryStep, v account.ManagedRecoveryVersion) (bool, error) {
	writer, ok := r.AccountRepository.(account.ManagedRecoveryWriter)
	if !ok {
		return false, account.ErrManagedRecoveryUnavailable
	}
	return writer.ApplyManagedRecoveryStep(ctx, step, v)
}
