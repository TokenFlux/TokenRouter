package provider

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/index"
)

// index_client_test.go verifies catalog transfer trust decisions through real TLS HTTP fixtures.
// Generated keys remain local to each test, and the transport trusts only its
// fixture certificate. Cases distinguish missing optional signatures, explicit pins,
// official mirrors, tampering, redirect policy, and streamed body size enforcement.

// TestIndexClientClassifiesTrustPolicies fetches the same catalog under builtin, pinned, mirror, and unsigned policies.
// Existing invalid signatures never downgrade to unsigned, and a configured pin cannot be bypassed by a different official signer.
func TestIndexClientClassifiesTrustPolicies(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	other, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	data := []byte(`{"schemaVersion":1}`)
	signature, err := index.Sign(data, private)
	require.NoError(t, err)
	encoded := base64.StdEncoding.EncodeToString(public)
	for _, test := range []struct {
		name      string   // named source trust scenario
		builtin   bool     // official verification is mandatory
		pin       string   // optional configured verification key
		roots     []string // injected official verification roots
		signature []byte   // fixture detached signature response
		status    int      // fixture signature HTTP status
		want      string   // expected signature classification
		failure   bool     // verification or transfer must fail
	}{
		{name: "builtin", builtin: true, roots: []string{encoded}, signature: signature, status: 200, want: plugin.SignatureOfficial},
		{name: "official mirror", roots: []string{encoded}, signature: signature, status: 200, want: plugin.SignatureOfficial},
		{name: "custom pin", pin: encoded, signature: signature, status: 200, want: plugin.SignatureCustom},
		{name: "pinned official", pin: encoded, roots: []string{encoded}, signature: signature, status: 200, want: plugin.SignatureOfficial},
		{name: "unsigned", status: 404, want: plugin.SignatureUnsigned},
		{name: "unknown signer", signature: signature, status: 200, want: plugin.SignatureInvalid, failure: true},
		{name: "malformed signature", roots: []string{encoded}, signature: []byte("invalid"), status: 200, want: plugin.SignatureInvalid, failure: true},
		{name: "missing pinned signature", pin: encoded, status: 404, want: plugin.SignatureInvalid, failure: true},
		{name: "missing builtin signature", builtin: true, roots: []string{encoded}, status: 404, want: plugin.SignatureInvalid, failure: true},
		{name: "pin mismatch", pin: base64.StdEncoding.EncodeToString(other), roots: []string{encoded}, signature: signature, status: 200, want: plugin.SignatureInvalid, failure: true},
		{name: "signature transport failure", status: 503, failure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Empty(t, r.Header.Get("Authorization"))
				if r.URL.Path == "/index.json" {
					_, _ = w.Write(data)
					return
				}
				w.WriteHeader(test.status)
				_, _ = w.Write(test.signature)
			}))
			t.Cleanup(server.Close)
			client, err := NewIndexClient("", test.roots)
			require.NoError(t, err)
			client.client.Transport = server.Client().Transport
			result, err := client.Fetch(t.Context(), plugin.IndexFetchRequest{URL: server.URL + "/index.json", PublicKey: test.pin, Builtin: test.builtin})
			require.Equal(t, test.want, result.SignatureStatus)
			if test.failure {
				require.Error(t, err)
				require.Empty(t, result.Document)
			} else {
				require.NoError(t, err)
				require.JSONEq(t, string(data), string(result.Document))
			}
		})
	}
}

// TestIndexClientPreservesEncodedSignatureLocation uses an encoded mirror path and a query on both catalog resources.
// Signature lookup preserves their exact escaped path meaning, and tampering with the downloaded document remains rejected by the pinned key.
func TestIndexClientPreservesEncodedSignatureLocation(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	data := []byte(`{"name":"original"}`)
	signature, err := index.Sign(data, private)
	require.NoError(t, err)
	var tampered atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "token=fixture", r.URL.RawQuery)
		switch r.URL.EscapedPath() {
		case "/mirror%2Findex.json":
			if tampered.Load() {
				_, _ = io.WriteString(w, `{"name":"changed"}`)
			} else {
				_, _ = w.Write(data)
			}
		case "/mirror%2Findex.json.sig":
			_, _ = w.Write(signature)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewIndexClient("", nil)
	require.NoError(t, err)
	client.client.Transport = server.Client().Transport
	request := plugin.IndexFetchRequest{URL: server.URL + "/mirror%2Findex.json?token=fixture", PublicKey: base64.StdEncoding.EncodeToString(public)}
	_, err = client.Fetch(t.Context(), request)
	require.NoError(t, err)
	tampered.Store(true)
	result, err := client.Fetch(t.Context(), request)
	require.ErrorIs(t, err, plugin.ErrIndexSignatureInvalid)
	require.Equal(t, plugin.SignatureInvalid, result.SignatureStatus)
}

// TestIndexClientBoundsResponsesAndRedirects sends a chunked response beyond its read limit and an insecure redirect.
// Both fail before an accepted document is returned, and an unprovisioned builtin source cannot silently become unsigned.
func TestIndexClientBoundsResponsesAndRedirects(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "http://insecure.invalid/index.json", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		require.NoError(t, http.NewResponseController(w).Flush())
		_, _ = io.WriteString(w, strings.Repeat("x", 9))
	}))
	t.Cleanup(server.Close)
	client, err := NewIndexClient("", nil)
	require.NoError(t, err)
	client.client.Transport = server.Client().Transport
	_, _, err = client.read(t.Context(), server.URL+"/large", 8)
	require.Error(t, err)
	_, err = client.Fetch(t.Context(), plugin.IndexFetchRequest{URL: server.URL + "/redirect"})
	require.ErrorIs(t, err, plugin.ErrDownloadFailed)
	_, err = client.Fetch(t.Context(), plugin.IndexFetchRequest{URL: server.URL + "/index.json", Builtin: true})
	require.ErrorIs(t, err, plugin.ErrIndexSignatureInvalid)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = client.Fetch(ctx, plugin.IndexFetchRequest{URL: server.URL + "/index.json"})
	require.Error(t, err)
}

// TestIndexClientUsesPublishedDocumentBudget checks the actual catalog Fetch limit.
// A response at the shared publisher byte allowance remains readable, while an
// oversized response is rejected before its bytes can become an accepted cache.
func TestIndexClientUsesPublishedDocumentBudget(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/exact":
			_, _ = io.WriteString(w, strings.Repeat("x", index.MaxDocumentBytes))
		case "/oversized":
			w.Header().Set("Content-Length", strconv.Itoa(index.MaxDocumentBytes+1))
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewIndexClient("", nil)
	require.NoError(t, err)
	client.client.Transport = server.Client().Transport
	result, err := client.Fetch(t.Context(), plugin.IndexFetchRequest{URL: server.URL + "/exact"})
	require.NoError(t, err)
	require.Len(t, result.Document, index.MaxDocumentBytes)
	result, err = client.Fetch(t.Context(), plugin.IndexFetchRequest{URL: server.URL + "/oversized"})
	require.ErrorIs(t, err, plugin.ErrDownloadFailed)
	require.Empty(t, result.Document)
}
