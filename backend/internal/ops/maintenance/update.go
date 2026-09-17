package maintenance

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

type Asset = ops.Asset
type GitHubRelease = ops.GitHubRelease

var ErrNoUpdateAvailable = infraerrors.Conflict("ALREADY_UP_TO_DATE", "no update available; current version is latest")
var ErrRollbackVersionNotAllowed = infraerrors.BadRequest("ROLLBACK_VERSION_NOT_ALLOWED", "version is not in the allowed rollback list")

type ReleaseQueries interface {
	CheckUpdate(context.Context, bool) (*ops.UpdateInfo, error)
	ListRollbackVersions(context.Context) ([]ops.RollbackVersion, error)
	FetchRollbackCandidates(context.Context) ([]*ops.GitHubRelease, error)
}
type Installer interface {
	Apply(context.Context, []ops.Asset) error
	Rollback() error
}

// UpdateService 只编排版本资格，文件与网络能力通过端口注入。
type UpdateService struct {
	ReleaseQueries
	installer Installer
}

func NewUpdateService(query ReleaseQueries, installer Installer) *UpdateService {
	return &UpdateService{query, installer}
}
func (s *UpdateService) Rollback() error { return s.installer.Rollback() }
func (s *UpdateService) PerformUpdate(ctx context.Context) error {
	info, err := s.CheckUpdate(ctx, true)
	if err != nil {
		return err
	}

	if !info.HasUpdate {
		return ErrNoUpdateAvailable
	}

	return s.installer.Apply(ctx, info.ReleaseInfo.Assets)
}

// applyReleaseAssets 下载当前平台的 release 包，校验 checksum，并原子替换运行中的二进制。
// PerformUpdate（最新版）和 RollbackToVersion（指定旧版）共用该流程。

func (s *UpdateService) RollbackToVersion(ctx context.Context, version string) error {
	target, ok := normalizeRollbackVersion(version)
	if !ok {
		return ErrRollbackVersionNotAllowed
	}

	releases, err := s.fetchRollbackCandidates(ctx)
	if err != nil {
		return err
	}

	var match *GitHubRelease
	for _, r := range releases {
		candidateVersion, _ := normalizeRollbackVersion(r.TagName)
		if candidateVersion == target {
			match = r
			break
		}
	}
	if match == nil {
		return ErrRollbackVersionNotAllowed
	}

	assets := make([]Asset, len(match.Assets))
	for i, a := range match.Assets {
		assets[i] = Asset{
			Name:        a.Name,
			DownloadURL: a.BrowserDownloadURL,
			Size:        a.Size,
		}
	}

	return s.installer.Apply(ctx, assets)
}

func (s *UpdateService) fetchRollbackCandidates(ctx context.Context) ([]*GitHubRelease, error) {
	return s.FetchRollbackCandidates(ctx)
}

func normalizeRollbackVersion(raw string) (string, bool) { return ops.NormalizeRollbackVersion(raw) }
