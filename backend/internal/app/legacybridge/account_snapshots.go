// 本文件维护 legacybridge 的所属能力；兼容入口复用唯一实现。
package legacybridge

import (
	context "context"
	account "github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	repository "github.com/TokenFlux/TokenRouter/internal/repository"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type AccountRecordsReader interface {
	GetByID(context.Context, int64) (*account.Record, error)
	GetByIDs(context.Context, []int64) ([]*account.Record, error)
}

func NewAccountEvents(reader AccountRecordsReader, cache service.SchedulerCache) accountpostgres.AccountEvents {
	return repository.AccountEventBinding{
		Read: func(ctx context.Context, id int64) (*service.Account, error) {
			v, err := reader.GetByID(ctx, id)
			return service.AccountFromRecord(v), err
		},
		ReadMany: func(ctx context.Context, ids []int64) ([]*service.Account, error) {
			values, err := reader.GetByIDs(ctx, ids)
			if values == nil {
				return nil, err
			}
			out := make([]*service.Account, len(values))
			for i := range values {
				out[i] = service.AccountFromRecord(values[i])
			}
			return out, err
		},
		Cache: cache,
	}
}

// AccountUsageEvents 只将账号值读取与原事件发布绑定给资金 Adapter。
func AccountUsageEvents(reader AccountRecordsReader, cache service.SchedulerCache, exec postgresinfra.Executor) billingpostgres.AccountUsageOptions {
	return repository.AccountUsagePublisher(NewAccountEvents(reader, cache), exec)
}
