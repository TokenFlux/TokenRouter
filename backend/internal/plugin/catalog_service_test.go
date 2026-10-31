package plugin

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

// catalog_service_test.go exercises service lifetime and source-qualified reads.
// Controlled transfer fixtures verify that shutdown waits for real completion
// and that stopped workers cannot publish late caches, while ordinary market
// results remain independent of the transport implementation.

// TestCatalogStopWaitsForActualFetcherReturn cancels service ownership while a transfer deliberately ignores cancellation.
// A deadline interrupts only the wait, and the eventual late result cannot publish a cache after the source context has been sealed.
func TestCatalogStopWaitsForActualFetcherReturn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		data := catalogJSON(t, "1.0.0")
		entered := make(chan struct{})
		release := make(chan struct{})
		service, store, _ := catalogServiceFixture(t, indexFetchFunc(func(context.Context, IndexFetchRequest) (FetchedIndex, error) {
			close(entered)
			<-release
			return FetchedIndex{Document: data, SignatureStatus: SignatureUnsigned}, nil
		}))
		source := &IndexSource{Name: "Test", URL: "https://example.invalid/index.json", Enabled: true}
		require.NoError(t, store.SaveIndex(t.Context(), source))
		result := make(chan error, 1)
		go func() { result <- service.Refresh(t.Context(), source.ID) }()
		<-entered
		deadline, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		require.ErrorIs(t, service.Stop(deadline), context.DeadlineExceeded)
		close(release)
		require.ErrorIs(t, <-result, context.Canceled)
		require.NoError(t, service.Stop(t.Context()))
		stored, err := store.GetIndex(t.Context(), source.ID)
		require.NoError(t, err)
		require.Empty(t, stored.CachedDocument)
		require.ErrorIs(t, service.Refresh(t.Context(), source.ID), ErrPluginsDisabled)
	})
}
