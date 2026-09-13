package scheduler

import "context"

// LiveConcurrencyCache 管理同时计入账号、用户和 API Key 的长会话租约。
type LiveConcurrencyCache interface {
	AcquireLiveLease(
		ctx context.Context,
		accountID int64,
		accountMax int,
		userID int64,
		userMax int,
		apiKeyID int64,
		leaseID string,
		replacingRegularSlots bool,
	) (bool, error)
	RefreshLiveLease(ctx context.Context, accountID, userID, apiKeyID int64, leaseID string) (bool, error)
	ReleaseLiveLease(ctx context.Context, accountID, userID, apiKeyID int64, leaseID string) error
}
