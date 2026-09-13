package ops

import (
	"context"
	"errors"
)

type OpsQueryMode string

const (
	OpsQueryModeAuto OpsQueryMode = "auto"
	OpsQueryModeRaw  OpsQueryMode = "raw"
)

// ErrOpsPreaggregatedNotPopulated 表示目标窗口尚未形成完整的运维聚合覆盖。
var ErrOpsPreaggregatedNotPopulated = errors.New("ops pre-aggregated tables not populated")

func (m OpsQueryMode) IsValid() bool {
	switch m {
	case OpsQueryModeAuto, OpsQueryModeRaw:
		return true
	default:
		return false
	}
}
func ShouldFallbackOpsPreagg(filter *OpsDashboardFilter, err error) bool {
	return filter != nil &&
		filter.QueryMode == OpsQueryModeAuto &&
		err != nil
}
func CloneOpsFilterWithMode(filter *OpsDashboardFilter, mode OpsQueryMode) *OpsDashboardFilter {
	if filter == nil {
		return nil
	}
	cloned := *filter
	cloned.QueryMode = mode
	return &cloned
}
func OpsIgnoredStatusCodesEqual(a, b []int) bool {
	a = NormalizeOpsIgnoredStatusCodes(a)
	b = NormalizeOpsIgnoredStatusCodes(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *OpsService) resolveOpsQueryModeWithIgnoredStatusCodes(ctx context.Context, filter *OpsDashboardFilter) {
	if filter == nil {
		return
	}
	filter.QueryMode = s.resolveOpsQueryMode(ctx, filter.QueryMode)
	s.applyOpsIgnoredStatusCodes(ctx, filter)
}
func (s *OpsService) applyOpsIgnoredStatusCodes(ctx context.Context, filter *OpsDashboardFilter) {
	if filter == nil {
		return
	}
	if filter.IgnoredStatusCodes == nil {
		filter.IgnoredStatusCodes = s.resolveOpsIgnoredStatusCodes(ctx)
	} else {
		filter.IgnoredStatusCodes = NormalizeOpsIgnoredStatusCodes(filter.IgnoredStatusCodes)
	}
	if !OpsIgnoredStatusCodesEqual(filter.IgnoredStatusCodes, DefaultOpsIgnoredStatusCodes()) {
		// 预聚合表没有把忽略状态码作为维度；自定义状态码时强制走 raw，确保设置立即生效。
		filter.QueryMode = OpsQueryModeRaw
	}
}
