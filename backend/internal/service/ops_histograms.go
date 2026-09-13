// 兼容入口只委托 Ops 的唯一实现，S15/S16 清理。
package service

import native "github.com/TokenFlux/TokenRouter/internal/ops"

func DefaultOpsLatencyBucketBoundariesMS() []int64 {
	return native.DefaultOpsLatencyBucketBoundariesMS()
}
func NormalizeOpsLatencyBucketBoundariesMS(boundaries []int64) ([]int64, error) {
	return native.NormalizeOpsLatencyBucketBoundariesMS(boundaries)
}
