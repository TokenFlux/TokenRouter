package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDownloadRejectsHTTPAndDowngradeRedirects exposes a local TLS redirect to an unencrypted destination.
// Both a direct HTTP URL and a downgrade redirect fail before archive bytes can be accepted.
func TestDownloadRejectsHTTPAndDowngradeRedirects(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.invalid/plugin", http.StatusFound)
	}))
	defer server.Close()
	download, err := newDownloader(DownloadOptions{})
	require.NoError(t, err)
	download.client.Transport = server.Client().Transport
	_, err = download.get(t.Context(), "http://example.invalid/plugin")
	require.Error(t, err)
	_, err = download.get(t.Context(), server.URL)
	require.Error(t, err)
}
