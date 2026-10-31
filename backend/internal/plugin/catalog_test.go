package plugin

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// catalog_test.go exercises source-qualified release selection without IO.
// Version fixtures cover withdrawal, prerelease visibility, host incompatibility,
// missing retained releases, and duplicate identities across indexes so recommendation
// rules cannot silently change an installed plugin's distribution authority.

// TestCatalogSelectsCompatibleStableRelease presents newer prerelease, yanked, and incompatible releases ahead of an eligible stable version.
// Only the eligible stable release is recommended, while detail metadata retains the reasons other releases cannot be installed by default.
func TestCatalogSelectsCompatibleStableRelease(t *testing.T) {
	pre := catalogRelease("1.3.0-beta.1")
	yanked := catalogRelease("1.2.0")
	yanked.Yanked = true
	future := catalogRelease("1.1.0")
	future.Manifest.MinHostVersion = "0.7.0"
	stable := catalogRelease("1.0.0")
	source, document, host := catalogFixture(pre, yanked, future, stable)
	entries := selectCatalog(source, document, nil, host)
	require.Len(t, entries, 1)
	require.Equal(t, "1.0.0", entries[0].RecommendedVersion)
	require.Equal(t, "1.2.0", entries[0].Manifest.Version)
	require.True(t, entries[0].Versions[0].Prerelease)
	require.True(t, entries[0].Versions[0].Installable)
	require.False(t, entries[0].Versions[1].Installable)
	require.Equal(t, "HOST_TOO_OLD", entries[0].Versions[2].Incompatibilities[0].Code)
	require.Equal(t, "https://example.invalid/v1/icons/plugin.svg", entries[0].IconURL)
}

// TestCatalogRetainsWithdrawnInstalledIdentity hides a delisted plugin from new installations but retains its source-owned current record.
// Yanked status remains visible, and deletion from the retained version window does not erase the installed version badge.
func TestCatalogRetainsWithdrawnInstalledIdentity(t *testing.T) {
	release := catalogRelease("1.0.0")
	release.Yanked = true
	release.YankReason = "Withdrawn for review"
	source, document, host := catalogFixture(release)
	document.Plugins[0].Delisted = true
	require.Empty(t, selectCatalog(source, document, nil, host))
	current := Installation{PluginID: "test.catalog", VersionSnapshot: VersionSnapshot{Version: "1.0.0", Source: SourceIndex, IndexID: &source.ID, Manifest: release.Manifest}}
	installed := map[string]Installation{current.PluginID: current}
	entries := selectCatalog(source, document, installed, host)
	require.Len(t, entries, 1)
	require.True(t, entries[0].CurrentVersionYanked)
	require.Equal(t, release.YankReason, entries[0].CurrentYankReason)
	require.Empty(t, entries[0].RecommendedVersion)
	document.Plugins[0].Versions = nil
	entries = selectCatalog(source, document, installed, host)
	require.Len(t, entries, 1)
	require.Equal(t, "1.0.0", entries[0].InstalledVersion)
}

// TestCatalogKeepsInstalledSourceAuthority compares identical plugin IDs from the accepted source and another index.
// An update belongs only to the accepted source, while duplicate entries remain visible but cannot claim installation authority.
func TestCatalogKeepsInstalledSourceAuthority(t *testing.T) {
	source, document, host := catalogFixture(catalogRelease("2.0.0"), catalogRelease("1.0.0"))
	current := Installation{PluginID: "test.catalog", VersionSnapshot: VersionSnapshot{Version: "1.0.0", Source: SourceIndex, IndexID: &source.ID, Manifest: catalogRelease("1.0.0").Manifest}}
	installed := map[string]Installation{current.PluginID: current}
	entries := selectCatalog(source, document, installed, host)
	require.Equal(t, "2.0.0", entries[0].UpdateVersion)
	other := source
	other.ID++
	entries = selectCatalog(other, document, installed, host)
	require.True(t, entries[0].InstalledFromOther)
	require.Empty(t, entries[0].RecommendedVersion)
	require.Empty(t, entries[0].UpdateVersion)
	require.False(t, entries[0].Versions[0].Installable)
	source.Enabled = false
	require.Empty(t, selectCatalog(source, document, installed, host))
}
