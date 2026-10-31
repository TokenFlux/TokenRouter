package plugin

import (
	"context"
	"encoding/base64"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// catalog_indexes_test.go protects source identity and accepted-cache ownership.
// Builtin configuration changes preserve administrator enablement, while custom
// trust changes invalidate old cache bytes. Controlled transfers prove source
// deletion cannot race a late cache publication into recreating the row.

// TestBuiltinSourcePreservesIdentityAndEnablement restarts catalog initialization with a changed configured location.
// The same row remains disabled, its old cache is cleared, and administrative authority edits or deletion are rejected.
func TestBuiltinSourcePreservesIdentityAndEnablement(t *testing.T) {
	catalog, indexes, _ := catalogServiceFixture(t, indexFetchFunc(func(context.Context, IndexFetchRequest) (FetchedIndex, error) {
		return FetchedIndex{}, ErrIndexInvalid
	}))
	catalog.options.OfficialURL = "https://example.invalid/v1/index.json"
	require.NoError(t, catalog.Start(t.Context()))
	rows, err := catalog.ListIndexes(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	id := rows[0].ID
	disabled := false
	_, err = catalog.UpdateIndex(t.Context(), id, IndexInput{Enabled: &disabled})
	require.NoError(t, err)
	row, err := indexes.GetIndex(t.Context(), id)
	require.NoError(t, err)
	row.CachedDocument = catalogJSON(t, "1.0.0")
	now := time.Now()
	row.CachedAt = &now
	require.NoError(t, indexes.SaveIndex(t.Context(), row))
	catalog.options.OfficialURL = "https://mirror.invalid/v1/index.json"
	require.NoError(t, catalog.Start(t.Context()))
	row, err = indexes.GetIndex(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, id, row.ID)
	require.False(t, row.Enabled)
	require.Empty(t, row.CachedDocument)
	require.Nil(t, row.CachedAt)
	require.Equal(t, catalog.options.OfficialURL, row.URL)
	name := "Replacement"
	_, err = catalog.UpdateIndex(t.Context(), id, IndexInput{Name: &name})
	require.ErrorIs(t, err, ErrIndexInvalid)
	require.ErrorIs(t, catalog.DeleteIndex(t.Context(), id), ErrIndexNotFound)
}

// TestCustomIndexTrustChangeClearsAcceptedCache replaces the verification key of a disabled source.
// The previous authority's document and signature status are removed, and duplicate source URLs remain rejected before persistence.
func TestCustomIndexTrustChangeClearsAcceptedCache(t *testing.T) {
	catalog, indexes, _ := catalogServiceFixture(t, indexFetchFunc(func(context.Context, IndexFetchRequest) (FetchedIndex, error) {
		return FetchedIndex{}, ErrIndexInvalid
	}))
	name, location, disabled := "Custom", "https://custom.invalid/index.json", false
	row, err := catalog.CreateIndex(t.Context(), IndexInput{Name: &name, URL: &location, Enabled: &disabled})
	require.NoError(t, err)
	row.CachedDocument = catalogJSON(t, "1.0.0")
	row.SignatureStatus = SignatureUnsigned
	require.NoError(t, indexes.SaveIndex(t.Context(), row))
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	changed, err := catalog.UpdateIndex(t.Context(), row.ID, IndexInput{PublicKey: &key})
	require.NoError(t, err)
	require.Empty(t, changed.CachedDocument)
	require.Equal(t, SignatureUnknown, changed.SignatureStatus)
	require.Equal(t, key, changed.PublicKey)
	_, err = catalog.CreateIndex(t.Context(), IndexInput{Name: &name, URL: &location, Enabled: &disabled})
	require.ErrorIs(t, err, ErrIndexInvalid)
}

// TestDeleteIndexWaitsForActualRefreshReturn overlaps deletion with a transport that has not returned.
// Deletion waits for accepted-cache publication, then removes the source permanently, so a late worker cannot resurrect its registration.
func TestDeleteIndexWaitsForActualRefreshReturn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		document := catalogJSON(t, "1.0.0")
		catalog, indexes, _ := catalogServiceFixture(t, indexFetchFunc(func(context.Context, IndexFetchRequest) (FetchedIndex, error) {
			close(entered)
			<-release
			return FetchedIndex{Document: document, SignatureStatus: SignatureUnsigned}, nil
		}))
		row := &IndexSource{Name: "Custom", URL: "https://custom.invalid/index.json", Enabled: true}
		require.NoError(t, indexes.SaveIndex(t.Context(), row))
		flight, err := catalog.beginRefresh(row.ID)
		require.NoError(t, err)
		<-entered
		deleted := make(chan error, 1)
		go func() { deleted <- catalog.DeleteIndex(t.Context(), row.ID) }()
		synctest.Wait()
		select {
		case err := <-deleted:
			t.Fatalf("deletion returned before transfer completion: %v", err)
		default:
		}
		close(release)
		require.NoError(t, <-deleted)
		<-flight.done
		require.NoError(t, flight.err)
		_, err = indexes.GetIndex(t.Context(), row.ID)
		require.ErrorIs(t, err, ErrIndexNotFound)
	})
}
