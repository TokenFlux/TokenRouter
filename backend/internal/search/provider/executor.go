// Executor 拥有原 HTTP 客户端缓存；替换配置后只关闭空闲连接，不打断在途请求。
package provider

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/proxy"
	"github.com/TokenFlux/TokenRouter/internal/search/contract"
)

type SearchRequest = contract.SearchRequest
type SearchResponse = contract.SearchResponse
type SearchResult = contract.SearchResult
type Provider = contract.Provider
type ProviderConfig = contract.ProviderConfig

const defaultMaxResults = contract.DefaultMaxResults
const proxyDialTimeout = 3 * time.Second
const proxyTLSTimeout = 3 * time.Second
const searchDataTimeout = 60 * time.Second
const searchRequestTimeout = searchDataTimeout + proxyDialTimeout
const maxCachedClients = 100

type Executor struct {
	clientMu    sync.Mutex
	clientCache map[string]*http.Client
}

func NewExecutor() *Executor { return &Executor{clientCache: make(map[string]*http.Client)} }
func (m *Executor) CloseIdle() {
	m.clientMu.Lock()
	defer m.clientMu.Unlock()
	for _, c := range m.clientCache {
		c.CloseIdleConnections()
	}
}
func (m *Executor) Search(ctx context.Context, cfg ProviderConfig, req SearchRequest) (*SearchResponse, error) {
	proxyURL := cfg.ProxyURL
	if req.ProxyURL != "" {
		proxyURL = req.ProxyURL
	}
	client, err := m.getOrCreateHTTPClient(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("websearch: %w", err)
	}
	provider := m.buildProvider(cfg, client)
	return provider.Search(ctx, req)
}
func (m *Executor) getOrCreateHTTPClient(proxyURL string) (*http.Client, error) {
	m.clientMu.Lock()
	defer m.clientMu.Unlock()

	if c, ok := m.clientCache[proxyURL]; ok {
		return c, nil
	}
	if len(m.clientCache) >= maxCachedClients {
		m.clientCache = make(map[string]*http.Client)
	}
	c, err := newHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	m.clientCache[proxyURL] = c
	return c, nil
}

// newHTTPClient creates an HTTP client with proper timeout settings.
// Uses proxy.ConfigureTransportProxy for unified proxy protocol support
// (HTTP/HTTPS/SOCKS5/SOCKS5H).
// Returns error if proxyURL is invalid — never falls back to direct connection.
func newHTTPClient(proxyURL string) (*http.Client, error) {
	transport := &http.Transport{
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		DialContext:           (&net.Dialer{Timeout: proxyDialTimeout}).DialContext,
		TLSHandshakeTimeout:   proxyTLSTimeout,
		ResponseHeaderTimeout: searchDataTimeout,
	}
	if proxyURL != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL %q: %w", proxyURL, err)
		}
		if err := proxy.ConfigureTransportProxy(transport, parsed); err != nil {
			return nil, fmt.Errorf("configure proxy: %w", err)
		}
	}
	return &http.Client{Transport: transport, Timeout: searchRequestTimeout}, nil
}
func (m *Executor) buildProvider(cfg ProviderConfig, client *http.Client) Provider {
	switch cfg.Type {
	case braveProviderName:
		return NewBraveProvider(cfg.APIKey, client)
	case tavilyProviderName:
		return NewTavilyProvider(cfg.APIKey, client)
	default:
		slog.Warn("websearch: unknown provider type, falling back to brave",
			"type", cfg.Type)
		return NewBraveProvider(cfg.APIKey, client)
	}
}

// isProxyError checks whether the error is likely caused by proxy or network connectivity
// (as opposed to an API-level error from the search provider).
func isProxyError(err error) bool {
	if err == nil {
		return false
	}
	// Network-level errors (timeout, connection refused, DNS failure)
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	// TLS handshake failures (often caused by proxy intercepting/blocking)
	var tlsErr *tls.RecordHeaderError
	if errors.As(err, &tlsErr) {
		return true
	}
	// String-based detection for wrapped errors
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "proxy") ||
		strings.Contains(msg, "socks") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "tls handshake") ||
		strings.Contains(msg, "certificate")
}
func (m *Executor) IsProxyError(err error) bool { return isProxyError(err) }
