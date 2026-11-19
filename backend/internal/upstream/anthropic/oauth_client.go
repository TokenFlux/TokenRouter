package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/imroc/req/v3"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic/oauth"
)

// oauth_client.go discovers Claude organizations, obtains authorization codes,
// and exchanges or refreshes OAuth credentials.
// Claude cookie authorization discovers an organization, requests a redirect
// containing an authorization code, and exchanges that code through the
// platform token endpoint. These calls keep the session cookie in the two
// browser-facing requests, while token exchange and refresh send JSON
// credential proofs.
//
// The redirect's code and optional state travel as one code#state value between
// methods. Exchange splits that value and sends its embedded state; its
// separate state and setup-token arguments do not influence the payload. The
// factory supplies each request's HTTP client and proxy choice, and request
// contexts carry cancellation. Higher-level authorization and refresh services
// own session validation, persistence, and competing credential updates.

// OAuthClient performs Claude browser authorization and platform token HTTP
// calls.
type OAuthClient struct {
	// BaseURL and TokenURL select browser and credential endpoints. ClientFactory
	// creates or supplies transport for each call; callers keep this configuration
	// stable during concurrent use.
	BaseURL  string // browser endpoint prefix
	TokenURL string // credential exchange endpoint
	// ClientFactory needs to return a usable req client on successful calls; each
	// OAuth method invokes it before forming the HTTP request.
	ClientFactory func(proxyURL string) (*req.Client, error) // per-call client supplier
}

// NewOAuthClient configures Claude browser and token endpoints with the default
// client factory. Callers may replace fields for transport injection before
// use, keeping shared configuration stable during concurrent requests.
func NewOAuthClient() *OAuthClient {
	return &OAuthClient{
		BaseURL:       "https://claude.ai",
		TokenURL:      oauth.TokenURL,
		ClientFactory: createReqClient,
	}
}

// GetOrganizationUUID requests organizations with the supplied session cookie
// and request context. Factory, HTTP, decoding, and unsuccessful-status errors
// return to the caller; an empty organization list is also an error.
//
// A single returned organization is selected directly. With multiple
// organizations, the first raven_type=team entry wins, and the first list entry
// is selected when no team is present. This resolves cookie authorization's
// organization choice using response order; the caller receives the chosen UUID
// and remains responsible for the broader authorization session. The selected
// UUID is returned as supplied, including empty text when its field is omitted.
func (s *OAuthClient) GetOrganizationUUID(ctx context.Context, sessionKey, proxyURL string) (string, error) {
	client, err := s.ClientFactory(proxyURL)
	if err != nil {
		return "", fmt.Errorf("create HTTP client: %w", err)
	}

	var orgs []struct {
		UUID string `json:"uuid"` // organization identifier text
		Name string `json:"name"` // organization display name
		// RavenType can be nil when the response omits classification. During
		// multi-organization selection, GetOrganizationUUID treats that entry as a
		// non-team candidate.
		RavenType *string `json:"raven_type"` // optional team selection label
	}

	targetURL := s.BaseURL + "/api/organizations"
	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 1: Getting organization UUID from %s", targetURL)

	resp, err := client.R().
		SetContext(ctx).
		SetCookies(&http.Cookie{
			Name:  "sessionKey",
			Value: sessionKey,
		}).
		SetSuccessResult(&orgs).
		Get(targetURL)
	if err != nil {
		logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 1 FAILED - Request error: %v", err)
		return "", fmt.Errorf("request failed: %w", err)
	}

	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 1 Response - Status: %d", resp.StatusCode)

	if !resp.IsSuccessState() {
		return "", fmt.Errorf("failed to get organizations: status %d, body: %s", resp.StatusCode, resp.String())
	}

	if len(orgs) == 0 {
		return "", fmt.Errorf("no organizations found")
	}

	if len(orgs) == 1 {
		logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 1 SUCCESS - Single org found, UUID: %s, Name: %s", orgs[0].UUID, orgs[0].Name)
		return orgs[0].UUID, nil
	}

	// Multiple cookie organizations need one authorization target. Select the
	// first team entry in response order before applying the first-entry default.
	for _, org := range orgs {
		if org.RavenType != nil && *org.RavenType == "team" {
			logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 1 SUCCESS - Selected team org, UUID: %s, Name: %s, RavenType: %s",
				org.UUID, org.Name, *org.RavenType)
			return org.UUID, nil
		}
	}

	// When no team entry matched, use response order to resolve the target;
	// this keeps cookie authorization usable for sessions containing only other
	// organization types.
	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 1 SUCCESS - No team org found, using first org, UUID: %s, Name: %s", orgs[0].UUID, orgs[0].Name)
	return orgs[0].UUID, nil
}

// GetAuthorizationCode posts organization, scope, state, and PKCE challenge
// using the supplied session cookie. Request context reaches the HTTP call, and
// unsuccessful responses or missing code information return errors.
//
// The response redirect is parsed as a URL and its code query value becomes the
// result. When the redirect also contains state, the result joins them as
// code#state for ExchangeCodeForToken to split. This method does not compare
// returned state or validate the redirect host, so authorization-session checks
// remain with its caller.
func (s *OAuthClient) GetAuthorizationCode(ctx context.Context, sessionKey, orgUUID, scope, codeChallenge, state, proxyURL string) (string, error) {
	client, err := s.ClientFactory(proxyURL)
	if err != nil {
		return "", fmt.Errorf("create HTTP client: %w", err)
	}

	authURL := fmt.Sprintf("%s/v1/oauth/%s/authorize", s.BaseURL, orgUUID)

	reqBody := map[string]any{
		"response_type":         "code",
		"client_id":             oauth.ClientID,
		"organization_uuid":     orgUUID,
		"redirect_uri":          oauth.RedirectURI,
		"scope":                 scope,
		"state":                 state,
		"code_challenge":        codeChallenge,
		"code_challenge_method": "S256",
	}

	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 2: Getting authorization code from %s", authURL)
	// The diagnostic map contains strings, so JSON encoding has no unsupported
	// value to reject. Redaction affects the logged copy while the original map
	// supplies authorization proof.
	reqBodyJSON, _ := json.Marshal(logredact.RedactMap(reqBody))
	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 2 Request Body: %s", string(reqBodyJSON))

	var result struct {
		RedirectURI string `json:"redirect_uri"` // code-bearing redirect URL
	}

	resp, err := client.R().
		SetContext(ctx).
		SetCookies(&http.Cookie{
			Name:  "sessionKey",
			Value: sessionKey,
		}).
		SetHeader("Accept", "application/json").
		SetHeader("Accept-Language", "en-US,en;q=0.9").
		SetHeader("Cache-Control", "no-cache").
		SetHeader("Origin", "https://claude.ai").
		SetHeader("Referer", "https://claude.ai/new").
		SetHeader("Content-Type", "application/json").
		SetBody(reqBody).
		SetSuccessResult(&result).
		Post(authURL)
	if err != nil {
		logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 2 FAILED - Request error: %v", err)
		return "", fmt.Errorf("request failed: %w", err)
	}

	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 2 Response - Status: %d, Body: %s", resp.StatusCode, logredact.RedactJSON(resp.Bytes()))

	if !resp.IsSuccessState() {
		return "", fmt.Errorf("failed to get authorization code: status %d, body: %s", resp.StatusCode, resp.String())
	}

	if result.RedirectURI == "" {
		return "", fmt.Errorf("no redirect_uri in response")
	}

	parsedURL, err := url.Parse(result.RedirectURI)
	if err != nil {
		return "", fmt.Errorf("failed to parse redirect_uri: %w", err)
	}

	queryParams := parsedURL.Query()
	authCode := queryParams.Get("code")
	responseState := queryParams.Get("state")

	if authCode == "" {
		return "", fmt.Errorf("no authorization code in redirect_uri")
	}

	// Carry returned state beside the authorization code for the next exchange
	// call. The joined value retains the response's state even when it differs
	// from the requested state.
	fullCode := authCode
	if responseState != "" {
		fullCode = authCode + "#" + responseState
	}

	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 2 SUCCESS - Got authorization code")
	return fullCode, nil
}

// ExchangeCodeForToken posts an authorization-code proof and decodes the
// returned token response under ctx. Code may contain an embedded state after
// its first hash separator; that state is sent when nonempty.
//
// The separate state argument and isSetupToken flag do not affect this payload.
// Either setup flag value sends the client, callback, and verifier fields with no
// expires_in request value. Factory, transport, decoding, and non-success
// responses return errors; credential persistence and validation of the
// authorization session remain with higher-level callers.
func (s *OAuthClient) ExchangeCodeForToken(ctx context.Context, code, codeVerifier, state, proxyURL string, isSetupToken bool) (*oauth.TokenResponse, error) {
	client, err := s.ClientFactory(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("create HTTP client: %w", err)
	}

	// Recover the state carried by GetAuthorizationCode's joined result before
	// serializing the exchange proof; only the first separator splits these two values.
	authCode := code
	codeState := ""
	if before, after, ok := strings.Cut(code, "#"); ok {
		authCode = before
		codeState = after
	}

	reqBody := map[string]any{
		"code":          authCode,
		"grant_type":    "authorization_code",
		"client_id":     oauth.ClientID,
		"redirect_uri":  oauth.RedirectURI,
		"code_verifier": codeVerifier,
	}

	if codeState != "" {
		reqBody["state"] = codeState
	}

	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 3: Exchanging code for token at %s", s.TokenURL)
	// The diagnostic copy uses only string values, allowing its encoding error to
	// be discarded. Token exchange still serializes reqBody with its code,
	// optional state, and verifier.
	reqBodyJSON, _ := json.Marshal(logredact.RedactMap(reqBody))
	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 3 Request Body: %s", string(reqBodyJSON))

	var tokenResp oauth.TokenResponse

	resp, err := client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json, text/plain, */*").
		SetHeader("Content-Type", "application/json").
		SetHeader("User-Agent", "axios/1.13.6").
		SetBody(reqBody).
		SetSuccessResult(&tokenResp).
		Post(s.TokenURL)
	if err != nil {
		logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 3 FAILED - Request error: %v", err)
		return nil, fmt.Errorf("request failed: %w", err)
	}

	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 3 Response - Status: %d, Body: %s", resp.StatusCode, logredact.RedactJSON(resp.Bytes()))

	if !resp.IsSuccessState() {
		return nil, fmt.Errorf("token exchange failed: status %d, body: %s", resp.StatusCode, resp.String())
	}

	logger.LegacyPrintf("repository.claude_oauth", "[OAuth] Step 3 SUCCESS - Got access token")
	return &tokenResp, nil
}

// RefreshToken posts the supplied refresh proof and returns decoded token data
// under ctx. It returns client, request, decoding, and unsuccessful-status
// errors; callers decide how returned credentials are persisted and coordinate
// competing refreshes.
func (s *OAuthClient) RefreshToken(ctx context.Context, refreshToken, proxyURL string) (*oauth.TokenResponse, error) {
	client, err := s.ClientFactory(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("create HTTP client: %w", err)
	}

	reqBody := map[string]any{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     oauth.ClientID,
	}

	var tokenResp oauth.TokenResponse

	resp, err := client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json, text/plain, */*").
		SetHeader("Content-Type", "application/json").
		SetHeader("User-Agent", "axios/1.13.6").
		SetBody(reqBody).
		SetSuccessResult(&tokenResp).
		Post(s.TokenURL)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if !resp.IsSuccessState() {
		return nil, fmt.Errorf("token refresh failed: status %d, body: %s", resp.StatusCode, resp.String())
	}

	return &tokenResp, nil
}

// createReqClient selects the shared Chrome-impersonating req client with a
// sixty-second timeout and disabled cookie jar so authorization requests reuse
// pooled connections without accumulating cookies across sessions.
func createReqClient(proxyURL string) (*req.Client, error) {
	return httpclient.GetSharedReqClient(httpclient.ReqClientOptions{
		ProxyURL:    proxyURL,
		Timeout:     60 * time.Second,
		Impersonate: true,
	})
}
