package plugin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// catalog_refresh_test.go controls shared transfers and cache publication under cancellation.
// Synthetic time and explicit barriers verify singleflight, retained accepted data,
// and rejected-cache replacement without depending on network
// scheduling or external signing infrastructure.

// TestCatalogRefreshSharesWorkAndPreservesCacheAfterSignatureFailure joins two waiters and cancels one while the source transfer remains active.
// The surviving waiter receives one accepted cache, and a later invalid signature updates diagnostics without replacing that document or timestamp.
func TestCatalogRefreshSharesWorkAndPreservesCacheAfterSignatureFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		data := catalogJSON(t, "1.0.0")
		entered := make(chan struct{})
		release := make(chan struct{})
		var calls atomic.Int32
		service, store, _ := catalogServiceFixture(t, indexFetchFunc(func(ctx context.Context, request IndexFetchRequest) (FetchedIndex, error) {
			if calls.Add(1) == 1 {
				close(entered)
				<-release
				return FetchedIndex{Document: data, SignatureStatus: SignatureCustom}, nil
			}
			return FetchedIndex{SignatureStatus: SignatureInvalid}, ErrIndexSignatureInvalid
		}))
		source := &IndexSource{Name: "Test", URL: "https://example.invalid/index.json", Enabled: true}
		require.NoError(t, store.SaveIndex(t.Context(), source))
		ctx, cancel := context.WithCancel(t.Context())
		first := make(chan error, 1)
		second := make(chan error, 1)
		go func() { first <- service.Refresh(ctx, source.ID) }()
		<-entered
		go func() { second <- service.Refresh(t.Context(), source.ID) }()
		synctest.Wait()
		cancel()
		require.ErrorIs(t, <-first, context.Canceled)
		require.Equal(t, int32(1), calls.Load())
		close(release)
		require.NoError(t, <-second)
		accepted, err := store.GetIndex(t.Context(), source.ID)
		require.NoError(t, err)
		require.JSONEq(t, string(data), string(accepted.CachedDocument))
		require.ErrorIs(t, service.Refresh(t.Context(), source.ID), ErrIndexSignatureInvalid)
		failed, err := store.GetIndex(t.Context(), source.ID)
		require.NoError(t, err)
		require.JSONEq(t, string(accepted.CachedDocument), string(failed.CachedDocument))
		require.Equal(t, accepted.CachedAt, failed.CachedAt)
		require.Equal(t, SignatureInvalid, failed.SignatureStatus)
		require.NotEmpty(t, failed.LastError)
	})
}

// TestCatalogRefreshRejectsAuthenticatedMalformedData supplies verified bytes that do not satisfy the catalog schema.
// Schema failure preserves the last accepted document, demonstrating that cryptographic verification alone cannot publish an unusable cache.
func TestCatalogRefreshRejectsAuthenticatedMalformedData(t *testing.T) {
	service, store, _ := catalogServiceFixture(t, indexFetchFunc(func(context.Context, IndexFetchRequest) (FetchedIndex, error) {
		return FetchedIndex{Document: []byte(`{"schemaVersion":99}`), SignatureStatus: SignatureOfficial}, nil
	}))
	now := time.Now()
	source := &IndexSource{Name: "Test", URL: "https://example.invalid/index.json", Enabled: true, CachedDocument: catalogJSON(t, "1.0.0"), CachedAt: &now, SignatureStatus: SignatureOfficial}
	require.NoError(t, store.SaveIndex(t.Context(), source))
	err := service.Refresh(t.Context(), source.ID)
	require.True(t, errors.Is(err, ErrIndexInvalid))
	stored, err := store.GetIndex(t.Context(), source.ID)
	require.NoError(t, err)
	require.JSONEq(t, string(source.CachedDocument), string(stored.CachedDocument))
	require.True(t, stored.CachedAt.Equal(*source.CachedAt))
}

// TestIndexFailureMessageKeepsReadableBoundedCauses projects structured source failures into administrator status text.
// Internal error serialization disappears while useful causes remain, and truncation never splits a multibyte character or exceeds the response budget.
func TestIndexFailureMessageKeepsReadableBoundedCauses(t *testing.T) {
	message := indexFailureMessage(fmt.Errorf("%w: official keys are not configured", ErrIndexSignatureInvalid))
	require.Equal(t, "Plugin index signature is invalid: official keys are not configured", message)
	message = indexFailureMessage(ErrDownloadFailed.WithCause(errors.New("fixture transfer timed out")))
	require.Equal(t, "Plugin download failed: fixture transfer timed out", message)
	message = indexFailureMessage(errors.New(strings.Repeat("界", 1000)))
	require.LessOrEqual(t, len(message), 1024)
	require.True(t, utf8.ValidString(message))
	require.True(t, strings.HasSuffix(message, "..."))
}
