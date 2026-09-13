// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	account "github.com/TokenFlux/TokenRouter/internal/account"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

type testCapacityAccounts struct {
	Repository AccountRepository
	Settings   OpenAIQuotaAutoPauseSettingsReader
}

func (r testCapacityAccounts) ListSchedulableByGroupID(ctx context.Context, id int64) ([]account.CapacitySnapshot, error) {
	values, err := r.Repository.ListSchedulableByGroupID(ctx, id)
	if err != nil {
		return nil, err
	}
	return AccountCapacitySnapshots(ctx, values, r.Settings), nil
}

type testtestCapacityAccountBatchReader interface {
	ListSchedulableCapacityByGroupIDs(context.Context, []int64) ([]GroupAccountCapacityRow, error)
}
type testCapacityAccountBatch struct {
	testCapacityAccounts
	Reader testtestCapacityAccountBatchReader
}

func (r testCapacityAccountBatch) ListSchedulableCapacityByGroupIDs(ctx context.Context, ids []int64) ([]routing.CapacityAccountRow, error) {
	values, err := r.Reader.ListSchedulableCapacityByGroupIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return AccountCapacityRows(ctx, values, r.Settings), nil
}
func newTestCapacityAccounts(repo AccountRepository, settings OpenAIQuotaAutoPauseSettingsReader) routing.CapacityAccounts {
	base := testCapacityAccounts{Repository: repo, Settings: settings}
	if batch, ok := repo.(testtestCapacityAccountBatchReader); ok {
		return testCapacityAccountBatch{testCapacityAccounts: base, Reader: batch}
	}
	return base
}

type testCapacityGroups struct{ GroupRepository }

func (r testCapacityGroups) ListActiveIDs(ctx context.Context) ([]int64, error) {
	if actual, ok := r.GroupRepository.(interface {
		ListActiveIDs(context.Context) ([]int64, error)
	}); ok {
		return actual.ListActiveIDs(ctx)
	}
	values, err := r.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(values))
	for i := range values {
		ids = append(ids, values[i].ID)
	}
	return ids, nil
}
func newTestGroupCapacityService(accounts AccountRepository, groups GroupRepository, concurrency *ConcurrencyService, sessions SessionLimitCache, rpm RPMCache, settings OpenAIQuotaAutoPauseSettingsReader) *routing.CapacityService {
	var counters routing.CapacityConcurrency
	if concurrency != nil {
		counters = concurrency
	}
	return routing.NewCapacityService(newTestCapacityAccounts(accounts, settings), testCapacityGroups{groups}, counters, sessions, rpm)
}
