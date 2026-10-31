package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPluginDirectoryPrecedence checks explicit storage before deployment and
// existing data roots. Normalization must derive a plugin directory without
// creating files or changing a configured administrator path.
func TestPluginDirectoryPrecedence(t *testing.T) {
	t.Setenv("DATA_DIR", "/deployment/data")
	p := PluginsConfig{}
	p.normalize("/pricing/data")
	if p.Dir != filepath.Join("/deployment/data", "plugins") {
		t.Fatal(p.Dir)
	}
	p.Dir = " /explicit/plugins "
	p.normalize("/pricing/data")
	if p.Dir != "/explicit/plugins" {
		t.Fatal(p.Dir)
	}
	t.Setenv("DATA_DIR", "")
	p.Dir = ""
	p.normalize("/pricing/data")
	if p.Dir != filepath.Join("/pricing/data", "plugins") {
		t.Fatal(p.Dir)
	}
}

// TestPluginValidationRejectsUnsafeSchemes checks download and child proxy policies.
// Invalid settings must fail before startup, and error messages must not echo
// embedded passwords from administrator-supplied proxy URLs.
func TestPluginValidationRejectsUnsafeSchemes(t *testing.T) {
	for _, p := range []PluginsConfig{
		{Enabled: true, MaxRunning: 0},
		{OfficialIndexURL: "http://example.com/index.json"},
		{OfficialIndexURL: "https://user:secret@example.com/index.json"},
		{DownloadURLPrefix: "file:///tmp/"},
		{DownloadProxy: "ftp://user:secret@example.com"},
		{PluginHTTPProxy: "file:///tmp/proxy"},
	} {
		err := p.validate()
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe validation result: %v", err)
		}
	}
	if err := (PluginsConfig{Enabled: true, MaxRunning: 20, DownloadProxy: "socks5://localhost:1080", PluginHTTPProxy: "http://localhost:8080"}).validate(); err != nil {
		t.Fatal(err)
	}
}
