// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
)

func (s *adminServiceImpl) ListUsers(ctx context.Context, page, pageSize int, filters UserListFilters, sortBy, sortOrder string) ([]User, int64, error) {
	v, total, e := s.identityAdministration().ListUsers(ctx, page, pageSize, filters, sortBy, sortOrder)
	return usersFromIdentity(v), total, e
}

func (s *adminServiceImpl) GetUser(ctx context.Context, id int64) (*User, error) {
	v, e := s.identityAdministration().GetUser(ctx, id)
	return UserFromIdentity(v), e
}

func (s *adminServiceImpl) GetUserIncludeDeleted(ctx context.Context, id int64) (*User, error) {
	v, e := s.identityAdministration().GetUserIncludeDeleted(ctx, id)
	return UserFromIdentity(v), e
}

func (s *adminServiceImpl) CreateUser(ctx context.Context, input *CreateUserInput) (*User, error) {
	v, e := s.identityAdministration().CreateUser(ctx, input)
	return UserFromIdentity(v), e
}

func (s *adminServiceImpl) UpdateUser(ctx context.Context, id int64, input *UpdateUserInput) (*User, error) {
	v, e := s.identityAdministration().UpdateUser(ctx, id, input)
	return UserFromIdentity(v), e
}

func (s *adminServiceImpl) DeleteUser(ctx context.Context, id int64) error {
	return s.identityAdministration().DeleteUser(ctx, id)
}

func (s *adminServiceImpl) BatchUpdateConcurrency(ctx context.Context, userIDs []int64, value int, mode string) (int, error) {
	return s.identityAdministration().BatchUpdateConcurrency(ctx, userIDs, value, mode)
}

func (s *adminServiceImpl) BatchUpdateLimits(ctx context.Context, userIDs []int64, concurrency, rpmLimit *int) (int, error) {
	return s.identityAdministration().BatchUpdateLimits(ctx, userIDs, concurrency, rpmLimit)
}

func (s *adminServiceImpl) UpdateUserBalance(ctx context.Context, userID int64, balance float64, operation string, notes string) (*User, error) {
	v, e := s.identityAdministration().UpdateUserBalance(ctx, userID, balance, operation, notes)
	return UserFromIdentity(v), e
}

func (s *adminServiceImpl) GetUserAPIKeys(ctx context.Context, userID int64, page, pageSize int, sortBy, sortOrder string) ([]APIKey, int64, error) {
	v, total, e := s.keyAdministration().GetUserAPIKeys(ctx, userID, page, pageSize, sortBy, sortOrder)
	return keyRowsFromView(v), total, e
}

func (s *adminServiceImpl) GetUserRPMStatus(ctx context.Context, userID int64) (*UserRPMStatus, error) {
	return s.identityAdministration().GetUserRPMStatus(ctx, userID)
}

func (s *adminServiceImpl) GetUserUsageStats(ctx context.Context, userID int64, period string) (any, error) {
	return s.identityAdministration().GetUserUsageStats(ctx, userID, period)
}

func (s *adminServiceImpl) GetUserBalanceHistory(ctx context.Context, userID int64, page, pageSize int, codeType string) ([]RedeemCode, int64, float64, error) {
	return s.identityAdministration().GetUserBalanceHistory(ctx, userID, page, pageSize, codeType)
}

func (s *adminServiceImpl) BindUserAuthIdentity(ctx context.Context, userID int64, input AdminBindAuthIdentityInput) (*AdminBoundAuthIdentity, error) {
	return s.identityAdministration().BindUserAuthIdentity(ctx, userID, input)
}

func (s *adminServiceImpl) ListRedeemCodes(ctx context.Context, page, pageSize int, codeType, status, search string, sortBy, sortOrder string) ([]RedeemCode, int64, error) {
	return s.redeemAdministration().ListRedeemCodes(ctx, page, pageSize, codeType, status, search, sortBy, sortOrder)
}

func (s *adminServiceImpl) GetRedeemCode(ctx context.Context, id int64) (*RedeemCode, error) {
	return s.redeemAdministration().GetRedeemCode(ctx, id)
}

func (s *adminServiceImpl) GenerateRedeemCodes(ctx context.Context, input *GenerateRedeemCodesInput) ([]RedeemCode, error) {
	return s.redeemAdministration().GenerateRedeemCodes(ctx, input)
}

func (s *adminServiceImpl) DeleteRedeemCode(ctx context.Context, id int64) error {
	return s.redeemAdministration().DeleteRedeemCode(ctx, id)
}

func (s *adminServiceImpl) BatchDeleteRedeemCodes(ctx context.Context, ids []int64) (int64, error) {
	return s.redeemAdministration().BatchDeleteRedeemCodes(ctx, ids)
}

func (s *adminServiceImpl) ExpireRedeemCode(ctx context.Context, id int64) (*RedeemCode, error) {
	return s.redeemAdministration().ExpireRedeemCode(ctx, id)
}
