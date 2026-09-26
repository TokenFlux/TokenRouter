// 本文件提供注入式存储替身，价格与分组策略规则始终使用生产实现。
package testkit

import (
	"context"
	"log/slog"
	"time"

	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// PricingConfigRepositoryStub 由用例明确注入各个存储结果。
type PricingConfigRepositoryStub struct {
	ListAllFn                        func(ctx context.Context) ([]Configuration, error)
	GetGroupPlatformsFn              func(ctx context.Context, groupIDs []int64) (map[int64]string, error)
	CreateFn                         func(ctx context.Context, pricingConfig *Configuration) error
	GetByIDFn                        func(ctx context.Context, id int64) (*Configuration, error)
	UpdateFn                         func(ctx context.Context, pricingConfig *Configuration) error
	DeleteFn                         func(ctx context.Context, id int64) error
	ListFn                           func(ctx context.Context, params pagination.PaginationParams, status, search string) ([]Configuration, *pagination.PaginationResult, error)
	ExistsByNameFn                   func(ctx context.Context, name string) (bool, error)
	ExistsByNameExcludingFn          func(ctx context.Context, name string, excludeID int64) (bool, error)
	GetGroupIDsFn                    func(ctx context.Context, pricingConfigID int64) ([]int64, error)
	SetGroupIDsFn                    func(ctx context.Context, pricingConfigID int64, groupIDs []int64) error
	GetPricingConfigIDByGroupIDFn    func(ctx context.Context, groupID int64) (int64, error)
	GetGroupsInOtherPricingConfigsFn func(ctx context.Context, pricingConfigID int64, groupIDs []int64) ([]int64, error)
	ListModelPricingFn               func(ctx context.Context, pricingConfigID int64) ([]routing.ModelPricingEntry, error)
	CreateModelPricingFn             func(ctx context.Context, pricing *routing.ModelPricingEntry) error
	UpdateModelPricingFn             func(ctx context.Context, pricing *routing.ModelPricingEntry) error
	DeleteModelPricingFn             func(ctx context.Context, id int64) error
	ReplaceModelPricingFn            func(ctx context.Context, pricingConfigID int64, pricingList []routing.ModelPricingEntry) error
}

func (m *PricingConfigRepositoryStub) Create(ctx context.Context, pricingConfig *Configuration) error {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, pricingConfig)
	}
	return nil
}

func (m *PricingConfigRepositoryStub) GetByID(ctx context.Context, id int64) (*Configuration, error) {
	if m.GetByIDFn != nil {
		return m.GetByIDFn(ctx, id)
	}
	return nil, routing.ErrPricingConfigNotFound
}

func (m *PricingConfigRepositoryStub) Update(ctx context.Context, pricingConfig *Configuration) error {
	if m.UpdateFn != nil {
		return m.UpdateFn(ctx, pricingConfig)
	}
	return nil
}

func (m *PricingConfigRepositoryStub) Delete(ctx context.Context, id int64) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, id)
	}
	return nil
}

func (m *PricingConfigRepositoryStub) List(ctx context.Context, params pagination.PaginationParams, status, search string) ([]Configuration, *pagination.PaginationResult, error) {
	if m.ListFn != nil {
		return m.ListFn(ctx, params, status, search)
	}
	return nil, nil, nil
}

func (m *PricingConfigRepositoryStub) ListAll(ctx context.Context) ([]Configuration, error) {
	if m.ListAllFn != nil {
		return m.ListAllFn(ctx)
	}
	return nil, nil
}

func (m *PricingConfigRepositoryStub) ExistsByName(ctx context.Context, name string) (bool, error) {
	if m.ExistsByNameFn != nil {
		return m.ExistsByNameFn(ctx, name)
	}
	return false, nil
}

func (m *PricingConfigRepositoryStub) ExistsByNameExcluding(ctx context.Context, name string, excludeID int64) (bool, error) {
	if m.ExistsByNameExcludingFn != nil {
		return m.ExistsByNameExcludingFn(ctx, name, excludeID)
	}
	return false, nil
}

func (m *PricingConfigRepositoryStub) GetGroupIDs(ctx context.Context, pricingConfigID int64) ([]int64, error) {
	if m.GetGroupIDsFn != nil {
		return m.GetGroupIDsFn(ctx, pricingConfigID)
	}
	return nil, nil
}

func (m *PricingConfigRepositoryStub) SetGroupIDs(ctx context.Context, pricingConfigID int64, groupIDs []int64) error {
	if m.SetGroupIDsFn != nil {
		return m.SetGroupIDsFn(ctx, pricingConfigID, groupIDs)
	}
	return nil
}

func (m *PricingConfigRepositoryStub) GetPricingConfigIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	if m.GetPricingConfigIDByGroupIDFn != nil {
		return m.GetPricingConfigIDByGroupIDFn(ctx, groupID)
	}
	return 0, nil
}

func (m *PricingConfigRepositoryStub) GetGroupsInOtherPricingConfigs(ctx context.Context, pricingConfigID int64, groupIDs []int64) ([]int64, error) {
	if m.GetGroupsInOtherPricingConfigsFn != nil {
		return m.GetGroupsInOtherPricingConfigsFn(ctx, pricingConfigID, groupIDs)
	}
	return nil, nil
}

func (m *PricingConfigRepositoryStub) GetGroupPlatforms(ctx context.Context, groupIDs []int64) (map[int64]string, error) {
	if m.GetGroupPlatformsFn != nil {
		return m.GetGroupPlatformsFn(ctx, groupIDs)
	}
	return nil, nil
}

func (m *PricingConfigRepositoryStub) ListModelPricing(ctx context.Context, pricingConfigID int64) ([]routing.ModelPricingEntry, error) {
	if m.ListModelPricingFn != nil {
		return m.ListModelPricingFn(ctx, pricingConfigID)
	}
	return nil, nil
}

func (m *PricingConfigRepositoryStub) CreateModelPricing(ctx context.Context, pricing *routing.ModelPricingEntry) error {
	if m.CreateModelPricingFn != nil {
		return m.CreateModelPricingFn(ctx, pricing)
	}
	return nil
}

func (m *PricingConfigRepositoryStub) UpdateModelPricing(ctx context.Context, pricing *routing.ModelPricingEntry) error {
	if m.UpdateModelPricingFn != nil {
		return m.UpdateModelPricingFn(ctx, pricing)
	}
	return nil
}

func (m *PricingConfigRepositoryStub) DeleteModelPricing(ctx context.Context, id int64) error {
	if m.DeleteModelPricingFn != nil {
		return m.DeleteModelPricingFn(ctx, id)
	}
	return nil
}

func (m *PricingConfigRepositoryStub) ReplaceModelPricing(ctx context.Context, pricingConfigID int64, pricingList []routing.ModelPricingEntry) error {
	if m.ReplaceModelPricingFn != nil {
		return m.ReplaceModelPricingFn(ctx, pricingConfigID, pricingList)
	}
	return nil
}

// NewConfigServiceFixture 使用原时钟和时区规则构造真实模型配置服务。
func NewConfigServiceFixture(repo *PricingConfigRepositoryStub) *routing.PricingConfigService {
	return NewPricingConfigService(repo, nil, routing.PricingConfigOptions{Warn: slog.Warn, Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation})
}

// StandardPricingConfigRepository 提供一个配置及指定分组平台的测试记录。
func StandardPricingConfigRepository(ch Configuration, groupPlatforms map[int64]string) *PricingConfigRepositoryStub {
	return &PricingConfigRepositoryStub{
		ListAllFn: func(_ context.Context) ([]Configuration, error) {
			return []Configuration{ch}, nil
		},
		GetGroupPlatformsFn: func(_ context.Context, _ []int64) (map[int64]string, error) {
			return groupPlatforms, nil
		},
	}
}
