// 旧使用记录仓储只转换记录形状，SQL/批处理/缓存全部由 usage/postgres 提供。
package repository

import (
	"context"
	"database/sql"
	time "time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	usagepostgres "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

type usageLogRepository struct{ *usagepostgres.Store }

func NewUsageLogRepository(client *dbent.Client, db *sql.DB, settings *service.PreAggregationSettingsService) service.UsageLogRepository {
	return &usageLogRepository{usagepostgres.NewUsageLogRepository(client, db, settings)}
}

func (r *usageLogRepository) NativeUsageStore() *usagepostgres.Store { return r.Store }
func (r *usageLogRepository) Create(ctx context.Context, row *service.UsageLog) (bool, error) {
	applyClientModel(ctx, row)
	v := service.UsageLogView(row)
	inserted, err := r.Store.Create(ctx, v)
	applyUsageLogView(row, v)
	return inserted, err
}
func (r *usageLogRepository) CreateBestEffort(ctx context.Context, row *service.UsageLog) error {
	applyClientModel(ctx, row)
	v := service.UsageLogView(row)
	err := r.Store.CreateBestEffort(ctx, v)
	applyUsageLogView(row, v)
	return err
}
func applyUsageLogView(dst *service.UsageLog, v *usage.UsageLog) {
	if dst == nil || v == nil {
		return
	}
	dst.ID = v.ID
	dst.UserID = v.UserID
	dst.BillingUserID = v.BillingUserID
	dst.TeamID = v.TeamID
	dst.APIKeyID = v.APIKeyID
	dst.AccountID = v.AccountID
	dst.RequestID = v.RequestID
	dst.Model = v.Model
	dst.RequestedModel = v.RequestedModel
	dst.UpstreamModel = v.UpstreamModel
	dst.ChannelID = v.ChannelID
	dst.ModelMappingChain = v.ModelMappingChain
	dst.BillingTier = v.BillingTier
	dst.BillingMode = v.BillingMode
	dst.ServiceTier = v.ServiceTier
	dst.ReasoningEffort = v.ReasoningEffort
	dst.RequestedReasoningEffort = v.RequestedReasoningEffort
	dst.InboundEndpoint = v.InboundEndpoint
	dst.UpstreamEndpoint = v.UpstreamEndpoint
	dst.GroupID = v.GroupID
	dst.SubscriptionID = v.SubscriptionID
	dst.InputTokens = v.InputTokens
	dst.OutputTokens = v.OutputTokens
	dst.CacheCreationTokens = v.CacheCreationTokens
	dst.CacheReadTokens = v.CacheReadTokens
	dst.CacheCreation5mTokens = v.CacheCreation5mTokens
	dst.CacheCreation1hTokens = v.CacheCreation1hTokens
	dst.ImageInputTokens = v.ImageInputTokens
	dst.ImageInputCost = v.ImageInputCost
	dst.ImageOutputTokens = v.ImageOutputTokens
	dst.ImageOutputCost = v.ImageOutputCost
	dst.InputCost = v.InputCost
	dst.OutputCost = v.OutputCost
	dst.CacheCreationCost = v.CacheCreationCost
	dst.CacheReadCost = v.CacheReadCost
	dst.TotalCost = v.TotalCost
	dst.ActualCost = v.ActualCost
	dst.SubscriptionAmountUSD = v.SubscriptionAmountUSD
	dst.BalanceAmountUSD = v.BalanceAmountUSD
	dst.BillingAllocations = v.BillingAllocations
	dst.RateMultiplier = v.RateMultiplier
	dst.LongContextBillingApplied = v.LongContextBillingApplied
	dst.AccountRateMultiplier = v.AccountRateMultiplier
	dst.AccountStatsCost = v.AccountStatsCost
	dst.BillingType = v.BillingType
	dst.RequestType = v.RequestType
	dst.Stream = v.Stream
	dst.OpenAIWSMode = v.OpenAIWSMode
	dst.NativeCompactionV2 = v.NativeCompactionV2
	dst.DurationMs = v.DurationMs
	dst.FirstTokenMs = v.FirstTokenMs
	dst.UserAgent = v.UserAgent
	dst.IPAddress = v.IPAddress
	dst.SessionID = v.SessionID
	dst.UpstreamRequestID = v.UpstreamRequestID
	dst.CacheTTLOverridden = v.CacheTTLOverridden
	dst.ImageCount = v.ImageCount
	dst.ImageSize = v.ImageSize
	dst.ImageInputSize = v.ImageInputSize
	dst.ImageOutputSize = v.ImageOutputSize
	dst.ImageSizeSource = v.ImageSizeSource
	dst.ImageSizeBreakdown = v.ImageSizeBreakdown
	dst.MediaType = v.MediaType
	dst.VideoCount = v.VideoCount
	dst.VideoResolution = v.VideoResolution
	dst.VideoDurationSeconds = v.VideoDurationSeconds
	dst.CreatedAt = v.CreatedAt
}
func (r *usageLogRepository) ListByUser(ctx context.Context, userID int64, params pagination.PaginationParams) ([]service.UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := r.Store.ListByUser(ctx, userID, params)
	return usageLogsLegacy(rows), page, err
}
func (r *usageLogRepository) ListByAPIKey(ctx context.Context, apiKeyID int64, params pagination.PaginationParams) ([]service.UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := r.Store.ListByAPIKey(ctx, apiKeyID, params)
	return usageLogsLegacy(rows), page, err
}
func (r *usageLogRepository) ListByAccount(ctx context.Context, accountID int64, params pagination.PaginationParams) ([]service.UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := r.Store.ListByAccount(ctx, accountID, params)
	return usageLogsLegacy(rows), page, err
}
func (r *usageLogRepository) ListByUserAndTimeRange(ctx context.Context, userID int64, startTime, endTime time.Time) ([]service.UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := r.Store.ListByUserAndTimeRange(ctx, userID, startTime, endTime)
	return usageLogsLegacy(rows), page, err
}
func (r *usageLogRepository) ListByAPIKeyAndTimeRange(ctx context.Context, apiKeyID int64, startTime, endTime time.Time) ([]service.UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := r.Store.ListByAPIKeyAndTimeRange(ctx, apiKeyID, startTime, endTime)
	return usageLogsLegacy(rows), page, err
}
func (r *usageLogRepository) ListByAccountAndTimeRange(ctx context.Context, accountID int64, startTime, endTime time.Time) ([]service.UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := r.Store.ListByAccountAndTimeRange(ctx, accountID, startTime, endTime)
	return usageLogsLegacy(rows), page, err
}
func (r *usageLogRepository) ListByModelAndTimeRange(ctx context.Context, modelName string, startTime, endTime time.Time) ([]service.UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := r.Store.ListByModelAndTimeRange(ctx, modelName, startTime, endTime)
	return usageLogsLegacy(rows), page, err
}
func (r *usageLogRepository) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters UsageLogFilters) ([]service.UsageLog, *pagination.PaginationResult, error) {
	rows, page, err := r.Store.ListWithFilters(ctx, params, filters)
	return usageLogsLegacy(rows), page, err
}
func usageLogsLegacy(rows []usage.UsageLog) []service.UsageLog {
	if rows == nil {
		return nil
	}
	out := make([]service.UsageLog, len(rows))
	for i := range rows {
		out[i] = *service.UsageLogFromView(&rows[i])
	}
	return out
}

type UsageRankingItem = usagepostgres.UsageRankingItem
type UsageRankingResponse = usagepostgres.UsageRankingResponse
type UserStats = usagepostgres.UserStats
type DashboardStats = usagepostgres.DashboardStats
type UserDashboardStats = usagepostgres.UserDashboardStats
type PlatformDashboardStats = usagepostgres.PlatformDashboardStats
type TrendDataPoint = usagepostgres.TrendDataPoint
type ModelStat = usagepostgres.ModelStat
type UserUsageTrendPoint = usagepostgres.UserUsageTrendPoint
type UserSpendingRankingItem = usagepostgres.UserSpendingRankingItem
type UserSpendingRankingResponse = usagepostgres.UserSpendingRankingResponse
type APIKeyUsageTrendPoint = usagepostgres.APIKeyUsageTrendPoint
type UsageLogFilters = usagepostgres.UsageLogFilters
type UsageStats = usagepostgres.UsageStats
type BatchUserUsageStats = usagepostgres.BatchUserUsageStats
type PlatformUsage = usagepostgres.PlatformUsage
type BatchAPIKeyUsageStats = usagepostgres.BatchAPIKeyUsageStats
type AccountUsageHistory = usagepostgres.AccountUsageHistory
type AccountUsageSummary = usagepostgres.AccountUsageSummary
type AccountUsageStatsResponse = usagepostgres.AccountUsageStatsResponse
type EndpointStat = usagepostgres.EndpointStat

func (r *usageLogRepository) GetByID(ctx context.Context, id int64) (*service.UsageLog, error) {
	v, e := r.Store.GetByID(ctx, id)
	return service.UsageLogFromView(v), e
}

// WrapUsageStore 只为尚未迁移的调用者投影同一 Store，不构造资源。
func WrapUsageStore(store *usagepostgres.Store) service.UsageLogRepository {
	return &usageLogRepository{store}
}
