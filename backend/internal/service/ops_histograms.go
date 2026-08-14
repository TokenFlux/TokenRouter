package service

import (
	"context"
	"fmt"

	infraerrors "github.com/BrandonVee/TokenRouter/internal/pkg/errors"
)

const (
	opsLatencyBucketBoundaryCount = 5
	opsLatencyBucketBoundaryMaxMS = int64(86_400_000)
)

var defaultOpsLatencyBucketBoundariesMS = []int64{100, 200, 500, 1000, 2000}

// DefaultOpsLatencyBucketBoundariesMS 返回默认边界的副本，避免调用方修改全局配置。
func DefaultOpsLatencyBucketBoundariesMS() []int64 {
	return append([]int64(nil), defaultOpsLatencyBucketBoundariesMS...)
}

// NormalizeOpsLatencyBucketBoundariesMS 校验并复制时长分桶边界；空值使用默认边界。
func NormalizeOpsLatencyBucketBoundariesMS(boundaries []int64) ([]int64, error) {
	if len(boundaries) == 0 {
		return DefaultOpsLatencyBucketBoundariesMS(), nil
	}
	if len(boundaries) != opsLatencyBucketBoundaryCount {
		return nil, fmt.Errorf("exactly %d bucket boundaries are required", opsLatencyBucketBoundaryCount)
	}

	normalized := append([]int64(nil), boundaries...)
	for i, boundary := range normalized {
		if boundary <= 0 || boundary > opsLatencyBucketBoundaryMaxMS {
			return nil, fmt.Errorf("bucket boundary must be between 1 and %d milliseconds", opsLatencyBucketBoundaryMaxMS)
		}
		if i > 0 && boundary <= normalized[i-1] {
			return nil, fmt.Errorf("bucket boundaries must be strictly increasing")
		}
	}
	return normalized, nil
}

func (s *OpsService) GetLatencyHistogram(ctx context.Context, filter *OpsDashboardFilter, bucketBoundariesMS []int64) (*OpsLatencyHistogramResponse, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return nil, err
	}
	if s.opsRepo == nil {
		return nil, infraerrors.ServiceUnavailable("OPS_REPO_UNAVAILABLE", "Ops repository not available")
	}
	if filter == nil {
		return nil, infraerrors.BadRequest("OPS_FILTER_REQUIRED", "filter is required")
	}
	if filter.StartTime.IsZero() || filter.EndTime.IsZero() {
		return nil, infraerrors.BadRequest("OPS_TIME_RANGE_REQUIRED", "start_time/end_time are required")
	}
	if filter.StartTime.After(filter.EndTime) {
		return nil, infraerrors.BadRequest("OPS_TIME_RANGE_INVALID", "start_time must be <= end_time")
	}
	boundaries, err := NormalizeOpsLatencyBucketBoundariesMS(bucketBoundariesMS)
	if err != nil {
		return nil, infraerrors.BadRequest("OPS_LATENCY_BUCKET_BOUNDARIES_INVALID", err.Error())
	}
	filter.QueryMode = s.resolveOpsQueryMode(ctx, filter.QueryMode)

	result, err := s.opsRepo.GetLatencyHistogram(ctx, filter, boundaries)
	if err != nil && shouldFallbackOpsPreagg(filter, err) {
		rawFilter := cloneOpsFilterWithMode(filter, OpsQueryModeRaw)
		result, err = s.opsRepo.GetLatencyHistogram(ctx, rawFilter, boundaries)
	}
	if result != nil {
		result.BucketBoundariesMS = append([]int64(nil), boundaries...)
	}
	return result, err
}
