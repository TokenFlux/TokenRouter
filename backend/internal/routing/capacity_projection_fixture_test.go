// 容量测试只组合原生记录、只读查询与计数端口，规则由 routing/account 持有。
package routing_test

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

type testCapacityAccounts struct {
	Repository capacityFixtureRepository
	Settings   capacitySettingsReader
}

func (r testCapacityAccounts) ListSchedulableByGroupID(ctx context.Context, id int64) ([]account.CapacitySnapshot, error) {
	values, err := r.Repository.ListSchedulableByGroupID(ctx, id)
	if err != nil {
		return nil, err
	}
	return capacitySnapshots(ctx, values, r.Settings), nil
}

type testtestCapacityAccountBatchReader interface {
	ListSchedulableCapacityByGroupIDs(context.Context, []int64) ([]account.GroupAccountCapacityRow, error)
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
	return capacityRows(ctx, values, r.Settings), nil
}
func newTestCapacityAccounts(repo capacityFixtureRepository, settings capacitySettingsReader) routing.CapacityAccounts {
	base := testCapacityAccounts{Repository: repo, Settings: settings}
	if batch, ok := repo.(testtestCapacityAccountBatchReader); ok {
		return testCapacityAccountBatch{testCapacityAccounts: base, Reader: batch}
	}
	return base
}

type testCapacityGroups struct{ routing.GroupRepository }

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
func newTestGroupCapacityService(accounts capacityFixtureRepository, groups routing.GroupRepository, concurrency *scheduler.ConcurrencyService, sessions scheduler.SessionLimitCache, rpm scheduler.RPMCache, settings capacitySettingsReader) *routing.CapacityService {
	var counters routing.CapacityConcurrency
	if concurrency != nil {
		counters = concurrency
	}
	return routing.NewCapacityService(newTestCapacityAccounts(accounts, settings), testCapacityGroups{groups}, counters, sessions, rpm)
}

// 容量夹具只提供本组原断言实际读取的两个投影，不重建旧仓储接口。
type capacityFixtureRepository interface {
	ListSchedulableByGroupID(context.Context, int64) ([]account.Record, error)
}
type capacitySettingsReader interface {
	GetOpenAIQuotaAutoPauseSettings(context.Context) account.QuotaAutoPauseSettings
}

func capacitySettings(ctx context.Context, reader capacitySettingsReader) account.QuotaAutoPauseSettings {
	if reader != nil {
		return reader.GetOpenAIQuotaAutoPauseSettings(ctx)
	}
	return account.QuotaAutoPauseSettings{}
}
func capacitySnapshots(ctx context.Context, values []account.Record, settings capacitySettingsReader) []account.CapacitySnapshot {
	if len(values) == 0 {
		return nil
	}
	snapshot := capacitySettings(ctx, settings)
	out := make([]account.CapacitySnapshot, len(values))
	for i, a := range values {
		out[i] = account.ProjectObservedCapacity(account.GroupAccountCapacityRow{AccountID: a.ID, Platform: a.Platform, Concurrency: a.Concurrency, Extra: a.Extra, SessionWindowStart: a.SessionWindowStart, SessionWindowEnd: a.SessionWindowEnd}, snapshot, time.Now())
	}
	return out
}
func capacityRows(ctx context.Context, rows []account.GroupAccountCapacityRow, settings capacitySettingsReader) []routing.CapacityAccountRow {
	if len(rows) == 0 {
		return nil
	}
	snapshot := capacitySettings(ctx, settings)
	out := make([]routing.CapacityAccountRow, len(rows))
	for i, row := range rows {
		out[i] = routing.CapacityAccountRow{GroupID: row.GroupID, Account: account.ProjectObservedCapacity(row, snapshot, time.Now())}
	}
	return out
}
