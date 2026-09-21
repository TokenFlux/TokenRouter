// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"
	time "time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	accessview "github.com/TokenFlux/TokenRouter/internal/routing/accessview"
)

// AccountStoreOptions 仅注入跨模块值映射、原事件写入和技术时钟。
type AccountStoreOptions struct {
	Events         AccountEvents
	OllamaIdentity func(*accountcore.Record) bool
	Group          func(*dbent.Group) *accessview.GroupConfig
	Proxy          func(*dbent.Proxy) *egress.Proxy
	Observe        func(string, ...any)
	Now            func() time.Time
	LoadLocation   func(string) (*time.Location, error)
}
type AccountStore struct {
	client  *dbent.Client
	sql     postgresinfra.Executor
	options AccountStoreOptions
}

func NewAccountStore(client *dbent.Client, executor postgresinfra.Executor, options AccountStoreOptions) *AccountStore {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.LoadLocation == nil {
		options.LoadLocation = time.LoadLocation
	}
	return &AccountStore{client: client, sql: executor, options: options}
}
func (r *AccountStore) observe(message string, args ...any) {
	if r.options.Observe != nil {
		r.options.Observe(message, args...)
	}
}
func (r *AccountStore) publish(ctx context.Context, exec postgresinfra.Executor, id int64, groups []int64) error {
	return r.enqueue(ctx, exec, AccountChanged, &id, nil, r.groupPayload(groups))
}

const postgresParameterBatchSize = 50000

func normalizeJSONMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}
func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (r *AccountStore) recordFromEntity(entity *dbent.Account) *accountcore.Record {
	value := RecordFromEntity(entity)
	if value != nil {
		value.Now = r.options.Now
		value.LoadLocation = r.options.LoadLocation
	}
	return value
}

func (r *AccountStore) afterChange(ctx context.Context, id int64) {
	if r.options.Events != nil {
		r.options.Events.SyncOne(ctx, id)
	}
}

// SetEvents 在构造图阶段完成回调绑定，不启动或查询任何资源。
func (r *AccountStore) SetEvents(events AccountEvents) { r.options.Events = events }
