package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/ops/maintenance"
	op "github.com/TokenFlux/TokenRouter/internal/ops/provider"
)

type UpdateCache = ops.UpdateCache
type UpdateInfo = ops.UpdateInfo
type ReleaseInfo = ops.ReleaseInfo
type Asset = ops.Asset
type GitHubRelease = ops.GitHubRelease
type RollbackVersion = ops.RollbackVersion
type GitHubAsset = ops.GitHubAsset
type GitHubReleaseClient interface {
	FetchLatestRelease(context.Context, string) (*GitHubRelease, error)
	FetchRecentReleases(context.Context, string, int) ([]*GitHubRelease, error)
	DownloadFile(context.Context, string, string, int64) error
	FetchChecksumFile(context.Context, string) ([]byte, error)
}
type UpdateService = maintenance.UpdateService

var ErrNoUpdateAvailable = maintenance.ErrNoUpdateAvailable
var ErrRollbackVersionNotAllowed = maintenance.ErrRollbackVersionNotAllowed

func NewUpdateService(cache UpdateCache, client GitHubReleaseClient, version, buildType string) *UpdateService {
	return WrapUpdateQuery(ops.NewReleaseQuery(cache, client, version, buildType), client)
}
func WrapUpdateQuery(query *ops.ReleaseQuery, client GitHubReleaseClient) *UpdateService {
	return maintenance.NewUpdateService(query, op.NewBinaryInstaller(client, nil))
}
