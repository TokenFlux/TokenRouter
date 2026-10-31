//go:build integration

package plugins

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/infra/crypto"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/internal/plugin/postgres"
	"github.com/TokenFlux/TokenRouter/internal/plugin/provider"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// catalog_scenario_integration_test.go exercises signed distribution, durable
// installation, and native release recovery across catalog_service.go, service_versions.go,
// provider/index_client.go, and provider/package_download.go together. A local HTTPS origin serves
// synthetic archives through the configured GitHub mirror prefix, while PostgreSQL
// retains accepted metadata and the real SDK fixture exposes startup outcomes.

// TestSignedCatalogInstallationAndFailedUpdate runs signed index and package transfers before a native release replacement.
// Tampering retains accepted cache, checksum failure leaves no installation, permission refusal leaves the current release untouched, and failed initialization restores running code.
func TestSignedCatalogInstallationAndFailedUpdate(t *testing.T) {
	db := databaseSuite.New(t)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	store := postgres.New(client)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	platform := runtime.GOOS + "/" + runtime.GOARCH
	files := make(map[string][]byte)
	var mu sync.Mutex
	var mirrored int
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/mirror/https://github.com/") {
			mirrored++
		}
		data, found := files[r.URL.Path]
		if !found {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(origin.Close)
	releases := make([]index.Version, 0, 3)
	for _, version := range []string{"3.0.0", "2.0.0", "1.0.0"} {
		permissions := []string{}
		if version == "3.0.0" {
			permissions = []string{"wallet.credit"}
		}
		release, archive := catalogArchive(t, "test.installation", version, permissions)
		location := "https://github.com/example/plugin/releases/download/v" + version + "/plugin.tar.gz"
		artifact := release.Artifacts[platform]
		artifact.URL = location
		release.Artifacts[platform] = artifact
		files["/mirror/"+location] = archive
		releases = append(releases, release)
	}
	broken, archive := catalogArchive(t, "test.corrupt", "1.0.0", nil)
	brokenArtifact := broken.Artifacts[platform]
	brokenArtifact.URL = "https://github.com/example/broken/releases/download/v1.0.0/plugin.tar.gz"
	broken.Artifacts[platform] = brokenArtifact
	archive[len(archive)-1] ^= 1
	files["/mirror/"+brokenArtifact.URL] = archive
	document := index.Document{SchemaVersion: 1, Name: "Fixture", GeneratedAt: time.Now(), Plugins: []index.Plugin{
		{ID: "test.installation", Repository: "https://github.com/example/plugin", Categories: []string{"tools"}, Versions: releases},
		{ID: "test.corrupt", Repository: "https://github.com/example/broken", Categories: []string{"tools"}, Versions: []index.Version{broken}},
	}}
	require.NoError(t, document.Validate())
	raw, err := json.Marshal(document)
	require.NoError(t, err)
	signature, err := index.Sign(raw, private)
	require.NoError(t, err)
	files["/index.json"], files["/index.json.sig"] = raw, signature
	// Constructors clone the transport, so fixture trust is captured before the
	// process-wide default is restored and background catalog work can begin.
	previousTransport := http.DefaultTransport
	http.DefaultTransport = origin.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	directory := t.TempDir()
	packages, packageErr := provider.NewPackageFiles(directory, provider.DownloadOptions{URLPrefix: origin.URL + "/mirror/"})
	fetcher, fetchErr := provider.NewIndexClient("", nil)
	http.DefaultTransport = previousTransport
	require.NoError(t, packageErr)
	require.NoError(t, fetchErr)
	cipher, err := crypto.NewAESEncryptor(bytes.Repeat([]byte{8}, 32))
	require.NoError(t, err)
	configuration := plugin.NewConfiguration(cipher, true)
	processes := provider.NewProcesses(plugin.RuntimeOptions{HostVersion: "0.6.0", InstanceID: "catalog-test", Timezone: "UTC"}, logger, nil)
	manager := plugin.NewManager(plugin.ManagerOptions{Enabled: true, MaxRunning: 20, HostVersion: "0.6.0", StateRoot: filepath.Join(directory, "state")}, store, packages, processes, configuration, logger)
	catalog := plugin.NewCatalogService(plugin.CatalogOptions{Enabled: true, Host: manifest.HostInfo{Version: "0.6.0", APIVersions: []int{1}, Platform: platform, Permissions: manifest.Permissions()}}, store, store, fetcher, logger)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		require.NoError(t, catalog.Stop(ctx))
		require.NoError(t, manager.Stop(ctx))
	})
	name, location, key := "Fixture", origin.URL+"/index.json", base64.StdEncoding.EncodeToString(public)
	source, err := catalog.CreateIndex(t.Context(), plugin.IndexInput{Name: &name, URL: &location, PublicKey: &key})
	require.NoError(t, err)
	require.NoError(t, catalog.Refresh(t.Context(), source.ID))
	accepted, err := store.GetIndex(t.Context(), source.ID)
	require.NoError(t, err)
	require.Equal(t, plugin.SignatureCustom, accepted.SignatureStatus)
	service := plugin.NewService(manager, packages, catalog, processes)
	_, err = service.Install(t.Context(), plugin.InstallInput{Source: plugin.SourceIndex, IndexID: source.ID, PluginID: "test.corrupt", Version: "1.0.0"}, 1)
	require.ErrorIs(t, err, plugin.ErrChecksumMismatch)
	_, err = store.Get(t.Context(), "test.corrupt")
	require.ErrorIs(t, err, plugin.ErrPluginNotFound)
	installed, err := service.Install(t.Context(), plugin.InstallInput{Source: plugin.SourceIndex, IndexID: source.ID, PluginID: "test.installation", Version: "1.0.0", Enable: true}, 1)
	require.NoError(t, err)
	require.True(t, installed.NeedsConfiguration)
	require.NoError(t, service.SetConfig(t.Context(), "test.installation", map[string]any{"credential": "fixture-secret", "label": "retained"}, 1))
	installed, err = service.Enable(t.Context(), "test.installation", 1)
	require.NoError(t, err)
	require.Equal(t, plugin.StateRunning, installed.Status.State)
	before, err := store.Get(t.Context(), "test.installation")
	require.NoError(t, err)
	_, err = service.Update(t.Context(), "test.installation", plugin.UpdateInput{Version: "3.0.0"}, 1)
	require.ErrorIs(t, err, plugin.ErrPermissionsNotAccepted)
	_, err = service.Update(t.Context(), "test.installation", plugin.UpdateInput{Version: "2.0.0"}, 1)
	require.ErrorIs(t, err, plugin.ErrUpdateFailed)
	after, err := store.Get(t.Context(), "test.installation")
	require.NoError(t, err)
	require.Equal(t, before.VersionSnapshot, after.VersionSnapshot)
	require.Nil(t, after.Previous)
	require.Equal(t, plugin.StateRunning, manager.Status("test.installation").State)
	mu.Lock()
	files["/index.json"] = append(bytes.Clone(raw), ' ')
	observedMirrors := mirrored
	mu.Unlock()
	require.Positive(t, observedMirrors)
	require.ErrorIs(t, catalog.Refresh(t.Context(), source.ID), plugin.ErrIndexSignatureInvalid)
	rejected, err := store.GetIndex(t.Context(), source.ID)
	require.NoError(t, err)
	require.Equal(t, accepted.CachedDocument, rejected.CachedDocument)
	require.Equal(t, accepted.CachedAt, rejected.CachedAt)
	require.Equal(t, plugin.SignatureInvalid, rejected.SignatureStatus)
	entry, err := catalog.Get(t.Context(), source.ID, "test.installation")
	require.NoError(t, err)
	require.Len(t, entry.Versions, 3)
}

// catalogArchive packages one synthetic native release and its exact published declaration.
// Archive identity is computed before the test supplies a remote URL. The same
// real executable rejects version two during Init, permitting deterministic recovery assertions.
func catalogArchive(t *testing.T, id, version string, permissions []string) (index.Version, []byte) {
	t.Helper()
	binary, err := os.ReadFile(installationBinary)
	require.NoError(t, err)
	declaration := &manifest.Manifest{
		SchemaVersion: 1, ID: id, Version: version, APIVersion: 1, MinHostVersion: "0.5.0", Name: manifest.LocalizedText{"en": "Fixture"}, Description: manifest.LocalizedText{"en": "Native distribution fixture"}, Permissions: permissions,
		Backend: &manifest.Backend{Executable: "bin/plugin"}, Config: manifest.Config{Fields: []manifest.Field{
			{Key: "credential", Type: "secret", Required: true, Label: manifest.LocalizedText{"en": "Credential"}},
			{Key: "label", Type: "string", Label: manifest.LocalizedText{"en": "Label"}},
		}},
	}
	raw, err := json.Marshal(declaration)
	require.NoError(t, err)
	name := "bin/plugin"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for path, data := range map[string][]byte{"plugin.json": raw, name: binary} {
		require.NoError(t, archive.WriteHeader(&tar.Header{Name: path, Mode: 0o755, Typeflag: tar.TypeReg, Size: int64(len(data))}))
		_, err := archive.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, archive.Close())
	require.NoError(t, compressed.Close())
	manifestHash, archiveHash := sha256.Sum256(raw), sha256.Sum256(buffer.Bytes())
	return index.Version{Version: version, PublishedAt: time.Now(), Manifest: declaration, ManifestSHA256: hex.EncodeToString(manifestHash[:]), Artifacts: map[string]index.Artifact{runtime.GOOS + "/" + runtime.GOARCH: {SHA256: hex.EncodeToString(archiveHash[:]), Size: int64(buffer.Len())}}}, buffer.Bytes()
}
