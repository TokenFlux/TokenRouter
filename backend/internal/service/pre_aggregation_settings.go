package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/BrandonVee/TokenRouter/internal/pkg/logger"
	"golang.org/x/sync/singleflight"
)

const (
	PreAggregationMinIntervalSeconds = 30
	PreAggregationMaxIntervalSeconds = 3600
	preAggregationSettingsCacheTTL   = 15 * time.Second
)

// PreAggregationUsageSettings 定义使用记录预聚合运行参数。
type PreAggregationUsageSettings struct {
	Enabled         bool `json:"enabled"`
	IntervalSeconds int  `json:"interval_seconds"`
}

// PreAggregationOpsSettings 定义运维预聚合运行参数。
type PreAggregationOpsSettings struct {
	Enabled bool `json:"enabled"`
}

// PreAggregationSettings 是预聚合唯一的数据库运行时配置。
type PreAggregationSettings struct {
	Usage PreAggregationUsageSettings `json:"usage"`
	Ops   PreAggregationOpsSettings   `json:"ops"`
}

// PreAggregationAvailability 描述部署层是否允许启用对应任务。
type PreAggregationAvailability struct {
	UsageAvailable          bool
	UsageDisabledReason     string
	OpsAvailable            bool
	OpsDisabledReason       string
	ManualBackfillAvailable bool
	ManualBackfillMaxDays   int
}

type cachedPreAggregationSettings struct {
	settings  PreAggregationSettings
	expiresAt int64
}

type persistedPreAggregationSettings struct {
	Usage *struct {
		Enabled         *bool `json:"enabled"`
		IntervalSeconds *int  `json:"interval_seconds"`
	} `json:"usage"`
	Ops *struct {
		Enabled *bool `json:"enabled"`
	} `json:"ops"`
}

// PreAggregationSettingsService 提供带短缓存的统一配置读取和热更新通知。
type PreAggregationSettingsService struct {
	settingRepo SettingRepository
	cfg         *config.Config

	cache atomic.Pointer[cachedPreAggregationSettings]
	load  singleflight.Group

	updateMu    sync.Mutex
	listenersMu sync.RWMutex
	listeners   []func(PreAggregationSettings, PreAggregationSettings)
}

// NewPreAggregationSettingsService 创建统一预聚合配置服务。
func NewPreAggregationSettingsService(settingRepo SettingRepository, cfg *config.Config) *PreAggregationSettingsService {
	return &PreAggregationSettingsService{settingRepo: settingRepo, cfg: cfg}
}

// Availability 返回部署硬开关和手动回填限制。
func (s *PreAggregationSettingsService) Availability() PreAggregationAvailability {
	availability := PreAggregationAvailability{
		UsageAvailable:          true,
		OpsAvailable:            true,
		ManualBackfillAvailable: true,
		ManualBackfillMaxDays:   31,
	}
	if s == nil || s.cfg == nil {
		return availability
	}
	availability.UsageAvailable = s.cfg.DashboardAgg.Enabled
	if !availability.UsageAvailable {
		availability.UsageDisabledReason = "dashboard_aggregation_disabled"
	}
	availability.OpsAvailable = s.cfg.Ops.Enabled && s.cfg.Ops.Aggregation.Enabled
	if !s.cfg.Ops.Enabled {
		availability.OpsDisabledReason = "ops_disabled"
	} else if !s.cfg.Ops.Aggregation.Enabled {
		availability.OpsDisabledReason = "ops_aggregation_disabled"
	}
	availability.ManualBackfillAvailable = availability.UsageAvailable && s.cfg.DashboardAgg.BackfillEnabled
	if s.cfg.DashboardAgg.BackfillMaxDays > 0 {
		availability.ManualBackfillMaxDays = s.cfg.DashboardAgg.BackfillMaxDays
	}
	return availability
}

// Get 返回规范化后的运行时配置；设置不存在时直接使用部署层默认值。
func (s *PreAggregationSettingsService) Get(ctx context.Context) (PreAggregationSettings, error) {
	defaults := s.defaults()
	if s == nil || s.settingRepo == nil {
		return defaults, nil
	}
	if cached := s.cache.Load(); cached != nil && time.Now().UnixNano() < cached.expiresAt {
		return cached.settings, nil
	}

	value, err, _ := s.load.Do(SettingKeyPreAggregationSettings, func() (any, error) {
		raw, getErr := s.settingRepo.GetValue(ctx, SettingKeyPreAggregationSettings)
		if getErr != nil {
			if errors.Is(getErr, ErrSettingNotFound) {
				s.storeCache(defaults)
				return defaults, nil
			}
			return PreAggregationSettings{}, getErr
		}
		parsed, parseErr := parsePreAggregationSettings(raw, defaults)
		if parseErr != nil {
			return PreAggregationSettings{}, parseErr
		}
		parsed = s.normalize(parsed)
		s.storeCache(parsed)
		return parsed, nil
	})
	if err != nil {
		return defaults, err
	}
	settings, ok := value.(PreAggregationSettings)
	if !ok {
		return defaults, errors.New("预聚合设置加载结果类型无效")
	}
	return settings, nil
}

// Resolve 供查询热路径使用；数据库暂时不可用时沿用缓存或部署默认值。
func (s *PreAggregationSettingsService) Resolve(ctx context.Context) PreAggregationSettings {
	settings, err := s.Get(ctx)
	if err == nil {
		return settings
	}
	if cached := s.cache.Load(); cached != nil {
		return cached.settings
	}
	logger.LegacyPrintf("service.pre_aggregation", "[PreAggregation] 读取运行时配置失败，使用部署默认值: %v", err)
	return s.defaults()
}

// Update 持久化完整配置并立即通知本实例中的聚合任务。
func (s *PreAggregationSettingsService) Update(ctx context.Context, next PreAggregationSettings) (PreAggregationSettings, error) {
	if s == nil || s.settingRepo == nil {
		return PreAggregationSettings{}, errors.New("预聚合配置服务不可用")
	}
	s.updateMu.Lock()
	defer s.updateMu.Unlock()

	previous := s.Resolve(ctx)
	next = s.normalize(next)
	payload, err := json.Marshal(next)
	if err != nil {
		return PreAggregationSettings{}, fmt.Errorf("编码预聚合配置: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyPreAggregationSettings, string(payload)); err != nil {
		return PreAggregationSettings{}, fmt.Errorf("保存预聚合配置: %w", err)
	}
	s.storeCache(next)
	s.notify(previous, next)
	return next, nil
}

// RegisterListener 注册同进程热更新回调。
func (s *PreAggregationSettingsService) RegisterListener(listener func(PreAggregationSettings, PreAggregationSettings)) {
	if s == nil || listener == nil {
		return
	}
	s.listenersMu.Lock()
	s.listeners = append(s.listeners, listener)
	s.listenersMu.Unlock()
}

// UsageEnabled 返回部署层和数据库运行时开关的合并结果。
func (s *PreAggregationSettingsService) UsageEnabled(ctx context.Context) bool {
	availability := s.Availability()
	return availability.UsageAvailable && s.Resolve(ctx).Usage.Enabled
}

// OpsEnabled 返回部署层和数据库运行时开关的合并结果。
func (s *PreAggregationSettingsService) OpsEnabled(ctx context.Context) bool {
	availability := s.Availability()
	return availability.OpsAvailable && s.Resolve(ctx).Ops.Enabled
}

func (s *PreAggregationSettingsService) defaults() PreAggregationSettings {
	settings := PreAggregationSettings{
		Usage: PreAggregationUsageSettings{Enabled: true, IntervalSeconds: 60},
		Ops:   PreAggregationOpsSettings{Enabled: true},
	}
	if s == nil || s.cfg == nil {
		return settings
	}
	settings.Usage.Enabled = s.cfg.DashboardAgg.Enabled
	settings.Ops.Enabled = s.cfg.Ops.Enabled && s.cfg.Ops.Aggregation.Enabled
	if s.cfg.DashboardAgg.IntervalSeconds > 0 {
		settings.Usage.IntervalSeconds = s.cfg.DashboardAgg.IntervalSeconds
	}
	return s.normalize(settings)
}

func (s *PreAggregationSettingsService) normalize(settings PreAggregationSettings) PreAggregationSettings {
	if settings.Usage.IntervalSeconds < PreAggregationMinIntervalSeconds {
		settings.Usage.IntervalSeconds = PreAggregationMinIntervalSeconds
	}
	if settings.Usage.IntervalSeconds > PreAggregationMaxIntervalSeconds {
		settings.Usage.IntervalSeconds = PreAggregationMaxIntervalSeconds
	}
	availability := s.Availability()
	settings.Usage.Enabled = settings.Usage.Enabled && availability.UsageAvailable
	settings.Ops.Enabled = settings.Ops.Enabled && availability.OpsAvailable
	return settings
}

func (s *PreAggregationSettingsService) storeCache(settings PreAggregationSettings) {
	if s == nil {
		return
	}
	s.cache.Store(&cachedPreAggregationSettings{
		settings:  settings,
		expiresAt: time.Now().Add(preAggregationSettingsCacheTTL).UnixNano(),
	})
}

func (s *PreAggregationSettingsService) notify(previous, next PreAggregationSettings) {
	s.listenersMu.RLock()
	listeners := append([]func(PreAggregationSettings, PreAggregationSettings){}, s.listeners...)
	s.listenersMu.RUnlock()
	for _, listener := range listeners {
		listener(previous, next)
	}
}

func parsePreAggregationSettings(raw string, defaults PreAggregationSettings) (PreAggregationSettings, error) {
	var persisted persistedPreAggregationSettings
	if err := json.Unmarshal([]byte(raw), &persisted); err != nil {
		return PreAggregationSettings{}, fmt.Errorf("解析预聚合配置: %w", err)
	}
	result := defaults
	if persisted.Usage != nil {
		if persisted.Usage.Enabled != nil {
			result.Usage.Enabled = *persisted.Usage.Enabled
		}
		if persisted.Usage.IntervalSeconds != nil {
			result.Usage.IntervalSeconds = *persisted.Usage.IntervalSeconds
		}
	}
	if persisted.Ops != nil && persisted.Ops.Enabled != nil {
		result.Ops.Enabled = *persisted.Ops.Enabled
	}
	return result, nil
}
