package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// oauth_client.go exchanges OAuth authorization codes and refresh tokens
// through the selected transport and client identity.
// OAuth code exchange and refresh submit form-encoded requests to the
// configured token endpoint. A supplied TLS profile selects the shared
// provider-aware HTTP transport; requests with only a User-Agent option keep
// the shared req client path with its two-minute timeout.
//
// The first request-options value controls identity and TLS selection. An
// explicit User-Agent is sent without a generated originator, while absent UA
// uses the canonical paired authentication identity. Direct transport failures
// can produce a proxy-selection hint when no proxy is configured and the
// context is still live; caller cancellation retains its ordinary error
// handling.

// OAuthClient holds the token endpoint and optional provider-aware HTTP
// transport.
type OAuthClient struct {
	tokenURL     string            // selected OAuth token endpoint
	httpUpstream OAuthHTTPUpstream // provider-aware TLS exchange transport
}

// OAuthTokenRequestOptions selects client identity and TLS settings for one
// token exchange.
type OAuthTokenRequestOptions struct {
	// UserAgent is trimmed when supplied and sent without a generated originator; a
	// blank value selects the canonical paired authentication identity.
	UserAgent string
	// TLSProfile selects the provider-aware transport when nonnil; absent profiles
	// keep the shared req client path even when other options are supplied.
	TLSProfile          *tlsfingerprint.Profile
	ProviderID          int64 // transport isolation provider identifier
	ProviderConcurrency int   // provider transport concurrency value
}

// OAuthHTTPUpstream sends requests through caller-selected provider and TLS
// transport resources.
type OAuthHTTPUpstream interface {
	// DoWithTLS sends a request using proxy, provider capacity, and the selected
	// TLS profile. Callers close successful response bodies after their consumer
	// finishes reading to release transport ownership.
	DoWithTLS(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error)
}

// NewOAuthClient creates an OAuth client using TokenURL and the supplied
// transport. A nil transport is sufficient for the req path; exchanges
// selecting a TLS profile require the provider-aware transport.
func NewOAuthClient(httpUpstream OAuthHTTPUpstream) *OAuthClient {
	return &OAuthClient{tokenURL: TokenURL, httpUpstream: httpUpstream}
}

// ExchangeCode submits an authorization code and PKCE verifier for token
// exchange. Empty redirect and trimmed-empty client ID use compiled defaults;
// only the first options value is used, and ctx controls the request.
func (s *OAuthClient) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI, proxyURL, clientID string, options ...OAuthTokenRequestOptions) (*TokenResponse, error) {
	if redirectURI == "" {
		redirectURI = DefaultRedirectURI
	}
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = ClientID
	}

	formData := url.Values{}
	formData.Set("grant_type", "authorization_code")
	formData.Set("client_id", clientID)
	formData.Set("code", code)
	formData.Set("redirect_uri", redirectURI)
	formData.Set("code_verifier", codeVerifier)

	return s.postTokenForm(ctx, formData, proxyURL, "OPENAI_OAUTH_TOKEN_EXCHANGE_FAILED", "token exchange failed", options...)
}

// RefreshToken refreshes tokens using the default OpenAI OAuth client ID. It
// delegates request construction and accepts the same optional identity/TLS
// settings; callers persist returned credentials through their provider use
// case.
func (s *OAuthClient) RefreshToken(ctx context.Context, refreshToken, proxyURL string, options ...OAuthTokenRequestOptions) (*TokenResponse, error) {
	return s.RefreshTokenWithClientID(ctx, refreshToken, proxyURL, "", options...)
}

// RefreshTokenWithClientID submits a refresh using the trimmed supplied client
// ID. Blank IDs use ClientID, and the first optional request setting controls
// identity and whether the provider-aware TLS path is selected.
func (s *OAuthClient) RefreshTokenWithClientID(ctx context.Context, refreshToken, proxyURL string, clientID string, options ...OAuthTokenRequestOptions) (*TokenResponse, error) {
	// Use the supplied client ID; legacy records without one use the OpenAI default.
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = ClientID
	}
	return s.refreshTokenWithClientID(ctx, refreshToken, proxyURL, clientID, options...)
}

// refreshTokenWithClientID builds the refresh grant with RefreshScopes and
// delegates sending. The caller resolves clientID first; scope excludes
// offline_access while the response remains the token endpoint response value.
func (s *OAuthClient) refreshTokenWithClientID(ctx context.Context, refreshToken, proxyURL, clientID string, options ...OAuthTokenRequestOptions) (*TokenResponse, error) {
	formData := url.Values{}
	formData.Set("grant_type", "refresh_token")
	formData.Set("refresh_token", refreshToken)
	formData.Set("client_id", clientID)
	formData.Set("scope", RefreshScopes)

	return s.postTokenForm(ctx, formData, proxyURL, "OPENAI_OAUTH_TOKEN_REFRESH_FAILED", "token refresh failed", options...)
}

// postTokenForm selects the first option and sends through its TLS path or the
// shared req client. It returns decoded success credentials; status failures
// use the supplied reason/message and eligible direct transport failures
// receive the proxy hint.
func (s *OAuthClient) postTokenForm(ctx context.Context, formData url.Values, proxyURL, failureReason, failureMessage string, options ...OAuthTokenRequestOptions) (*TokenResponse, error) {
	option := firstOpenAIOAuthTokenRequestOption(options)
	if option.TLSProfile != nil {
		// A TLS profile requires the shared provider-aware pool so the selected
		// fingerprint, proxy, and provider isolation accompany the token exchange together.
		return s.postTokenFormWithTLS(ctx, formData, proxyURL, failureReason, failureMessage, option)
	}

	client, err := CreateOAuthHTTPClient(proxyURL)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_OAUTH_CLIENT_INIT_FAILED", "create HTTP client: %v", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_OAUTH_REQUEST_FAILED", "build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	userAgent, originator := resolveOpenAIOAuthTokenIdentity(option)
	req.Header.Set("User-Agent", userAgent)
	if originator != "" {
		req.Header.Set("originator", originator)
	}

	resp, err := client.Do(req)
	if err != nil {
		// Direct failures with a live caller context receive proxy-selection guidance;
		// configured proxy failures and cancellation remain in their normal request-error path.
		if shouldReturnOpenAINoProxyHint(ctx, proxyURL, err) {
			return nil, newOpenAINoProxyHintError(err)
		}
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_OAUTH_REQUEST_FAILED", "request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Cap OpenAI OAuth token responses at 4 MiB to bound buffered error or JSON reads.
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_OAUTH_REQUEST_FAILED", "read response: %v", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, infraerrors.Newf(http.StatusBadGateway, failureReason, "%s: status %d, body: %s", failureMessage, resp.StatusCode, string(bodyBytes))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(bodyBytes, &tokenResp); err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_OAUTH_REQUEST_FAILED", "decode token response: %v", err)
	}
	return &tokenResp, nil
}

// postTokenFormWithTLS sends a form request through the provider-aware HTTP
// pool and closes its response body. A configured pool is required; provider
// ID, concurrency, proxy, and TLS profile select the transport resources used
// for this exchange.
func (s *OAuthClient) postTokenFormWithTLS(ctx context.Context, formData url.Values, proxyURL, failureReason, failureMessage string, option OAuthTokenRequestOptions) (*TokenResponse, error) {
	if s.httpUpstream == nil {
		return nil, infraerrors.New(http.StatusBadGateway, "OPENAI_OAUTH_CLIENT_INIT_FAILED", "HTTP upstream is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_OAUTH_REQUEST_FAILED", "build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	userAgent, originator := resolveOpenAIOAuthTokenIdentity(option)
	req.Header.Set("User-Agent", userAgent)
	if originator != "" {
		req.Header.Set("originator", originator)
	}
	req = req.WithContext(upstream.WithHTTPUpstreamProfile(req.Context(), upstream.HTTPUpstreamProfileOpenAI))

	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, option.ProviderID, option.ProviderConcurrency, option.TLSProfile)
	if err != nil {
		// Direct failures with a live caller context receive proxy-selection guidance;
		// configured proxy failures and cancellation remain in their normal request-error path.
		if shouldReturnOpenAINoProxyHint(ctx, proxyURL, err) {
			return nil, newOpenAINoProxyHintError(err)
		}
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_OAUTH_REQUEST_FAILED", "request failed: %v", err)
	}
	// Release pool ownership on body read, status, and decode exits so failed
	// token exchanges cannot retain provider capacity through an acquired response body.
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_OAUTH_REQUEST_FAILED", "read response: %v", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, infraerrors.Newf(http.StatusBadGateway, failureReason, "%s: status %d, body: %s", failureMessage, resp.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, infraerrors.Newf(http.StatusBadGateway, "OPENAI_OAUTH_REQUEST_FAILED", "decode token response: %v", err)
	}
	return &tokenResp, nil
}

// CreateOAuthHTTPClient returns the shared HTTP client configured for proxyURL
// and a two-minute timeout. The client belongs to the shared HTTP
// infrastructure, so callers leave its cached configuration unchanged.
func CreateOAuthHTTPClient(proxyURL string) (*http.Client, error) {
	return httpclient.GetClient(httpclient.Options{
		ProxyURL: proxyURL,
		Timeout:  120 * time.Second,
	})
}

// firstOpenAIOAuthTokenRequestOption returns the first value or empty default
// settings. Later variadic options are ignored, keeping one identity and TLS
// configuration active for each OAuth token request.
func firstOpenAIOAuthTokenRequestOption(options []OAuthTokenRequestOptions) OAuthTokenRequestOptions {
	if len(options) == 0 {
		return OAuthTokenRequestOptions{}
	}
	return options[0]
}

// resolveOpenAIOAuthTokenIdentity trims an explicit User-Agent and pairs it
// with an empty originator. Missing UA returns the canonical auth identity,
// allowing token requests to distinguish configured UA from generated Codex
// identity.
func resolveOpenAIOAuthTokenIdentity(option OAuthTokenRequestOptions) (string, string) {
	if ua := strings.TrimSpace(option.UserAgent); ua != "" {
		return ua, ""
	}
	return CodexCanonicalAuthIdentity()
}

// shouldReturnOpenAINoProxyHint reports whether a direct request failure
// warrants a proxy-selection hint. A configured proxy, nil error, cancelled
// context, or context.Canceled cause suppresses the hint; other live direct
// failures qualify.
func shouldReturnOpenAINoProxyHint(ctx context.Context, proxyURL string, err error) bool {
	if strings.TrimSpace(proxyURL) != "" || err == nil {
		return false
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	return !errors.Is(err, context.Canceled)
}

// newOpenAINoProxyHintError wraps a direct OAuth failure with proxy-selection
// guidance. It attaches cause to the BadGateway application error so callers
// can retain the transport failure while reporting the specific recovery
// message.
func newOpenAINoProxyHintError(cause error) error {
	return infraerrors.New(
		http.StatusBadGateway,
		"OPENAI_OAUTH_PROXY_REQUIRED",
		"OpenAI OAuth request failed: no proxy is configured and this server could not reach OpenAI directly. Select a proxy that can access OpenAI, then retry; if the authorization code has expired, regenerate the authorization URL.",
	).WithCause(cause)
}
