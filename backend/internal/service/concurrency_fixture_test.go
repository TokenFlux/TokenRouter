//go:build unit

// 未迁任务测试使用的端口替身，不包含并发实现。
package service

import (
	"context"
	"sync/atomic"
)

type stubConcurrencyCacheForTest struct {
	acquireResult        bool
	acquireErr           error
	releaseErr           error
	concurrency          int
	concurrencyErr       error
	waitAllowed          bool
	waitErr              error
	waitCount            int
	waitCountErr         error
	loadBatch            map[int64]*AccountLoadInfo
	loadBatchErr         error
	usersLoadBatch       map[int64]*UserLoadInfo
	usersLoadErr         error
	cleanupErr           error
	apiKeyTrackErr       error
	apiKeyReleaseErr     error
	apiKeyConcurrency    map[int64]int
	apiKeyConcurrencyErr error

	// 记录调用
	releasedAccountIDs       []int64
	releasedRequestIDs       []string
	loadBatchCalls           atomic.Int64
	trackedAPIKeyIDs         []int64
	trackedAPIKeyRequestIDs  []string
	releasedAPIKeyIDs        []int64
	releasedAPIKeyRequestIDs []string
}

func (c *stubConcurrencyCacheForTest) AcquireAccountSlot(_ context.Context, _ int64, _ int, _ string) (bool, error) {
	return c.acquireResult, c.acquireErr
}
func (c *stubConcurrencyCacheForTest) ReleaseAccountSlot(_ context.Context, accountID int64, requestID string) error {
	c.releasedAccountIDs = append(c.releasedAccountIDs, accountID)
	c.releasedRequestIDs = append(c.releasedRequestIDs, requestID)
	return c.releaseErr
}
func (c *stubConcurrencyCacheForTest) GetAccountConcurrency(_ context.Context, _ int64) (int, error) {
	return c.concurrency, c.concurrencyErr
}
func (c *stubConcurrencyCacheForTest) GetAccountConcurrencyBatch(_ context.Context, accountIDs []int64) (map[int64]int, error) {
	result := make(map[int64]int, len(accountIDs))
	for _, accountID := range accountIDs {
		if c.concurrencyErr != nil {
			return nil, c.concurrencyErr
		}
		result[accountID] = c.concurrency
	}
	return result, nil
}
func (c *stubConcurrencyCacheForTest) IncrementAccountWaitCount(_ context.Context, _ int64, _ int) (bool, error) {
	return c.waitAllowed, c.waitErr
}
func (c *stubConcurrencyCacheForTest) DecrementAccountWaitCount(_ context.Context, _ int64) error {
	return nil
}
func (c *stubConcurrencyCacheForTest) GetAccountWaitingCount(_ context.Context, _ int64) (int, error) {
	return c.waitCount, c.waitCountErr
}
func (c *stubConcurrencyCacheForTest) AcquireUserSlot(_ context.Context, _ int64, _ int, _ string) (bool, error) {
	return c.acquireResult, c.acquireErr
}
func (c *stubConcurrencyCacheForTest) ReleaseUserSlot(_ context.Context, _ int64, _ string) error {
	return c.releaseErr
}
func (c *stubConcurrencyCacheForTest) GetUserConcurrency(_ context.Context, _ int64) (int, error) {
	return c.concurrency, c.concurrencyErr
}
func (c *stubConcurrencyCacheForTest) TrackAPIKeySlot(_ context.Context, apiKeyID int64, requestID string) error {
	c.trackedAPIKeyIDs = append(c.trackedAPIKeyIDs, apiKeyID)
	c.trackedAPIKeyRequestIDs = append(c.trackedAPIKeyRequestIDs, requestID)
	return c.apiKeyTrackErr
}
func (c *stubConcurrencyCacheForTest) ReleaseAPIKeySlot(_ context.Context, apiKeyID int64, requestID string) error {
	c.releasedAPIKeyIDs = append(c.releasedAPIKeyIDs, apiKeyID)
	c.releasedAPIKeyRequestIDs = append(c.releasedAPIKeyRequestIDs, requestID)
	return c.apiKeyReleaseErr
}
func (c *stubConcurrencyCacheForTest) GetAPIKeyConcurrencyBatch(_ context.Context, apiKeyIDs []int64) (map[int64]int, error) {
	if c.apiKeyConcurrencyErr != nil {
		return nil, c.apiKeyConcurrencyErr
	}
	result := make(map[int64]int, len(apiKeyIDs))
	for _, apiKeyID := range apiKeyIDs {
		result[apiKeyID] = c.apiKeyConcurrency[apiKeyID]
	}
	return result, nil
}
func (c *stubConcurrencyCacheForTest) IncrementWaitCount(_ context.Context, _ int64, _ int) (bool, error) {
	return c.waitAllowed, c.waitErr
}
func (c *stubConcurrencyCacheForTest) DecrementWaitCount(_ context.Context, _ int64) error {
	return nil
}
func (c *stubConcurrencyCacheForTest) GetAccountsLoadBatch(_ context.Context, _ []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	c.loadBatchCalls.Add(1)
	return c.loadBatch, c.loadBatchErr
}
func (c *stubConcurrencyCacheForTest) GetUsersLoadBatch(_ context.Context, _ []UserWithConcurrency) (map[int64]*UserLoadInfo, error) {
	return c.usersLoadBatch, c.usersLoadErr
}
func (c *stubConcurrencyCacheForTest) CleanupExpiredAccountSlots(_ context.Context, _ int64) error {
	return c.cleanupErr
}
func (c *stubConcurrencyCacheForTest) CleanupExpiredAccountSlotKeys(_ context.Context) error {
	return c.cleanupErr
}
func (c *stubConcurrencyCacheForTest) CleanupStaleProcessSlots(_ context.Context, _ string) error {
	return c.cleanupErr
}
