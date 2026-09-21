//go:build unit

package creative

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/settings"

	"github.com/stretchr/testify/require"
)

// IsCreativeEnabled 是创作台请求期门控读取：显式 "false" 关闭，键缺失或读取失败默认开启。
func TestSettingService_IsCreativeEnabled(t *testing.T) {
	repo := &creativeRuntimeSettingsFixture{values: map[string]string{SettingKeyCreativeEnabled: "false"}}
	svc := NewRuntimeSettings(repo, settings.ErrSettingNotFound)
	require.False(t, svc.IsCreativeEnabled(context.Background()))

	// 键缺失（旧版本库未写入）时默认开启。
	repo = &creativeRuntimeSettingsFixture{values: map[string]string{}}
	svc = NewRuntimeSettings(repo, settings.ErrSettingNotFound)
	require.True(t, svc.IsCreativeEnabled(context.Background()))
}

// 设置读取替身只提供原键值和缺键结果，不重建旧设置服务。
type creativeRuntimeSettingsFixture struct{ values map[string]string }

func (r *creativeRuntimeSettingsFixture) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", settings.ErrSettingNotFound
}
