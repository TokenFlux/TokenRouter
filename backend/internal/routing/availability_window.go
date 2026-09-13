// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

const (
	DefaultMarketplaceAvailabilityWindowDays    = 7
	DefaultMarketplaceAvailabilityBucketMinutes = 120
	MinMarketplaceAvailabilityWindowDays        = 1
	MaxMarketplaceAvailabilityWindowDays        = 90
	MinMarketplaceAvailabilityBucketMinutes     = 5
	MaxMarketplaceAvailabilityBucketMinutes     = 1440
	MaxMarketplaceAvailabilityBuckets           = 720
)

func NormalizeMarketplaceAvailabilityWindow(windowDays int, bucketMinutes int) (int, int) {
	if windowDays <= 0 {
		windowDays = DefaultMarketplaceAvailabilityWindowDays
	}
	if bucketMinutes <= 0 {
		bucketMinutes = DefaultMarketplaceAvailabilityBucketMinutes
	}
	windowDays = ClampInt(windowDays, MinMarketplaceAvailabilityWindowDays, MaxMarketplaceAvailabilityWindowDays)
	bucketMinutes = ClampInt(bucketMinutes, MinMarketplaceAvailabilityBucketMinutes, MaxMarketplaceAvailabilityBucketMinutes)

	totalMinutes := windowDays * 24 * 60
	// 限制公开接口返回的桶数量，避免配置过细导致模型广场响应和渲染成本失控。
	if bucketCount := (totalMinutes + bucketMinutes - 1) / bucketMinutes; bucketCount > MaxMarketplaceAvailabilityBuckets {
		bucketMinutes = (totalMinutes + MaxMarketplaceAvailabilityBuckets - 1) / MaxMarketplaceAvailabilityBuckets
	}
	return windowDays, bucketMinutes
}

func ClampInt(value int, minValue int, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
