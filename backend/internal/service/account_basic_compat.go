// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

func (s *AccountService) basicAccounts() *acctcore.BasicAccounts {
	return acctcore.NewBasicAccounts(legacyAccountAdminStore{s.accountRepo}, legacyAccountAdminGroups{s.groupRepo}, newCodexFingerprintSeed)
}
func (r legacyAccountAdminStore) List(ctx context.Context, params pagination.PaginationParams) ([]acctcore.Record, *pagination.PaginationResult, error) {
	v, page, err := r.AccountRepository.List(ctx, params)
	return AccountRecordsView(v), page, err
}
func (r legacyAccountAdminStore) ListByPlatform(ctx context.Context, platform string) ([]acctcore.Record, error) {
	v, err := r.AccountRepository.ListByPlatform(ctx, platform)
	return AccountRecordsView(v), err
}
