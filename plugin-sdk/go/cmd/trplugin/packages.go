package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pkgfmt"
)

// packages.go validates publisher inputs and assembles platform release assets.
// Package trees are built in temporary directories from the declared manifest,
// frontend assets, and cross-compiled binaries. The shared package inspector
// checks those trees before packing; a checksums file binds the resulting
// compressed archives for later validation and catalog ingestion.
//
// Validation reads a sibling checksums file when present and rejects changed
// bytes before extraction. Without that file it checks structure and declared
// asset references only; authors should publish checksums alongside all release
// archives. Temporary extraction and staging directories are owned by each
// command and removed after validation or publication, including failure paths.

// validateCommand validates directories or canonical archive names supplied by the user.
// Sibling checksums are enforced when available. Directory inspection selects
// the current platform unless the manifest contains only a static frontend.
func validateCommand(args []string, output io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: trplugin validate <directory|archive> [additional paths]")
	}
	for _, name := range args {
		info, err := os.Stat(name)
		if err != nil {
			return err
		}
		if info.IsDir() {
			raw, err := os.ReadFile(filepath.Join(name, "plugin.json"))
			if err != nil {
				return err
			}
			m, err := manifest.Parse(raw)
			if err != nil {
				return err
			}
			platform := runtime.GOOS + "/" + runtime.GOARCH
			if m.Backend == nil {
				platform = "any"
			}
			if _, err := pkgfmt.Inspect(name, pkgfmt.Options{Platform: platform}); err != nil {
				return err
			}
		} else if err := validateArchive(name); err != nil {
			return err
		}
		fmt.Fprintf(output, "valid %s\n", name)
	}
	return nil
}

// validateArchive checks checksums, structure, and identity of one archive.
// Its extraction directory is always temporary. An absent checksum list permits
// structural validation, but a present list must contain the requested archive.
func validateArchive(name string) error {
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, pkgfmt.MaxArchiveBytes+1))
	if err != nil {
		return err
	}
	if len(data) > pkgfmt.MaxArchiveBytes {
		return fmt.Errorf("archive exceeds compressed size limit")
	}
	if err := verifySiblingChecksum(name, data); err != nil {
		return err
	}
	platform, err := archivePlatform(filepath.Base(name))
	if err != nil {
		return err
	}
	temporary, err := os.MkdirTemp("", "trplugin-validate-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(temporary) }()
	p, err := pkgfmt.Extract(bytes.NewReader(data), filepath.Join(temporary, "package"), pkgfmt.Options{Platform: platform})
	if err != nil {
		return err
	}
	if filepath.Base(name) != pkgfmt.Filename(p.Manifest.ID, p.Manifest.Version, platform) {
		return fmt.Errorf("archive filename does not match manifest identity")
	}
	return nil
}

// verifySiblingChecksum authenticates archive bytes against a local release list.
// Missing lists allow standalone structural checks. Existing malformed lists or
// missing archive entries fail rather than silently disabling integrity verification.
func verifySiblingChecksum(name string, data []byte) error {
	checksums, err := os.ReadFile(filepath.Join(filepath.Dir(name), "checksums.txt"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for line := range strings.SplitSeq(string(checksums), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 {
			return fmt.Errorf("invalid checksums.txt entry")
		}
		if strings.TrimPrefix(parts[1], "*") == filepath.Base(name) {
			digest := sha256.Sum256(data)
			if parts[0] != hex.EncodeToString(digest[:]) {
				return fmt.Errorf("archive checksum mismatch: %s", name)
			}
			return nil
		}
	}
	return fmt.Errorf("archive is absent from checksums.txt: %s", name)
}

// archivePlatform obtains a supported platform from a canonical artifact suffix.
func archivePlatform(name string) (string, error) {
	for _, platform := range append(pkgfmt.Platforms(), "any") {
		if strings.HasSuffix(name, "-"+strings.ReplaceAll(platform, "/", "-")+".tar.gz") {
			return platform, nil
		}
	}
	return "", fmt.Errorf("unrecognized archive platform in %q", name)
}

// packCommand assembles declared frontend and binary outputs into release archives.
// A backend release requires all five supported binaries at bin-dir/os/arch.
// Static releases produce one any archive. GITHUB_REF_NAME, when present, must
// match the manifest's release tag before any archive is published.
func packCommand(args []string, output io.Writer) error {
	flags := flag.NewFlagSet("pack", flag.ContinueOnError)
	manifestPath := flags.String("manifest", "plugin.json", "release manifest path")
	frontend := flags.String("frontend", "dist/frontend", "built frontend directory")
	binDir := flags.String("bin-dir", "dist", "platform binary directory")
	out := flags.String("out", "release", "release output directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("pack accepts flags only")
	}
	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		return err
	}
	m, err := manifest.Parse(raw)
	if err != nil {
		return err
	}
	if tag := os.Getenv("GITHUB_REF_NAME"); tag != "" && tag != "v"+m.Version {
		return fmt.Errorf("manifest version does not match release tag %q", tag)
	}
	platforms := []string{"any"}
	if m.Backend != nil {
		platforms = pkgfmt.Platforms()
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	var checksums strings.Builder
	for _, platform := range platforms {
		archive, err := assembleArchive(m, raw, filepath.Dir(*manifestPath), *frontend, *binDir, *out, platform)
		if err != nil {
			return err
		}
		fmt.Fprintf(&checksums, "%s  %s\n", archive.SHA256, filepath.Base(archive.Path))
		fmt.Fprintln(output, archive.Path)
	}
	if err := os.WriteFile(filepath.Join(*out, "plugin.json"), raw, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(*out, "checksums.txt"), []byte(checksums.String()), 0o644)
}

// assembleArchive copies one release's declared inputs into an isolated package tree.
// Only manifest-referenced assets and optional README/LICENSE files are included.
// The resulting archive is validated against the original manifest bytes before
// its temporary input tree is removed by this function.
func assembleArchive(m *manifest.Manifest, raw []byte, base, frontend, binDir, out, platform string) (*pkgfmt.Archive, error) {
	stage, err := os.MkdirTemp("", "trplugin-pack-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	if err := os.WriteFile(filepath.Join(stage, "plugin.json"), raw, 0o644); err != nil {
		return nil, err
	}
	assets := make(map[string]string)
	if m.Icon != "" {
		assets[m.Icon] = publisherAsset(base, m.Icon)
	}
	if m.Frontend != nil {
		for _, asset := range append([]string{m.Frontend.Entry}, m.Frontend.Styles...) {
			assets[asset] = filepath.Join(frontend, strings.TrimPrefix(asset, "frontend/"))
		}
		for _, menu := range m.Frontend.Menus {
			if menu.Icon != "" {
				assets[menu.Icon] = publisherAsset(base, menu.Icon)
			}
		}
	}
	if m.Backend != nil {
		name := m.Backend.Executable
		if strings.HasPrefix(platform, "windows/") {
			name += ".exe"
		}
		assets[name] = filepath.Join(binDir, filepath.FromSlash(platform), filepath.FromSlash(name))
	}
	for _, name := range []string{"README.md", "LICENSE"} {
		if _, err := os.Stat(filepath.Join(base, name)); err == nil {
			assets[name] = filepath.Join(base, name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	for name, source := range assets {
		if err := copyAsset(source, filepath.Join(stage, filepath.FromSlash(name))); err != nil {
			return nil, err
		}
	}
	return pkgfmt.Pack(stage, filepath.Join(out, pkgfmt.Filename(m.ID, m.Version, platform)), pkgfmt.Options{Platform: platform, ManifestBytes: raw})
}

// publisherAsset selects a declared source path or the template's assets directory.
// Existing declared files take precedence. Missing declarations fall back to
// assets/basename so the template can keep source icons outside build outputs.
func publisherAsset(base, name string) string {
	declared := filepath.Join(base, filepath.FromSlash(name))
	if _, err := os.Stat(declared); err == nil {
		return declared
	}
	return filepath.Join(base, "assets", filepath.Base(name))
}

// copyAsset copies a bounded regular source file into its package destination.
// Symlink sources are rejected. The destination belongs to a fresh temporary
// tree and receives fixed permissions independently of publisher filesystem modes.
func copyAsset(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > pkgfmt.MaxExpandedBytes {
		return fmt.Errorf("invalid package asset %q", source)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, io.LimitReader(in, pkgfmt.MaxExpandedBytes+1))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
