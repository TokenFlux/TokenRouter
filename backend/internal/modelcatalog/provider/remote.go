package provider

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
)

// remote.go fetches bounded catalog bodies with conditional HTTP requests and
// configured proxy policy.
// A configured catalog proxy can fail during client construction before any
// HTTP request is sent. Construction represents that failure as a client that
// reports it on every fetch unless the configured policy permits replacing
// the client with one using the standard HTTP transport. An empty proxy uses
// the shared transport's direct configuration.
//
// Conditional catalog requests pair a body with its ETag. A 304 result keeps
// the supplied validator and marks the body unchanged for the publication
// caller, while successful downloads return fresh body bytes and their response
// validator. Downloads read one byte past a fixed size limit to distinguish
// complete accepted bodies from oversized candidates before parsing begins.

// remoteClient fetches catalog bodies through a configured HTTP transport.
type remoteClient struct {
	httpClient *http.Client // retained catalog HTTP client
}

// remoteClientError retains a proxy initialization failure.
// Every fetch returns that failure, preventing an unintended direct connection.
type remoteClientError struct {
	err error // retained client initialization failure
}

// FetchCatalog returns the retained initialization failure for every request.
// Context, URL, and validator inputs do not recreate the transport, so callers
// construct another client after repairing its configuration.
func (c *remoteClientError) FetchCatalog(_ context.Context, _, _ string) ([]byte, string, bool, error) {
	return nil, "", false, c.err
}

// NewRemoteClient creates a catalog client with a thirty-second request
// timeout and the shared HTTP client's proxy handling. Empty proxy values
// select its direct configuration; supported schemes include HTTP, HTTPS,
// SOCKS5, and SOCKS5H.
//
// A configured proxy failure returns a permanently failing client when
// allowDirectOnProxyError is false. Permitting replacement creates an
// http.Client using Go's default transport, whose environment proxy settings
// can still apply. The returned client retains its HTTP transport for repeated
// catalog requests and leaves successful shared clients unmodified.
func NewRemoteClient(proxyURL string, allowDirectOnProxyError bool) RemoteClient {
	// Proxy initialization errors can reach server logs and the returned error
	// client. Their formatting comes from httpclient.GetClient; this caller adds
	// context and passes the diagnostic through to those consumers.
	sharedClient, err := httpclient.GetClient(httpclient.Options{
		Timeout:  30 * time.Second,
		ProxyURL: proxyURL,
	})
	if err != nil {
		if strings.TrimSpace(proxyURL) != "" && !allowDirectOnProxyError {
			slog.Warn("proxy client init failed, all requests will fail", "service", "pricing", "error", err)
			return &remoteClientError{err: fmt.Errorf("proxy client init failed and direct fallback is disabled; set outbound_proxy.allow_direct_on_error=true to allow fallback: %w", err)}
		}
		// The policy permits replacing a failed configured client. Use the
		// standard transport here, including its environment proxy behavior,
		// with the same catalog request timeout.
		sharedClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &remoteClient{
		httpClient: sharedClient,
	}
}

// FetchCatalog downloads a catalog with an optional If-None-Match validator
// and the caller's cancellation context. A 304 response returns the supplied
// ETag with unchanged true and no body, allowing publication to retain its
// current catalog bytes.
//
// HTTP 200 returns body bytes and the response ETag with unchanged false.
// Other statuses, request failures, and bodies exceeding 32 MiB return an
// error; the body is closed before return. Read errors can accompany partial
// downloaded bytes, so callers use those bytes only when the error is nil.
func (c *remoteClient) FetchCatalog(ctx context.Context, url, etag string) ([]byte, string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", false, err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", false, err
	}
	// Close the response on every status and size path so the retained client
	// releases request resources after the fetch consumes or rejects its
	// response, before the catalog candidate or error is returned.
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotModified {
		return nil, etag, true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", false, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// Read one byte beyond the accepted catalog size to detect overflow without
	// treating a truncated prefix as a complete JSON candidate for the
	// publication caller.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024+1))
	if len(body) > 32*1024*1024 {
		return nil, "", false, fmt.Errorf("model catalog exceeds 32 MiB")
	}
	return body, resp.Header.Get("ETag"), false, err
}
