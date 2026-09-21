package scheduler

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	"github.com/stretchr/testify/require"
)

// 设置读取替身记录原批量失败降级的完整键序列。
type runtimeSettingFixture struct {
	RuntimeSettingSource
	keys  []string
	batch int
	fail  bool
}

func (f *runtimeSettingFixture) GetMultiple(context.Context, []string) (map[string]string, error) {
	f.batch++
	if f.fail {
		return nil, errors.New("批量读取失败")
	}
	return map[string]string{SettingKeyAdvancedSchedulerWeightLoad: "0"}, nil
}
func (f *runtimeSettingFixture) GetValue(_ context.Context, key string) (string, error) {
	f.keys = append(f.keys, key)
	if key == SettingKeyAdvancedSchedulerWeightLoad {
		return "0", nil
	}
	return "", nil
}
func TestSettingsRuntimePreservesFallbackAndIsolatesValues(t *testing.T) {
	source := &runtimeSettingFixture{fail: true}
	runtime := NewSettingsRuntime(Diagnostics{})
	first := runtime.Load(context.Background(), source, policy.RuntimeSettings{})
	require.Equal(t, AdvancedSchedulerRuntimeSettingKeys(), source.keys)
	require.Equal(t, float64(0), first.WeightOverrides["load"])
	first.WeightOverrides["load"] = 7
	second := runtime.Load(context.Background(), source, policy.RuntimeSettings{})
	require.Equal(t, 1, source.batch)
	require.Equal(t, float64(0), second.WeightOverrides["load"])
	value := policy.RuntimeSettings{WeightOverrides: map[string]float64{"priority": 2}, LbTopKOverride: 3}
	runtime.Store(value)
	value.WeightOverrides["priority"] = 9
	third := runtime.Load(context.Background(), source, policy.RuntimeSettings{})
	require.Equal(t, float64(2), third.WeightOverrides["priority"])
	require.Equal(t, 3, third.LbTopKOverride)
}

// 管理写入校验不能复用热路径缓存，也不能在批量读取失败后掩盖错误。
func TestValidationWeightsReadFreshAndPropagateFailure(t *testing.T) {
	source := &runtimeSettingFixture{}
	defaults := DefaultAdminSettingsDefaults()
	first, err := LoadValidationWeights(context.Background(), source, defaults)
	require.NoError(t, err)
	require.Zero(t, first.Load, "显式零权重必须覆盖启动值")
	require.Equal(t, defaults.Weights.Priority, first.Priority)
	_, err = LoadValidationWeights(context.Background(), source, defaults)
	require.NoError(t, err)
	require.Equal(t, 2, source.batch, "每次管理校验都读取最新值")
	source.fail = true
	_, err = LoadValidationWeights(context.Background(), source, defaults)
	require.ErrorContains(t, err, "load advanced scheduler settings: 批量读取失败")
	require.Empty(t, source.keys, "管理校验不使用热路径的逐键降级")
}
