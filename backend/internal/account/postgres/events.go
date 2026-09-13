package postgres

import (
	"context"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
)

// AccountEvent 表达存储写入产生的技术变更，实际 outbox 名称与编码由装配提供。
type AccountEvent uint8

const (
	AccountChanged AccountEvent = iota
	AccountGroupsChanged
	AccountLastUsed
	AccountBulkChanged
)

// AccountEvents 统一原 outbox 编码及提交后的缓存发布，不在账号存储复制调度缓存规则。
type AccountEvents interface {
	Name(AccountEvent) string
	Write(context.Context, postgresinfra.Executor, AccountEvent, *int64, *int64, any) error
	GroupPayload([]int64) any
	SyncOne(context.Context, int64)
	SyncMany(context.Context, []int64)
	Drop(context.Context, int64)
}

func (r *AccountStore) enqueue(ctx context.Context, exec postgresinfra.Executor, event AccountEvent, accountID, groupID *int64, payload any) error {
	if r.options.Events == nil {
		return nil
	}
	return r.options.Events.Write(ctx, exec, event, accountID, groupID, payload)
}
func (r *AccountStore) groupPayload(ids []int64) any {
	if r.options.Events == nil {
		return nil
	}
	return r.options.Events.GroupPayload(ids)
}
func (r *AccountStore) eventName(event AccountEvent) string { return r.options.Events.Name(event) }
func (r *AccountStore) afterChanges(ctx context.Context, ids []int64) {
	if r.options.Events != nil {
		r.options.Events.SyncMany(ctx, ids)
	}
}
func (r *AccountStore) dropSnapshot(ctx context.Context, id int64) {
	if r.options.Events != nil {
		r.options.Events.Drop(ctx, id)
	}
}
