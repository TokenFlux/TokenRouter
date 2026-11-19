package codeassist

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

// oauth_client.go exchanges authorization codes and refresh tokens for the
// selected Gemini OAuth account type.
// Code Assist and Google One token exchanges use the built-in Gemini CLI
// credentials even when configured credentials exist. AI Studio reads its
// configured client on each exchange or refresh, and the shared HTTP client
// pool supplies proxy routing and a sixty-second timeout.

// OAuthClient exchanges Google authorization codes and refresh tokens using
// dynamic configuration.
type OAuthClient struct {
	TokenURL string // Google token exchange endpoint
	// Config is required and called once per exchange or refresh. Its current
	// credentials are replaced for Code Assist and Google One before effective
	// configuration is resolved.
	Config func() OAuthConfig // current OAuth configuration callback
}

// TokenResponse represents Google access, refresh, expiry, and granted-scope
// observations.
type TokenResponse = wire.TokenResponse

// NewOAuthClient retains config and the fixed Google token URL for later
// requests. Callers supply a nonnil callback; each exchange or refresh reads
// current configuration through that callback.
func NewOAuthClient(config func() OAuthConfig) *OAuthClient {
	return &OAuthClient{TokenURL: TokenURL, Config: config}
}

// ExchangeCode exchanges the supplied code, verifier, and redirect URI for
// token fields. Code Assist and Google One replace configured credentials with
// the built-in client; HTTP, proxy, configuration, and decode errors return to
// the caller.
func (c *OAuthClient) ExchangeCode(ctx context.Context, oauthType, code, codeVerifier, redirectURI, proxyURL string) (*TokenResponse, error) {
	// Resolve each request from current configuration, then clear configured
	// credentials for the CLI variants so EffectiveOAuthConfig selects their
	// built-in OAuth identity.
	oauthCfgInput := c.Config()
	if oauthType == "code_assist" || oauthType == "google_one" {
		oauthCfgInput.ClientID = ""
		oauthCfgInput.ClientSecret = ""
	}

	oauthCfg, err := EffectiveOAuthConfig(oauthCfgInput, oauthType)
	if err != nil {
		return nil, err
	}

	formData := url.Values{}
	formData.Set("grant_type", "authorization_code")
	formData.Set("client_id", oauthCfg.ClientID)
	formData.Set("client_secret", oauthCfg.ClientSecret)
	formData.Set("code", code)
	formData.Set("code_verifier", codeVerifier)
	formData.Set("redirect_uri", redirectURI)

	return c.postTokenForm(ctx, proxyURL, formData, "token exchange failed")
}

// RefreshToken exchanges refreshToken using current effective OAuth
// configuration. Code Assist and Google One select built-in credentials before
// resolving scopes; the returned fields are server observations and do not
// validate access-token presence here.
func (c *OAuthClient) RefreshToken(ctx context.Context, oauthType, refreshToken, proxyURL string) (*TokenResponse, error) {
	oauthCfgInput := c.Config()
	if oauthType == "code_assist" || oauthType == "google_one" {
		// Refresh with the same built-in client identity used by these CLI
		// authorization variants, even when the current application configuration
		// contains custom client credentials.
		oauthCfgInput.ClientID = ""
		oauthCfgInput.ClientSecret = ""
	}

	oauthCfg, err := EffectiveOAuthConfig(oauthCfgInput, oauthType)
	if err != nil {
		return nil, err
	}

	formData := url.Values{}
	formData.Set("grant_type", "refresh_token")
	formData.Set("refresh_token", refreshToken)
	formData.Set("client_id", oauthCfg.ClientID)
	formData.Set("client_secret", oauthCfg.ClientSecret)

	return c.postTokenForm(ctx, proxyURL, formData, "token refresh failed")
}

// postTokenForm posts formData to TokenURL through the shared HTTP client pool
// and decodes a successful TokenResponse.
func (c *OAuthClient) postTokenForm(ctx context.Context, proxyURL string, formData url.Values, errPrefix string) (*TokenResponse, error) {
	client, err := CreateOAuthHTTPClient(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("create HTTP client: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Cap Google OAuth token responses at 4 MiB to bound buffered error or JSON reads.
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%s: status %d, body: %s", errPrefix, resp.StatusCode, SanitizeBodyForLogs(string(bodyBytes)))
	}
	var tokenResp TokenResponse
	if err := json.Unmarshal(bodyBytes, &tokenResp); err != nil {
		return nil, fmt.Errorf("decode token response: %w", err)
	}
	return &tokenResp, nil
}

// CreateOAuthHTTPClient selects the shared proxy-aware HTTP client with a
// sixty-second timeout. Client construction errors return to token operations.
func CreateOAuthHTTPClient(proxyURL string) (*http.Client, error) {
	return httpclient.GetClient(httpclient.Options{
		ProxyURL: proxyURL,
		Timeout:  60 * time.Second,
	})
}
