// 并发测试替身仅供接口合同使用，不接入生产装配。
package testkit

import (
	"context"
	"sync/atomic"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

type ConcurrencyHooks struct {
	AcquireUserSlotFn     func(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error)
	AcquireAccountSlotFn  func(ctx context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error)
	AcquireIngressLeaseFn func(ctx context.Context, apiKeyID int64, maxConnections int, leaseID string) (bool, error)
	ReleaseIngressLeaseFn func(ctx context.Context, apiKeyID int64, leaseID string) error
	ReleaseUserCalled     int32
	ReleaseAccountCalled  int32
	ReleaseIngressCalled  int32
}

func (m *ConcurrencyHooks) AcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error) {
	if m.AcquireAccountSlotFn != nil {
		return m.AcquireAccountSlotFn(ctx, accountID, maxConcurrency, requestID)
	}
	return false, nil
}

func (m *ConcurrencyHooks) ReleaseAccountSlot(ctx context.Context, accountID int64, requestID string) error {
	atomic.AddInt32(&m.ReleaseAccountCalled, 1)
	return nil
}

func (m *ConcurrencyHooks) GetAccountConcurrency(ctx context.Context, accountID int64) (int, error) {
	return 0, nil
}

func (m *ConcurrencyHooks) GetAccountConcurrencyBatch(ctx context.Context, accountIDs []int64) (map[int64]int, error) {
	result := make(map[int64]int, len(accountIDs))
	for _, accountID := range accountIDs {
		result[accountID] = 0
	}
	return result, nil
}

func (m *ConcurrencyHooks) IncrementAccountWaitCount(ctx context.Context, accountID int64, maxWait int) (bool, error) {
	return true, nil
}

func (m *ConcurrencyHooks) DecrementAccountWaitCount(ctx context.Context, accountID int64) error {
	return nil
}

func (m *ConcurrencyHooks) GetAccountWaitingCount(ctx context.Context, accountID int64) (int, error) {
	return 0, nil
}

func (m *ConcurrencyHooks) AcquireUserSlot(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error) {
	if m.AcquireUserSlotFn != nil {
		return m.AcquireUserSlotFn(ctx, userID, maxConcurrency, requestID)
	}
	return false, nil
}

func (m *ConcurrencyHooks) ReleaseUserSlot(ctx context.Context, userID int64, requestID string) error {
	atomic.AddInt32(&m.ReleaseUserCalled, 1)
	return nil
}

func (m *ConcurrencyHooks) GetUserConcurrency(ctx context.Context, userID int64) (int, error) {
	return 0, nil
}

func (m *ConcurrencyHooks) IncrementWaitCount(ctx context.Context, userID int64, maxWait int) (bool, error) {
	return true, nil
}

func (m *ConcurrencyHooks) DecrementWaitCount(ctx context.Context, userID int64) error {
	return nil
}

func (m *ConcurrencyHooks) GetAccountsLoadBatch(ctx context.Context, accounts []scheduler.AccountWithConcurrency) (map[int64]*scheduler.AccountLoadInfo, error) {
	return map[int64]*scheduler.AccountLoadInfo{}, nil
}

func (m *ConcurrencyHooks) GetUsersLoadBatch(ctx context.Context, users []scheduler.UserWithConcurrency) (map[int64]*scheduler.UserLoadInfo, error) {
	return map[int64]*scheduler.UserLoadInfo{}, nil
}

func (m *ConcurrencyHooks) CleanupExpiredAccountSlots(ctx context.Context, accountID int64) error {
	return nil
}

func (m *ConcurrencyHooks) CleanupExpiredAccountSlotKeys(ctx context.Context) error {
	return nil
}

func (m *ConcurrencyHooks) CleanupStaleProcessSlots(ctx context.Context, activeRequestPrefix string) error {
	return nil
}

func (m *ConcurrencyHooks) AcquireOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, maxConnections int, leaseID string) (bool, error) {
	if m.AcquireIngressLeaseFn != nil {
		return m.AcquireIngressLeaseFn(ctx, apiKeyID, maxConnections, leaseID)
	}
	return false, nil
}

func (m *ConcurrencyHooks) RefreshOpenAIWSIngressLease(context.Context, int64, string) (bool, error) {
	return true, nil
}

func (m *ConcurrencyHooks) ReleaseOpenAIWSIngressLease(ctx context.Context, apiKeyID int64, leaseID string) error {
	atomic.AddInt32(&m.ReleaseIngressCalled, 1)
	if m.ReleaseIngressLeaseFn != nil {
		return m.ReleaseIngressLeaseFn(ctx, apiKeyID, leaseID)
	}
	return nil
}
