// ReleaseQuery 只负责发布信息与缓存，不执行二进制替换或回滚。
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ReleaseQueryClient interface {
	FetchLatestRelease(context.Context, string) (*GitHubRelease, error)
	FetchRecentReleases(context.Context, string, int) ([]*GitHubRelease, error)
}
type ReleaseQuery struct {
	cache                     UpdateCache
	githubClient              ReleaseQueryClient
	currentVersion, buildType string
}

// @project-doc docs/operations/ops_monitoring_and_alerting.md#ops_release_and_maintenance
func NewReleaseQuery(cache UpdateCache, client ReleaseQueryClient, version, buildType string) *ReleaseQuery {
	return &ReleaseQuery{cache, client, version, buildType}
}

const updateCacheTTL = 1200
const githubRepo = "TokenFlux/TokenRouter"
const maxRollbackVersions = 3
const rollbackFetchPageSize = 15

func parseVersion(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	if idx := strings.IndexByte(v, '-'); idx != -1 {
		v = v[:idx]
	}
	parts := strings.Split(v, ".")
	result := [3]int{0, 0, 0}
	for i := 0; i < len(parts) && i < 3; i++ {
		if parsed, err := strconv.Atoi(parts[i]); err == nil {
			result[i] = parsed
		}
	}
	return result
}

// ParseVersion 保留旧比较入口的唯一规则。
func ParseVersion(v string) [3]int { return parseVersion(v) }

// compareVersions compares two semantic versions
func compareVersions(current, latest string) int {
	currentParts := parseVersion(current)
	latestParts := parseVersion(latest)

	for i := 0; i < 3; i++ {
		if currentParts[i] < latestParts[i] {
			return -1
		}
		if currentParts[i] > latestParts[i] {
			return 1
		}
	}
	return 0
}

// CompareVersions 保留旧比较入口的唯一规则。
func CompareVersions(current, latest string) int { return compareVersions(current, latest) }
func (s *ReleaseQuery) SaveToCache(ctx context.Context, info *UpdateInfo) {
	cacheData := struct {
		Latest      string       `json:"latest"`
		ReleaseInfo *ReleaseInfo `json:"release_info"`
		Timestamp   int64        `json:"timestamp"`
	}{
		Latest:      info.LatestVersion,
		ReleaseInfo: info.ReleaseInfo,
		Timestamp:   time.Now().Unix(),
	}

	data, _ := json.Marshal(cacheData)
	_ = s.cache.SetUpdateInfo(ctx, string(data), time.Duration(updateCacheTTL)*time.Second)
}
func (s *ReleaseQuery) GetFromCache(ctx context.Context) (*UpdateInfo, error) {
	data, err := s.cache.GetUpdateInfo(ctx)
	if err != nil {
		return nil, err
	}

	var cached struct {
		Latest      string       `json:"latest"`
		ReleaseInfo *ReleaseInfo `json:"release_info"`
		Timestamp   int64        `json:"timestamp"`
	}
	if err := json.Unmarshal([]byte(data), &cached); err != nil {
		return nil, err
	}

	if time.Now().Unix()-cached.Timestamp > updateCacheTTL {
		return nil, fmt.Errorf("cache expired")
	}

	return &UpdateInfo{
		CurrentVersion: s.currentVersion,
		LatestVersion:  cached.Latest,
		HasUpdate:      compareVersions(s.currentVersion, cached.Latest) < 0,
		ReleaseInfo:    cached.ReleaseInfo,
		Cached:         true,
		BuildType:      s.buildType,
	}, nil
}
func (s *ReleaseQuery) FetchLatestRelease(ctx context.Context) (*UpdateInfo, error) {
	release, err := s.githubClient.FetchLatestRelease(ctx, githubRepo)
	if err != nil {
		return nil, err
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")

	assets := make([]Asset, len(release.Assets))
	for i, a := range release.Assets {
		assets[i] = Asset{
			Name:        a.Name,
			DownloadURL: a.BrowserDownloadURL,
			Size:        a.Size,
		}
	}

	return &UpdateInfo{
		CurrentVersion: s.currentVersion,
		LatestVersion:  latestVersion,
		HasUpdate:      compareVersions(s.currentVersion, latestVersion) < 0,
		ReleaseInfo: &ReleaseInfo{
			Name:        release.Name,
			Body:        release.Body,
			PublishedAt: release.PublishedAt,
			HTMLURL:     release.HTMLURL,
			Assets:      assets,
		},
		Cached:    false,
		BuildType: s.buildType,
	}, nil
}

// normalizeRollbackVersion 只接受可安全用于下载与手动命令展示的 v?MAJOR.MINOR.PATCH。
// 严格格式既保证排序语义，也防止 release tag 中的 shell 元字符进入复制命令。
func normalizeRollbackVersion(raw string) (string, bool) {
	version := strings.TrimSpace(raw)
	version = strings.TrimPrefix(version, "v")
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return "", false
	}
	for _, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return "", false
		}
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return "", false
			}
		}
		if _, err := strconv.Atoi(part); err != nil {
			return "", false
		}
	}
	return strings.Join(parts, "."), true
}

// NormalizeRollbackVersion 保留旧比较入口的唯一规则。
func NormalizeRollbackVersion(raw string) (string, bool) { return normalizeRollbackVersion(raw) }

// fetchRollbackCandidates 拉取最近 release，并只保留严格早于当前版本的最新候选。
func (s *ReleaseQuery) FetchRollbackCandidates(ctx context.Context) ([]*GitHubRelease, error) {
	releases, err := s.githubClient.FetchRecentReleases(ctx, githubRepo, rollbackFetchPageSize)
	if err != nil {
		return nil, err
	}

	currentVersion, ok := normalizeRollbackVersion(s.currentVersion)
	if !ok {
		return []*GitHubRelease{}, nil
	}

	seen := make(map[string]bool, len(releases))
	candidates := make([]*GitHubRelease, 0, maxRollbackVersions)
	for _, r := range releases {
		if r == nil || r.Draft || r.Prerelease {
			continue
		}
		v, valid := normalizeRollbackVersion(r.TagName)
		if !valid || seen[v] {
			continue
		}
		// 仅允许严格早于当前版本的正式语义版本，同时排除当前版本。
		if compareVersions(v, currentVersion) >= 0 {
			continue
		}
		seen[v] = true
		candidates = append(candidates, r)
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		left, _ := normalizeRollbackVersion(candidates[i].TagName)
		right, _ := normalizeRollbackVersion(candidates[j].TagName)
		return compareVersions(
			left,
			right,
		) > 0
	})

	if len(candidates) > maxRollbackVersions {
		candidates = candidates[:maxRollbackVersions]
	}
	return candidates, nil
}

// ListRollbackVersions 返回严格早于当前版本的最近正式版本，按新到旧排序。
// 草稿、预发布和非标准版本号均不会进入列表。
func (s *ReleaseQuery) ListRollbackVersions(ctx context.Context) ([]RollbackVersion, error) {
	releases, err := s.FetchRollbackCandidates(ctx)
	if err != nil {
		return nil, err
	}

	versions := make([]RollbackVersion, 0, len(releases))
	for _, r := range releases {
		version, _ := normalizeRollbackVersion(r.TagName)
		versions = append(versions, RollbackVersion{
			Version:     version,
			PublishedAt: r.PublishedAt,
			HTMLURL:     r.HTMLURL,
		})
	}
	return versions, nil
}

// CheckUpdate checks for available updates
func (s *ReleaseQuery) CheckUpdate(ctx context.Context, force bool) (*UpdateInfo, error) {
	// Try cache first
	if !force {
		if cached, err := s.GetFromCache(ctx); err == nil && cached != nil {
			return cached, nil
		}
	}

	// Fetch from GitHub
	info, err := s.FetchLatestRelease(ctx)
	if err != nil {
		// Return cached on error
		if cached, cacheErr := s.GetFromCache(ctx); cacheErr == nil && cached != nil {
			cached.Warning = "Using cached data: " + err.Error()
			return cached, nil
		}
		return &UpdateInfo{
			CurrentVersion: s.currentVersion,
			LatestVersion:  s.currentVersion,
			HasUpdate:      false,
			Warning:        err.Error(),
			BuildType:      s.buildType,
		}, nil
	}

	// Cache result
	s.SaveToCache(ctx, info)
	return info, nil
}

type GitHubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

// RollbackVersion 描述系统允许回退到的正式版本。
type RollbackVersion struct {
	Version     string `json:"version"` // 不带 v 前缀，例如 0.1.146。
	PublishedAt string `json:"published_at"`
	HTMLURL     string `json:"html_url"`
}

// GitHubRelease represents GitHub API response
type GitHubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	PublishedAt string        `json:"published_at"`
	HTMLURL     string        `json:"html_url"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	Assets      []GitHubAsset `json:"assets"`
}

// Asset represents a release asset
type Asset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"download_url"`
	Size        int64  `json:"size"`
}

// ReleaseInfo contains GitHub release details
type ReleaseInfo struct {
	Name        string  `json:"name"`
	Body        string  `json:"body"`
	PublishedAt string  `json:"published_at"`
	HTMLURL     string  `json:"html_url"`
	Assets      []Asset `json:"assets,omitempty"`
}

// UpdateInfo contains update information
type UpdateInfo struct {
	CurrentVersion string       `json:"current_version"`
	LatestVersion  string       `json:"latest_version"`
	HasUpdate      bool         `json:"has_update"`
	ReleaseInfo    *ReleaseInfo `json:"release_info,omitempty"`
	Cached         bool         `json:"cached"`
	Warning        string       `json:"warning,omitempty"`
	BuildType      string       `json:"build_type"` // "source" or "release"
}

// UpdateCache defines cache operations for update service
type UpdateCache interface {
	GetUpdateInfo(ctx context.Context) (string, error)
	SetUpdateInfo(ctx context.Context, data string, ttl time.Duration) error
}
