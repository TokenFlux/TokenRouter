//go:build integration

package postgres

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// TestIndexRemovalPreservesAcceptedSnapshots deletes a custom catalog referenced by current and previous installation versions.
// Both foreign keys become absent while artifact identity and rollback configuration remain; the builtin catalog cannot be deleted.
func TestIndexRemovalPreservesAcceptedSnapshots(t *testing.T) {
	store, _, _ := persistenceFixture(t)
	source := &plugin.IndexSource{Name: "Custom", URL: "https://example.invalid/index.json", Enabled: true, SignatureStatus: "unsigned"}
	require.NoError(t, store.SaveIndex(t.Context(), source))
	require.Positive(t, source.ID)
	item := storedInstallation(t)
	item.IndexID = &source.ID
	item.Source = plugin.SourceIndex
	previous := item.VersionSnapshot
	item.Previous = &previous
	require.NoError(t, store.Save(t.Context(), item))
	require.NoError(t, store.DeleteIndex(t.Context(), source.ID))
	read, err := store.Get(t.Context(), item.PluginID)
	require.NoError(t, err)
	require.Nil(t, read.IndexID)
	require.Nil(t, read.Previous.IndexID)
	require.Equal(t, item.ManifestSHA256, read.ManifestSHA256)
	builtin := &plugin.IndexSource{Name: "Official", URL: "https://official.invalid/index.json", Builtin: true, Enabled: true, SignatureStatus: "unknown"}
	require.NoError(t, store.SaveIndex(t.Context(), builtin))
	require.ErrorIs(t, store.DeleteIndex(t.Context(), builtin.ID), plugin.ErrIndexNotFound)
	indexes, err := store.ListIndexes(t.Context())
	require.NoError(t, err)
	require.Len(t, indexes, 1)
}
