//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

type s15DelayedBackendRepo struct {
	settingUpdateRepoStub
	entered, release chan struct{}
}

func (r *s15DelayedBackendRepo) GetValue(ctx context.Context, key string) (string, error) {
	if key == SettingKeyBackendModeEnabled {
		close(r.entered)
		<-r.release
		return "false", nil
	}
	return r.settingUpdateRepoStub.GetValue(ctx, key)
}

// 已完成的管理更新不能被早先启动的回源覆盖。
func TestSettingUpdateLateBackendModeLoad(t *testing.T) {
	repo := &s15DelayedBackendRepo{settingUpdateRepoStub: settingUpdateRepoStub{values: map[string]string{}}, entered: make(chan struct{}), release: make(chan struct{})}
	svc := NewSettingService(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	done := make(chan struct{})
	go func() { svc.IsBackendModeEnabled(context.Background()); close(done) }()
	<-repo.entered
	settings, err := svc.GetAllSettings(context.Background())
	require.NoError(t, err)
	settings.BackendModeEnabled = true
	err = svc.UpdateSettings(context.Background(), settings)
	close(repo.release)
	<-done
	require.NoError(t, err)
	require.True(t, svc.IsBackendModeEnabled(context.Background()), "旧回源覆盖管理员刚开启的 backend mode")
}
