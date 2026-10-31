package catalogbuild

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/mod/semver"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pkgfmt"
)

// build.go turns registered GitHub releases into validated catalog entries.
// Every newly admitted package is downloaded, checked against checksums.txt,
// extracted under shared limits, and compared byte-for-byte with its release
// manifest. A successful cached repository/tag keeps its original artifact
// digests even if the publisher later replaces that GitHub asset.
//
// Failed releases retain diagnostics and can be checked again on a later run,
// allowing publishers to complete an interrupted release upload. Moderation
// controls are reapplied from current registrations without redownloading
// admitted versions. The final catalog keeps at most thirty versions per
// plugin in descending SemVer order, and stores copied icon bytes separately
// from package data for publication alongside the signed document.

// State retains immutable admitted releases and diagnostics for failed ingestion.
type State struct {
	Entries map[string]Entry `json:"entries"` // cached repository/tag ingestion results
}

// Entry records one release's validated metadata or its most recent failure.
type Entry struct {
	Version       *index.Version `json:"version,omitempty"`       // admitted immutable release metadata
	Icon          []byte         `json:"icon,omitempty"`          // validated display icon bytes
	IconExtension string         `json:"iconExtension,omitempty"` // original icon file extension
	Error         string         `json:"error,omitempty"`         // last ingestion failure explanation
}

// Result contains publishable catalog data and reusable ingestion state.
type Result struct {
	Document *index.Document   // validated catalog document
	State    State             // updated release ingestion state
	Icons    map[string][]byte // catalog-relative display icon files
	Failures []string          // rejected release diagnostics
}

// Build validates releases, reuses admitted artifacts, and applies moderation.
// Repository listing failures abort the build, preserving the previous published
// index at the caller. Invalid individual releases are recorded and omitted.
//
// Cached successes are copied before current yank controls are applied, so
// future runs cannot accidentally persist changed release identity or digests.
// The returned state is independently allocated; callers publish it only with
// the matching document and icons after signing succeeds. Source operations
// receive the caller's context throughout download and validation work.
func Build(ctx context.Context, registry *Registry, source Source, previous State, name string, now time.Time) (*Result, error) {
	stateBytes, err := json.Marshal(previous)
	if err != nil {
		return nil, err
	}
	var copied State
	if err := json.Unmarshal(stateBytes, &copied); err != nil {
		return nil, err
	}
	previous = copied
	result := &Result{Document: &index.Document{SchemaVersion: 1, Name: name, GeneratedAt: now.UTC()}, State: State{Entries: make(map[string]Entry)}, Icons: make(map[string][]byte)}
	maps.Copy(result.State.Entries, previous.Entries)
	for _, registration := range registry.Plugins {
		releases, err := source.Releases(ctx, registration.Repository)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", registration.ID, err)
		}
		plugin := index.Plugin{ID: registration.ID, Repository: registration.Repository, Categories: registration.Categories, Delisted: registration.Delisted}
		icons := make(map[string]Entry)
		for _, release := range releases {
			version, eligible := eligibleRelease(registration, release)
			if !eligible {
				continue
			}
			key := registration.Repository + "@" + release.Tag
			entry, found := previous.Entries[key]
			if !found || entry.Version == nil {
				entry, err = ingestRelease(ctx, source, registration.ID, version, release)
				if err != nil {
					entry = Entry{Error: err.Error()}
				}
			}
			result.State.Entries[key] = entry
			if entry.Version == nil {
				result.Failures = append(result.Failures, key+": "+entry.Error)
				continue
			}
			if entry.Version.Manifest == nil || entry.Version.Manifest.ID != registration.ID {
				return nil, fmt.Errorf("cached release identity conflicts with registration %s", registration.ID)
			}
			current := *entry.Version
			current.Yanked = false
			current.YankReason = ""
			for _, yank := range registration.Yanked {
				if yank.Version == version {
					current.Yanked = true
					current.YankReason = yank.Reason
				}
			}
			plugin.Versions = append(plugin.Versions, current)
			icons[version] = entry
		}
		slices.SortStableFunc(plugin.Versions, func(a, b index.Version) int { return -semver.Compare("v"+a.Version, "v"+b.Version) })
		if len(plugin.Versions) > 30 {
			plugin.Versions = plugin.Versions[:30]
		}
		for _, version := range plugin.Versions {
			if version.Prerelease {
				continue
			}
			entry := icons[version.Version]
			if len(entry.Icon) > 0 {
				plugin.IconURL = "icons/" + registration.ID + entry.IconExtension
				result.Icons[plugin.IconURL] = entry.Icon
			}
			break
		}
		result.Document.Plugins = append(result.Document.Plugins, plugin)
	}
	if err := result.Document.Validate(); err != nil {
		return nil, err
	}
	return result, nil
}

// eligibleRelease filters drafts, malformed tags, and versions below registration bounds.
func eligibleRelease(registration Registration, release Release) (string, bool) {
	version, ok := strings.CutPrefix(release.Tag, "v")
	if release.Draft || !ok || !manifest.ValidVersion(version) {
		return "", false
	}
	if registration.MinVersion != "" && semver.Compare("v"+version, "v"+registration.MinVersion) < 0 {
		return "", false
	}
	return version, true
}

// ingestRelease admits a release only after every advertised canonical archive validates.
// Backend releases require both Linux architectures. Original manifest bytes
// are compared in every package, preserving the release asset as the single
// identity authority despite platform-specific binary contents.
//
// Archive digests come from checksums.txt and are independently recomputed over
// downloaded bytes before extraction. Only after all packages succeed does the
// function return a cacheable Version. Temporary extraction trees are discarded
// per artifact, leaving only validated metadata and bounded icon bytes in state.
func ingestRelease(ctx context.Context, source Source, id, version string, release Release) (Entry, error) {
	assets := make(map[string]ReleaseAsset)
	for _, asset := range release.Assets {
		if _, duplicate := assets[asset.Name]; duplicate {
			return Entry{}, fmt.Errorf("duplicate release asset %s", asset.Name)
		}
		assets[asset.Name] = asset
	}
	manifestAsset, ok := assets["plugin.json"]
	if !ok {
		return Entry{}, fmt.Errorf("missing plugin.json release asset")
	}
	checksumsAsset, ok := assets["checksums.txt"]
	if !ok {
		return Entry{}, fmt.Errorf("missing checksums.txt release asset")
	}
	raw, err := source.Download(ctx, manifestAsset.URL, 1<<20)
	if err != nil {
		return Entry{}, err
	}
	m, err := manifest.Parse(raw)
	if err != nil {
		return Entry{}, err
	}
	if m.ID != id || m.Version != version {
		return Entry{}, fmt.Errorf("manifest identity differs from registered release")
	}
	checksumsRaw, err := source.Download(ctx, checksumsAsset.URL, 1<<20)
	if err != nil {
		return Entry{}, err
	}
	checksums, err := parseChecksums(checksumsRaw)
	if err != nil {
		return Entry{}, err
	}
	digest := sha256.Sum256(raw)
	entry := Entry{Version: &index.Version{Version: version, PublishedAt: release.PublishedAt, Prerelease: release.Prerelease || semver.Prerelease("v"+version) != "", Manifest: m, ManifestSHA256: hex.EncodeToString(digest[:]), Artifacts: make(map[string]index.Artifact)}}
	entry.Version.Changelog = truncateChangelog(release.Body)
	platforms := pkgfmt.Platforms()
	if m.Backend == nil {
		platforms = []string{"any"}
	}
	for _, platform := range platforms {
		filename := pkgfmt.Filename(id, version, platform)
		expected, listed := checksums[filename]
		asset, present := assets[filename]
		if !listed && !present {
			continue
		}
		if !listed || !present {
			return Entry{}, fmt.Errorf("package and checksum must both exist for %s", filename)
		}
		if !index.HTTPSURL(asset.URL) || asset.Size <= 0 || asset.Size > pkgfmt.MaxArchiveBytes {
			return Entry{}, fmt.Errorf("invalid package metadata for %s", filename)
		}
		data, err := source.Download(ctx, asset.URL, pkgfmt.MaxArchiveBytes)
		if err != nil {
			return Entry{}, err
		}
		digest := sha256.Sum256(data)
		if expected != hex.EncodeToString(digest[:]) || int64(len(data)) != asset.Size {
			return Entry{}, fmt.Errorf("package digest or size mismatch for %s", filename)
		}
		icon, err := inspectReleaseArchive(data, raw, platform, m.Icon)
		if err != nil {
			return Entry{}, fmt.Errorf("%s: %w", filename, err)
		}
		if len(entry.Icon) == 0 {
			entry.Icon = icon
			entry.IconExtension = path.Ext(m.Icon)
		}
		entry.Version.Artifacts[platform] = index.Artifact{URL: asset.URL, SHA256: expected, Size: int64(len(data))}
	}
	if m.Backend != nil {
		for _, platform := range []string{"linux/amd64", "linux/arm64"} {
			if _, ok := entry.Version.Artifacts[platform]; !ok {
				return Entry{}, fmt.Errorf("missing required platform %s", platform)
			}
		}
	} else if _, ok := entry.Version.Artifacts["any"]; !ok {
		return Entry{}, fmt.Errorf("missing static any package")
	}
	return entry, nil
}

// inspectReleaseArchive validates one package and copies only its bounded icon.
// Temporary files never survive the function. The manifest comparison uses
// original release bytes rather than a reserialized declaration object.
func inspectReleaseArchive(data, manifestBytes []byte, platform, iconPath string) ([]byte, error) {
	directory, err := os.MkdirTemp("", "trplugin-release-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	packageDir := filepath.Join(directory, "package")
	if _, err := pkgfmt.Extract(bytes.NewReader(data), packageDir, pkgfmt.Options{Platform: platform, ManifestBytes: manifestBytes}); err != nil {
		return nil, err
	}
	if iconPath == "" {
		return nil, nil
	}
	return os.ReadFile(filepath.Join(packageDir, filepath.FromSlash(iconPath)))
}

// parseChecksums accepts unique sha256sum-style entries with portable release filenames.
// SemVer build metadata may contain plus signs. The returned map retains the
// exact filename so its digest still binds the original GitHub asset name.
func parseChecksums(data []byte) (map[string]string, error) {
	result := make(map[string]string)
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !index.ValidSHA256(fields[0]) {
			return nil, fmt.Errorf("invalid checksums.txt entry")
		}
		name := strings.TrimPrefix(fields[1], "*")
		// Release filenames can include SemVer build metadata; validate equivalent
		// portable characters while retaining the original spelling for exact asset lookup.
		if !manifest.ValidPath(strings.ReplaceAll(name, "+", "-")) || strings.Contains(name, "/") || result[name] != "" {
			return nil, fmt.Errorf("invalid or duplicate checksum filename")
		}
		result[name] = fields[0]
	}
	return result, nil
}

// truncateChangelog keeps release notes within 4096 bytes without splitting UTF-8.
// Long Markdown remains plain source text for downstream sanitized rendering.
// The truncation does not attempt to balance author-provided Markdown delimiters.
func truncateChangelog(value string) string {
	if len(value) <= 4096 {
		return value
	}
	// Back up to a rune boundary after the byte cap so catalog JSON never replaces a split UTF-8 sequence.
	value = value[:4096]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
