package provider

import (
	"context"
	"os"
	"path/filepath"
	"runtime"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pkgfmt"
)

// package_directory.go validates unpacked plugin inputs through the shared SDK
// package inspector. It returns absolute executable locations without changing
// publisher files or copying their contents into managed installation storage.
// The core source decides whether that validated directory is eligible to run.

// DirectoryReader adapts local package inspection to the core's directory capability.
type DirectoryReader struct{}

// ReadDirectory validates an unpacked plugin and resolves its platform executable.
// Static packages select any-platform validation. Cancellation is checked before
// and after filesystem inspection, and original directory contents remain unchanged.
func (DirectoryReader) ReadDirectory(ctx context.Context, directory string) (plugin.DirectoryPackage, error) {
	if err := ctx.Err(); err != nil {
		return plugin.DirectoryPackage{}, err
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return plugin.DirectoryPackage{}, err
	}
	raw, err := os.ReadFile(filepath.Join(absolute, "plugin.json"))
	if err != nil {
		return plugin.DirectoryPackage{}, err
	}
	m, err := manifest.Parse(raw)
	if err != nil {
		return plugin.DirectoryPackage{}, err
	}
	platform := runtime.GOOS + "/" + runtime.GOARCH
	if m.Backend == nil {
		platform = "any"
	}
	if _, err := pkgfmt.Inspect(absolute, pkgfmt.Options{Platform: platform}); err != nil {
		return plugin.DirectoryPackage{}, err
	}
	if err := ctx.Err(); err != nil {
		return plugin.DirectoryPackage{}, err
	}
	result := plugin.DirectoryPackage{Manifest: m}
	if m.Backend != nil {
		name := m.Backend.Executable
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		result.Executable = filepath.Join(absolute, filepath.FromSlash(name))
	}
	return result, nil
}
