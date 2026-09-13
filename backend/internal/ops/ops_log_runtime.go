// 本文件拥有 Ops 观测用例；配置与外部状态经端口注入。
package ops

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func defaultOpsRuntimeLogConfig(cfg *Options) *OpsRuntimeLogConfig {
	out := &OpsRuntimeLogConfig{
		Level:           "info",
		EnableSampling:  false,
		SamplingInitial: 100,
		SamplingNext:    100,
		Caller:          true,
		StacktraceLevel: "error",
		RetentionDays:   30,
	}
	if cfg == nil {
		return out
	}
	out.Level = strings.ToLower(strings.TrimSpace(cfg.Log.Level))
	out.EnableSampling = cfg.Log.Sampling.Enabled
	out.SamplingInitial = cfg.Log.Sampling.Initial
	out.SamplingNext = cfg.Log.Sampling.Thereafter
	out.Caller = cfg.Log.Caller
	out.StacktraceLevel = strings.ToLower(strings.TrimSpace(cfg.Log.StacktraceLevel))
	if cfg.Ops.Cleanup.SystemLogRetentionDays > 0 {
		out.RetentionDays = cfg.Ops.Cleanup.SystemLogRetentionDays
	}
	return out
}

func normalizeOpsRuntimeLogConfig(cfg *OpsRuntimeLogConfig, defaults *OpsRuntimeLogConfig) {
	if cfg == nil || defaults == nil {
		return
	}
	cfg.Level = strings.ToLower(strings.TrimSpace(cfg.Level))
	if cfg.Level == "" {
		cfg.Level = defaults.Level
	}
	cfg.StacktraceLevel = strings.ToLower(strings.TrimSpace(cfg.StacktraceLevel))
	if cfg.StacktraceLevel == "" {
		cfg.StacktraceLevel = defaults.StacktraceLevel
	}
	if cfg.SamplingInitial <= 0 {
		cfg.SamplingInitial = defaults.SamplingInitial
	}
	if cfg.SamplingNext <= 0 {
		cfg.SamplingNext = defaults.SamplingNext
	}
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = defaults.RetentionDays
	}
}

func validateOpsRuntimeLogConfig(cfg *OpsRuntimeLogConfig) error {
	if cfg == nil {
		return errors.New("invalid config")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Level)) {
	case "debug", "info", "warn", "error":
	default:
		return errors.New("level must be one of: debug/info/warn/error")
	}
	switch strings.ToLower(strings.TrimSpace(cfg.StacktraceLevel)) {
	case "none", "error", "fatal":
	default:
		return errors.New("stacktrace_level must be one of: none/error/fatal")
	}
	if cfg.SamplingInitial <= 0 {
		return errors.New("sampling_initial must be positive")
	}
	if cfg.SamplingNext <= 0 {
		return errors.New("sampling_thereafter must be positive")
	}
	if cfg.RetentionDays < 1 || cfg.RetentionDays > 3650 {
		return errors.New("retention_days must be between 1 and 3650")
	}
	return nil
}

func (s *OpsService) GetRuntimeLogConfig(ctx context.Context) (*OpsRuntimeLogConfig, error) {
	if s == nil || s.settingRepo == nil {
		var cfg *Options
		if s != nil {
			cfg = s.cfg
		}
		defaultCfg := defaultOpsRuntimeLogConfig(cfg)
		return defaultCfg, nil
	}
	defaultCfg := defaultOpsRuntimeLogConfig(s.cfg)
	if ctx == nil {
		ctx = context.Background()
	}

	raw, err := s.settingRepo.GetValue(ctx, SettingKeyOpsRuntimeLogConfig)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			b, _ := json.Marshal(defaultCfg)
			_ = s.settingRepo.Set(ctx, SettingKeyOpsRuntimeLogConfig, string(b))
			return defaultCfg, nil
		}
		return nil, err
	}

	cfg := &OpsRuntimeLogConfig{}
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		return defaultCfg, nil
	}
	normalizeOpsRuntimeLogConfig(cfg, defaultCfg)
	return cfg, nil
}

func (s *OpsService) UpdateRuntimeLogConfig(ctx context.Context, req *OpsRuntimeLogConfig, operatorID int64) (*OpsRuntimeLogConfig, error) {
	if s == nil || s.settingRepo == nil {
		return nil, errors.New("setting repository not initialized")
	}
	if req == nil {
		return nil, errors.New("invalid config")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if operatorID <= 0 {
		return nil, errors.New("invalid operator id")
	}

	oldCfg, err := s.GetRuntimeLogConfig(ctx)
	if err != nil {
		return nil, err
	}
	next := *req
	normalizeOpsRuntimeLogConfig(&next, defaultOpsRuntimeLogConfig(s.cfg))
	if err := validateOpsRuntimeLogConfig(&next); err != nil {
		s.auditRuntimeLogConfigFailure(operatorID, oldCfg, &next, "validation_failed: "+err.Error())
		return nil, err
	}

	if err := s.applyRuntimeLogConfig(&next); err != nil {
		s.auditRuntimeLogConfigFailure(operatorID, oldCfg, &next, "apply_failed: "+err.Error())
		return nil, err
	}

	next.Source = "runtime_setting"
	next.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	next.UpdatedByUserID = operatorID

	encoded, err := json.Marshal(&next)
	if err != nil {
		return nil, err
	}
	if err := s.settingRepo.Set(ctx, SettingKeyOpsRuntimeLogConfig, string(encoded)); err != nil {
		// 存储失败时回滚到旧配置，避免内存状态与持久化状态不一致。
		_ = s.applyRuntimeLogConfig(oldCfg)
		s.auditRuntimeLogConfigFailure(operatorID, oldCfg, &next, "persist_failed: "+err.Error())
		return nil, err
	}

	s.auditRuntimeLogConfigChange(operatorID, oldCfg, &next, "updated")
	// 保留期同时驱动系统日志清理，保存后立即刷新清理任务的生效配置。
	if s.cleanupReloader != nil {
		if reloadErr := s.cleanupReloader.Reload(ctx); reloadErr != nil {
			s.report(
				"[OpsSettings] cleanup reload after runtime-log update failed: %v", reloadErr)
		}
	}

	return &next, nil
}

func (s *OpsService) ResetRuntimeLogConfig(ctx context.Context, operatorID int64) (*OpsRuntimeLogConfig, error) {
	if s == nil || s.settingRepo == nil {
		return nil, errors.New("setting repository not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if operatorID <= 0 {
		return nil, errors.New("invalid operator id")
	}

	oldCfg, err := s.GetRuntimeLogConfig(ctx)
	if err != nil {
		return nil, err
	}

	resetCfg := defaultOpsRuntimeLogConfig(s.cfg)
	normalizeOpsRuntimeLogConfig(resetCfg, defaultOpsRuntimeLogConfig(s.cfg))
	if err := validateOpsRuntimeLogConfig(resetCfg); err != nil {
		s.auditRuntimeLogConfigFailure(operatorID, oldCfg, resetCfg, "reset_validation_failed: "+err.Error())
		return nil, err
	}
	if err := s.applyRuntimeLogConfig(resetCfg); err != nil {
		s.auditRuntimeLogConfigFailure(operatorID, oldCfg, resetCfg, "reset_apply_failed: "+err.Error())
		return nil, err
	}

	// 清理 runtime 覆盖配置，回退到 env/yaml baseline。
	if err := s.settingRepo.Delete(ctx, SettingKeyOpsRuntimeLogConfig); err != nil && !errors.Is(err, ErrSettingNotFound) {
		_ = s.applyRuntimeLogConfig(oldCfg)
		s.auditRuntimeLogConfigFailure(operatorID, oldCfg, resetCfg, "reset_persist_failed: "+err.Error())
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	resetCfg.Source = "baseline"
	resetCfg.UpdatedAt = now
	resetCfg.UpdatedByUserID = operatorID

	s.auditRuntimeLogConfigChange(operatorID, oldCfg, resetCfg, "reset")
	// 重置保留期后同步刷新清理任务，避免继续使用旧的系统日志截止时间。
	if s.cleanupReloader != nil {
		if reloadErr := s.cleanupReloader.Reload(ctx); reloadErr != nil {
			s.report(
				"[OpsSettings] cleanup reload after runtime-log reset failed: %v", reloadErr)
		}
	}
	return resetCfg, nil
}

func (s *OpsService) ApplyRuntimeLogConfigOnStartup(ctx context.Context) {
	if s == nil {
		return
	}
	cfg, err := s.GetRuntimeLogConfig(ctx)
	if err != nil {
		return
	}
	_ = s.applyRuntimeLogConfig(cfg)
}

func (s *OpsService) applyRuntimeLogConfig(c *OpsRuntimeLogConfig) error {
	if s.logging == nil {
		return nil
	}
	return s.logging.Apply(c)
}
func (s *OpsService) auditRuntimeLogConfigChange(id int64, old, next *OpsRuntimeLogConfig, action string) {
	if s.logging != nil {
		s.logging.Changed(id, old, next, action)
	}
}
func (s *OpsService) auditRuntimeLogConfigFailure(id int64, old, next *OpsRuntimeLogConfig, reason string) {
	if s.logging != nil {
		s.logging.Failed(id, old, next, reason)
	}
}

// CompatDefaultOpsRuntimeLogConfig 为旧入口提供过渡委托。
func CompatDefaultOpsRuntimeLogConfig(cfg *Options) *OpsRuntimeLogConfig {
	return defaultOpsRuntimeLogConfig(cfg)
}

// CompatNormalizeOpsRuntimeLogConfig 为旧入口提供过渡委托。
func CompatNormalizeOpsRuntimeLogConfig(cfg *OpsRuntimeLogConfig, defaults *OpsRuntimeLogConfig) {
	normalizeOpsRuntimeLogConfig(cfg, defaults)
}

// CompatValidateOpsRuntimeLogConfig 为旧入口提供过渡委托。
func CompatValidateOpsRuntimeLogConfig(cfg *OpsRuntimeLogConfig) error {
	return validateOpsRuntimeLogConfig(cfg)
}
