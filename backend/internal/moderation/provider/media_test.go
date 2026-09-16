package provider

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/moderation/contract"
	"github.com/stretchr/testify/require"
)

func TestContentModerationSnapshotStopsImmediatelyWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	media := make([]contract.ContentModerationMedia, 100)
	for index := range media {
		media[index] = contract.ContentModerationMedia{OriginalRef: "https://example.com/image.png", SnapshotStatus: "pending"}
	}

	result := SnapshotMedia(ctx, media)

	require.Len(t, result, len(media))
	for _, item := range result {
		require.Equal(t, "error", item.SnapshotStatus)
		require.Contains(t, item.SnapshotError, context.Canceled.Error())
	}
}
func TestContentModerationSnapshotTotalByteBudgetIsAtomic(t *testing.T) {
	var retained atomic.Int64

	require.True(t, reserveContentModerationSnapshotBytes(&retained, maxContentModerationSnapshotTotalBytes-1))
	require.False(t, reserveContentModerationSnapshotBytes(&retained, 2))
	require.Equal(t, int64(maxContentModerationSnapshotTotalBytes-1), retained.Load())
}
func TestContentModerationDataImageRejectsDecodedPayloadOverLimit(t *testing.T) {
	payload := strings.Repeat("A", base64.StdEncoding.EncodedLen(maxContentModerationSnapshotBytes+1))

	_, _, err := decodeContentModerationDataImage("data:image/png;base64," + payload)

	require.Error(t, err)
	require.Contains(t, err.Error(), "20 MB")
}
func TestContentModerationSnapshotRejectsWrongMIMEAndPrivateRedirect(t *testing.T) {
	_, err := normalizeContentModerationImageMIME("text/plain")
	require.Error(t, err)

	client := newContentModerationSnapshotClient()
	redirect := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/redirected.png", nil)
	require.Error(t, client.CheckRedirect(redirect, nil))
}
func TestContentModerationRemoteSnapshotRejectsPrivateAddress(t *testing.T) {
	_, _, err := fetchContentModerationImage(context.Background(), "http://127.0.0.1/private.png")

	require.Error(t, err)
	require.Contains(t, err.Error(), "私网")
}
