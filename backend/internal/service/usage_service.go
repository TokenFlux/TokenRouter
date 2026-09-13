// 旧用量查询入口只委托 usage；旧记录关联在这里投影。
package service

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/usagestats"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

type UsageStats = usage.UsageSummary

var ErrUsageLogNotFound = usage.ErrUsageLogNotFound

type UsageService struct{ *usage.UsageService }

func NewUsageService(repo UsageLogRepository) *UsageService {
	return &UsageService{usage.NewUsageService(legacyUsageRepository{repo}, legacyUsageReaders(repo))}
}
func legacyUsageReaders(repo UsageLogRepository) usage.QueryReaders {
	out := usage.QueryReaders{}
	out.UsageTrendWithFiltersRepo, _ = repo.(usage.UsageTrendWithFiltersRepo)
	out.ModelStatsWithUsageFiltersRepo, _ = repo.(usage.ModelStatsWithUsageFiltersRepo)
	out.ModelStatsBySourceRepo, _ = repo.(usage.ModelStatsBySourceRepo)
	out.GroupStatsWithUsageFiltersRepo, _ = repo.(usage.GroupStatsWithUsageFiltersRepo)
	out.UserStatsReader, _ = repo.(usage.UserStatsReader)
	return out
}
func (s *UsageService) GetByID(ctx context.Context, id int64) (*UsageLog, error) {
	row, err := s.UsageService.GetByID(ctx, id)
	return UsageLogFromView(row), err
}
func (s *UsageService) ListByUser(ctx context.Context, userID int64, params pagination.PaginationParams) ([]UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := s.UsageService.ListByUser(ctx, userID, params)
	return usageLogsFromView(rows), page, err
}
func (s *UsageService) ListByAPIKey(ctx context.Context, apiKeyID int64, params pagination.PaginationParams) ([]UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := s.UsageService.ListByAPIKey(ctx, apiKeyID, params)
	return usageLogsFromView(rows), page, err
}
func (s *UsageService) ListByAccount(ctx context.Context, accountID int64, params pagination.PaginationParams) ([]UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := s.UsageService.ListByAccount(ctx, accountID, params)
	return usageLogsFromView(rows), page, err
}

func (s *UsageService) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters usagestats.UsageLogFilters) ([]UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := s.UsageService.ListWithFilters(ctx, params, filters)
	return usageLogsFromView(rows), page, err
}

func usageLogsFromView(rows []usage.UsageLog) []UsageLog {
	if rows == nil {
		return nil
	}
	out := make([]UsageLog, len(rows))
	for i := range rows {
		out[i] = *UsageLogFromView(&rows[i])
	}
	return out
}
func usageLogsView(rows []UsageLog) []usage.UsageLog {
	if rows == nil {
		return nil
	}
	out := make([]usage.UsageLog, len(rows))
	for i := range rows {
		out[i] = *UsageLogView(&rows[i])
	}
	return out
}

type legacyUsageRepository struct{ UsageLogRepository }

func (r legacyUsageRepository) Create(ctx context.Context, row *usage.UsageLog) (bool, error) {
	old := UsageLogFromView(row)
	inserted, err := r.UsageLogRepository.Create(ctx, old)
	if row != nil && old != nil {
		*row = *UsageLogView(old)
	}
	return inserted, err
}
func (r legacyUsageRepository) GetByID(ctx context.Context, id int64) (log *usage.UsageLog, err error) {
	row, err := r.UsageLogRepository.GetByID(ctx, id)
	return UsageLogView(row), err
}
func (r legacyUsageRepository) ListByUser(ctx context.Context, userID int64, params pagination.PaginationParams) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := r.UsageLogRepository.ListByUser(ctx, userID, params)
	return usageLogsView(rows), page, err
}
func (r legacyUsageRepository) ListByAPIKey(ctx context.Context, apiKeyID int64, params pagination.PaginationParams) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := r.UsageLogRepository.ListByAPIKey(ctx, apiKeyID, params)
	return usageLogsView(rows), page, err
}
func (r legacyUsageRepository) ListByAccount(ctx context.Context, accountID int64, params pagination.PaginationParams) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := r.UsageLogRepository.ListByAccount(ctx, accountID, params)
	return usageLogsView(rows), page, err
}
func (r legacyUsageRepository) ListByUserAndTimeRange(ctx context.Context, userID int64, startTime, endTime time.Time) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := r.UsageLogRepository.ListByUserAndTimeRange(ctx, userID, startTime, endTime)
	return usageLogsView(rows), page, err
}
func (r legacyUsageRepository) ListByAPIKeyAndTimeRange(ctx context.Context, apiKeyID int64, startTime, endTime time.Time) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := r.UsageLogRepository.ListByAPIKeyAndTimeRange(ctx, apiKeyID, startTime, endTime)
	return usageLogsView(rows), page, err
}
func (r legacyUsageRepository) ListByAccountAndTimeRange(ctx context.Context, accountID int64, startTime, endTime time.Time) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := r.UsageLogRepository.ListByAccountAndTimeRange(ctx, accountID, startTime, endTime)
	return usageLogsView(rows), page, err
}
func (r legacyUsageRepository) ListByModelAndTimeRange(ctx context.Context, modelName string, startTime, endTime time.Time) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := r.UsageLogRepository.ListByModelAndTimeRange(ctx, modelName, startTime, endTime)
	return usageLogsView(rows), page, err
}
func (r legacyUsageRepository) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters usagestats.UsageLogFilters) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := r.UsageLogRepository.ListWithFilters(ctx, params, filters)
	return usageLogsView(rows), page, err
}
