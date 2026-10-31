package catalogbuild

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pkgfmt"
)

// build_test.go checks catalog generation with immutable release and archive fixtures.
// The cases verify first-admitted digest retention, bounded package validation, and
// release filename handling without depending on remote GitHub state or executing
// the binaries contained in accepted archives.

// fixtureSource supplies immutable release metadata and counted in-memory downloads.
type fixtureSource struct {
	releases  []Release         // available fixture release metadata
	assets    map[string][]byte // downloadable fixture asset bytes
	downloads int               // observed asset download count
}

// Releases returns the fixture's declared release metadata without network access.
func (s *fixtureSource) Releases(context.Context, string) ([]Release, error) { return s.releases, nil }

// Download returns fixture bytes while preserving the source's bounded-read contract.
func (s *fixtureSource) Download(_ context.Context, address string, limit int64) ([]byte, error) {
	s.downloads++
	data, ok := s.assets[address]
	if !ok || int64(len(data)) > limit {
		return nil, fmt.Errorf("fixture asset unavailable or oversized")
	}
	return data, nil
}

// Authorized accepts fixture authors without querying a remote organization directory.
func (*fixtureSource) Authorized(context.Context, string, []string) (bool, error) { return true, nil }

// TestBuildCachesFirstAcceptedDigestsAndAppliesYanks ingests a complete package
// and then changes its remote bytes. Cached successful releases must retain
// their first digest while current moderation controls can withdraw the version.
func TestBuildCachesFirstAcceptedDigestsAndAppliesYanks(t *testing.T) {
	source := staticRelease(t)
	registry := &Registry{Plugins: []Registration{{ID: "test.plugin", Repository: "https://github.com/test/plugin", Categories: []string{"tools"}}}}
	first, err := Build(context.Background(), registry, source, State{}, "Fixture", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Failures) != 0 || len(first.Document.Plugins[0].Versions) != 1 || source.downloads != 3 {
		t.Fatalf("unexpected ingestion: %+v", first.Failures)
	}
	digest := first.Document.Plugins[0].Versions[0].Artifacts["any"].SHA256
	source.assets["https://assets.example/package"] = []byte("replaced release asset")
	source.downloads = 0
	registry.Plugins[0].Yanked = []Yank{{Version: "1.0.0", Reason: "Fixture withdrawal"}}
	second, err := Build(context.Background(), registry, source, first.State, "Fixture", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	version := second.Document.Plugins[0].Versions[0]
	if source.downloads != 0 || version.Artifacts["any"].SHA256 != digest || !version.Yanked {
		t.Fatal("cached digest or yank rules changed")
	}
	if first.Document.Plugins[0].Versions[0].Yanked {
		t.Fatal("previous result mutated")
	}
}

// TestBuildRejectsManifestByteMismatch protects release identity across assets.
// Changing only whitespace in the standalone manifest must fail package
// comparison even though both parsed declarations have identical semantic values.
func TestBuildRejectsManifestByteMismatch(t *testing.T) {
	source := staticRelease(t)
	source.assets["https://assets.example/plugin.json"] = append(source.assets["https://assets.example/plugin.json"], '\n')
	result, err := Build(context.Background(), &Registry{Plugins: []Registration{{ID: "test.plugin", Repository: "https://github.com/test/plugin"}}}, source, State{}, "Fixture", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Failures) != 1 || len(result.Document.Plugins[0].Versions) != 0 {
		t.Fatal("mismatched package admitted")
	}
}

// staticRelease builds a complete static archive for catalog ingestion tests.
// All download addresses are inert fixture keys. Package creation uses the
// production packer so checksum and manifest comparisons exercise actual archive bytes.
func staticRelease(t *testing.T) *fixtureSource {
	t.Helper()
	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, "frontend"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"schemaVersion":1,"id":"test.plugin","version":"1.0.0","apiVersion":1,"minHostVersion":"0.5.0","name":{"en":"Test"},"description":{"en":"Fixture plugin"},"frontend":{"entry":"frontend/index.js","pages":[]}}`)
	for name, data := range map[string][]byte{"plugin.json": raw, "frontend/index.js": []byte("console.log('fixture')")} {
		if err := os.WriteFile(filepath.Join(directory, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	filename := pkgfmt.Filename("test.plugin", "1.0.0", "any")
	archive, err := pkgfmt.Pack(directory, filepath.Join(t.TempDir(), filename), pkgfmt.Options{Platform: "any"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(archive.Path)
	if err != nil {
		t.Fatal(err)
	}
	assets := map[string][]byte{"https://assets.example/plugin.json": raw, "https://assets.example/checksums.txt": []byte(archive.SHA256 + "  " + filename + "\n"), "https://assets.example/package": data}
	return &fixtureSource{assets: assets, releases: []Release{{Tag: "v1.0.0", PublishedAt: time.Now().UTC(), Assets: []ReleaseAsset{{Name: "plugin.json", URL: "https://assets.example/plugin.json", Size: int64(len(raw))}, {Name: "checksums.txt", URL: "https://assets.example/checksums.txt"}, {Name: filename, URL: "https://assets.example/package", Size: archive.Size}}}}}
}

// TestChecksumsRetainSemverBuildMetadata reads an archive filename containing legal SemVer build metadata.
// The exact plus-bearing name remains the lookup key, while path traversal and nonportable separators remain rejected.
func TestChecksumsRetainSemverBuildMetadata(t *testing.T) {
	name := "test.plugin-1.0.0+build.7-linux-amd64.tar.gz"
	digest := strings.Repeat("a", 64)
	checksums, err := parseChecksums([]byte(digest + "  " + name + "\n"))
	if err != nil || checksums[name] != digest {
		t.Fatalf("build metadata filename rejected or changed: %v %v", checksums, err)
	}
	for _, unsafe := range []string{"../plugin.tar.gz", "nested/plugin.tar.gz", `bad\plugin.tar.gz`} {
		if _, err := parseChecksums([]byte(digest + "  " + unsafe)); err == nil {
			t.Fatalf("accepted unsafe filename %q", unsafe)
		}
	}
}
