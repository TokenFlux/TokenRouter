// Options 只包含观测运行时实际读取的配置；装配层负责投影。
package ops

import (
	"context"
	"errors"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

type Options struct {
	Ops              RuntimeOptions
	Database         struct{ MaxOpenConns int }
	Redis            struct{ PoolSize int }
	Log              LogOptions
	RunMode          string
	Timezone         string
	IsNotFound       func(error) bool
	Logf             func(string, ...any)
	CleanupCompleted func(string)
}
type RuntimeOptions struct {
	Enabled               bool
	Cleanup               CleanupOptions
	Aggregation           AggregationOptions
	MetricsCollectorCache MetricsCollectorCacheOptions
}
type CleanupOptions struct {
	Enabled                                                                                                                        bool
	Schedule                                                                                                                       string
	BatchSize, BatchPauseMS, ErrorLogRetentionDays, SystemLogRetentionDays, MinuteMetricsRetentionDays, HourlyMetricsRetentionDays int
}
type AggregationOptions struct{ Enabled bool }
type MetricsCollectorCacheOptions struct {
	Enabled bool
	TTL     time.Duration
}
type LogOptions struct {
	Level, StacktraceLevel string
	Caller                 bool
	Sampling               SamplingOptions
}
type SamplingOptions struct {
	Enabled             bool
	Initial, Thereafter int
}
type Settings interface {
	GetValue(context.Context, string) (string, error)
	GetMultiple(context.Context, []string) (map[string]string, error)
	Set(context.Context, string, string) error
	Delete(context.Context, string) error
}
type PreAggregationReader interface{ OpsEnabled(context.Context) bool }

var ErrSettingNotFound = settings.ErrSettingNotFound
var ErrRowNotFound = errors.New("ops row not found")

type GroupObservation struct {
	ID             int64
	Name, Platform string
}
type AccountObservation struct {
	ID                                                      int64
	Name, Platform, Status, ErrorMessage                    string
	Schedulable                                             bool
	Concurrency, LoadFactor                                 int
	Groups                                                  []*GroupObservation
	TempUnschedulableUntil, RateLimitResetAt, OverloadUntil *time.Time
}

func (a AccountObservation) EffectiveLoadFactor() int { return a.LoadFactor }

type UserObservation struct {
	ID              int64
	Email, Username string
	Concurrency     int
}
type AccountReader interface {
	ListPage(context.Context, pagination.PaginationParams, string, int64) ([]AccountObservation, *pagination.PaginationResult, error)
}
type AccountStatsReader interface {
	ListOpsAccountsForStats(context.Context, string, *int64) ([]AccountObservation, error)
}
type UserReader interface {
	ListActivePage(context.Context, pagination.PaginationParams) ([]UserObservation, *pagination.PaginationResult, error)
}
type ConcurrencyReader interface {
	GetAccountsLoadBatch(context.Context, []scheduler.AccountWithConcurrency) (map[int64]*scheduler.AccountLoadInfo, error)
	GetUsersLoadBatch(context.Context, []scheduler.UserWithConcurrency) (map[int64]*scheduler.UserLoadInfo, error)
}
type AccountWithConcurrency = scheduler.AccountWithConcurrency
type AccountLoadInfo = scheduler.AccountLoadInfo
type UserWithConcurrency = scheduler.UserWithConcurrency
type UserLoadInfo = scheduler.UserLoadInfo
type AuthHealthReader interface {
	Health(context.Context) apikey.AuthCacheInvalidationHealth
}
type KeyHealthReader interface {
	AuthCacheInvalidationSubscriberHealth() apikey.AuthCacheInvalidationSubscriberHealth
	AuthLookupMetrics() apikey.APIKeyAuthLookupMetrics
	InvalidAuthAbuseHealth() apikey.InvalidAuthAbuseHealth
}
type OpsAuthCacheInvalidationHealth = apikey.OpsAuthCacheInvalidationHealth
type LogControl interface {
	Apply(*OpsRuntimeLogConfig) error
	Changed(int64, *OpsRuntimeLogConfig, *OpsRuntimeLogConfig, string)
	Failed(int64, *OpsRuntimeLogConfig, *OpsRuntimeLogConfig, string)
}

func (s *OpsService) isNotFound(e error) bool {
	if s != nil && s.cfg != nil && s.cfg.IsNotFound != nil {
		return s.cfg.IsNotFound(e)
	}
	return errors.Is(e, ErrRowNotFound)
}
func (s *OpsService) SetAuthObservers(worker AuthHealthReader, key KeyHealthReader) {
	s.authCacheInvalidationWorker = worker
	s.apiKeyService = key
}

// report 沿用注入的进程日志后端。
func (s *OpsService) report(format string, args ...any) {
	if s != nil && s.cfg != nil && s.cfg.Logf != nil {
		s.cfg.Logf(format, args...)
	}
}
