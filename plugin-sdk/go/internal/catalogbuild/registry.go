package catalogbuild

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// registry.go reads the small publisher and plugin registrations maintained in
// an index repository. Registration paths bind plugin IDs to a publisher, and
// strict YAML decoding rejects misspelled review controls. Release contents
// remain the builder's responsibility; registration alone never makes a new
// artifact eligible for publication in the signed catalog.

// Registry contains validated publisher identities and their registered repositories.
type Registry struct {
	Publishers map[string]Publisher // publishers keyed by identifier
	Plugins    []Registration       // registered plugin release sources
}

// Publisher lists the GitHub identities allowed to maintain a registered prefix.
type Publisher struct {
	ID     string   `yaml:"id"`     // publisher identifier prefix
	Name   string   `yaml:"name"`   // publisher display name
	URL    string   `yaml:"url"`    // publisher public profile URL
	GitHub []string `yaml:"github"` // allowed accounts or organizations
}

// Registration identifies a plugin repository and its catalog moderation controls.
type Registration struct {
	ID         string   `yaml:"id"`                   // publisher-qualified plugin identity
	Repository string   `yaml:"repository"`           // public GitHub repository URL
	Categories []string `yaml:"categories"`           // catalog discovery categories
	MinVersion string   `yaml:"minVersion,omitempty"` // optional minimum indexed SemVer
	Delisted   bool     `yaml:"delisted,omitempty"`   // catalog listing withdrawal flag
	Yanked     []Yank   `yaml:"yanked,omitempty"`     // individually withdrawn release versions
}

// Yank records one withdrawn version and its public moderation explanation.
type Yank struct {
	Version string `yaml:"version"` // withdrawn release SemVer
	Reason  string `yaml:"reason"`  // public withdrawal explanation
}

// ReadRegistry validates publisher and plugin YAML files under one repository.
// It rejects links, unknown fields, conflicting IDs, and identity/path mismatches.
// The returned registration order is stable for deterministic build traversal.
func ReadRegistry(directory string) (*Registry, error) {
	registry := &Registry{Publishers: make(map[string]Publisher)}
	entries, err := os.ReadDir(filepath.Join(directory, "publishers"))
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		var publisher Publisher
		if err := decodeYAML(filepath.Join(directory, "publishers", entry.Name()), &publisher); err != nil {
			return nil, err
		}
		if !manifest.ValidID(publisher.ID+".plugin") || entry.Name() != publisher.ID+".yaml" || publisher.Name == "" || len(publisher.GitHub) == 0 {
			return nil, fmt.Errorf("invalid publisher registration %s", entry.Name())
		}
		if _, ok := registry.Publishers[publisher.ID]; ok {
			return nil, fmt.Errorf("duplicate publisher %s", publisher.ID)
		}
		registry.Publishers[publisher.ID] = publisher
	}
	seen := make(map[string]bool)
	err = filepath.WalkDir(filepath.Join(directory, "plugins"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("registry links are not allowed: %s", path)
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return nil
		}
		var registration Registration
		if err := decodeYAML(path, &registration); err != nil {
			return err
		}
		if !manifest.ValidID(registration.ID) || seen[registration.ID] {
			return fmt.Errorf("invalid or duplicate plugin %q", registration.ID)
		}
		parts := strings.SplitN(registration.ID, ".", 2)
		expected := filepath.Join(directory, "plugins", parts[0], parts[1]+".yaml")
		if filepath.Clean(path) != filepath.Clean(expected) {
			return fmt.Errorf("registration path differs from ID %s", registration.ID)
		}
		if _, ok := registry.Publishers[parts[0]]; !ok {
			return fmt.Errorf("publisher %s is not registered", parts[0])
		}
		if !strings.HasPrefix(registration.Repository, "https://github.com/") || !index.HTTPSURL(registration.Repository) || len(strings.Split(strings.TrimPrefix(registration.Repository, "https://github.com/"), "/")) != 2 {
			return fmt.Errorf("plugin %s requires a GitHub repository URL", registration.ID)
		}
		if registration.MinVersion != "" && !manifest.ValidVersion(registration.MinVersion) {
			return fmt.Errorf("invalid minVersion for %s", registration.ID)
		}
		for _, category := range registration.Categories {
			if !slices.Contains([]string{"engagement", "tools", "analytics", "content", "integration", "other"}, category) {
				return fmt.Errorf("unknown category %s", category)
			}
		}
		for _, yank := range registration.Yanked {
			if !manifest.ValidVersion(yank.Version) || strings.TrimSpace(yank.Reason) == "" {
				return fmt.Errorf("invalid yanked version for %s", registration.ID)
			}
		}
		seen[registration.ID] = true
		registry.Plugins = append(registry.Plugins, registration)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return registry, nil
}

// decodeYAML reads one bounded regular registration with strict field checking.
// Multiple YAML documents are rejected. The caller chooses the destination
// type, and errors retain the file path without exposing workflow secrets.
func decodeYAML(path string, destination any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return fmt.Errorf("invalid registration file %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("%s must contain one YAML document", path)
	}
	return nil
}
