package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

// TestPreAggregationSettingsDefaultsFromDeployment 验证新设置缺失时只采用部署能力默认值。
func TestPreAggregationSettingsDefaultsFromDeployment(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	repo.values[SettingKeyOpsAdvancedSettings] = `{"aggregation":{"aggregation_enabled":false}}`
	repo.values["ops_query_mode_default"] = "raw"
	cfg := &config.Config{
		DashboardAgg: config.DashboardAggregationConfig{Enabled: true, IntervalSeconds: 120},
		Ops:          config.OpsConfig{Enabled: true, Aggregation: config.OpsAggregationConfig{Enabled: true}},
	}

	settings, err := NewPreAggregationSettingsService(repo, cfg).Get(context.Background())
	require.NoError(t, err)
	require.True(t, settings.Usage.Enabled)
	require.Equal(t, 120, settings.Usage.IntervalSeconds)
	require.True(t, settings.Ops.Enabled)
	require.Equal(t, 1, repo.getValueCalls)
}

// TestPreAggregationSettingsDeploymentCeiling 验证数据库设置不能绕过部署层硬开关。
func TestPreAggregationSettingsDeploymentCeiling(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	repo.values[SettingKeyPreAggregationSettings] = `{"usage":{"enabled":true,"interval_seconds":60},"ops":{"enabled":true}}`
	cfg := &config.Config{
		DashboardAgg: config.DashboardAggregationConfig{Enabled: false},
		Ops:          config.OpsConfig{Enabled: true, Aggregation: config.OpsAggregationConfig{Enabled: false}},
	}

	service := NewPreAggregationSettingsService(repo, cfg)
	settings, err := service.Get(context.Background())
	require.NoError(t, err)
	require.False(t, settings.Usage.Enabled)
	require.False(t, settings.Ops.Enabled)
	require.False(t, service.UsageEnabled(context.Background()))
	require.False(t, service.OpsEnabled(context.Background()))
}

// TestPreAggregationSettingsUpdateNotifies 验证完整更新会规范化周期并立即通知任务。
func TestPreAggregationSettingsUpdateNotifies(t *testing.T) {
	repo := newRuntimeSettingRepoStub()
	cfg := &config.Config{
		DashboardAgg: config.DashboardAggregationConfig{Enabled: true, IntervalSeconds: 60},
		Ops:          config.OpsConfig{Enabled: true, Aggregation: config.OpsAggregationConfig{Enabled: true}},
	}
	service := NewPreAggregationSettingsService(repo, cfg)
	var previous, next PreAggregationSettings
	called := false
	service.RegisterListener(func(before, after PreAggregationSettings) {
		called = true
		previous = before
		next = after
	})

	updated, err := service.Update(context.Background(), PreAggregationSettings{
		Usage: PreAggregationUsageSettings{Enabled: false, IntervalSeconds: 10},
		Ops:   PreAggregationOpsSettings{Enabled: false},
	})
	require.NoError(t, err)
	require.True(t, called)
	require.True(t, previous.Usage.Enabled)
	require.Equal(t, PreAggregationMinIntervalSeconds, updated.Usage.IntervalSeconds)
	require.Equal(t, updated, next)

	var persisted PreAggregationSettings
	require.NoError(t, json.Unmarshal([]byte(repo.values[SettingKeyPreAggregationSettings]), &persisted))
	require.Equal(t, updated, persisted)
}
