// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerpostgres "github.com/TokenFlux/TokenRouter/internal/scheduler/postgres"

	context "context"

	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"

	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"

	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// AccountEventBinding 只把账号事件映射到既有 publisher，状态仍在传入的原调度缓存。
type AccountEventBinding struct {
	Read     func(context.Context, int64) (*service.Account, error)
	ReadMany func(context.Context, []int64) ([]*service.Account, error)
	Cache    service.SchedulerCache
}

func (AccountEventBinding) Name(event accountpostgres.AccountEvent) string {
	switch event {
	case accountpostgres.AccountChanged:
		return scheduler.SchedulerOutboxEventAccountChanged
	case accountpostgres.AccountGroupsChanged:
		return scheduler.SchedulerOutboxEventAccountGroupsChanged
	case accountpostgres.AccountLastUsed:
		return scheduler.SchedulerOutboxEventAccountLastUsed
	case accountpostgres.AccountBulkChanged:
		return scheduler.SchedulerOutboxEventAccountBulkChanged
	default:
		panic("未知账号事件")
	}
}
func (b AccountEventBinding) Write(ctx context.Context, exec postgresinfra.Executor, event accountpostgres.AccountEvent, id, group *int64, payload any) error {
	return schedulerpostgres.EnqueueSchedulerChange(ctx, exec, b.Name(event), id, group, payload)
}
func (AccountEventBinding) GroupPayload(ids []int64) any { return buildSchedulerGroupPayload(ids) }
func (b AccountEventBinding) SyncOne(ctx context.Context, id int64) {
	PublishAccountSnapshot(ctx, id, b.Read, b.Cache)
}
func (b AccountEventBinding) SyncMany(ctx context.Context, ids []int64) {
	PublishAccountSnapshots(ctx, ids, b.ReadMany, b.Cache)
}
func (b AccountEventBinding) Drop(ctx context.Context, id int64) {
	DropAccountSnapshot(ctx, id, b.Cache)
}
