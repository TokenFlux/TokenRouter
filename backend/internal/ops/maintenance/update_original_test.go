//go:build unit

package maintenance_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/ops/provider"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/ops/maintenance"
	"github.com/stretchr/testify/require"
)

type updateServiceCacheStub struct {
	data string
}

func (s *updateServiceCacheStub) GetUpdateInfo(context.Context) (string, error) {
	if s.data == "" {
		return "", errors.New("cache miss")
	}
	return s.data, nil
}

func (s *updateServiceCacheStub) SetUpdateInfo(_ context.Context, data string, _ time.Duration) error {
	s.data = data
	return nil
}

type updateServiceGitHubClientStub struct {
	release        *ops.GitHubRelease
	recentReleases []*ops.GitHubRelease
	recentErr      error
}

func (s *updateServiceGitHubClientStub) FetchLatestRelease(context.Context, string) (*ops.GitHubRelease, error) {
	return s.release, nil
}

func (s *updateServiceGitHubClientStub) FetchRecentReleases(context.Context, string, int) ([]*ops.GitHubRelease, error) {
	return s.recentReleases, s.recentErr
}

func (s *updateServiceGitHubClientStub) DownloadFile(context.Context, string, string, int64) error {
	panic("DownloadFile should not be called when no update is available")
}

func (s *updateServiceGitHubClientStub) FetchChecksumFile(context.Context, string) ([]byte, error) {
	panic("FetchChecksumFile should not be called when no update is available")
}

func TestUpdateServicePerformUpdateNoUpdateReturnsSentinel(t *testing.T) {
	svc := newOriginalMaintenanceUpdate(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{
			release: &ops.GitHubRelease{
				TagName: "v0.1.132",
				Name:    "v0.1.132",
			},
		},
		"0.1.132",
		"release",
	)

	err := svc.PerformUpdate(context.Background())

	require.Error(t, err)
	require.True(t, errors.Is(err, maintenance.ErrNoUpdateAvailable))
	require.ErrorIs(t, err, maintenance.ErrNoUpdateAvailable)
}

func newRollbackTestService(current string, releases []*ops.GitHubRelease) *maintenance.UpdateService {
	return newOriginalMaintenanceUpdate(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentReleases: releases},
		current,
		"release",
	)
}

func TestUpdateServiceRollbackToVersionRejectsDisallowedTargets(t *testing.T) {
	releases := []*ops.GitHubRelease{
		{TagName: "v0.1.148"},
		{TagName: "v0.1.147"},
		{TagName: "v0.1.146"},
		{TagName: "v0.1.145"},
		{TagName: "v0.1.144"},
		{TagName: "v0.1.143"},
		{TagName: "v0.1.142"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	for _, target := range []string{
		"",         // 空版本
		"0.1.147",  // 当前版本
		"v0.1.147", // 带前缀的当前版本
		"0.1.148",  // 更新版本
		"0.1.142",  // 超出最近 3 个版本
		"9.9.9",    // 不存在的版本
		"0.1.146;$(touch /tmp/tokenrouter-pwned)", // 非法 shell 字符
	} {
		err := svc.RollbackToVersion(context.Background(), target)
		require.ErrorIs(t, err, maintenance.ErrRollbackVersionNotAllowed, "target %q should be rejected", target)
	}
}

func TestUpdateServiceRollbackToVersionAcceptsVPrefix(t *testing.T) {
	// release 中没有当前平台资产：目标应先通过允许列表，再在资产查找阶段失败。
	releases := []*ops.GitHubRelease{
		{TagName: "v0.1.147"},
		{TagName: "v0.1.146"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	err := svc.RollbackToVersion(context.Background(), "v0.1.146")

	require.Error(t, err)
	require.NotErrorIs(t, err, maintenance.ErrRollbackVersionNotAllowed)
	require.Contains(t, err.Error(), "no compatible release found")
}

// newOriginalMaintenanceUpdate 只装配原发布查询与安装器，拒绝路径仍运行真实维护规则。
func newOriginalMaintenanceUpdate(cache ops.UpdateCache, client *updateServiceGitHubClientStub, version, buildType string) *maintenance.UpdateService {
	return maintenance.NewUpdateService(ops.NewReleaseQuery(cache, client, version, buildType), provider.NewBinaryInstaller(client, nil))
}
