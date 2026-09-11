//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCalculateImageCost_DefaultPricing 测试无分组配置时使用默认价格
func TestCalculateImageCost_DefaultPricing(t *testing.T) {
	svc := newBillingServiceWithPrices(nil, nil, map[string]*ModelPricing{}) // pricingService 为 nil，使用硬编码默认值

	// 2K 尺寸，默认价格 $0.134 * 1.5 = $0.201
	cost := svc.CalculateImageCost("gemini-3-pro-image", "2K", 1, 1.0)
	require.InDelta(t, 0.201, cost.TotalCost, 0.0001)
	require.InDelta(t, 0.201, cost.ActualCost, 0.0001)

	// 多张图片
	cost = svc.CalculateImageCost("gemini-3-pro-image", "2K", 3, 1.0)
	require.InDelta(t, 0.603, cost.TotalCost, 0.0001)
}

func TestCalculateImageCost_NormalizesInvalidSizeTo2K(t *testing.T) {
	svc := newBillingServiceWithPrices(nil, nil, map[string]*ModelPricing{})

	for _, imageSize := range []string{"", "auto", "not-a-size"} {
		t.Run(imageSize, func(t *testing.T) {
			cost := svc.CalculateImageCost("gemini-3-pro-image", imageSize, 2, 1.0)
			require.InDelta(t, 0.402, cost.TotalCost, 0.0001)
			require.InDelta(t, 0.402, cost.ActualCost, 0.0001)
		})
	}
}

// TestCalculateImageCost_4KDoublePrice 测试 4K 默认价格翻倍
func TestCalculateImageCost_4KDoublePrice(t *testing.T) {
	svc := newBillingServiceWithPrices(nil, nil, map[string]*ModelPricing{})

	// 4K 尺寸，默认价格翻倍 $0.134 * 2 = $0.268
	cost := svc.CalculateImageCost("gemini-3-pro-image", "4K", 1, 1.0)
	require.InDelta(t, 0.268, cost.TotalCost, 0.0001)
}

// TestCalculateImageCost_RateMultiplier 测试费率倍数
func TestCalculateImageCost_RateMultiplier(t *testing.T) {
	svc := newBillingServiceWithPrices(nil, nil, map[string]*ModelPricing{})

	// 费率倍数 1.5x
	cost := svc.CalculateImageCost("gemini-3-pro-image", "2K", 1, 1.5)
	require.InDelta(t, 0.201, cost.TotalCost, 0.0001)   // TotalCost = 0.134 * 1.5
	require.InDelta(t, 0.3015, cost.ActualCost, 0.0001) // ActualCost = 0.201 * 1.5

	// 费率倍数 2.0x
	cost = svc.CalculateImageCost("gemini-3-pro-image", "2K", 2, 2.0)
	require.InDelta(t, 0.402, cost.TotalCost, 0.0001)
	require.InDelta(t, 0.804, cost.ActualCost, 0.0001)
}

// TestCalculateImageCost_ZeroCount 测试 imageCount=0
func TestCalculateImageCost_ZeroCount(t *testing.T) {
	svc := newBillingServiceWithPrices(nil, nil, map[string]*ModelPricing{})

	cost := svc.CalculateImageCost("gemini-3-pro-image", "2K", 0, 1.0)
	require.Equal(t, 0.0, cost.TotalCost)
	require.Equal(t, 0.0, cost.ActualCost)
}

// TestCalculateImageCost_NegativeCount 测试 imageCount=-1
func TestCalculateImageCost_NegativeCount(t *testing.T) {
	svc := newBillingServiceWithPrices(nil, nil, map[string]*ModelPricing{})

	cost := svc.CalculateImageCost("gemini-3-pro-image", "2K", -1, 1.0)
	require.Equal(t, 0.0, cost.TotalCost)
	require.Equal(t, 0.0, cost.ActualCost)
}

// TestCalculateImageCost_ZeroRateMultiplier 锁定新行为：倍率 0 直接按 0 计费
// （保存时已强制 > 0；若仍有 0 泄漏到计费层，零消耗比历史的 1.0 更安全）。
func TestCalculateImageCost_ZeroRateMultiplier(t *testing.T) {
	svc := newBillingServiceWithPrices(nil, nil, map[string]*ModelPricing{})

	cost := svc.CalculateImageCost("gemini-3-pro-image", "2K", 1, 0)
	require.InDelta(t, 0.201, cost.TotalCost, 0.0001)
	require.InDelta(t, 0.0, cost.ActualCost, 1e-10)
}

// TestGetDefaultImagePrice_FallbackHardcoded 测试 PricingService 无数据时使用硬编码默认值
func TestGetDefaultImagePrice_FallbackHardcoded(t *testing.T) {
	svc := newBillingServiceWithPrices(nil, nil, map[string]*ModelPricing{}) // pricingService 为 nil

	// 1K 默认价格 $0.134，2K 默认价格 $0.201 (1.5倍)
	cost := svc.CalculateImageCost("gemini-3-pro-image", "1K", 1, 1.0)
	require.InDelta(t, 0.134, cost.TotalCost, 0.0001)

	cost = svc.CalculateImageCost("gemini-3-pro-image", "2K", 1, 1.0)
	require.InDelta(t, 0.201, cost.TotalCost, 0.0001)
}
