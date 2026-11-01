package pkgfmt

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInspectChecksReferencedFilesAndManifestDigest modifies an extracted valid
// package without executing its synthetic binary. Missing assets, oversized
// icons, and changed manifest bytes must invalidate later inspection.
func TestInspectChecksReferencedFilesAndManifestDigest(t *testing.T) {
	data, _ := validArchive(t)
	directory := filepath.Join(t.TempDir(), "package")
	p, err := Extract(bytes.NewReader(data), directory, Options{Platform: "linux/amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(directory, Options{Platform: "any"}); err == nil {
		t.Fatal("native package accepted as any")
	}
	if _, err := Inspect(directory, Options{Platform: "linux/amd64", ManifestSHA256: strings.Repeat("0", 64)}); err == nil {
		t.Fatal("wrong manifest checksum accepted")
	}
	p.Manifest.Icon = "icon.svg"
	raw, err := json.Marshal(p.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "plugin.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(directory, Options{Platform: "linux/amd64"}); err == nil {
		t.Fatal("missing icon accepted")
	}
	if err := os.WriteFile(filepath.Join(directory, "icon.svg"), bytes.Repeat([]byte("x"), 64<<10+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(directory, Options{Platform: "linux/amd64"}); err == nil {
		t.Fatal("oversized icon accepted")
	}
	if err := os.WriteFile(filepath.Join(directory, "icon.svg"), []byte("<svg/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(directory, Options{Platform: "linux/amd64"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(directory, "bin/plugin")); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(directory, Options{Platform: "linux/amd64"}); err == nil {
		t.Fatal("missing executable accepted")
	}
}

// TestInspectRejectsSymlink checks unpacked development and CLI inputs as well
// as tar inputs. A preexisting symlink must never be hashed as though it were
// an ordinary file belonging to the package.
func TestInspectRejectsSymlink(t *testing.T) {
	data, _ := validArchive(t)
	directory := filepath.Join(t.TempDir(), "package")
	if _, err := Extract(bytes.NewReader(data), directory, Options{Platform: "linux/amd64"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("plugin.json", filepath.Join(directory, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(directory, Options{Platform: "linux/amd64"}); err == nil {
		t.Fatal("symlink accepted")
	}
}

// TestInspectRejectsInstallerMetadata checks unpacked packages containing reserved
// host metadata or portable aliases. Inspection must reject them before a
// publisher can create an archive that overwrites installation bookkeeping.
func TestInspectRejectsInstallerMetadata(t *testing.T) {
	for _, name := range []string{ReservedMetadataName, ".TR-INSTALLED.", ReservedMetadataName + "/child"} {
		t.Run(name, func(t *testing.T) {
			data, _ := validArchive(t)
			directory := filepath.Join(t.TempDir(), "package")
			if _, err := Extract(bytes.NewReader(data), directory, Options{Platform: "linux/amd64"}); err != nil {
				t.Fatal(err)
			}
			filename := filepath.Join(directory, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filename, []byte("publisher data"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Inspect(directory, Options{Platform: "linux/amd64"}); err == nil {
				t.Fatal("reserved installer path accepted")
			}
		})
	}
}
