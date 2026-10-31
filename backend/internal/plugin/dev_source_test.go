package plugin

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// directoryFixture returns supplied package declarations without filesystem access.
type directoryFixture map[string]DirectoryPackage

// ReadDirectory returns the declaration selected by the configured fixture directory.
func (f directoryFixture) ReadDirectory(_ context.Context, directory string) (DirectoryPackage, error) {
	return f[directory], nil
}

// TestDevSourceRequiresBothSwitches checks that directories remain inactive unless both switches permit them.
// Disabled source loading never attempts filesystem inspection or publishes development records.
func TestDevSourceRequiresBothSwitches(t *testing.T) {
	for _, options := range []DevOptions{{Enabled: false, DevMode: true}, {Enabled: true, DevMode: false}} {
		source := NewDevSource(options, nil, newMemoryInstallations(), NewConfiguration(nil, false), slog.New(slog.NewTextHandler(io.Discard, nil)))
		require.NoError(t, source.Load(t.Context()))
		items, err := source.List(t.Context())
		require.NoError(t, err)
		require.Empty(t, items)
	}
}

// TestDevSourceOverlayPreservesDurableIdentity loads a static and native development declaration beside a colliding durable record.
// It verifies ordered ordinary installation records, isolated reads, and no durable shadowing.
func TestDevSourceOverlayPreservesDurableIdentity(t *testing.T) {
	store := newMemoryInstallations()
	require.NoError(t, store.Save(t.Context(), &Installation{PluginID: "test.alpha", VersionSnapshot: VersionSnapshot{Version: "2.0.0", Source: SourceUpload}}))
	reader := directoryFixture{
		"z": {Manifest: &manifest.Manifest{ID: "test.zulu", Version: "1.0.0", APIVersion: 1, Backend: &manifest.Backend{}, Permissions: []string{"wallet.credit"}}},
		"a": {Manifest: &manifest.Manifest{ID: "test.alpha", Version: "1.0.0", APIVersion: 1}},
		"s": {Manifest: &manifest.Manifest{ID: "test.static", Version: "1.0.0", APIVersion: 1}},
	}
	source := NewDevSource(DevOptions{Enabled: true, DevMode: true, Directories: []string{"z", "a", "s"}}, reader, store, NewConfiguration(nil, false), slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, source.Load(t.Context()))
	items, err := source.List(t.Context())
	require.NoError(t, err)
	require.Len(t, items, 3)
	require.Equal(t, "2.0.0", items[0].Version)
	require.Equal(t, "test.static", items[1].PluginID)
	require.Equal(t, SourceDev, items[1].Source)
	require.True(t, items[1].Enabled)
	require.Equal(t, []string{"wallet.credit"}, items[2].GrantedPermissions)
	items[2].GrantedPermissions[0] = "wallet.debit"
	reread, err := source.Get(t.Context(), "test.zulu")
	require.NoError(t, err)
	require.Equal(t, []string{"wallet.credit"}, reread.GrantedPermissions)
	require.NoError(t, source.Delete(t.Context(), "test.zulu", true))
	_, err = source.Get(t.Context(), "test.zulu")
	require.ErrorIs(t, err, ErrPluginNotFound)
	require.Equal(t, []bool{true}, store.purge)
}
