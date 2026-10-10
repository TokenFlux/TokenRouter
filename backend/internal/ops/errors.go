package ops

import (
	"context"
)

func (s *OpsService) GetErrorTrend(ctx context.Context, filter *OpsDashboardFilter, bucketSeconds int) (*OpsErrorTrendResponse, error) {
	if err := s.validateDashboardQuery(ctx, filter); err != nil {
		return nil, err
	}
	s.resolveOpsQueryModeWithIgnoredStatusCodes(ctx, filter)

	result, err := s.opsRepo.GetErrorTrend(ctx, filter, bucketSeconds)
	if err != nil && ShouldFallbackOpsPreagg(filter, err) {
		rawFilter := CloneOpsFilterWithMode(filter, OpsQueryModeRaw)
		return s.opsRepo.GetErrorTrend(ctx, rawFilter, bucketSeconds)
	}
	return result, err
}

func (s *OpsService) GetErrorDistribution(ctx context.Context, filter *OpsDashboardFilter) (*OpsErrorDistributionResponse, error) {
	if err := s.validateDashboardQuery(ctx, filter); err != nil {
		return nil, err
	}
	s.resolveOpsQueryModeWithIgnoredStatusCodes(ctx, filter)

	result, err := s.opsRepo.GetErrorDistribution(ctx, filter)
	if err != nil && ShouldFallbackOpsPreagg(filter, err) {
		rawFilter := CloneOpsFilterWithMode(filter, OpsQueryModeRaw)
		return s.opsRepo.GetErrorDistribution(ctx, rawFilter)
	}
	return result, err
}
