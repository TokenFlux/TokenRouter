// 本文件提供注入式存储替身，渠道规则和缓存始终使用生产实现。
package testkit

import (
	context "context"
	slog "log/slog"
	time "time"

	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// ChannelRepositoryStub 由用例明确注入各个存储结果。
type ChannelRepositoryStub struct {
	ListAllFn                  func(ctx context.Context) ([]routing.Channel, error)
	GetGroupPlatformsFn        func(ctx context.Context, groupIDs []int64) (map[int64]string, error)
	CreateFn                   func(ctx context.Context, channel *routing.Channel) error
	GetByIDFn                  func(ctx context.Context, id int64) (*routing.Channel, error)
	UpdateFn                   func(ctx context.Context, channel *routing.Channel) error
	DeleteFn                   func(ctx context.Context, id int64) error
	ListFn                     func(ctx context.Context, params pagination.PaginationParams, status, search string) ([]routing.Channel, *pagination.PaginationResult, error)
	ExistsByNameFn             func(ctx context.Context, name string) (bool, error)
	ExistsByNameExcludingFn    func(ctx context.Context, name string, excludeID int64) (bool, error)
	GetGroupIDsFn              func(ctx context.Context, channelID int64) ([]int64, error)
	SetGroupIDsFn              func(ctx context.Context, channelID int64, groupIDs []int64) error
	GetChannelIDByGroupIDFn    func(ctx context.Context, groupID int64) (int64, error)
	GetGroupsInOtherChannelsFn func(ctx context.Context, channelID int64, groupIDs []int64) ([]int64, error)
	ListModelPricingFn         func(ctx context.Context, channelID int64) ([]routing.ChannelModelPricing, error)
	CreateModelPricingFn       func(ctx context.Context, pricing *routing.ChannelModelPricing) error
	UpdateModelPricingFn       func(ctx context.Context, pricing *routing.ChannelModelPricing) error
	DeleteModelPricingFn       func(ctx context.Context, id int64) error
	ReplaceModelPricingFn      func(ctx context.Context, channelID int64, pricingList []routing.ChannelModelPricing) error
}

func (m *ChannelRepositoryStub) Create(ctx context.Context, channel *routing.Channel) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, channel)
	}
	return nil
}

func (m *ChannelRepositoryStub) GetByID(ctx context.Context, id int64) (*routing.Channel, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, routing.ErrChannelNotFound
}

func (m *ChannelRepositoryStub) Update(ctx context.Context, channel *routing.Channel) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, channel)
	}
	return nil
}

func (m *ChannelRepositoryStub) Delete(ctx context.Context, id int64) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, id)
	}
	return nil
}

func (m *ChannelRepositoryStub) List(ctx context.Context, params pagination.PaginationParams, status, search string) ([]routing.Channel, *pagination.PaginationResult, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, params, status, search)
	}
	return nil, nil, nil
}

func (m *ChannelRepositoryStub) ListAll(ctx context.Context) ([]routing.Channel, error) {
	if m.ListAllFn != nil {
		return m.ListAllFn(ctx)
	}
	return nil, nil
}

func (m *ChannelRepositoryStub) ExistsByName(ctx context.Context, name string) (bool, error) {
	if m.ExistsByNameFn != nil {
		return m.ExistsByNameFn(ctx, name)
	}
	return false, nil
}

func (m *ChannelRepositoryStub) ExistsByNameExcluding(ctx context.Context, name string, excludeID int64) (bool, error) {
	if m.ExistsByNameExcludingFn != nil {
		return m.ExistsByNameExcludingFn(ctx, name, excludeID)
	}
	return false, nil
}

func (m *ChannelRepositoryStub) GetGroupIDs(ctx context.Context, channelID int64) ([]int64, error) {
	if m.GetGroupIDsFn != nil {
		return m.GetGroupIDsFn(ctx, channelID)
	}
	return nil, nil
}

func (m *ChannelRepositoryStub) SetGroupIDs(ctx context.Context, channelID int64, groupIDs []int64) error {
	if m.SetGroupIDsFn != nil {
		return m.SetGroupIDsFn(ctx, channelID, groupIDs)
	}
	return nil
}

func (m *ChannelRepositoryStub) GetChannelIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	if m.GetChannelIDByGroupIDFn != nil {
		return m.GetChannelIDByGroupIDFn(ctx, groupID)
	}
	return 0, nil
}

func (m *ChannelRepositoryStub) GetGroupsInOtherChannels(ctx context.Context, channelID int64, groupIDs []int64) ([]int64, error) {
	if m.GetGroupsInOtherChannelsFn != nil {
		return m.GetGroupsInOtherChannelsFn(ctx, channelID, groupIDs)
	}
	return nil, nil
}

func (m *ChannelRepositoryStub) GetGroupPlatforms(ctx context.Context, groupIDs []int64) (map[int64]string, error) {
	if m.GetGroupPlatformsFn != nil {
		return m.GetGroupPlatformsFn(ctx, groupIDs)
	}
	return nil, nil
}

func (m *ChannelRepositoryStub) ListModelPricing(ctx context.Context, channelID int64) ([]routing.ChannelModelPricing, error) {
	if m.ListModelPricingFn != nil {
		return m.ListModelPricingFn(ctx, channelID)
	}
	return nil, nil
}

func (m *ChannelRepositoryStub) CreateModelPricing(ctx context.Context, pricing *routing.ChannelModelPricing) error {
	if m.CreateModelPricingFn != nil {
		return m.CreateModelPricingFn(ctx, pricing)
	}
	return nil
}

func (m *ChannelRepositoryStub) UpdateModelPricing(ctx context.Context, pricing *routing.ChannelModelPricing) error {
	if m.UpdateModelPricingFn != nil {
		return m.UpdateModelPricingFn(ctx, pricing)
	}
	return nil
}

func (m *ChannelRepositoryStub) DeleteModelPricing(ctx context.Context, id int64) error {
	if m.DeleteModelPricingFn != nil {
		return m.DeleteModelPricingFn(ctx, id)
	}
	return nil
}

func (m *ChannelRepositoryStub) ReplaceModelPricing(ctx context.Context, channelID int64, pricingList []routing.ChannelModelPricing) error {
	if m.ReplaceModelPricingFn != nil {
		return m.ReplaceModelPricingFn(ctx, channelID, pricingList)
	}
	return nil
}

// ChannelWithRepository 使用原时钟和时区规则构造真实渠道服务。
func ChannelWithRepository(repo *ChannelRepositoryStub) *routing.ChannelService {
	return routing.NewChannelService(repo, nil, routing.ChannelOptions{Warn: slog.Warn, Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation})
}

// StandardChannelRepository 提供一个渠道及指定分组平台的测试记录。
func StandardChannelRepository(ch routing.Channel, groupPlatforms map[int64]string) *ChannelRepositoryStub {
	return &ChannelRepositoryStub{
		ListAllFn: func(_ context.Context) ([]routing.Channel, error) {
			return []routing.Channel{ch}, nil
		},
		GetGroupPlatformsFn: func(_ context.Context, _ []int64) (map[int64]string, error) {
			return groupPlatforms, nil
		},
	}
}
