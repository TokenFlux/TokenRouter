//go:build unit

package batchimage_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 模拟两次读取之间管理员提交了新配置，两份配置各自的最终价格保持相同。
type changingMediaPricingGroupRepo struct {
	calls              int
	oldGroup, newGroup *routing.Group
}

func (r *changingMediaPricingGroupRepo) GetByIDLite(context.Context, int64) (*routing.Group, error) {
	r.calls++
	if r.calls == 1 {
		return r.oldGroup, nil
	}
	return r.newGroup, nil
}

func TestBatchImagePricingSnapshotUsesOneGroupVersion(t *testing.T) {
	oldGroup := &routing.Group{
		ID: 7, Platform: capability.PlatformGemini, AllowBatchImageGeneration: true, RateMultiplier: 10, BatchImageDiscountMultiplier: 0.5, BatchImageHoldMultiplier: 0.6,
		ModelPricing: []routing.ModelPricingEntry{{Models: []string{"gemini-3.1-flash-image"}, BillingMode: routing.BillingModeImage, PerRequestPrice: testPtrFloat64(1)}},
	}
	newGroup := *oldGroup
	newGroup.RateMultiplier = 1
	newGroup.ModelPricing = []routing.ModelPricingEntry{{Models: []string{"gemini-3.1-flash-image"}, BillingMode: routing.BillingModeImage, PerRequestPrice: testPtrFloat64(10)}}
	groups := &changingMediaPricingGroupRepo{oldGroup: oldGroup, newGroup: &newGroup}
	svc := newBatchPublicFixture(nil, nil, nil, groups, nil, nil, nil, &batchimage.Pricing{Resolver: publicPriceResolverFixture(), GroupRepo: batchGroupReader{groups}}, nil, nil, nil)
	snapshot, err := svc.ResolvePricingSnapshot(context.Background(), batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &oldGroup.ID, BillingMode: apikey.APIKeyBillingModeBalance}, batchimage.BatchImageSubmitRequest{Model: "gemini-3.1-flash-image", ImageSize: "1K"}, "gemini_api", nil)
	require.NoError(t, err)
	require.Equal(t, 1, groups.calls)
	require.Equal(t, 1.0, snapshot.BaseUnitPrice)
	require.Equal(t, 10.0, snapshot.GroupRateMultiplier)
	require.InDelta(t, 5.0, snapshot.BillableUnitPrice, 1e-12)
	require.InDelta(t, 6.0, snapshot.HoldUnitPrice, 1e-12)
}

func testPtrFloat64(value float64) *float64 { return &value }
