package account

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// 账号运行设置使用既有键，由账号模块维护解释权。
const (
	SettingKeyOpenAI403CooldownSettings                    = "openai_oauth_403_cooldown_settings"
	SettingKeyOpenAIAPIKeyHealthBreakerSettings            = "openai_apikey_health_breaker_settings"
	SettingKeyOpenAIImagesOAuthUnavailableCooldownSettings = "openai_images_oauth_unavailable_cooldown_settings"
	SettingKeyOverloadCooldownSettings                     = "overload_cooldown_settings"
	SettingKeyRateLimit429CooldownSettings                 = "rate_limit_429_cooldown_settings"
	SettingKeyStreamTimeoutSettings                        = "stream_timeout_settings"
)

// RuntimeSettingsStore 仅提供账号配置的键值存取，不暴露数据库或旧业务。
type RuntimeSettingsStore interface {
	GetValue(context.Context, string) (string, error)
	Set(context.Context, string, string) error
}

// RuntimeSettings 独占账号健康配置及其已有缓存。
type RuntimeSettings struct {
	accountSchedulingThresholdsCache atomic.Value
	accountSchedulingThresholdsSF    singleflight.Group
	settingRepo                      RuntimeSettingsStore
	notFound                         error
	openAIAPIKeyHealthBreakerCache   atomic.Value
}

// NewRuntimeSettings 构造无 I/O 的账号配置实例。
func NewRuntimeSettings(repo RuntimeSettingsStore, notFound error) *RuntimeSettings {
	return &RuntimeSettings{settingRepo: repo, notFound: notFound}
}

// OpenAIImagesOAuthUnavailableCooldownSettings 由账号健康设置拥有，保留旧 JSON 形状。
type OpenAIImagesOAuthUnavailableCooldownSettings struct {
	CooldownMinutes int `json:"cooldown_minutes"`
}

// OpenAIAPIKeyHealthBreakerSettings 由账号健康设置拥有，保留旧 JSON 形状。
type OpenAIAPIKeyHealthBreakerSettings struct {
	Enabled          bool `json:"enabled"`
	WindowMinutes    int  `json:"window_minutes"`
	FailureThreshold int  `json:"failure_threshold"`
	CooldownMinutes  int  `json:"cooldown_minutes"`
}

// DefaultStreamTimeoutSettings 沿用原内置默认值。
func DefaultStreamTimeoutSettings() *StreamTimeoutSettings {
	return &StreamTimeoutSettings{
		Enabled:                false,
		Action:                 StreamTimeoutActionTempUnsched,
		TempUnschedMinutes:     5,
		ThresholdCount:         3,
		ThresholdWindowMinutes: 10,
	}
}

// DefaultOverloadCooldownSettings 沿用原内置默认值。
func DefaultOverloadCooldownSettings() *OverloadCooldownSettings {
	return &OverloadCooldownSettings{
		Enabled:         true,
		CooldownMinutes: 10,
	}
}

// DefaultOpenAIImagesOAuthUnavailableCooldownSettings 沿用原内置默认值。
func DefaultOpenAIImagesOAuthUnavailableCooldownSettings() *OpenAIImagesOAuthUnavailableCooldownSettings {
	return &OpenAIImagesOAuthUnavailableCooldownSettings{CooldownMinutes: OpenAIImagesOAuthUnavailableDefaultCooldownMinutes}
}

// DefaultOpenAIAPIKeyHealthBreakerSettings 沿用原内置默认值。
func DefaultOpenAIAPIKeyHealthBreakerSettings() *OpenAIAPIKeyHealthBreakerSettings {
	return &OpenAIAPIKeyHealthBreakerSettings{
		Enabled:          false,
		WindowMinutes:    2,
		FailureThreshold: 10,
		CooldownMinutes:  5,
	}
}

// OpenAIImagesOAuthUnavailableDefaultCooldownMinutes 保持原分钟边界。
const OpenAIImagesOAuthUnavailableDefaultCooldownMinutes = 30

// OpenAIImagesOAuthUnavailableMaxCooldownMinutes 保持原分钟边界。
const OpenAIImagesOAuthUnavailableMaxCooldownMinutes = 120
const openAIAPIKeyHealthBreakerSettingsCacheTTL = 30 * time.Second

type cachedOpenAIAPIKeyHealthBreakerSettings struct {
	settings  OpenAIAPIKeyHealthBreakerSettings
	expiresAt time.Time
}

func normalizeOpenAIAPIKeyHealthBreakerSettings(settings *OpenAIAPIKeyHealthBreakerSettings) *OpenAIAPIKeyHealthBreakerSettings {
	if settings == nil {
		return DefaultOpenAIAPIKeyHealthBreakerSettings()
	}
	result := *settings
	if result.WindowMinutes < 1 {
		result.WindowMinutes = 1
	} else if result.WindowMinutes > 60 {
		result.WindowMinutes = 60
	}
	if result.FailureThreshold < 1 {
		result.FailureThreshold = 1
	} else if result.FailureThreshold > 10000 {
		result.FailureThreshold = 10000
	}
	if result.CooldownMinutes < 1 {
		result.CooldownMinutes = 1
	} else if result.CooldownMinutes > 60 {
		result.CooldownMinutes = 60
	}
	return &result
}

// GetOverloadCooldownSettings 保留已有独立端点的校验、默认与写入行为。
func (s *RuntimeSettings) GetOverloadCooldownSettings(ctx context.Context) (*OverloadCooldownSettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyOverloadCooldownSettings)
	if err != nil {
		if errors.Is(err, s.notFound) {
			return DefaultOverloadCooldownSettings(), nil
		}
		return nil, fmt.Errorf("get overload cooldown settings: %w", err)
	}
	if value == "" {
		return DefaultOverloadCooldownSettings(), nil
	}

	var settings OverloadCooldownSettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return DefaultOverloadCooldownSettings(), nil
	}

	// 修正配置值范围
	if settings.CooldownMinutes < 1 {
		settings.CooldownMinutes = 1
	}
	if settings.CooldownMinutes > 120 {
		settings.CooldownMinutes = 120
	}

	return &settings, nil
}

// SetOverloadCooldownSettings 保留已有独立端点的校验、默认与写入行为。
func (s *RuntimeSettings) SetOverloadCooldownSettings(ctx context.Context, settings *OverloadCooldownSettings) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}

	// 禁用时修正为合法值即可，不拒绝请求
	if settings.CooldownMinutes < 1 || settings.CooldownMinutes > 120 {
		if settings.Enabled {
			return fmt.Errorf("cooldown_minutes must be between 1-120")
		}
		settings.CooldownMinutes = 10 // 禁用状态下归一化为默认值
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal overload cooldown settings: %w", err)
	}

	return s.settingRepo.Set(ctx, SettingKeyOverloadCooldownSettings, string(data))
}

// GetRateLimit429CooldownSettings 保留已有独立端点的校验、默认与写入行为。
func (s *RuntimeSettings) GetRateLimit429CooldownSettings(ctx context.Context) (*RateLimit429CooldownSettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyRateLimit429CooldownSettings)
	if err != nil {
		if errors.Is(err, s.notFound) {
			return DefaultRateLimit429CooldownSettings(), nil
		}
		return nil, fmt.Errorf("get 429 cooldown settings: %w", err)
	}
	if value == "" {
		return DefaultRateLimit429CooldownSettings(), nil
	}

	var settings RateLimit429CooldownSettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return DefaultRateLimit429CooldownSettings(), nil
	}

	if settings.CooldownSeconds < 1 {
		settings.CooldownSeconds = 1
	}
	if settings.CooldownSeconds > 7200 {
		settings.CooldownSeconds = 7200
	}

	return &settings, nil
}

// SetRateLimit429CooldownSettings 保留已有独立端点的校验、默认与写入行为。
func (s *RuntimeSettings) SetRateLimit429CooldownSettings(ctx context.Context, settings *RateLimit429CooldownSettings) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}

	if settings.CooldownSeconds < 1 || settings.CooldownSeconds > 7200 {
		if settings.Enabled {
			return fmt.Errorf("cooldown_seconds must be between 1-7200")
		}
		settings.CooldownSeconds = 5
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal 429 cooldown settings: %w", err)
	}

	return s.settingRepo.Set(ctx, SettingKeyRateLimit429CooldownSettings, string(data))
}

// GetOpenAIImagesOAuthUnavailableCooldownSettings 保留已有独立端点的校验、默认与写入行为。
func (s *RuntimeSettings) GetOpenAIImagesOAuthUnavailableCooldownSettings(ctx context.Context) (*OpenAIImagesOAuthUnavailableCooldownSettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAIImagesOAuthUnavailableCooldownSettings)
	if err != nil {
		if errors.Is(err, s.notFound) {
			return DefaultOpenAIImagesOAuthUnavailableCooldownSettings(), nil
		}
		return nil, fmt.Errorf("get OpenAI images OAuth unavailable cooldown settings: %w", err)
	}
	if value == "" {
		return DefaultOpenAIImagesOAuthUnavailableCooldownSettings(), nil
	}

	var settings OpenAIImagesOAuthUnavailableCooldownSettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil ||
		settings.CooldownMinutes <= 0 || settings.CooldownMinutes > OpenAIImagesOAuthUnavailableMaxCooldownMinutes {
		return DefaultOpenAIImagesOAuthUnavailableCooldownSettings(), nil
	}
	return &settings, nil
}

// SetOpenAIImagesOAuthUnavailableCooldownSettings 保留已有独立端点的校验、默认与写入行为。
func (s *RuntimeSettings) SetOpenAIImagesOAuthUnavailableCooldownSettings(ctx context.Context, settings *OpenAIImagesOAuthUnavailableCooldownSettings) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}
	if settings.CooldownMinutes <= 0 || settings.CooldownMinutes > OpenAIImagesOAuthUnavailableMaxCooldownMinutes {
		return fmt.Errorf("cooldown_minutes must be between 1-%d", OpenAIImagesOAuthUnavailableMaxCooldownMinutes)
	}
	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal OpenAI images OAuth unavailable cooldown settings: %w", err)
	}
	return s.settingRepo.Set(ctx, SettingKeyOpenAIImagesOAuthUnavailableCooldownSettings, string(data))
}

// GetStreamTimeoutSettings 保留已有独立端点的校验、默认与写入行为。
func (s *RuntimeSettings) GetStreamTimeoutSettings(ctx context.Context) (*StreamTimeoutSettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyStreamTimeoutSettings)
	if err != nil {
		if errors.Is(err, s.notFound) {
			return DefaultStreamTimeoutSettings(), nil
		}
		return nil, fmt.Errorf("get stream timeout settings: %w", err)
	}
	if value == "" {
		return DefaultStreamTimeoutSettings(), nil
	}

	var settings StreamTimeoutSettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return DefaultStreamTimeoutSettings(), nil
	}

	// 验证并修正配置值
	if settings.TempUnschedMinutes < 1 {
		settings.TempUnschedMinutes = 1
	}
	if settings.TempUnschedMinutes > 60 {
		settings.TempUnschedMinutes = 60
	}
	if settings.ThresholdCount < 1 {
		settings.ThresholdCount = 1
	}
	if settings.ThresholdCount > 10 {
		settings.ThresholdCount = 10
	}
	if settings.ThresholdWindowMinutes < 1 {
		settings.ThresholdWindowMinutes = 1
	}
	if settings.ThresholdWindowMinutes > 60 {
		settings.ThresholdWindowMinutes = 60
	}

	// 验证 action
	switch settings.Action {
	case StreamTimeoutActionTempUnsched, StreamTimeoutActionError, StreamTimeoutActionNone:
		// valid
	default:
		settings.Action = StreamTimeoutActionTempUnsched
	}

	return &settings, nil
}

// SetStreamTimeoutSettings 保留已有独立端点的校验、默认与写入行为。
func (s *RuntimeSettings) SetStreamTimeoutSettings(ctx context.Context, settings *StreamTimeoutSettings) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}

	// 验证配置值
	if settings.TempUnschedMinutes < 1 || settings.TempUnschedMinutes > 60 {
		return fmt.Errorf("temp_unsched_minutes must be between 1-60")
	}
	if settings.ThresholdCount < 1 || settings.ThresholdCount > 10 {
		return fmt.Errorf("threshold_count must be between 1-10")
	}
	if settings.ThresholdWindowMinutes < 1 || settings.ThresholdWindowMinutes > 60 {
		return fmt.Errorf("threshold_window_minutes must be between 1-60")
	}

	switch settings.Action {
	case StreamTimeoutActionTempUnsched, StreamTimeoutActionError, StreamTimeoutActionNone:
		// valid
	default:
		return fmt.Errorf("invalid action: %s", settings.Action)
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal stream timeout settings: %w", err)
	}

	return s.settingRepo.Set(ctx, SettingKeyStreamTimeoutSettings, string(data))
}

// GetOpenAI403CooldownSettings 保留已有独立端点的校验、默认与写入行为。
func (s *RuntimeSettings) GetOpenAI403CooldownSettings(ctx context.Context) (*OpenAI403CooldownSettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAI403CooldownSettings)
	if err != nil {
		if errors.Is(err, s.notFound) {
			return DefaultOpenAI403CooldownSettings(), nil
		}
		return nil, fmt.Errorf("get openai 403 cooldown settings: %w", err)
	}
	if value == "" {
		return DefaultOpenAI403CooldownSettings(), nil
	}

	settings := *DefaultOpenAI403CooldownSettings()
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return DefaultOpenAI403CooldownSettings(), nil
	}

	if settings.CooldownMinutes < 1 {
		settings.CooldownMinutes = OpenAI403CooldownMinutesDefault
	}
	if settings.CooldownMinutes > 120 {
		settings.CooldownMinutes = 120
	}
	if settings.ThresholdCount < 1 {
		settings.ThresholdCount = OpenAI403DisableThresholdDefault
	}
	if settings.ThresholdCount > 20 {
		settings.ThresholdCount = 20
	}
	if settings.ThresholdWindowMinutes < 1 {
		settings.ThresholdWindowMinutes = OpenAI403CounterWindowMinutesDefault
	}
	if settings.ThresholdWindowMinutes > 1440 {
		settings.ThresholdWindowMinutes = 1440
	}

	return &settings, nil
}

// SetOpenAI403CooldownSettings 保留已有独立端点的校验、默认与写入行为。
func (s *RuntimeSettings) SetOpenAI403CooldownSettings(ctx context.Context, settings *OpenAI403CooldownSettings) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}

	// 禁用时修正为合法值即可，不拒绝请求
	if settings.CooldownMinutes < 1 || settings.CooldownMinutes > 120 {
		if settings.Enabled {
			return fmt.Errorf("cooldown_minutes must be between 1-120")
		}
		settings.CooldownMinutes = OpenAI403CooldownMinutesDefault
	}
	if settings.ThresholdCount < 1 || settings.ThresholdCount > 20 {
		if settings.Enabled && settings.ErrorOnThresholdEnabled {
			return fmt.Errorf("threshold_count must be between 1-20")
		}
		settings.ThresholdCount = OpenAI403DisableThresholdDefault
	}
	if settings.ThresholdWindowMinutes < 1 || settings.ThresholdWindowMinutes > 1440 {
		if settings.Enabled && settings.ErrorOnThresholdEnabled {
			return fmt.Errorf("threshold_window_minutes must be between 1-1440")
		}
		settings.ThresholdWindowMinutes = OpenAI403CounterWindowMinutesDefault
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal openai 403 cooldown settings: %w", err)
	}

	return s.settingRepo.Set(ctx, SettingKeyOpenAI403CooldownSettings, string(data))
}

// GetOpenAIAPIKeyHealthBreakerSettings 保留已有独立端点的校验、默认与写入行为。
func (s *RuntimeSettings) GetOpenAIAPIKeyHealthBreakerSettings(ctx context.Context) (*OpenAIAPIKeyHealthBreakerSettings, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultOpenAIAPIKeyHealthBreakerSettings(), nil
	}
	if cached, ok := s.openAIAPIKeyHealthBreakerCache.Load().(*cachedOpenAIAPIKeyHealthBreakerSettings); ok && cached != nil && time.Now().Before(cached.expiresAt) {
		result := cached.settings
		return &result, nil
	}

	settings := DefaultOpenAIAPIKeyHealthBreakerSettings()
	value, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAIAPIKeyHealthBreakerSettings)
	if err != nil && !errors.Is(err, s.notFound) {
		return nil, fmt.Errorf("get OpenAI API key health breaker settings: %w", err)
	}
	if err == nil && strings.TrimSpace(value) != "" {
		var stored OpenAIAPIKeyHealthBreakerSettings
		if json.Unmarshal([]byte(value), &stored) == nil {
			settings = normalizeOpenAIAPIKeyHealthBreakerSettings(&stored)
		}
	}
	s.openAIAPIKeyHealthBreakerCache.Store(&cachedOpenAIAPIKeyHealthBreakerSettings{
		settings:  *settings,
		expiresAt: time.Now().Add(openAIAPIKeyHealthBreakerSettingsCacheTTL),
	})
	result := *settings
	return &result, nil
}
