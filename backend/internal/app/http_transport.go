package app

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/transport"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	providerauth "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// http_transport.go constructs upstream transport and OAuth clients from
// application configuration. Upstream transport reads application options
// through a callback while requests execute. The app maps resolved-IP checks,
// connection-pool settings, timeouts, and HTTP/2 policy into transport options;
// platform OAuth and usage clients receive their respective configuration
// readers or transport interfaces.

// provideHTTPUpstream constructs the shared transport with a getter over cfg.
// Option readers see configuration at use time; callers coordinate any cfg
// writes with concurrent requests because this callback reads the captured
// object directly.
// @project-doc docs/architecture/system_architecture.md#dependency_layers
func provideHTTPUpstream(cfg *config.Config) *transport.Client {
	return transport.New(func() *transport.Options {
		return httpTransportOptions(cfg)
	})
}

// httpTransportOptions maps process configuration to fresh transport options.
// Nil configuration returns nil so transport option readers select their own
// defaults; nested HTTP/2 settings are copied into the returned options.
func httpTransportOptions(cfg *config.Config) *transport.Options {
	if cfg == nil {
		return nil
	}
	gateway := cfg.Gateway
	http2 := gateway.OpenAI.HTTP2
	return &transport.Options{
		// These deployment flags govern the configured resolved-IP check. Allowing
		// private hosts clears this option; request-specific policy belongs to the
		// transport's per-request policy handling.
		ValidateResolvedIP:          cfg.Security.URLAllowlist.Enabled && !cfg.Security.URLAllowlist.AllowPrivateHosts,
		ConnectionPoolIsolation:     gateway.Transport.ConnectionPoolIsolation,
		MaxUpstreamClients:          gateway.Transport.ClientCache.MaxClients,
		ClientIdleTTLSeconds:        gateway.Transport.ClientCache.IdleTTLSeconds,
		MaxIdleConns:                gateway.Transport.MaxIdleConns,
		MaxIdleConnsPerHost:         gateway.Transport.MaxIdleConnsPerHost,
		MaxConnsPerHost:             gateway.Transport.MaxConnsPerHost,
		IdleConnTimeoutSeconds:      gateway.Transport.IdleConnTimeoutSeconds,
		ResponseHeaderTimeout:       gateway.Transport.ResponseHeaderTimeoutSeconds,
		OpenAIResponseHeaderTimeout: gateway.OpenAI.ResponseHeaderTimeoutSeconds,
		GrokResponseHeaderTimeout:   gateway.Grok.ResponseHeaderTimeoutSeconds,
		OpenAIHTTP2: transport.HTTP2Options{
			Enabled:                   http2.Enabled,
			AllowProxyFallbackToHTTP1: http2.AllowProxyFallbackToHTTP1,
			FallbackErrorThreshold:    http2.FallbackErrorThreshold,
			FallbackWindowSeconds:     http2.FallbackWindowSeconds,
			FallbackTTLSeconds:        http2.FallbackTTLSeconds,
		},
	}
}

// provideClaudeUsageFetcher constructs Claude usage queries with the supplied
// TLS-aware upstream callback. A nil upstream leaves that callback unset, so
// the usage client selects its ordinary HTTP path even when a profile is supplied.
func provideClaudeUsageFetcher(upstream httpclient.UpstreamTransport) provideradapter.ClaudeUsageClient {
	// The usage client supports ordinary HTTP when a custom TLS transport is
	// absent. Leave its callback unset so that client chooses the ordinary path.
	if upstream == nil {
		return anthropic.NewUsageClient(nil)
	}
	return anthropic.NewUsageClient(upstream.DoWithTLS)
}

// provideOpenAIOAuthClient constructs OpenAI OAuth with the supplied TLS
// transport. Requests with a TLS profile use that transport and fail when it
// is nil; requests without a profile use the OAuth client's shared req client.
func provideOpenAIOAuthClient(upstream httpclient.UpstreamTransport) provideradapter.OpenAIOAuthClient {
	return openai.NewOAuthClient(upstream)
}

// provideGeminiOAuthClient constructs a Google OAuth client with a cfg getter.
// The getter reads the ClientID, ClientSecret, and Scopes string values at use time.
// Exchanges and refreshes read those values from the captured cfg, so callers
// coordinate cfg writes with concurrent client use.
func provideGeminiOAuthClient(cfg *config.Config) providerauth.GeminiOAuthClient {
	return codeassist.NewOAuthClient(func() codeassist.OAuthConfig {
		return codeassist.OAuthConfig{
			ClientID:     cfg.Provider.Gemini.OAuth.ClientID,
			ClientSecret: cfg.Provider.Gemini.OAuth.ClientSecret,
			Scopes:       cfg.Provider.Gemini.OAuth.Scopes,
		}
	})
}
