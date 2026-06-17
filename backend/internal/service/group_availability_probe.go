package service

import (
	"context"
	"time"
)

const (
	GroupAvailabilityProbeStatusSuccess = "success"
	GroupAvailabilityProbeStatusFailed  = "failed"
)

// GroupAvailabilityProbeDueGroup 是等待主动探测的分组快照。
type GroupAvailabilityProbeDueGroup struct {
	GroupID int64
	Name    string
	// Platform 用于选择对应平台的账号调度器。
	Platform string
	Config   GroupAvailabilityProbeConfig
}

// GroupAvailabilityProbeResult 是单次主动探测结果。
type GroupAvailabilityProbeResult struct {
	GroupID      int64
	AccountID    *int64
	ModelID      string
	Status       string
	Success      bool
	LatencyMs    int64
	ErrorMessage string
	StartedAt    time.Time
	FinishedAt   time.Time
}

// GroupAvailabilityBucket 是模型广场条形组件的时间桶聚合。
type GroupAvailabilityBucket struct {
	Date             string
	SuccessCount     int64
	TotalCount       int64
	AvailabilityRate *float64
}

// GroupAvailabilitySummary 是分组主动可用性摘要。
type GroupAvailabilitySummary struct {
	WindowDays       int
	BucketMinutes    int
	SuccessCount     int64
	TotalCount       int64
	AvailabilityRate *float64
	LastStatus       string
	LastCheckedAt    *time.Time
	Days             []GroupAvailabilityBucket
}

// GroupAvailabilityProbeRepository 定义分组主动可用性探测的数据访问接口。
type GroupAvailabilityProbeRepository interface {
	ClaimDue(ctx context.Context, now time.Time, lockUntil time.Time, lockedBy string, limit int) ([]GroupAvailabilityProbeDueGroup, error)
	SaveResultAndScheduleNext(ctx context.Context, result *GroupAvailabilityProbeResult, nextRunAt time.Time) error
	GetSummaryByGroupIDs(ctx context.Context, groupIDs []int64, days int, bucketMinutes int, timezone string, now time.Time) (map[int64]*GroupAvailabilitySummary, error)
	CleanupOldResults(ctx context.Context, before time.Time) error
}
