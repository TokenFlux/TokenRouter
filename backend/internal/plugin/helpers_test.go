package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// helpers_test.go supplies independent storage and process fixtures for plugin core tests.
// Serialized rows prevent tests from accidentally sharing mutable persisted state,
// while explicit barriers expose lifecycle and refresh ordering without launching
// native processes or contacting external distribution services.

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
	failBinary     string            // archive identity rejected during fixture startup
	failVersion    string            // release rejected during fixture startup
	stopError      error             // injected drain completion error
	stopEntered    chan struct{}     // optional drain arrival notification
	stopRelease    chan struct{}     // optional drain completion barrier
	replaceEntered chan struct{}     // optional replacement arrival notification
	replaceRelease chan struct{}     // optional replacement completion barrier
	configCalls    int               // configuration reconciliation attempt count
	configError    error             // injected configuration delivery failure
}

// packageFixture records managed cleanup while returning deterministic verified package metadata.
type packageFixture struct {
	mu      sync.Mutex   // serializes package fixture observations
	removed []string     // observed managed cleanup identifiers
	pruned  [][]string   // retained versions for each cleanup
	ensured []PackageRef // verified candidate package selections
}

// memoryIndexes retains independently decoded source rows for catalog coordination tests.
type memoryIndexes struct {
	mu   sync.Mutex       // protects serialized source fixture rows
	rows map[int64][]byte // independently serialized source records
	next int64            // next fixture source identifier
}

// indexFetchFunc adapts one controlled transfer function to the production catalog port.
type indexFetchFunc func(context.Context, IndexFetchRequest) (FetchedIndex, error)

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
	if spec.Version == f.failVersion || spec.BinarySHA256 == f.failBinary {
		f.states[spec.PluginID] = Status{State: StateFailed, LastError: "fixture init failed"}
		return nil
	}
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

// ApplyConfig records reconciliation attempts and newly applied fixture configuration.
// Successful identical values are already applied and need no new record.
// Injected failures preserve the old applied values for a later retry.
func (f *processFixture) ApplyConfig(_ context.Context, spec ProcessSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configCalls++
	if f.configError != nil {
		return f.configError
	}
	for _, previous := range slices.Backward(f.started) {
		if previous.PluginID == spec.PluginID {
			if previous.Version == spec.Version && bytes.Equal(previous.ConfigJSON, spec.ConfigJSON) {
				return nil
			}
			break
		}
	}
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
func (f *packageFixture) Ensure(_ context.Context, ref PackageRef) (Package, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensured = append(f.ensured, ref)
	return Package{
		Directory:      "/fixture",
		Executable:     "/fixture/bin/plugin",
		ArchiveSHA256:  ref.Artifact.SHA256,
		BinarySHA256:   ref.Artifact.SHA256,
		ManifestSHA256: ref.ManifestSHA256,
	}, nil
}

// Prune accepts retained release versions without deleting real filesystem entries.
func (f *packageFixture) Prune(_ context.Context, _ string, refs ...PackageRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	versions := make([]string, 0, len(refs))
	for _, ref := range refs {
		versions = append(versions, ref.Version)
	}
	f.pruned = append(f.pruned, versions)
	return nil
}

// Remove records a cleanup request for later uninstall ordering assertions.
func (f *packageFixture) Remove(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	return manager, NewService(manager, nil, nil, nil), store, processes, packages
}

// catalogRelease creates one portable frontend release with independently owned metadata.
func catalogRelease(version string) index.Version {
	return index.Version{Version: version, PublishedAt: time.Unix(1, 0), ManifestSHA256: strings.Repeat("a", 64), Manifest: &manifest.Manifest{
		SchemaVersion: 1, ID: "test.catalog", Version: version, APIVersion: 1, MinHostVersion: "0.5.0",
		Name: manifest.LocalizedText{"en": "Catalog"}, Description: manifest.LocalizedText{"en": "Catalog fixture"},
		Frontend: &manifest.Frontend{Entry: "frontend/index.js"},
	}, Artifacts: map[string]index.Artifact{"any": {URL: "https://example.invalid/plugin.tar.gz", SHA256: strings.Repeat("b", 64), Size: 10}}}
}

// catalogFixture combines ordered releases with one enabled index and a fixed host capability set.
func catalogFixture(versions ...index.Version) (IndexSource, *index.Document, manifest.HostInfo) {
	return IndexSource{ID: 1, Name: "Fixture", URL: "https://example.invalid/v1/index.json", Enabled: true, SignatureStatus: "official"},
		&index.Document{SchemaVersion: 1, Name: "Fixture", GeneratedAt: time.Unix(1, 0), Plugins: []index.Plugin{{ID: "test.catalog", Repository: "https://github.com/example/plugin", IconURL: "icons/plugin.svg", Categories: []string{"tools"}, Versions: versions}}},
		manifest.HostInfo{Version: "0.6.0", APIVersions: []int{1}, Platform: "linux/amd64", Permissions: manifest.Permissions()}
}

// Fetch delegates one bounded source request to the test's controlled transfer behavior.
func (f indexFetchFunc) Fetch(ctx context.Context, request IndexFetchRequest) (FetchedIndex, error) {
	return f(ctx, request)
}

// ListIndexes returns independent source copies in the storage contract's stable ID order.
func (s *memoryIndexes) ListIndexes(ctx context.Context) ([]IndexSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]IndexSource, 0, len(s.rows))
	for _, data := range s.rows {
		var item IndexSource
		if err := json.Unmarshal(data, &item); err != nil {
			return nil, err
		}
		if string(item.CachedDocument) == "null" {
			item.CachedDocument = nil
		}
		result = append(result, item)
	}
	slices.SortFunc(result, func(a, b IndexSource) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return result, nil
}

// GetIndex decodes one fixture source or returns the production missing-source sentinel.
func (s *memoryIndexes) GetIndex(ctx context.Context, id int64) (*IndexSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, exists := s.rows[id]
	if !exists {
		return nil, ErrIndexNotFound
	}
	var item IndexSource
	if err := json.Unmarshal(data, &item); err != nil {
		return nil, err
	}
	if string(item.CachedDocument) == "null" {
		item.CachedDocument = nil
	}
	return &item, nil
}

// SaveIndex persists one independent fixture source and assigns new identities on creation.
// Existing identifiers must remain present. Caller values are serialized so later
// changes cannot mutate a published cache before the next explicit write.
func (s *memoryIndexes) SaveIndex(ctx context.Context, item *IndexSource) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if item.ID == 0 {
		s.next++
		item.ID = s.next
		item.CreatedAt = time.Now()
	} else if _, exists := s.rows[item.ID]; !exists {
		return ErrIndexNotFound
	}
	item.UpdatedAt = time.Now()
	data, err := json.Marshal(item)
	if err == nil {
		s.rows[item.ID] = data
	}
	return err
}

// DeleteIndex removes a custom fixture source while preserving the builtin deletion guard.
func (s *memoryIndexes) DeleteIndex(ctx context.Context, id int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, exists := s.rows[id]
	if !exists {
		return ErrIndexNotFound
	}
	var item IndexSource
	if err := json.Unmarshal(data, &item); err != nil {
		return err
	}
	if item.Builtin {
		return ErrIndexNotFound
	}
	delete(s.rows, id)
	return nil
}

// catalogServiceFixture creates an enabled catalog with isolated storage and cleanup ownership.
func catalogServiceFixture(t *testing.T, fetch IndexFetcher) (*CatalogService, *memoryIndexes, *memoryInstallations) {
	t.Helper()
	_, _, host := catalogFixture()
	indexes := &memoryIndexes{rows: make(map[int64][]byte)}
	installations := newMemoryInstallations()
	service := NewCatalogService(CatalogOptions{Enabled: true, Host: host}, indexes, installations, fetch, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { require.NoError(t, service.Stop(context.Background())) })
	return service, indexes, installations
}

// catalogJSON serializes validated release fixtures for accepted-cache and transfer scenarios.
func catalogJSON(t *testing.T, version string) []byte {
	t.Helper()
	_, document, _ := catalogFixture(catalogRelease(version))
	require.NoError(t, document.Validate())
	data, err := json.Marshal(document)
	require.NoError(t, err)
	return data
}
