package service

import (
	"context"
	"errors"
	"strings"
)

type OpsQueryMode string

const (
	OpsQueryModeAuto   OpsQueryMode = "auto"
	OpsQueryModeRaw    OpsQueryMode = "raw"
	OpsQueryModePreagg OpsQueryMode = "preagg"
)

// ErrOpsPreaggregatedNotPopulated indicates that raw logs exist for a window, but the
// pre-aggregation tables are not populated yet. This is primarily used to implement
// the forced `preagg` mode UX.
var ErrOpsPreaggregatedNotPopulated = errors.New("ops pre-aggregated tables not populated")

func ParseOpsQueryMode(raw string) OpsQueryMode {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case string(OpsQueryModeRaw):
		return OpsQueryModeRaw
	case string(OpsQueryModePreagg):
		return OpsQueryModePreagg
	default:
		return OpsQueryModeAuto
	}
}

func (m OpsQueryMode) IsValid() bool {
	switch m {
	case OpsQueryModeAuto, OpsQueryModeRaw, OpsQueryModePreagg:
		return true
	default:
		return false
	}
}

func shouldFallbackOpsPreagg(filter *OpsDashboardFilter, err error) bool {
	return filter != nil &&
		filter.QueryMode == OpsQueryModeAuto &&
		errors.Is(err, ErrOpsPreaggregatedNotPopulated)
}

func cloneOpsFilterWithMode(filter *OpsDashboardFilter, mode OpsQueryMode) *OpsDashboardFilter {
	if filter == nil {
		return nil
	}
	cloned := *filter
	cloned.QueryMode = mode
	return &cloned
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
	if !opsIgnoredStatusCodesEqual(filter.IgnoredStatusCodes, DefaultOpsIgnoredStatusCodes()) {
		// 预聚合表没有把忽略状态码作为维度；自定义状态码时强制走 raw，确保设置立即生效。
		filter.QueryMode = OpsQueryModeRaw
	}
}

func (s *OpsService) resolveOpsQueryModeWithIgnoredStatusCodes(ctx context.Context, filter *OpsDashboardFilter) {
	if filter == nil {
		return
	}
	filter.QueryMode = s.resolveOpsQueryMode(ctx, filter.QueryMode)
	s.applyOpsIgnoredStatusCodes(ctx, filter)
}

func opsIgnoredStatusCodesEqual(a, b []int) bool {
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
