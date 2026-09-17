package service

import (
	"context"
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/settings/composite"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"

	"github.com/TokenFlux/TokenRouter/internal/gateway"

	runtimesettings "github.com/TokenFlux/TokenRouter/internal/settings"
)

// OmittedSettingKeys 标记调用方载荷中未包含的设置键。
// SystemSettings 是普通结构体，调用方省略的字段会以零值传入，无法与主动清空区分。
// 把键加入此集合可在写入前将其排除，从而保留存储中的原值。
//
// nil 或空集合保留整份文档语义，即写入所有设置键。
type OmittedSettingKeys = runtimesettings.OmittedKeys

// UpdateSettings 更新系统设置
func (s *SettingService) UpdateSettings(ctx context.Context, settings *SystemSettings) error {
	return s.UpdateSettingsOmitting(ctx, settings, nil)
}

// UpdateSettingsOmitting 持久化系统设置，并保留 omitted 中各键的存储值。
func (s *SettingService) UpdateSettingsOmitting(ctx context.Context, settings *SystemSettings, omitted OmittedSettingKeys) error {
	updates, err := s.buildSystemSettingsUpdates(ctx, settings)
	if err != nil {
		return err
	}
	omitted.DropFrom(updates)

	if err := s.settingRepo.SetMultiple(ctx, updates); err != nil {
		return err
	}
	s.refreshCachedSettingsAfterWrite(ctx, settings, omitted)
	return nil
}

// UpdateSettingsWithAuthSourceDefaults 在一次写入中持久化系统设置与认证来源默认值。
func (s *SettingService) UpdateSettingsWithAuthSourceDefaults(ctx context.Context, settings *SystemSettings, authDefaults *AuthSourceDefaultSettings) error {
	return s.UpdateSettingsWithAuthSourceDefaultsOmitting(ctx, settings, authDefaults, nil)
}

// UpdateSettingsWithAuthSourceDefaultsOmitting 在一次写入中持久化系统设置与认证来源默认值，
// 同时保留 omitted 中各键的存储值。
func (s *SettingService) UpdateSettingsWithAuthSourceDefaultsOmitting(ctx context.Context, settings *SystemSettings, authDefaults *AuthSourceDefaultSettings, omitted OmittedSettingKeys) error {
	updates, err := s.PrepareSettingsWithAuthSourceDefaults(ctx, settings, authDefaults, omitted)
	if err != nil {
		return err
	}
	if err := s.settingRepo.SetMultiple(ctx, updates); err != nil {
		return err
	}
	s.refreshCachedSettingsAfterWrite(ctx, settings, omitted)
	return nil
}

// PrepareSettingsWithAuthSourceDefaults 只执行原校验及字段投影，综合入口统一提交。
func (s *SettingService) PrepareSettingsWithAuthSourceDefaults(ctx context.Context, settings *SystemSettings, authDefaults *AuthSourceDefaultSettings, omitted OmittedSettingKeys) (map[string]string, error) {
	updates, err := s.buildSystemSettingsUpdates(ctx, settings)
	if err != nil {
		return nil, err
	}

	authSourceUpdates, err := s.buildAuthSourceDefaultUpdates(ctx, authDefaults)
	if err != nil {
		return nil, err
	}
	for key, value := range authSourceUpdates {
		updates[key] = value
	}
	omitted.DropFrom(updates)

	return updates, nil
}

// refreshCachedSettingsAfterWrite 使进程内缓存与刚完成的写入保持一致。
// 部分载荷会把省略字段表示为零值，因此此时必须从存储重建缓存，而不能使用请求结构体。
func (s *SettingService) refreshCachedSettingsAfterWrite(ctx context.Context, settings *SystemSettings, omitted OmittedSettingKeys) {
	if len(omitted) == 0 {
		s.refreshCachedSettings(settings)
		return
	}
	stored, err := s.GetAllSettings(ctx)
	if err != nil {
		slog.Warn("refresh cached settings after partial update failed", "error", err)
		return
	}
	s.refreshCachedSettings(stored)
}

func (s *SettingService) buildSystemSettingsUpdates(ctx context.Context, value *SystemSettings) (map[string]string, error) {
	return composite.Prepare(ctx, value, composite.PrepareOptions{ValidatePlans: s.validateDefaultSubscriptionPlans, ReadValues: func(ctx context.Context) (map[string]string, error) {
		if s == nil || s.settingRepo == nil {
			return nil, nil
		}
		return s.settingRepo.GetAll(ctx)
	}, Gateway: s.GatewayAdminRules(), Scheduler: s.SchedulerAdminDefaults()})
}

func (s *SettingService) buildAuthSourceDefaultUpdates(ctx context.Context, value *AuthSourceDefaultSettings) (map[string]string, error) {
	return s.GrantSettings().PrepareAuthSourceDefaults(ctx, value)
}

func (s *SettingService) refreshCachedSettings(settings *SystemSettings) {
	if settings == nil {
		return
	}

	s.backendMode.Publish(settings.BackendModeEnabled)
	s.GatewaySettings().PublishForwarding(settings.MinClaudeCodeVersion, settings.MaxClaudeCodeVersion, gateway.ForwardingSnapshot{
		OpenAITTFTMode:                   normalizeOpenAITTFTMode(settings.OpenAITTFTMode),
		FingerprintUnification:           settings.EnableFingerprintUnification,
		MetadataPassthrough:              settings.EnableMetadataPassthrough,
		CCHSigning:                       settings.EnableCCHSigning,
		ClaudeOAuthSystemPromptInjection: settings.EnableClaudeOAuthSystemPromptInjection,
		ClaudeOAuthSystemPrompt:          settings.ClaudeOAuthSystemPrompt,
		ClaudeOAuthSystemPromptBlocks:    settings.ClaudeOAuthSystemPromptBlocks,
		AnthropicCacheTTL1hInjection:     settings.EnableAnthropicCacheTTL1hInjection,
		RewriteMessageCacheControl:       settings.RewriteMessageCacheControl,
		ClientDatelineNormalization:      settings.EnableClientDatelineNormalization,
	})
	s.GatewaySettings().PublishClientUserAgents(settings.AntigravityUserAgentVersion, settings.OpenAICodexUserAgent)
	userPromptReplacementCache.Store((*compiledUserPromptReplacementConfig)(nil))
	SchedulerSettingsRuntime().Store(scheduler.RuntimeSettingsFromAdmin(settings.SchedulerAdminSettings(), s.SchedulerAdminDefaults()))

	// 使配额自动暂停缓存失效，并让下一次读取触发重新加载。
	// 这里无法判断 ops_advanced_settings 是否也被修改，因此采用防御式处理：
	// 写入一个已过期条目，GetOpenAIQuotaAutoPauseSettings 会先返回旧值并触发异步刷新，
	// 不会阻塞后续请求。
	s.QuotaSettings().Apply(settings.OpenAIQuotaAutoPauseSettings, settings.OpenAIQuotaAutoPauseSettingsSet)
	s.AccountSettings().ApplySchedulingThresholds(settings.AccountSchedulingThresholds)
	if s.cfg != nil {
		s.ForwardedSettings().Apply(runtimeconfig.ForwardedInput{APIKeyACLTrustForwardedIP: settings.APIKeyACLTrustForwardedIP, ForwardedClientIPHeaders: settings.ForwardedClientIPHeaders})
	}
	s.GatewaySettings().PublishCodexPlugin(settings.OpenAIAllowClaudeCodeCodexPlugin)
	if s.runtimeSettings != nil {
		s.runtimeSettings.NotifyUpdated()
	}
	if s.creativeWorkerCountCallback != nil {
		s.creativeWorkerCountCallback(settings.CreativeWorkerCount)
	}
}

// defaultRewriteMessageCacheControl 返回消息 cache_control 改写的默认开关。
func (s *SettingService) defaultRewriteMessageCacheControl() bool {
	return false
}

// BeginSettingsUpdate 在旧 HTTP 读取旧值之前进入唯一综合更新保护。
func (s *SettingService) BeginSettingsUpdate(ctx context.Context) (*runtimesettings.UpdateSession, error) {
	return s.settingRuntime().Updates().Begin(ctx)
}

// ApplyPersistedSettings 根据已经提交的完整值刷新，读取失败不得报告应用成功。
func (s *SettingService) ApplyPersistedSettings(ctx context.Context) error {
	stored, err := s.GetAllSettings(ctx)
	if err != nil {
		return err
	}
	s.refreshCachedSettings(stored)
	return nil
}
