package plugin

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// memoryInstallations stores serialized records so tests exercise independent storage snapshots.
type memoryInstallations struct {
	mu      sync.Mutex        // fixture state publication lock
	rows    map[string][]byte // serialized persisted installation records
	deleted []string          // observed installation deletion identifiers
	purge   []bool            // observed business data purge choices
}

// processFixture models admission and drain boundaries without executing child processes.
type processFixture struct {
	mu             sync.Mutex        // fixture process observation lock
	started        []ProcessSpec     // accepted process generation specifications
	states         map[string]Status // observed lifecycle state by plugin
	stopError      error             // injected drain completion error
	stopEntered    chan struct{}     // optional drain arrival notification
	stopRelease    chan struct{}     // optional drain completion barrier
	replaceEntered chan struct{}     // optional replacement arrival notification
	replaceRelease chan struct{}     // optional replacement completion barrier
}

// packageFixture records managed cleanup while returning deterministic verified package metadata.
type packageFixture struct {
	removed []string // observed managed cleanup identifiers
}

// newMemoryInstallations constructs an empty independently decoded installation store fixture.
func newMemoryInstallations() *memoryInstallations {
	return &memoryInstallations{rows: make(map[string][]byte)}
}

// List returns independent fixture records without sharing persisted configuration containers.
func (s *memoryInstallations) List(context.Context) ([]Installation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Installation, 0, len(s.rows))
	for _, data := range s.rows {
		item, err := decodeDevelopment(data)
		if err != nil {
			return nil, err
		}
		result = append(result, *item)
	}
	return result, nil
}

// Get returns a separately decoded record or the production missing-record sentinel.
func (s *memoryInstallations) Get(_ context.Context, id string) (*Installation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.rows[id]
	if !ok {
		return nil, ErrPluginNotFound
	}
	return decodeDevelopment(data)
}

// Save serializes caller-owned state before replacing its persisted fixture representation.
func (s *memoryInstallations) Save(_ context.Context, item *Installation) error {
	data, err := json.Marshal(item)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[item.PluginID] = data
	return nil
}

// Delete records purge intent and removes the selected installation fixture row.
func (s *memoryInstallations) Delete(_ context.Context, id string, purge bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, id)
	s.deleted = append(s.deleted, id)
	s.purge = append(s.purge, purge)
	return nil
}

// Replace records startup and optionally waits at a controlled mutation barrier.
func (f *processFixture) Replace(ctx context.Context, spec ProcessSpec) error {
	if f.replaceEntered != nil {
		close(f.replaceEntered)
		select {
		case <-f.replaceRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.states == nil {
		f.states = make(map[string]Status)
	}
	f.started = append(f.started, spec)
	f.states[spec.PluginID] = Status{State: StateRunning}
	return nil
}

// Acquire returns a fixture lease only while its recorded generation is running.
func (f *processFixture) Acquire(_ context.Context, id string) (*Lease, error) {
	if f.Status(id).State != StateRunning {
		return nil, ErrPluginUnavailable
	}
	return NewLease(&Conn{}, "1.0.0", func() {}), nil
}

// Stop models actual callback draining and retains running state on injected failure.
func (f *processFixture) Stop(ctx context.Context, id string) error {
	f.mu.Lock()
	entered, release := f.stopEntered, f.stopRelease
	f.stopEntered, f.stopRelease = nil, nil
	f.mu.Unlock()
	if entered != nil {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopError != nil {
		return f.stopError
	}
	if f.states == nil {
		f.states = make(map[string]Status)
	}
	f.states[id] = Status{State: StateStopped}
	return nil
}

// ApplyConfig records the plaintext configuration delivered to the fixture generation.
func (f *processFixture) ApplyConfig(_ context.Context, spec ProcessSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, spec)
	return nil
}

// Status returns a copied fixture observation or the ordinary stopped default.
func (f *processFixture) Status(id string) Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	if value, ok := f.states[id]; ok {
		return value
	}
	return Status{State: StateStopped}
}

// StopAll ends all recorded generations without requiring individual drain barriers.
func (f *processFixture) StopAll(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states = make(map[string]Status)
	return nil
}

// Ensure returns stable release paths and hashes without touching the filesystem.
func (*packageFixture) Ensure(_ context.Context, ref PackageRef) (Package, error) {
	return Package{
		Directory:      "/fixture",
		Executable:     "/fixture/bin/plugin",
		ArchiveSHA256:  ref.Artifact.SHA256,
		BinarySHA256:   "binary",
		ManifestSHA256: ref.ManifestSHA256,
	}, nil
}

// Prune accepts retained release versions without deleting real filesystem entries.
func (*packageFixture) Prune(context.Context, string, ...string) error { return nil }

// Remove records a cleanup request for later uninstall ordering assertions.
func (f *packageFixture) Remove(_ context.Context, id string) error {
	f.removed = append(f.removed, id)
	return nil
}

// managerInstallation builds a complete native installation suitable for core reconciliation tests.
func managerInstallation(t *testing.T, id string, enabled bool) *Installation {
	t.Helper()
	declaration, err := manifest.Parse([]byte(`{"schemaVersion":1,"id":"` + id + `","version":"1.0.0","apiVersion":1,"minHostVersion":"0.5.0","name":{"en":"Test"},"description":{"en":"Test plugin"},"publisher":{"name":"Test"},"license":"MIT","backend":{"executable":"bin/plugin"}}`))
	require.NoError(t, err)
	item := &Installation{
		PluginID: id,
		Enabled:  enabled,
		VersionSnapshot: VersionSnapshot{
			Version:            "1.0.0",
			Source:             SourceUpload,
			Manifest:           declaration,
			ManifestSHA256:     strings.Repeat("a", 64),
			Config:             map[string]any{},
			GrantedPermissions: []string{},
		},
	}
	platform := packageReference(item, "").Platform
	item.Artifacts = map[string]index.Artifact{platform: {SHA256: strings.Repeat("b", 64), Size: 1}}
	return item
}

// managerFixture assembles isolated in-memory management with one shared process limit.
func managerFixture(t *testing.T, maximum int) (*Manager, *Service, *memoryInstallations, *processFixture, *packageFixture) {
	t.Helper()
	store := newMemoryInstallations()
	processes := &processFixture{}
	packages := &packageFixture{}
	manager := NewManager(ManagerOptions{Enabled: true, MaxRunning: maximum, HostVersion: "0.5.4", StateRoot: t.TempDir()}, store, packages, processes, NewConfiguration(nil, false), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return manager, NewService(manager, nil), store, processes, packages
}
