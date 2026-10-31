package index

import (
	"encoding/json"
	"strings"
	"testing"
)

// host_test.go checks catalog admission across host-version and schema boundaries.
// A future contribution remains visible for compatibility reporting, while current
// declarations and outer integrity metadata still receive strict validation before
// callers can select a release for installation.

// TestParseForHostDefersFutureContributionSchema keeps an unknown provider declaration in a catalog for an older released host.
// The same bytes fail strict publisher validation and also fail when the host claims to understand that newer release.
func TestParseForHostDefersFutureContributionSchema(t *testing.T) {
	data := hostCatalogFixture()
	document, err := ParseForHost(data, "v0.6.0")
	if err != nil {
		t.Fatal(err)
	}
	version := document.Plugins[0].Versions[0]
	if version.Manifest.MinHostVersion != "0.7.0" || !strings.Contains(string(version.ManifestJSON), `"providers"`) {
		t.Fatal("future declaration lost its compatibility or stored JSON")
	}
	for _, host := range []string{"0.7.0", "0.0.0-dev", "development"} {
		if _, err := ParseForHost(data, host); err == nil {
			t.Fatalf("host %s silently accepted unsupported current schema", host)
		}
	}
	if _, err := Parse(data); err == nil {
		t.Fatal("strict publisher parser accepted an unknown contribution")
	}
	if err := document.Validate(); err == nil {
		t.Fatal("strict validation trusted a deferred declaration")
	}
}

// TestParseForHostRetainsOuterValidation corrupts metadata around a deferred future declaration.
// The minimum-host exception never admits unsafe artifact URLs, malformed hashes, identity mismatches, or unknown catalog fields.
func TestParseForHostRetainsOuterValidation(t *testing.T) {
	for _, replacement := range [][2]string{
		{`"schemaVersion":1`, `"schemaVersion":2`},
		{`"name":"Catalog"`, `"name":"Catalog","unexpected":true`},
		{`https://example.invalid/plugin.tar.gz`, `http://example.invalid/plugin.tar.gz`},
		{strings.Repeat("a", 64), "bad"},
		{`"minHostVersion":"0.7.0"`, `"minHostVersion":"0.7"`},
		{`"id":"test.future","version"`, `"id":"test.other","version"`},
	} {
		data := strings.ReplaceAll(string(hostCatalogFixture()), replacement[0], replacement[1])
		if _, err := ParseForHost([]byte(data), "0.6.0"); err == nil {
			t.Errorf("accepted invalid replacement %q", replacement)
		}
	}
}

// hostCatalogFixture returns one future provider release with valid outer catalog integrity metadata.
func hostCatalogFixture() []byte {
	declaration := json.RawMessage(`{"schemaVersion":1,"id":"test.future","version":"1.0.0","apiVersion":1,"minHostVersion":"0.7.0","name":{"en":"Future"},"description":{"en":"Future provider"},"publisher":{"name":"Tests"},"license":"MIT","backend":{"executable":"bin/plugin"},"providers":[{"key":"future"}]}`)
	data := `{"schemaVersion":1,"name":"Catalog","generatedAt":"2026-11-01T00:00:00Z","plugins":[{"id":"test.future","repository":"https://github.com/example/plugin","categories":["integration"],"versions":[{"version":"1.0.0","publishedAt":"2026-11-01T00:00:00Z","manifest":` + string(declaration) + `,"manifestSha256":"` + strings.Repeat("a", 64) + `","artifacts":{"linux/amd64":{"url":"https://example.invalid/plugin.tar.gz","sha256":"` + strings.Repeat("b", 64) + `","size":100}}}]}]}`
	return []byte(data)
}
