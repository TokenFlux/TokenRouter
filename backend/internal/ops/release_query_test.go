package ops

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	// bilingualReleaseBody exercises the complete Markdown structure retained for
	// administrator changelog rendering, including leading and trailing newlines.
	bilingualReleaseBody = "\n## Changes\n\n- Preserve `release_info.body` & release metadata.\n\n" +
		"```sh\nprintf 'hello\\n'\n```\n\n" +
		"<details>\n<summary>简体中文</summary>\n\n## 更新内容\n\n" +
		"- 保留完整正文、代码块和换行。\n\n```text\n第一行\n第二行\n```\n\n</details>\n"
)

// updateServiceCacheStub supplies one mutable serialized release value for
// query fixtures.
type updateServiceCacheStub struct {
	// data is the latest value written by SetUpdateInfo, with empty text
	// interpreted as a cache miss. The fixture records the requested expiration
	// without enforcing it and is used sequentially.
	data     string        // serialized cached release data
	ttl      time.Duration // last requested cache lifetime
	getCalls int           // recorded cache read count
	setCalls int           // recorded cache write count
}

// updateServiceGitHubClientStub returns supplied metadata records for
// deterministic release-query scenarios.
type updateServiceGitHubClientStub struct {
	// release is returned unchanged with latestErr, including nil when unset.
	// Successful latest lookups supply a usable record for the dereferencing query.
	release *GitHubRelease
	// latestErr controls latest lookup independently of the configured release.
	// ReleaseQuery discards that record when the supplied error is nonnil.
	latestErr   error
	latestCalls int    // recorded latest lookup count
	latestRepo  string // last queried repository identity
	// recentReleases is the supplied slice returned by recent-page lookup without
	// copying records or their assets. Candidate queries retain some of those same
	// pointers.
	recentReleases []*GitHubRelease
	// recentErr is returned alongside recentReleases, even when that slice is
	// nonempty. The query discards records when this configured error is nonnil.
	recentErr error
}

// GetUpdateInfo returns fixture data or a cache-miss error when it is empty.
// Context and cache expiration are not modeled here, leaving JSON age checking
// to the query under test.
func (s *updateServiceCacheStub) GetUpdateInfo(context.Context) (string, error) {
	s.getCalls++
	if s.data == "" {
		return "", errors.New("cache miss")
	}
	return s.data, nil
}

// SetUpdateInfo replaces fixture data and records the requested lifetime and
// write count. Context is ignored and expiration is not enforced; subsequent
// reads retain the supplied text until another write.
func (s *updateServiceCacheStub) SetUpdateInfo(_ context.Context, data string, ttl time.Duration) error {
	s.data = data
	s.ttl = ttl
	s.setCalls++
	return nil
}

// FetchLatestRelease records the repository and call count before returning
// the configured release and error. It ignores context and shares the release
// pointer; successful callers must supply a nonnil record.
func (s *updateServiceGitHubClientStub) FetchLatestRelease(_ context.Context, repo string) (*GitHubRelease, error) {
	s.latestCalls++
	s.latestRepo = repo
	return s.release, s.latestErr
}

// FetchRecentReleases returns the supplied recent slice and configured error
// while ignoring repository, page count, and context. Records remain shared, so
// tests keep their fixture metadata stable during candidate selection.
func (s *updateServiceGitHubClientStub) FetchRecentReleases(context.Context, string, int) ([]*GitHubRelease, error) {
	return s.recentReleases, s.recentErr
}

// DownloadFile panics unconditionally to reject asset transfers through this
// release-query fixture. The method implements no download behavior; metadata
// query services never call it, and installation tests need a downloader
// fixture.
func (s *updateServiceGitHubClientStub) DownloadFile(context.Context, string, string, int64) error {
	panic("DownloadFile should not be called when no update is available")
}

// FetchChecksumFile panics unconditionally when asked for checksum bytes
// through this metadata fixture. Query construction supplies only metadata
// methods, and callers use a checksum-capable fixture when exercising
// installation rather than release selection.
func (s *updateServiceGitHubClientStub) FetchChecksumFile(context.Context, string) ([]byte, error) {
	panic("FetchChecksumFile should not be called when no update is available")
}

// newRollbackTestService retains the supplied current version and recent
// release pointers in a ReleaseQuery with an empty cache and release build
// label. It performs no request; tests keep those borrowed records stable while
// querying.
func newRollbackTestService(current string, releases []*GitHubRelease) *ReleaseQuery {
	return NewReleaseQuery(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentReleases: releases},
		current,
		"release",
	)
}

// TestReleaseQueryFetchLatestReleasePreservesChangelog supplies complete and
// empty release bodies through the metadata client. It checks version mapping,
// publication metadata, assets, and exact Markdown content remain associated.
func TestReleaseQueryFetchLatestReleasePreservesChangelog(t *testing.T) {
	for _, tc := range []struct {
		name string // release body scenario name
		body string // supplied release Markdown body
	}{
		{name: "bilingual Markdown", body: bilingualReleaseBody},
		{name: "empty body", body: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &updateServiceGitHubClientStub{release: changelogTestRelease(tc.body)}
			query := NewReleaseQuery(&updateServiceCacheStub{}, client, "1.1.0", "source")

			info, err := query.FetchLatestRelease(t.Context())

			require.NoError(t, err)
			require.Equal(t, &UpdateInfo{
				CurrentVersion: "1.1.0",
				LatestVersion:  "1.2.0",
				HasUpdate:      true,
				ReleaseInfo: &ReleaseInfo{
					Name:        "Release 1.2.0",
					Body:        tc.body,
					PublishedAt: "2026-11-01T04:05:06Z",
					HTMLURL:     "https://github.com/TokenFlux/TokenRouter/releases/tag/v1.2.0",
					Assets: []Asset{{
						Name:        "tokenrouter_linux_amd64.tar.gz",
						DownloadURL: "https://github.com/TokenFlux/TokenRouter/releases/download/v1.2.0/tokenrouter_linux_amd64.tar.gz",
						Size:        4096,
					}},
				},
				BuildType: "source",
			}, info)
			require.Equal(t, 1, client.latestCalls)
			require.Equal(t, "TokenFlux/TokenRouter", client.latestRepo)
		})
	}
}

// TestReleaseQueryCheckUpdateCachesCompleteRelease reads each release from
// the client and then the serialized cache. It protects exact body round trips,
// twenty-minute storage, and cache hits that avoid another remote lookup.
func TestReleaseQueryCheckUpdateCachesCompleteRelease(t *testing.T) {
	for _, tc := range []struct {
		name string // release body scenario name
		body string // supplied release Markdown body
	}{
		{name: "bilingual Markdown", body: bilingualReleaseBody},
		{name: "empty body", body: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &updateServiceCacheStub{}
			client := &updateServiceGitHubClientStub{release: changelogTestRelease(tc.body)}
			query := NewReleaseQuery(cache, client, "1.1.0", "release")

			fresh, err := query.CheckUpdate(t.Context(), false)
			require.NoError(t, err)
			require.False(t, fresh.Cached)
			require.Equal(t, tc.body, fresh.ReleaseInfo.Body)
			require.Equal(t, 20*time.Minute, cache.ttl)
			require.Equal(t, 1, cache.setCalls)

			cached, err := query.CheckUpdate(t.Context(), false)
			require.NoError(t, err)
			expected := *fresh
			expected.Cached = true
			require.Equal(t, &expected, cached)
			require.NotSame(t, fresh.ReleaseInfo, cached.ReleaseInfo)
			require.Equal(t, 1, client.latestCalls)
			require.Equal(t, 2, cache.getCalls)
			require.Equal(t, 1, cache.setCalls)
		})
	}
}

// TestReleaseQueryCheckUpdateForceRefreshReplacesCachedRelease warms a cache
// before publishing different metadata through the client. It checks forced
// refresh skips the old cache and replaces version and body together.
func TestReleaseQueryCheckUpdateForceRefreshReplacesCachedRelease(t *testing.T) {
	cache := &updateServiceCacheStub{}
	client := &updateServiceGitHubClientStub{release: changelogTestRelease("Previous release notes")}
	query := NewReleaseQuery(cache, client, "1.1.0", "release")
	_, err := query.CheckUpdate(t.Context(), false)
	require.NoError(t, err)

	client.release = changelogTestRelease(bilingualReleaseBody)
	client.release.TagName = "v1.3.0"
	client.release.Name = "Release 1.3.0"
	client.release.PublishedAt = "2026-11-02T04:05:06Z"
	client.release.HTMLURL = "https://github.com/TokenFlux/TokenRouter/releases/tag/v1.3.0"
	client.release.Assets = nil

	fresh, err := query.CheckUpdate(t.Context(), true)

	require.NoError(t, err)
	require.False(t, fresh.Cached)
	require.True(t, fresh.HasUpdate)
	require.Empty(t, fresh.Warning)
	require.Equal(t, "1.3.0", fresh.LatestVersion)
	require.Equal(t, "Release 1.3.0", fresh.ReleaseInfo.Name)
	require.Equal(t, bilingualReleaseBody, fresh.ReleaseInfo.Body)
	require.Equal(t, client.release.PublishedAt, fresh.ReleaseInfo.PublishedAt)
	require.Equal(t, client.release.HTMLURL, fresh.ReleaseInfo.HTMLURL)
	require.Equal(t, 2, client.latestCalls)
	require.Equal(t, 1, cache.getCalls)
	require.Equal(t, 2, cache.setCalls)
	require.Equal(t, 20*time.Minute, cache.ttl)

	cached, err := query.CheckUpdate(t.Context(), false)
	require.NoError(t, err)
	require.True(t, cached.Cached)
	require.Equal(t, fresh.LatestVersion, cached.LatestVersion)
	require.Equal(t, fresh.ReleaseInfo.Body, cached.ReleaseInfo.Body)
	require.Equal(t, fresh.ReleaseInfo.HTMLURL, cached.ReleaseInfo.HTMLURL)
	require.Equal(t, 2, client.latestCalls)
}

// TestReleaseQueryCheckUpdateForceFailurePreservesCachedRelease warms the
// cache before a forced remote failure. It checks fallback retains all release
// metadata and adds a warning without overwriting cached content or diagnostics.
func TestReleaseQueryCheckUpdateForceFailurePreservesCachedRelease(t *testing.T) {
	cache := &updateServiceCacheStub{}
	client := &updateServiceGitHubClientStub{release: changelogTestRelease(bilingualReleaseBody)}
	query := NewReleaseQuery(cache, client, "1.1.0", "release")
	fresh, err := query.CheckUpdate(t.Context(), false)
	require.NoError(t, err)
	savedData := cache.data
	client.latestErr = errors.New("github unavailable")

	cached, err := query.CheckUpdate(t.Context(), true)

	require.NoError(t, err)
	expected := *fresh
	expected.Cached = true
	expected.Warning = "Using cached data: github unavailable"
	require.Equal(t, &expected, cached)
	require.Equal(t, savedData, cache.data)
	require.Equal(t, 2, client.latestCalls)
	require.Equal(t, 2, cache.getCalls)
	require.Equal(t, 1, cache.setCalls)

	next, err := query.CheckUpdate(t.Context(), false)
	require.NoError(t, err)
	expected.Warning = ""
	require.Equal(t, &expected, next)
	require.Equal(t, 2, client.latestCalls)
}

// changelogTestRelease creates fresh release and asset records for body
// preservation scenarios.
func changelogTestRelease(body string) *GitHubRelease {
	return &GitHubRelease{
		TagName:     "v1.2.0",
		Name:        "Release 1.2.0",
		Body:        body,
		PublishedAt: "2026-11-01T04:05:06Z",
		HTMLURL:     "https://github.com/TokenFlux/TokenRouter/releases/tag/v1.2.0",
		Assets: []GitHubAsset{{
			Name:               "tokenrouter_linux_amd64.tar.gz",
			BrowserDownloadURL: "https://github.com/TokenFlux/TokenRouter/releases/download/v1.2.0/tokenrouter_linux_amd64.tar.gz",
			Size:               4096,
		}},
	}
}

// TestUpdateServiceListRollbackVersionsFiltersAndCaps supplies current/newer
// versions, a prerelease, a draft, duplicates, and more eligible history than
// the display cap. It checks the newest three older stable normalized versions
// remain.
func TestUpdateServiceListRollbackVersionsFiltersAndCaps(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.148", PublishedAt: "2026-07-09T00:00:00Z"},                       // Exclude versions newer than the current version.
		{TagName: "v0.1.147", PublishedAt: "2026-07-08T00:00:00Z"},                       // Exclude the current version.
		{TagName: "v0.1.146-rc1", PublishedAt: "2026-07-07T12:00:00Z", Prerelease: true}, // Exclude prereleases.
		{TagName: "v0.1.146", PublishedAt: "2026-07-07T00:00:00Z"},
		{TagName: "v0.1.145", PublishedAt: "2026-07-06T00:00:00Z", Draft: true}, // Exclude drafts.
		{TagName: "v0.1.144", PublishedAt: "2026-07-05T00:00:00Z"},
		{TagName: "v0.1.144", PublishedAt: "2026-07-05T00:00:00Z"}, // Exclude duplicate versions.
		{TagName: "v0.1.143", PublishedAt: "2026-07-04T00:00:00Z"},
		{TagName: "v0.1.142", PublishedAt: "2026-07-03T00:00:00Z"}, // Exclude entries beyond the three-version limit.
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.146", versions[0].Version)
	require.Equal(t, "0.1.144", versions[1].Version)
	require.Equal(t, "0.1.143", versions[2].Version)
}

// TestUpdateServiceListRollbackVersionsSortsUnorderedInput supplies three
// eligible older releases in mixed numeric order. It checks display values are
// ordered newest first independently of the fixture response sequence.
func TestUpdateServiceListRollbackVersionsSortsUnorderedInput(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.144"},
		{TagName: "v0.1.146"},
		{TagName: "v0.1.145"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, "0.1.146", versions[0].Version)
	require.Equal(t, "0.1.145", versions[1].Version)
	require.Equal(t, "0.1.144", versions[2].Version)
}

// TestUpdateServiceListRollbackVersionsRejectsNonSemverTags supplies a numeric
// release alongside shell-like text, an alternate prefix, leading zeros, and
// build metadata. It checks strict rollback spelling accepts only the listed
// canonical numeric version.
func TestUpdateServiceListRollbackVersionsRejectsNonSemverTags(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.146"},
		{TagName: "v0.1.145;$(touch /tmp/tokenrouter-pwned)"},
		{TagName: "release-0.1.144"},
		{TagName: "v0.1.0143"},
		{TagName: "v0.1.142+meta"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Equal(t, []RollbackVersion{{Version: "0.1.146"}}, versions)
}

// TestUpdateServiceListRollbackVersionsEmptyWhenNoneOlder supplies the running
// version and a newer release as its entire recent page. It checks candidate
// display returns an empty result with no error.
func TestUpdateServiceListRollbackVersionsEmptyWhenNoneOlder(t *testing.T) {
	releases := []*GitHubRelease{
		{TagName: "v0.1.147"},
		{TagName: "v0.1.148"},
	}
	svc := newRollbackTestService("0.1.147", releases)

	versions, err := svc.ListRollbackVersions(context.Background())

	require.NoError(t, err)
	require.Empty(t, versions)
}

// TestUpdateServiceListRollbackVersionsPropagatesFetchError injects a
// recent-release reader error for a valid running version. It checks the
// display query returns that failure text instead of a successful candidate
// list.
func TestUpdateServiceListRollbackVersionsPropagatesFetchError(t *testing.T) {
	svc := NewReleaseQuery(
		&updateServiceCacheStub{},
		&updateServiceGitHubClientStub{recentErr: errors.New("github unavailable")},
		"0.1.147",
		"release",
	)

	_, err := svc.ListRollbackVersions(context.Background())

	require.Error(t, err)
	require.Contains(t, err.Error(), "github unavailable")
}
