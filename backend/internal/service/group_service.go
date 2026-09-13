// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

type GroupRepository interface {
	Create(ctx context.Context, group *Group) error
	GetByID(ctx context.Context, id int64) (*Group, error)
	GetByIDLite(ctx context.Context, id int64) (*Group, error)
	Update(ctx context.Context, group *Group) error
	Delete(ctx context.Context, id int64) error
	DeleteCascade(ctx context.Context, id int64) ([]int64, error)

	List(ctx context.Context, params pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error)
	ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]Group, *pagination.PaginationResult, error)
	ListActive(ctx context.Context) ([]Group, error)
	ListActiveByPlatform(ctx context.Context, platform string) ([]Group, error)
	// ListActiveByPlatformLite 返回活跃分组的轻量信息，不附带账号统计。
	ListActiveByPlatformLite(ctx context.Context, platform string) ([]Group, error)

	ExistsByName(ctx context.Context, name string) (bool, error)
	GetAccountCount(ctx context.Context, groupID int64) (total int64, active int64, err error)
	DeleteAccountGroupsByGroupID(ctx context.Context, groupID int64) (int64, error)
	// GetAccountIDsByGroupIDs 获取多个分组的所有账号 ID（去重）
	GetAccountIDsByGroupIDs(ctx context.Context, groupIDs []int64) ([]int64, error)
	// BindAccountsToGroup 将多个账号绑定到指定分组
	BindAccountsToGroup(ctx context.Context, groupID int64, accountIDs []int64) error
	// UpdateSortOrders 批量更新分组排序
	UpdateSortOrders(ctx context.Context, updates []GroupSortOrderUpdate) error
}

type GroupDuplicateRepository interface {
	// FindByDuplicateOperationID 在幂等存储结果不明确时执行只读恢复查询。
	FindByDuplicateOperationID(ctx context.Context, operationID string) (*Group, error)
	// CreateFromSource 原子保存分组、源分组账号优先级和调度 outbox 事件。
	CreateFromSource(ctx context.Context, group *Group, sourceGroupID int64) error
}

// GroupSortOrderRepository 串行化新建分组的末尾排序位置分配。
type GroupSortOrderRepository interface {
	LockGroupSortOrder(ctx context.Context) error
}

// AdminGroupRepository 将管理端专用写能力显式注入管理服务，避免扩大网关侧测试替身接口。
type AdminGroupRepository interface {
	GroupRepository
	GroupDuplicateRepository
	GroupSortOrderRepository
}

type GroupSortOrderUpdate = routing.GroupSortOrderUpdate

// CreateGroupRequest 创建分组请求
type CreateGroupRequest struct {
	Name                 string  `json:"name"`
	Description          string  `json:"description"`
	RateMultiplier       float64 `json:"rate_multiplier"`
	IsExclusive          bool    `json:"is_exclusive"`
	AllowImageGeneration bool    `json:"allow_image_generation"`
}

// UpdateGroupRequest 更新分组请求
type UpdateGroupRequest struct {
	Name                 *string  `json:"name"`
	Description          *string  `json:"description"`
	RateMultiplier       *float64 `json:"rate_multiplier"`
	IsExclusive          *bool    `json:"is_exclusive"`
	Status               *string  `json:"status"`
	AllowImageGeneration *bool    `json:"allow_image_generation"`
}

var ErrGroupNotFound = routing.ErrGroupNotFound

var ErrGroupExists = routing.ErrGroupExists

type GroupService struct{ *routing.GroupService }

func NewGroupService(repo routing.GroupRepository, invalidator GroupAuthInvalidator) *GroupService {
	return &GroupService{routing.NewGroupService(repo, invalidator)}
}

type GroupAuthInvalidator = routing.GroupAuthInvalidator

func (s *GroupService) Create(ctx context.Context, req CreateGroupRequest) (*Group, error) {
	value, err := s.GroupService.Create(ctx, routing.CreateGroupRequest(req))
	return GroupFromRouting(value), err
}
func (s *GroupService) GetByID(ctx context.Context, id int64) (*Group, error) {
	value, err := s.GroupService.GetByID(ctx, id)
	return GroupFromRouting(value), err
}
func (s *GroupService) List(ctx context.Context, params pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error) {
	values, page, err := s.GroupService.List(ctx, params)
	return groupsFromRouting(values), page, err
}
func (s *GroupService) ListActive(ctx context.Context) ([]Group, error) {
	values, err := s.GroupService.ListActive(ctx)
	return groupsFromRouting(values), err
}
func (s *GroupService) Update(ctx context.Context, id int64, req UpdateGroupRequest) (*Group, error) {
	value, err := s.GroupService.Update(ctx, id, routing.UpdateGroupRequest(req))
	return GroupFromRouting(value), err
}
func (s *GroupService) Delete(ctx context.Context, id int64) error {
	return s.GroupService.Delete(ctx, id)
}
func (s *GroupService) GetStats(ctx context.Context, id int64) (map[string]any, error) {
	return s.GroupService.GetStats(ctx, id)
}
func groupsFromRouting(values []routing.Group) []Group {
	if values == nil {
		return nil
	}
	out := make([]Group, len(values))
	for i := range values {
		out[i] = *GroupFromRouting(&values[i])
	}
	return out
}
