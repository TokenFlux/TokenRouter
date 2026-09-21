package preaggregation_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/settings"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/settings/preaggregation"
	"github.com/stretchr/testify/require"
)

// TestPreAggregationSettingsDefaultsFromDeployment 验证新设置缺失时只采用部署能力默认值。
func TestPreAggregationSettingsDefaultsFromDeployment(t *testing.T) {
	repo := newPreaggregationFixture()
	repo.values[ops.SettingKeyOpsAdvancedSettings] = `{"aggregation":{"aggregation_enabled":false}}`
	repo.values["ops_query_mode_default"] = "raw"
	cfg := &preaggregation.Options{Usage: preaggregation.UsageOptions{Enabled: true, IntervalSeconds: 120}, OpsEnabled: true, OpsAggregationEnabled: true}

	settings, err := preaggregation.NewPreAggregationSettingsService(repo, cfg).Get(context.Background())
	require.NoError(t, err)
	require.True(t, settings.Usage.Enabled)
	require.Equal(t, 120, settings.Usage.IntervalSeconds)
	require.True(t, settings.Ops.Enabled)
	require.Equal(t, 1, repo.getValueCalls)
}

// TestPreAggregationSettingsDeploymentCeiling 验证数据库设置不能绕过部署层硬开关。
func TestPreAggregationSettingsDeploymentCeiling(t *testing.T) {
	repo := newPreaggregationFixture()
	repo.values[preaggregation.SettingKeyPreAggregationSettings] = `{"usage":{"enabled":true,"interval_seconds":60},"ops":{"enabled":true}}`
	cfg := &preaggregation.Options{Usage: preaggregation.UsageOptions{Enabled: false}, OpsEnabled: true, OpsAggregationEnabled: false}

	service := preaggregation.NewPreAggregationSettingsService(repo, cfg)
	settings, err := service.Get(context.Background())
	require.NoError(t, err)
	require.False(t, settings.Usage.Enabled)
	require.False(t, settings.Ops.Enabled)
	require.False(t, service.UsageEnabled(context.Background()))
	require.False(t, service.OpsEnabled(context.Background()))
}

// TestPreAggregationSettingsUpdateNotifies 验证完整更新会规范化周期并立即通知任务。
func TestPreAggregationSettingsUpdateNotifies(t *testing.T) {
	repo := newPreaggregationFixture()
	cfg := &preaggregation.Options{Usage: preaggregation.UsageOptions{Enabled: true, IntervalSeconds: 60}, OpsEnabled: true, OpsAggregationEnabled: true}
	service := preaggregation.NewPreAggregationSettingsService(repo, cfg)
	var previous, next preaggregation.PreAggregationSettings
	called := false
	service.RegisterListener(func(before, after preaggregation.PreAggregationSettings) {
		called = true
		previous = before
		next = after
	})

	updated, err := service.Update(context.Background(), preaggregation.PreAggregationSettings{
		Usage: preaggregation.PreAggregationUsageSettings{Enabled: false, IntervalSeconds: 10},
		Ops:   preaggregation.PreAggregationOpsSettings{Enabled: false},
	})
	require.NoError(t, err)
	require.True(t, called)
	require.True(t, previous.Usage.Enabled)
	require.Equal(t, preaggregation.PreAggregationMinIntervalSeconds, updated.Usage.IntervalSeconds)
	require.Equal(t, updated, next)

	var persisted preaggregation.PreAggregationSettings
	require.NoError(t, json.Unmarshal([]byte(repo.values[preaggregation.SettingKeyPreAggregationSettings]), &persisted))
	require.Equal(t, updated, persisted)
}

// 夹具保留原读取计数和实际保存值，通知由真实控制器触发。
type preaggregationFixture struct {
	values        map[string]string
	getValueCalls int
}

func newPreaggregationFixture() *preaggregationFixture {
	return &preaggregationFixture{values: map[string]string{}}
}
func (r *preaggregationFixture) GetValue(_ context.Context, key string) (string, error) {
	r.getValueCalls++
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", settings.ErrSettingNotFound
}
func (r *preaggregationFixture) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}
