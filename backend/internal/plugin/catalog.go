package plugin

import (
	"net/url"
	"slices"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// catalog.go selects market presentation and release recommendations without storage
// or network access. Each entry retains its source index identity, so a matching
// plugin ID from another source cannot become an implicit update authority. Host
// compatibility, withdrawal, prerelease status, and existing installation ownership
// each contribute independently to the final installation and recommendation state.
//
// Withdrawn entries remain inspectable when installed from that source, including
// current versions that have fallen outside the index's retained release window.
// Recommendations choose the highest compatible stable release that is still
// offered. The source document and installation snapshots remain immutable; result
// slices belong to the caller while embedded release metadata is read-only.

// CatalogVersion combines published release metadata with current host admission decisions.
type CatalogVersion struct {
	index.Version
	Compatible        bool                       `json:"compatible"`        // current host satisfies declared requirements
	Incompatibilities []manifest.Incompatibility `json:"incompatibilities"` // independent host admission failures
	Installable       bool                       `json:"installable"`       // host compatibility and source policy permit selection
}

// CatalogEntry describes one source-qualified plugin and its installed-source relationship.
type CatalogEntry struct {
	HostPlatform    string   `json:"host_platform"`      // current host archive target
	IndexID         int64    `json:"index_id"`           // owning catalog source identifier
	IndexName       string   `json:"index_name"`         // configured catalog display name
	Builtin         bool     `json:"builtin"`            // source is the built-in registration
	SignatureStatus string   `json:"signature_status"`   // latest source verification classification
	ID              string   `json:"id"`                 // publisher-qualified plugin identity
	Repository      string   `json:"repository"`         // registered HTTPS source repository
	Categories      []string `json:"categories"`         // registered discovery category names
	IconURL         string   `json:"icon_url,omitempty"` // resolved HTTPS presentation icon
	// Manifest uses stable release metadata when present; prerelease-only or retained
	// installed entries provide their available declaration as the presentation fallback.
	Manifest             *manifest.Manifest `json:"manifest,omitempty"`
	Versions             []CatalogVersion   `json:"versions"`                      // retained releases and current admission decisions
	Delisted             bool               `json:"delisted"`                      // source withdrew the plugin listing
	RecommendedVersion   string             `json:"recommended_version"`           // highest installable stable release
	InstalledVersion     string             `json:"installed_version,omitempty"`   // accepted local installation version
	InstalledFromSource  bool               `json:"installed_from_source"`         // installation belongs to this source index
	InstalledFromOther   bool               `json:"installed_from_other_source"`   // another source already owns this identity
	CurrentVersionYanked bool               `json:"current_version_yanked"`        // source withdrew the installed release
	CurrentYankReason    string             `json:"current_yank_reason,omitempty"` // published reason for current withdrawal
	UpdateVersion        string             `json:"update_version,omitempty"`      // recommended release newer than this installation
}

// selectCatalog projects one accepted index for a fixed host and installation snapshot.
// Disabled sources contribute nothing. Delisted plugins remain visible only when
// their local installation belongs to that exact source; other-source ownership blocks installation.
//
// Compatibility alone never overrides withdrawal or ownership restrictions. Stable
// recommendations skip prereleases even though an explicit compatible prerelease
// can remain selectable in detail views. Installed-version badges come from durable
// intent, so index retention does not erase the visible current version. Release
// ordering is already validated by the index parser and is preserved here.
func selectCatalog(source IndexSource, document *index.Document, installed map[string]Installation, host manifest.HostInfo) []CatalogEntry {
	result := make([]CatalogEntry, 0)
	if !source.Enabled || document == nil {
		return result
	}
	for _, entry := range document.Plugins {
		current, exists := installed[entry.ID]
		here := exists && current.Source == SourceIndex && current.IndexID != nil && *current.IndexID == source.ID
		if entry.Delisted && !here {
			continue
		}
		view := CatalogEntry{
			HostPlatform: host.Platform, IndexID: source.ID, IndexName: source.Name, Builtin: source.Builtin, SignatureStatus: source.SignatureStatus,
			ID: entry.ID, Repository: entry.Repository, Categories: slices.Clone(entry.Categories),
			IconURL: catalogIconURL(source.URL, entry.IconURL), Delisted: entry.Delisted,
			InstalledFromSource: here, InstalledFromOther: exists && !here,
			Versions: make([]CatalogVersion, 0, len(entry.Versions)),
		}
		if exists {
			view.InstalledVersion = current.Version
		}
		for _, release := range entry.Versions {
			// SemVer prerelease identifiers remain prereleases even when a custom index
			// omits its advisory flag; explicit publisher prerelease flags remain effective too.
			release.Prerelease = release.Prerelease || semver.Prerelease("v"+release.Version) != ""
			platforms := make([]string, 0, len(release.Artifacts))
			for platform := range release.Artifacts {
				platforms = append(platforms, platform)
			}
			slices.Sort(platforms)
			issues := manifest.CheckCompatibility(release.Manifest, platforms, host)
			version := CatalogVersion{Version: release, Compatible: len(issues) == 0, Incompatibilities: issues}
			version.Installable = version.Compatible && !release.Yanked && !entry.Delisted && !view.InstalledFromOther
			view.Versions = append(view.Versions, version)
			if view.Manifest == nil && !release.Prerelease {
				view.Manifest = release.Manifest
			}
			if here && release.Version == current.Version && release.Yanked {
				view.CurrentVersionYanked = true
				view.CurrentYankReason = release.YankReason
			}
			if view.RecommendedVersion == "" && version.Installable && !release.Prerelease {
				view.RecommendedVersion = release.Version
			}
		}
		// Prerelease-only listings still need an identity, while an installed entry
		// with no retained releases uses its accepted declaration instead of disappearing.
		if view.Manifest == nil {
			if len(entry.Versions) > 0 {
				view.Manifest = entry.Versions[0].Manifest
			} else if here {
				view.Manifest = current.Manifest
			}
		}
		if view.Manifest == nil {
			continue
		}
		if here && view.RecommendedVersion != "" && semver.Compare("v"+view.RecommendedVersion, "v"+current.Version) > 0 {
			view.UpdateVersion = view.RecommendedVersion
		}
		result = append(result, view)
	}
	return result
}

// catalogIconURL resolves a declared icon against the catalog's HTTPS directory.
// Invalid or missing locations produce an empty presentation value. This does
// not affect release admission or replace the backend's archive integrity checks.
func catalogIconURL(base, icon string) string {
	if icon == "" {
		return ""
	}
	location, err := url.Parse(base)
	if err != nil {
		return ""
	}
	reference, err := url.Parse(icon)
	if err != nil {
		return ""
	}
	resolved := location.ResolveReference(reference).String()
	if !index.HTTPSURL(resolved) {
		return ""
	}
	return resolved
}

// sortedCatalog sorts a caller-owned entry slice by source and plugin identity.
// It returns that same slice for response assembly, so callers must not pass
// a shared published cache slice whose ordering another reader still owns.
func sortedCatalog(entries []CatalogEntry) []CatalogEntry {
	slices.SortFunc(entries, func(left, right CatalogEntry) int {
		if left.IndexID < right.IndexID {
			return -1
		}
		if left.IndexID > right.IndexID {
			return 1
		}
		return strings.Compare(left.ID, right.ID)
	})
	return entries
}
