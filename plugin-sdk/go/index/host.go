package index

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"golang.org/x/mod/semver"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// host.go decodes catalogs for hosts that may precede a publisher's manifest schema.
// The outer catalog remains strict, while each embedded declaration stays opaque
// until its minimum host version has been inspected. A newer release can therefore
// remain visible as incompatible without making older, usable releases disappear
// merely because the newer declaration adds a contribution the current SDK lacks.
//
// Only released hosts below a valid minimum receive that exception. Current-host
// releases and development hosts still use the complete strict manifest parser.
// Deferred declarations retain their original embedded JSON for storage and later
// host upgrades, while common release identity and artifact metadata remain validated.
// Callers must perform compatibility checks before constructing an installation.

// hostDocument shadows only the plugin collection while retaining ordinary catalog metadata.
type hostDocument struct {
	Document
	Plugins []hostPlugin `json:"plugins"` // declarations awaiting host-aware manifest parsing
}

// hostPlugin shadows only releases while preserving registered repository and discovery metadata.
type hostPlugin struct {
	Plugin
	Versions []hostVersion `json:"versions"` // releases with opaque embedded declarations
}

// hostVersion preserves the embedded JSON without relaxing surrounding release field validation.
type hostVersion struct {
	Version
	Manifest json.RawMessage `json:"manifest"` // original embedded declaration bytes
}

// ParseForHost validates a catalog while deferring declarations requiring a newer released host.
// Signature verification must precede this call and still uses the original bytes.
// Deferred manifests are presentation metadata only; installers must reject their host-version incompatibility.
//
// Version inspection happens before strict declaration parsing, allowing a future
// providers contribution to coexist with current page plugins in one signed index.
// Catalog fields and common manifest field types remain strict enough to render
// identity and compatibility, but deferred contribution semantics are not claimed
// to be understood. Parse remains the strict publisher-facing validation entry point.
func ParseForHost(data []byte, hostVersion string) (*Document, error) {
	var wire hostDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return nil, fmt.Errorf("decode index: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("index must contain one JSON object")
	}
	document := wire.Document
	document.Plugins = make([]Plugin, 0, len(wire.Plugins))
	for _, source := range wire.Plugins {
		entry := source.Plugin
		entry.Versions = make([]Version, 0, len(source.Versions))
		for _, release := range source.Versions {
			version := release.Version
			declaration, err := parseHostManifest(release.Manifest, hostVersion)
			if err != nil {
				return nil, fmt.Errorf("manifest for %s@%s: %w", entry.ID, version.Version, err)
			}
			version.Manifest = declaration
			version.ManifestJSON = bytes.Clone(release.Manifest)
			entry.Versions = append(entry.Versions, version)
		}
		document.Plugins = append(document.Plugins, entry)
	}
	if err := document.validateMetadata(); err != nil {
		return nil, err
	}
	return &document, nil
}

// parseHostManifest chooses strict validation after inspecting the minimum released host.
// A valid newer minimum permits unknown contribution fields for display only.
// Invalid bounds, current releases, and development hosts receive the ordinary parser and its complete validation.
func parseHostManifest(data []byte, hostVersion string) (*manifest.Manifest, error) {
	var header struct {
		MinHostVersion string `json:"minHostVersion"` // declared minimum released host
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, err
	}
	version := strings.TrimPrefix(hostVersion, "v")
	future := version != "0.0.0-dev" && manifest.ValidVersion(version) &&
		manifest.ValidVersion(header.MinHostVersion) && semver.Compare("v"+version, "v"+header.MinHostVersion) < 0
	if !future {
		return manifest.Parse(data)
	}
	// Preserve known presentation fields without interpreting newer contribution semantics;
	// the caller's host-version check prevents this deferred declaration from entering execution.
	var result manifest.Manifest
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
