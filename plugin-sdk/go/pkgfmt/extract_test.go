package pkgfmt

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExtractRejectsUnsafeArchiveEntries exercises traversal, duplicate names,
// links, device nodes, and structural limits. Every rejected archive must leave
// neither a destination directory nor a file outside that directory.
func TestExtractRejectsUnsafeArchiveEntries(t *testing.T) {
	tests := []struct {
		name    string        // hostile archive scenario
		headers []*tar.Header // declared archive entry headers
	}{
		{"traversal", []*tar.Header{{Name: "../escape", Typeflag: tar.TypeReg}}},
		{"absolute", []*tar.Header{{Name: "/escape", Typeflag: tar.TypeReg}}},
		{"backslash", []*tar.Header{{Name: `dir\escape`, Typeflag: tar.TypeReg}}},
		{"symlink", []*tar.Header{{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "../escape"}}},
		{"parent-file", []*tar.Header{{Name: "file", Typeflag: tar.TypeReg}, {Name: "file/child", Typeflag: tar.TypeReg}}},
		{"hardlink", []*tar.Header{{Name: "link", Typeflag: tar.TypeLink, Linkname: "../escape"}}},
		{"device", []*tar.Header{{Name: "device", Typeflag: tar.TypeChar}}},
		{"duplicate", []*tar.Header{{Name: "file", Typeflag: tar.TypeReg}, {Name: "file", Typeflag: tar.TypeReg}}},
		{"installer-metadata", []*tar.Header{{Name: ReservedMetadataName, Typeflag: tar.TypeReg}}},
		{"installer-metadata-alias", []*tar.Header{{Name: ".TR-INSTALLED.", Typeflag: tar.TypeReg}}},
		{"installer-metadata-directory", []*tar.Header{{Name: ReservedMetadataName + "/child", Typeflag: tar.TypeReg}}},
		{"depth", []*tar.Header{{Name: strings.Repeat("dir/", 11) + "file", Typeflag: tar.TypeReg}}},
		{"expanded", []*tar.Header{{Name: "huge", Typeflag: tar.TypeReg, Size: MaxExpandedBytes + 1}}},
		{"frontend", []*tar.Header{{Name: "frontend/huge", Typeflag: tar.TypeReg, Size: MaxFrontendBytes + 1}}},
	}
	many := make([]*tar.Header, MaxEntries+1)
	for i := range many {
		many[i] = &tar.Header{Name: fmt.Sprintf("file-%d", i), Typeflag: tar.TypeReg}
	}
	tests = append(tests, struct {
		name    string
		headers []*tar.Header
	}{"entry-count", many})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := archiveHeaders(t, tt.headers)
			parent := t.TempDir()
			destination := filepath.Join(parent, "extract")
			if _, err := Extract(bytes.NewReader(data), destination, Options{Platform: "any"}); err == nil {
				t.Fatal("unsafe archive accepted")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatalf("partial extraction survived: %v", err)
			}
			if _, err := os.Stat(filepath.Join(parent, "escape")); !os.IsNotExist(err) {
				t.Fatalf("traversal created outside file: %v", err)
			}
		})
	}
}

// TestExtractChecksManifestAndModes builds a complete backend package with
// publisher-controlled executable bits. Only its declared binary becomes
// executable, and its original manifest bytes remain available for verification.
func TestExtractChecksManifestAndModes(t *testing.T) {
	data, raw := validArchive(t)
	destination := filepath.Join(t.TempDir(), "package")
	p, err := Extract(bytes.NewReader(data), destination, Options{Platform: "linux/amd64", ManifestBytes: raw})
	if err != nil {
		t.Fatal(err)
	}
	if p.BinarySHA256 == "" || p.ArchiveSHA256 == "" || !strings.HasPrefix(p.Files["frontend/index.js"].SRI, "sha384-") {
		t.Fatalf("missing digests: %#v", p)
	}
	for name, want := range map[string]os.FileMode{"bin/plugin": 0o755, "frontend/index.js": 0o644, "plugin.json": 0o644} {
		info, err := os.Stat(filepath.Join(destination, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode %o, want %o", name, info.Mode().Perm(), want)
		}
	}
	if _, err := Extract(bytes.NewReader(data), filepath.Join(t.TempDir(), "mismatch"), Options{Platform: "linux/amd64", ManifestBytes: append(raw, ' ')}); err == nil {
		t.Fatal("manifest byte mismatch accepted")
	}
	data[len(data)-1] ^= 1
	if _, err := Extract(bytes.NewReader(data), filepath.Join(t.TempDir(), "corrupt"), Options{Platform: "linux/amd64"}); err == nil {
		t.Fatal("corrupt gzip trailer accepted")
	}
}

// TestExtractPreservesExistingDestination checks that failed ownership acquisition
// cannot delete caller files. The extractor must reject a preexisting directory
// before its cleanup path becomes responsible for that location.
func TestExtractPreservesExistingDestination(t *testing.T) {
	data, _ := validArchive(t)
	destination := t.TempDir()
	keep := filepath.Join(destination, "keep")
	if err := os.WriteFile(keep, []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Extract(bytes.NewReader(data), destination, Options{Platform: "linux/amd64"}); err == nil {
		t.Fatal("existing destination accepted")
	}
	if got, err := os.ReadFile(keep); err != nil || string(got) != "retained" {
		t.Fatalf("existing data lost: %q, %v", got, err)
	}
}

// archiveHeaders emits intentionally incomplete oversized entries for boundary
// tests. Header rejection must occur before the extractor tries to consume
// their missing bodies, avoiding large allocations in the test process.
func archiveHeaders(t *testing.T, headers []*tar.Header) []byte {
	t.Helper()
	var raw bytes.Buffer
	tarWriter := tar.NewWriter(&raw)
	for _, header := range headers {
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
	}
	// Oversized fixtures intentionally omit their bodies; the validator must reject the header before reading or writing its declared payload.
	_ = tarWriter.Close()
	var result bytes.Buffer
	gz := gzip.NewWriter(&result)
	if _, err := gz.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return result.Bytes()
}
