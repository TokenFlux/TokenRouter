package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalGrokImagineVideoPriceFamily(t *testing.T) {
	t.Parallel()
	require.Equal(t, VideoPriceFamilyGrokImagineVideo, CanonicalGrokImagineVideoPriceFamily("grok-imagine-video"))
	require.Equal(t, VideoPriceFamilyGrokImagineVideo15, CanonicalGrokImagineVideoPriceFamily("grok-imagine-video-1.5"))
	require.Equal(t, VideoPriceFamilyGrokImagineVideo15, CanonicalGrokImagineVideoPriceFamily("grok-imagine-video-1.5-preview"))
	require.Equal(t, VideoPriceFamilyGrokImagineVideo15, CanonicalGrokImagineVideoPriceFamily("xai/grok-video-1.5"))
	require.Equal(t, "grok-imagine-video-2", CanonicalGrokImagineVideoPriceFamily("grok-imagine-video-2"))
	require.Equal(t, "grok-imagine-video-2", CanonicalGrokImagineVideoPriceFamily("xai/grok-imagine-video-2"))
}

func TestNormalizeAndLookupVideoModelPrices(t *testing.T) {
	t.Parallel()
	raw := map[string]map[string]float64{
		"grok-imagine-video-1.5-preview": {"480p": 0.08, "720p": 0.14},
		"grok-imagine-video":             {"480p": 0.05},
		"grok-imagine-video-2":           {"1080p": 0.4},
	}
	norm := NormalizeVideoModelPrices(raw)
	require.NotNil(t, norm)
	require.Contains(t, norm, VideoPriceFamilyGrokImagineVideo15)
	require.Contains(t, norm, VideoPriceFamilyGrokImagineVideo)
	require.Contains(t, norm, "grok-imagine-video-2")

	p15 := LookupVideoModelPrice(norm, "grok-imagine-video-1.5", "480p")
	require.NotNil(t, p15)
	require.InDelta(t, 0.08, *p15, 1e-9)

	pBase := LookupVideoModelPrice(norm, "grok-imagine-video", "480p")
	require.NotNil(t, pBase)
	require.InDelta(t, 0.05, *pBase, 1e-9)
	// 缺少模型专属档位时必须回退到统一档位价格，不能借用其他模型的分辨率价格。
	require.Nil(t, LookupVideoModelPrice(norm, "grok-imagine-video", "720p"))

	p2 := LookupVideoModelPrice(norm, "grok-imagine-video-2", "1080p")
	require.NotNil(t, p2)
	require.InDelta(t, 0.4, *p2, 1e-9)

	// 未命中模型时返回 nil，由调用方回退到平铺列或默认价格。
	require.Nil(t, LookupVideoModelPrice(norm, "unknown-model", "480p"))
}

func TestNormalizeVideoModelPricesDropsUnknownResolutions(t *testing.T) {
	t.Parallel()
	// 4k 与 1080i 不是可计费档位。若将其归入 480p，会让 480p 请求按运营方的高分辨率价格计费。
	norm := NormalizeVideoModelPrices(map[string]map[string]float64{
		"grok-imagine-video": {"480p": 0.05, "4k": 0.50, "1080i": 0.30},
	})
	require.NotNil(t, norm)
	require.Equal(t, map[string]float64{VideoBillingResolution480P: 0.05}, norm[VideoPriceFamilyGrokImagineVideo])

	// 所有档位都无法识别的模型不会生成任何价格族。
	require.Nil(t, NormalizeVideoModelPrices(map[string]map[string]float64{
		"grok-imagine-video": {"4k": 0.50},
	}))
}

func TestNormalizeVideoModelPricesIsDeterministicAcrossAliasConflicts(t *testing.T) {
	t.Parallel()
	// 两个键都会标准化为 grok-imagine-video-1.5，但其 480p 价格冲突。
	// 无论最终选择哪一价格，每次运行都必须一致，不能依赖 Go 映射遍历顺序。
	// 否则两个进程可能对同一请求采用不同价格。
	raw := map[string]map[string]float64{
		"grok-imagine-video-1.5":         {"480p": 0.08},
		"grok-imagine-video-1.5-preview": {"480p": 0.11},
		"grok-video-1.5":                 {"480p": 0.09},
	}
	first := NormalizeVideoModelPrices(raw)
	require.NotNil(t, first)
	for i := 0; i < 50; i++ {
		require.Equal(t, first, NormalizeVideoModelPrices(raw), "run %d diverged", i)
	}

	// 同一模型键内的别名也应确定性地归一化到同一档位。
	aliased := map[string]map[string]float64{
		"grok-imagine-video": {"720": 0.12, "720p": 0.13, "hd": 0.14},
	}
	firstAliased := NormalizeVideoModelPrices(aliased)
	require.NotNil(t, firstAliased)
	for i := 0; i < 50; i++ {
		require.Equal(t, firstAliased, NormalizeVideoModelPrices(aliased), "run %d diverged", i)
	}
}

func TestLookupVideoBillingResolutionReportsUnknownTiers(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"480", "480p", "SD", "720", "hd", "1080", "full-hd", " fhd "} {
		normalized, ok := LookupVideoBillingResolution(in)
		require.True(t, ok, "input=%q", in)
		require.NotEmpty(t, normalized)
	}
	for _, in := range []string{"", "4k", "1080i", "2160p", "potato"} {
		normalized, ok := LookupVideoBillingResolution(in)
		require.False(t, ok, "input=%q", in)
		require.Empty(t, normalized)
	}
	// 上游返回无法识别的值时，运行时计费仍需要一个档位。
	require.Equal(t, VideoBillingResolution480P, NormalizeVideoBillingResolutionOrDefault("4k"))
	require.Equal(t, VideoBillingResolution1080P, NormalizeVideoBillingResolutionOrDefault("full_hd"))
}

func TestVideoModelPriceMissingTierFallsBackToFlatTierPrice(t *testing.T) {
	t.Parallel()
	flat720P := 0.7
	service := &BillingService{}

	result := service.CalculateVideoCost("grok-imagine-video", "720p", 1, 1, &VideoPriceConfig{
		Price720P: &flat720P,
		ModelPrices: map[string]map[string]float64{
			VideoPriceFamilyGrokImagineVideo: {VideoBillingResolution480P: 0.05},
		},
	}, 1)

	require.InDelta(t, flat720P, result.TotalCost, 1e-9)
}
