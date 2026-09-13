// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	context "context"
	sql "database/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type sqlExecutor = postgresinfra.Executor
type groupRepository struct{ *routingpostgres.GroupStore }

func NewGroupRepository(client *dbent.Client, db *sql.DB) service.GroupRepository {
	return newGroupRepositoryWithSQL(client, db)
}
func NewAdminGroupRepository(client *dbent.Client, db *sql.DB) service.AdminGroupRepository {
	return newGroupRepositoryWithSQL(client, db)
}
func newGroupRepositoryWithSQL(client *dbent.Client, db sqlExecutor) *groupRepository {
	return &groupRepository{routingpostgres.NewGroupStore(client, db, routingpostgres.GroupStoreOptions{
		Accounts: func(exec postgresinfra.Executor) routingpostgres.GroupLinkParticipant {
			return accountpostgres.GroupLinksInTx(exec)
		},
		Users: func(exec postgresinfra.Executor) routingpostgres.GroupAccessParticipant {
			return identitypostgres.GroupAccessDeletionInTx(exec)
		},
		Enqueue: func(ctx context.Context, exec postgresinfra.Executor, id *int64) error {
			return enqueueSchedulerOutbox(ctx, exec, service.SchedulerOutboxEventGroupChanged, nil, id, nil)
		},
	})}
}
func WrapGroupStore(store *routingpostgres.GroupStore) service.AdminGroupRepository {
	return &groupRepository{store}
}

func (r *groupRepository) Create(ctx context.Context, groupIn *service.Group) error {
	projected := service.RoutingGroupView(groupIn)
	err := r.GroupStore.Create(ctx, projected)
	service.ApplyRoutingGroup(groupIn, projected)
	return err
}
func (r *groupRepository) FindByDuplicateOperationID(ctx context.Context, operationID string) (*service.Group, error) {
	value, err := r.GroupStore.FindByDuplicateOperationID(ctx, operationID)
	return service.GroupFromRouting(value), err
}
func (r *groupRepository) CreateFromSource(ctx context.Context, groupIn *service.Group, sourceGroupID int64) error {
	projected := service.RoutingGroupView(groupIn)
	err := r.GroupStore.CreateFromSource(ctx, projected, sourceGroupID)
	service.ApplyRoutingGroup(groupIn, projected)
	return err
}
func (r *groupRepository) GetByID(ctx context.Context, id int64) (*service.Group, error) {
	value, err := r.GroupStore.GetByID(ctx, id)
	return service.GroupFromRouting(value), err
}
func (r *groupRepository) GetByIDLite(ctx context.Context, id int64) (*service.Group, error) {
	value, err := r.GroupStore.GetByIDLite(ctx, id)
	return service.GroupFromRouting(value), err
}
func (r *groupRepository) Update(ctx context.Context, groupIn *service.Group) error {
	projected := service.RoutingGroupView(groupIn)
	err := r.GroupStore.Update(ctx, projected)
	service.ApplyRoutingGroup(groupIn, projected)
	return err
}
func (r *groupRepository) List(ctx context.Context, params pagination.PaginationParams) ([]service.Group, *pagination.PaginationResult, error) {
	values, page, err := r.GroupStore.List(ctx, params)
	return legacyGroupRows(values), page, err
}
func (r *groupRepository) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]service.Group, *pagination.PaginationResult, error) {
	values, page, err := r.GroupStore.ListWithFilters(ctx, params, platform, status, search, isExclusive)
	return legacyGroupRows(values), page, err
}
func (r *groupRepository) ListActive(ctx context.Context) ([]service.Group, error) {
	values, err := r.GroupStore.ListActive(ctx)
	return legacyGroupRows(values), err
}
func (r *groupRepository) ListActiveByPlatform(ctx context.Context, platform string) ([]service.Group, error) {
	values, err := r.GroupStore.ListActiveByPlatform(ctx, platform)
	return legacyGroupRows(values), err
}
func (r *groupRepository) ListActiveByPlatformLite(ctx context.Context, platform string) ([]service.Group, error) {
	values, err := r.GroupStore.ListActiveByPlatformLite(ctx, platform)
	return legacyGroupRows(values), err
}
func legacyGroupRows(values []routing.Group) []service.Group {
	if values == nil {
		return nil
	}
	out := make([]service.Group, len(values))
	for i := range values {
		out[i] = *service.GroupFromRouting(&values[i])
	}
	return out
}
