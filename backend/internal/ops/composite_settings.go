package ops

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// CompositeMonitoringSettings 是综合设置中公开的三个 Ops 开关。
type CompositeMonitoringSettings struct {
	OpsMonitoringEnabled         bool `json:"ops_monitoring_enabled"`
	OpsRealtimeMonitoringEnabled bool `json:"ops_realtime_monitoring_enabled"`
	OpsMetricsIntervalSeconds    int  `json:"ops_metrics_interval_seconds"`
}

// PrepareMonitoringSettings 保留非正采样间隔不写入的原行为。
func PrepareMonitoringSettings(v CompositeMonitoringSettings) map[string]string {
	values := map[string]string{"ops_monitoring_enabled": strconv.FormatBool(v.OpsMonitoringEnabled), "ops_realtime_monitoring_enabled": strconv.FormatBool(v.OpsRealtimeMonitoringEnabled)}
	if v.OpsMetricsIntervalSeconds > 0 {
		values["ops_metrics_interval_seconds"] = strconv.Itoa(v.OpsMetricsIntervalSeconds)
	}
	return values
}

// MergeQuotaAutoPauseSettings 是共享 Ops JSON 的唯一合并边界，不覆盖其它高级设置。
func MergeQuotaAutoPauseSettings(raw string, quota OpsOpenAIAccountQuotaAutoPauseSettings) (*OpsAdvancedSettings, error) {
	value := defaultOpsAdvancedSettings()
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), value); err != nil {
			return nil, fmt.Errorf("unmarshal ops advanced settings: %w", err)
		}
	}
	value.OpenAIAccountQuotaAutoPause = quota
	normalizeOpsAdvancedSettings(value)
	return value, nil
}

// ParseQuotaAutoPauseSettings 只读取本模块 JSON 中的账号只读投影。
func ParseQuotaAutoPauseSettings(raw string) OpsOpenAIAccountQuotaAutoPauseSettings {
	cfg := defaultOpsAdvancedSettings()
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), cfg); err != nil {
			return OpsOpenAIAccountQuotaAutoPauseSettings{}
		}
	}
	normalizeOpsAdvancedSettings(cfg)
	return cfg.OpenAIAccountQuotaAutoPause
}

// ParseRuntimeQuotaAutoPauseSettings 保留运行缓存与管理回显对坏 JSON 的原有差异。
func ParseRuntimeQuotaAutoPauseSettings(raw string) OpsOpenAIAccountQuotaAutoPauseSettings {
	cfg := defaultOpsAdvancedSettings()
	if strings.TrimSpace(raw) != "" {
		if jsonErr := json.Unmarshal([]byte(raw), cfg); jsonErr == nil {
			normalizeOpsAdvancedSettings(cfg)
		}
	}
	return cfg.OpenAIAccountQuotaAutoPause
}
