package batchimage

import (
	"context"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// ImagePrices 只提供已投影价卡的报价，不要求读取完整业务服务。
type ImagePrices interface {
	ResolveImageUnitPrice(context.Context, billing.PricingInput, string) (float64, error)
}

// Pricing 保留任务报价的按需分组读取顺序，价格算法仍由 billing 唯一实现。
type Pricing struct {
	Resolver  ImagePrices
	GroupRepo GroupReader
}

func (r *Pricing) BatchImageUnitPrice(ctx context.Context, input BatchImagePriceInput) (float64, error) {
	if r == nil || r.Resolver == nil {
		return 0, ErrBatchImageSettlementPricingMissing
	}
	group := input.Group
	if group == nil && input.GroupID != nil && r.GroupRepo != nil {
		var err error
		group, err = r.GroupRepo.GetByIDLite(ctx, *input.GroupID)
		if err != nil {
			return 0, err
		}
	}
	var priceGroup *billing.PriceGroup
	if group != nil {
		priceGroup = &group.Price
	}
	price, err := r.Resolver.ResolveImageUnitPrice(ctx, billing.PricingInput{Model: input.Model, GroupID: input.GroupID, Group: priceGroup}, input.ImageSize)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrBatchImageSettlementPricingMissing, err)
	}
	return price, nil
}
