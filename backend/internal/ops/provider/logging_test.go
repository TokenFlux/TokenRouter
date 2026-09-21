package provider

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

type runtimeSettingRepoStub struct {
	values           map[string]string
	deleted          map[string]bool
	setCalls         int
	getValueCalls    int
	getMultipleCalls int
	getValueFn       func(key string) (string, error)
	setFn            func(key, value string) error
	deleteFn         func(key string) error
}

func newRuntimeSettingRepoStub() *runtimeSettingRepoStub {
	return &runtimeSettingRepoStub{
		values:  map[string]string{},
		deleted: map[string]bool{},
	}
}

func (s *runtimeSettingRepoStub) Get(ctx context.Context, key string) (*Setting, error) {
	value, err := s.GetValue(ctx, key)
	if err != nil {
		return nil, err
	}
	return &Setting{Key: key, Value: value}, nil
}

func (s *runtimeSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	s.getValueCalls++
	if s.getValueFn != nil {
		return s.getValueFn(key)
	}
	value, ok := s.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (s *runtimeSettingRepoStub) Set(_ context.Context, key, value string) error {
	if s.setFn != nil {
		if err := s.setFn(key, value); err != nil {
			return err
		}
	}
	s.values[key] = value
	s.setCalls++
	return nil
}

func (s *runtimeSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	s.getMultipleCalls++
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (s *runtimeSettingRepoStub) SetMultiple(_ context.Context, settings map[string]string) error {
	for key, value := range settings {
		s.values[key] = value
	}
	return nil
}

func (s *runtimeSettingRepoStub) GetAll(_ context.Context) (map[string]string, error) {
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out, nil
}

func (s *runtimeSettingRepoStub) Delete(_ context.Context, key string) error {
	if s.deleteFn != nil {
		if err := s.deleteFn(key); err != nil {
			return err
		}
	}
	if _, ok := s.values[key]; !ok {
		return ErrSettingNotFound
	}
	delete(s.values, key)
	s.deleted[key] = true
	return nil
}

func TestUpdateRuntimeLogConfig_InvalidConfigShouldNotApply(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	svc := newTestService(testServiceInputs{
		settingRepo: repo,
		cfg: &ops.Options{
			Log: ops.LogOptions{
				Level:           "info",
				Caller:          true,
				StacktraceLevel: "error",
				Sampling: ops.SamplingOptions{
					Enabled:    false,
					Initial:    100,
					Thereafter: 100,
				},
			},
		},
	})

	if err := logging.Init(logging.InitOptions{
		Level:       "info",
		Format:      "json",
		ServiceName: "sub2api",
		Environment: "test",
		Output: logging.OutputOptions{
			ToStdout: true,
			ToFile:   false,
		},
	}); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	_, err := svc.UpdateRuntimeLogConfig(context.Background(), &OpsRuntimeLogConfig{
		Level:           "trace",
		EnableSampling:  true,
		SamplingInitial: 100,
		SamplingNext:    100,
		Caller:          true,
		StacktraceLevel: "error",
		RetentionDays:   30,
	}, 1)
	if err == nil {
		t.Fatalf("expected validation error")
	}
	if logging.CurrentLevel() != "info" {
		t.Fatalf("logger level changed unexpectedly: %s", logging.CurrentLevel())
	}
	if repo.setCalls != 1 {
		// GetRuntimeLogConfig() 会在 key 缺失时写入默认值，此处应只有这一次持久化。
		t.Fatalf("unexpected set calls: %d", repo.setCalls)
	}
}

func TestResetRuntimeLogConfig_ShouldFallbackToBaseline(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	existing := &OpsRuntimeLogConfig{
		Level:           "debug",
		EnableSampling:  true,
		SamplingInitial: 50,
		SamplingNext:    50,
		Caller:          true,
		StacktraceLevel: "error",
		RetentionDays:   60,
		Source:          "runtime_setting",
	}
	raw, _ := json.Marshal(existing)
	repo.values[SettingKeyOpsRuntimeLogConfig] = string(raw)

	svc := newTestService(testServiceInputs{
		settingRepo: repo,
		cfg: &ops.Options{
			Log: ops.LogOptions{
				Level:           "warn",
				Caller:          false,
				StacktraceLevel: "fatal",
				Sampling: ops.SamplingOptions{
					Enabled:    false,
					Initial:    100,
					Thereafter: 100,
				},
			},
			Ops: ops.RuntimeOptions{
				Cleanup: ops.CleanupOptions{
					ErrorLogRetentionDays:  7,
					SystemLogRetentionDays: 45,
				},
			},
		},
	})

	if err := logging.Init(logging.InitOptions{
		Level:       "debug",
		Format:      "json",
		ServiceName: "sub2api",
		Environment: "test",
		Output: logging.OutputOptions{
			ToStdout: true,
			ToFile:   false,
		},
	}); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	resetCfg, err := svc.ResetRuntimeLogConfig(context.Background(), 9)
	if err != nil {
		t.Fatalf("ResetRuntimeLogConfig() error: %v", err)
	}
	if resetCfg.Source != "baseline" {
		t.Fatalf("source = %q, want baseline", resetCfg.Source)
	}
	if resetCfg.Level != "warn" {
		t.Fatalf("level = %q, want warn", resetCfg.Level)
	}
	if resetCfg.RetentionDays != 45 {
		t.Fatalf("retention_days = %d, want 45", resetCfg.RetentionDays)
	}
	if logging.CurrentLevel() != "warn" {
		t.Fatalf("logger level = %q, want warn", logging.CurrentLevel())
	}
	if !repo.deleted[SettingKeyOpsRuntimeLogConfig] {
		t.Fatalf("runtime setting key should be deleted")
	}
}

func TestResetRuntimeLogConfig_InvalidOperator(t *testing.T) {
	svc := newTestService(testServiceInputs{settingRepo: newRuntimeSettingRepoStub()})
	_, err := svc.ResetRuntimeLogConfig(context.Background(), 0)
	if err == nil {
		t.Fatalf("expected invalid operator error")
	}
	if err.Error() != "invalid operator id" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetRuntimeLogConfig_InvalidJSONFallback(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	repo.values[SettingKeyOpsRuntimeLogConfig] = `{invalid-json}`

	svc := newTestService(testServiceInputs{
		settingRepo: repo,
		cfg: &ops.Options{
			Log: ops.LogOptions{
				Level:           "warn",
				Caller:          true,
				StacktraceLevel: "error",
				Sampling: ops.SamplingOptions{
					Enabled:    false,
					Initial:    100,
					Thereafter: 100,
				},
			},
		},
	})

	got, err := svc.GetRuntimeLogConfig(context.Background())
	if err != nil {
		t.Fatalf("GetRuntimeLogConfig() error: %v", err)
	}
	if got.Level != "warn" {
		t.Fatalf("level = %q, want warn", got.Level)
	}
}

func TestUpdateRuntimeLogConfig_PersistFailureRollback(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	oldCfg := &OpsRuntimeLogConfig{
		Level:           "info",
		EnableSampling:  false,
		SamplingInitial: 100,
		SamplingNext:    100,
		Caller:          true,
		StacktraceLevel: "error",
		RetentionDays:   30,
	}
	raw, _ := json.Marshal(oldCfg)
	repo.values[SettingKeyOpsRuntimeLogConfig] = string(raw)
	repo.setFn = func(key, value string) error {
		if key == SettingKeyOpsRuntimeLogConfig {
			return errors.New("db down")
		}
		return nil
	}

	svc := newTestService(testServiceInputs{
		settingRepo: repo,
		cfg: &ops.Options{
			Log: ops.LogOptions{
				Level:           "info",
				Caller:          true,
				StacktraceLevel: "error",
				Sampling: ops.SamplingOptions{
					Enabled:    false,
					Initial:    100,
					Thereafter: 100,
				},
			},
		},
	})

	if err := logging.Init(logging.InitOptions{
		Level:       "info",
		Format:      "json",
		ServiceName: "sub2api",
		Environment: "test",
		Output: logging.OutputOptions{
			ToStdout: true,
			ToFile:   false,
		},
	}); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	_, err := svc.UpdateRuntimeLogConfig(context.Background(), &OpsRuntimeLogConfig{
		Level:           "debug",
		EnableSampling:  false,
		SamplingInitial: 100,
		SamplingNext:    100,
		Caller:          true,
		StacktraceLevel: "error",
		RetentionDays:   30,
	}, 5)
	if err == nil {
		t.Fatalf("expected persist error")
	}
	// Persist failure should rollback runtime level back to old effective level.
	if logging.CurrentLevel() != "info" {
		t.Fatalf("logger level should rollback to info, got %s", logging.CurrentLevel())
	}
}

func TestApplyRuntimeLogConfigOnStartup(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	cfgRaw := `{"level":"debug","enable_sampling":false,"sampling_initial":100,"sampling_thereafter":100,"caller":true,"stacktrace_level":"error","retention_days":30}`
	repo.values[SettingKeyOpsRuntimeLogConfig] = cfgRaw

	svc := newTestService(testServiceInputs{
		settingRepo: repo,
		cfg: &ops.Options{
			Log: ops.LogOptions{
				Level:           "info",
				Caller:          true,
				StacktraceLevel: "error",
				Sampling: ops.SamplingOptions{
					Enabled:    false,
					Initial:    100,
					Thereafter: 100,
				},
			},
		},
	})

	if err := logging.Init(logging.InitOptions{
		Level:       "info",
		Format:      "json",
		ServiceName: "sub2api",
		Environment: "test",
		Output: logging.OutputOptions{
			ToStdout: true,
			ToFile:   false,
		},
	}); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	svc.ApplyRuntimeLogConfigOnStartup(context.Background())
	if logging.CurrentLevel() != "debug" {
		t.Fatalf("expected startup apply debug, got %s", logging.CurrentLevel())
	}
}

func TestGetRuntimeLogConfigFallbackAndErrors(t *testing.T) {
	var nilSvc *OpsService
	cfg, err := nilSvc.GetRuntimeLogConfig(context.Background())
	if err != nil {
		t.Fatalf("nil svc should fallback default: %v", err)
	}
	if cfg.Level != "info" {
		t.Fatalf("unexpected nil svc default level: %s", cfg.Level)
	}

	repo := newRuntimeSettingRepoStub()
	repo.getValueFn = func(key string) (string, error) {
		return "", errors.New("boom")
	}
	svc := newTestService(testServiceInputs{
		settingRepo: repo,
		cfg: &ops.Options{
			Log: ops.LogOptions{
				Level:           "warn",
				Caller:          true,
				StacktraceLevel: "error",
				Sampling: ops.SamplingOptions{
					Enabled:    false,
					Initial:    100,
					Thereafter: 100,
				},
			},
		},
	})
	if _, err := svc.GetRuntimeLogConfig(context.Background()); err == nil {
		t.Fatalf("expected get value error")
	}
}

func TestUpdateRuntimeLogConfig_PreconditionErrors(t *testing.T) {
	svc := newTestService(testServiceInputs{})
	if _, err := svc.UpdateRuntimeLogConfig(context.Background(), &OpsRuntimeLogConfig{}, 1); err == nil {
		t.Fatalf("expected setting repo not initialized")
	}

	svc = newTestService(testServiceInputs{settingRepo: newRuntimeSettingRepoStub()})
	if _, err := svc.UpdateRuntimeLogConfig(context.Background(), nil, 1); err == nil {
		t.Fatalf("expected invalid config")
	}
	if _, err := svc.UpdateRuntimeLogConfig(context.Background(), &OpsRuntimeLogConfig{
		Level:           "info",
		StacktraceLevel: "error",
		SamplingInitial: 1,
		SamplingNext:    1,
		RetentionDays:   1,
	}, 0); err == nil {
		t.Fatalf("expected invalid operator")
	}
}

func TestUpdateRuntimeLogConfig_Success(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	svc := newTestService(testServiceInputs{
		settingRepo: repo,
		cfg: &ops.Options{
			Log: ops.LogOptions{
				Level:           "info",
				Caller:          true,
				StacktraceLevel: "error",
				Sampling: ops.SamplingOptions{
					Enabled:    false,
					Initial:    100,
					Thereafter: 100,
				},
			},
		},
	})

	if err := logging.Init(logging.InitOptions{
		Level:       "info",
		Format:      "json",
		ServiceName: "sub2api",
		Environment: "test",
		Output: logging.OutputOptions{
			ToStdout: true,
			ToFile:   false,
		},
	}); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	next, err := svc.UpdateRuntimeLogConfig(context.Background(), &OpsRuntimeLogConfig{
		Level:           "debug",
		EnableSampling:  false,
		SamplingInitial: 100,
		SamplingNext:    100,
		Caller:          true,
		StacktraceLevel: "error",
		RetentionDays:   30,
	}, 2)
	if err != nil {
		t.Fatalf("UpdateRuntimeLogConfig() error: %v", err)
	}
	if next.Source != "runtime_setting" || next.UpdatedByUserID != 2 || next.UpdatedAt == "" {
		t.Fatalf("unexpected metadata: %+v", next)
	}
	if logging.CurrentLevel() != "debug" {
		t.Fatalf("expected applied level debug, got %s", logging.CurrentLevel())
	}
}

func TestResetRuntimeLogConfig_IgnoreNotFoundDelete(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	repo.deleteFn = func(key string) error { return ErrSettingNotFound }
	svc := newTestService(testServiceInputs{
		settingRepo: repo,
		cfg: &ops.Options{
			Log: ops.LogOptions{
				Level:           "info",
				Caller:          true,
				StacktraceLevel: "error",
				Sampling: ops.SamplingOptions{
					Enabled:    false,
					Initial:    100,
					Thereafter: 100,
				},
			},
		},
	})
	if _, err := svc.ResetRuntimeLogConfig(context.Background(), 1); err != nil {
		t.Fatalf("reset should ignore ErrSettingNotFound: %v", err)
	}
}

func TestApplyRuntimeLogConfigHelpers(t *testing.T) {
	if err := (LogControl{}).Apply(nil); err == nil {
		t.Fatalf("expected nil config error")
	}

	var nilSvc *OpsService
	nilSvc.ApplyRuntimeLogConfigOnStartup(context.Background())
}

type OpsService = ops.OpsService
type OpsRuntimeLogConfig = ops.OpsRuntimeLogConfig
type Setting = settings.Setting

var ErrSettingNotFound = settings.ErrSettingNotFound

const SettingKeyOpsRuntimeLogConfig = ops.SettingKeyOpsRuntimeLogConfig

type testServiceInputs struct {
	settingRepo ops.Settings
	cfg         *ops.Options
}

func newTestService(v testServiceInputs) *OpsService {
	return ops.BuildOpsService(nil, v.settingRepo, v.cfg, nil, nil, nil, nil, LogControl{})
}
