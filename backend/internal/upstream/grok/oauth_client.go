package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/imroc/req/v3"

	sharedhttp "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	wiregrok "github.com/TokenFlux/TokenRouter/internal/protocol/grok"
)

// oauth_client.go exchanges Grok OAuth grants, obtains password-login SSO
// tokens, and maps authorization failures.
// OAuth token exchanges use a validated endpoint pinned when OAuthClient is
// constructed. Invalid token overrides select the trusted default before
// codes or refresh tokens are sent. Password authorization follows a separate
// accounts-session path and returns a temporary SSO token that its caller
// converts and keeps out of stored provider credentials.
//
// Error handling separates explicit entitlement denial from other forbidden
// token responses. Quota-exhaustion phrases take precedence over denial
// markers so those responses retain the ordinary exchange failure category.
// SSO conversion maps typed status and context errors separately and uses a
// client that exposes redirects to the device flow. CAPTCHA task polling uses
// the request context and a local polling deadline, while each network
// exchange depends on its client's timeout and cancellation handling.

// Password login uses fixed accounts and Turnstile task endpoints; these values
// locate its session exchange and the CAPTCHA provider's create and polling requests.
const (
	providersBaseURL     = "https://accounts.x.ai"                    // accounts session service origin
	loginRPCEndpoint     = providersBaseURL + "/api/rpc"              // accounts session RPC endpoint
	turnstileWebsiteURL  = providersBaseURL                           // Turnstile challenge site URL
	turnstileWebsiteKey  = "0x4AAAAAAAhr9JGVDZbrZOo0"                 // Turnstile challenge site key
	yesCaptchaCreateTask = "https://api.yescaptcha.com/createTask"    // CAPTCHA task creation endpoint
	yesCaptchaGetResult  = "https://api.yescaptcha.com/getTaskResult" // CAPTCHA task polling endpoint
)

// OAuthClient performs token grants against its construction-time validated
// xAI token endpoint.
type OAuthClient struct {
	// tokenURL is selected and validated by NewOAuthClient once. Later exchanges
	// reuse it, so configuration changes require constructing another client to
	// select a new endpoint.
	tokenURL string // pinned OAuth token endpoint
}

// NewOAuthClient pins the currently validated token endpoint for later
// grants. An invalid or blank effective endpoint selects DefaultTokenURL,
// preventing a rejected configuration override from receiving codes or refresh
// tokens through this client.
func NewOAuthClient() *OAuthClient {
	// Token grants reuse this construction-time endpoint. Validate it before
	// credential-bearing requests so an configuration override must pass the issuer policy.
	tokenURL, err := ValidatedTokenURL()
	if err != nil || strings.TrimSpace(tokenURL) == "" {
		// Invalid configuration cannot become a token destination through this client;
		// the known default supplies a usable issuer endpoint after the validation failure.
		tokenURL = DefaultTokenURL
	}
	return &OAuthClient{tokenURL: tokenURL}
}

// ExchangeCode submits an authorization-code form using the client's pinned
// token endpoint and supplied request context/proxy. Blank clientID selects
// EffectiveClientID; redirect selection uses EffectiveRedirectURI. Callers
// verify authorization state and retain the matching code verifier before
// invoking this exchange.
func (c *OAuthClient) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI, proxyURL, clientID string) (*TokenResponse, error) {
	client, err := createGrokReqClient(proxyURL)
	if err != nil {
		return nil, infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_CLIENT_INIT_FAILED", "create HTTP client: %v", err)
	}

	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = EffectiveClientID()
	}

	formData := url.Values{}
	formData.Set("grant_type", "authorization_code")
	formData.Set("client_id", clientID)
	formData.Set("code", code)
	formData.Set("redirect_uri", EffectiveRedirectURI(redirectURI))
	formData.Set("code_verifier", codeVerifier)

	var tokenResp TokenResponse
	resp, err := client.R().
		SetContext(ctx).
		SetHeader("User-Agent", "tokenrouter-grok-oauth/1.0").
		SetFormDataFromValues(formData).
		SetSuccessResult(&tokenResp).
		Post(c.tokenURL)
	if err != nil {
		return nil, infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_REQUEST_FAILED", "request failed: %v", err)
	}
	if !resp.IsSuccessState() {
		return nil, grokOAuthStatusError("GROK_OAUTH_TOKEN_EXCHANGE_FAILED", "token exchange failed", resp)
	}
	return &tokenResp, nil
}

// RefreshToken submits the supplied refresh token using the pinned endpoint
// and caller context/proxy. Blank clientID selects EffectiveClientID. The
// returned token fields are decoded exchange data; callers coordinate refresh
// races and persist successful credential rotation.
func (c *OAuthClient) RefreshToken(ctx context.Context, refreshToken, proxyURL, clientID string) (*TokenResponse, error) {
	client, err := createGrokReqClient(proxyURL)
	if err != nil {
		return nil, infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_CLIENT_INIT_FAILED", "create HTTP client: %v", err)
	}

	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		clientID = EffectiveClientID()
	}

	formData := url.Values{}
	formData.Set("grant_type", "refresh_token")
	formData.Set("client_id", clientID)
	formData.Set("refresh_token", refreshToken)

	var tokenResp TokenResponse
	resp, err := client.R().
		SetContext(ctx).
		SetHeader("User-Agent", "tokenrouter-grok-oauth/1.0").
		SetFormDataFromValues(formData).
		SetSuccessResult(&tokenResp).
		Post(c.tokenURL)
	if err != nil {
		return nil, infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_REQUEST_FAILED", "request failed: %v", err)
	}
	if !resp.IsSuccessState() {
		return nil, grokOAuthStatusError("GROK_OAUTH_TOKEN_REFRESH_FAILED", "token refresh failed", resp)
	}
	return &tokenResp, nil
}

// LoginWithPassword solves Turnstile, creates an accounts session, and
// extracts its temporary SSO cookie. Callers supply cancellation and enforce
// the password-authorization feature setting before invocation, then keep
// passwords and returned SSO tokens out of provider credentials and logs.
func (c *OAuthClient) LoginWithPassword(ctx context.Context, email, password, proxyURL string) (*wiregrok.PasswordLoginResult, error) {
	turnstileToken, err := solveTurnstile(ctx)
	if err != nil {
		return nil, err
	}
	httpClient, err := createGrokHTTPClient(proxyURL, true)
	if err != nil {
		return nil, infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_CLIENT_INIT_FAILED", "create HTTP client: %v", err)
	}
	cookieSetterURL, err := createGrokPasswordSession(ctx, httpClient, strings.TrimSpace(email), password, turnstileToken)
	if err != nil {
		return nil, err
	}
	ssoToken, err := extractGrokSSOToken(ctx, httpClient, cookieSetterURL)
	if err != nil {
		return nil, err
	}
	return &wiregrok.PasswordLoginResult{
		Email:    strings.TrimSpace(email),
		SSOToken: ssoToken,
	}, nil
}

// ConvertSSOToBuild converts a supplied SSO token using a proxy-aware client
// and an overall SSOConversionTimeout child context. It maps device-flow
// errors into application categories; callers consume returned token fields
// and coordinate any provider credential persistence.
func (c *OAuthClient) ConvertSSOToBuild(ctx context.Context, ssoToken, proxyURL string) (*TokenResponse, error) {
	client, err := createGrokSSOHTTPClient(proxyURL)
	if err != nil {
		return nil, infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_SSO_CLIENT_INIT_FAILED", "create HTTP client: %v", err)
	}

	requestCtx, cancel := context.WithTimeout(ctx, SSOConversionTimeout)
	defer cancel()
	tokenResp, err := ConvertSSOToBuild(requestCtx, ssoToken, &SSODeviceOptions{HTTPClient: client})
	if err != nil {
		return nil, grokSSOConversionError(err)
	}
	return tokenResp, nil
}

// createGrokReqClient obtains the shared req client for a supplied proxy and
// sixty-second request timeout. Shared-client construction errors propagate;
// OAuth grant methods attach credential form values to individual requests
// while client selection uses its proxy and timeout options.
func createGrokReqClient(proxyURL string) (*req.Client, error) {
	return sharedhttp.GetSharedReqClient(sharedhttp.ReqClientOptions{
		ProxyURL: proxyURL,
		Timeout:  60 * time.Second,
	})
}

// createGrokSSOHTTPClient obtains a shared proxy client, then copies its
// client configuration to expose redirects without following them. The copied
// client retains its transport and timeout settings, allowing the device flow
// to validate each redirect itself.
func createGrokSSOHTTPClient(proxyURL string) (*http.Client, error) {
	client, err := sharedhttp.GetClient(sharedhttp.Options{
		ProxyURL:              proxyURL,
		Timeout:               SSOConversionTimeout,
		ResponseHeaderTimeout: 30 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	clone := *client
	// The device flow handles redirects and cookies itself. Change the copied
	// client's redirect policy while retaining the shared transport for the configured proxy.
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clone, nil
}

// grokSSOConversionError maps known SSO sentinels, typed HTTP failures, and
// context cancellation/deadlines to application errors. Generic polling-
// timeout text remains an ordinary conversion failure; only matched context
// errors select the gateway-timeout category.
func grokSSOConversionError(err error) error {
	if errors.Is(err, ErrSSOUnauthorized) {
		return infraerrors.New(infraerrors.Category(http.StatusUnauthorized), "GROK_SSO_UNAUTHORIZED", "Grok Web SSO cookie is invalid or expired")
	}
	if errors.Is(err, ErrSSOAuthorizationDenied) {
		return infraerrors.New(infraerrors.Category(http.StatusForbidden), "GROK_SSO_AUTHORIZATION_DENIED", "xAI device authorization was denied or expired")
	}
	if statusErr, ok := errors.AsType[SSOHTTPError](err); ok {
		statusCode := http.StatusBadGateway
		if statusErr.Status == http.StatusForbidden {
			statusCode = http.StatusForbidden
		}
		return infraerrors.Newf(infraerrors.Category(statusCode), "GROK_SSO_UPSTREAM_FAILED", "xAI SSO conversion failed: %v", err)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return infraerrors.Newf(infraerrors.Category(http.StatusGatewayTimeout), "GROK_SSO_TIMEOUT", "xAI SSO conversion timed out: %v", err)
	}
	return infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_SSO_CONVERSION_FAILED", "xAI SSO conversion failed: %v", err)
}

// grokOAuthStatusError builds a redacted exchange error with upstream
// status/body context. Only a 403 whose body passes explicit entitlement
// classification changes the category and reason to entitlement denial; nil
// responses retain status zero and ordinary failure metadata.
func grokOAuthStatusError(code, message string, resp *req.Response) error {
	statusCode := http.StatusBadGateway
	errorCode := code
	upstreamStatus := 0
	body := ""
	if resp != nil {
		upstreamStatus = resp.StatusCode
		body = logredact.RedactText(resp.String())
		if resp.StatusCode == http.StatusForbidden && grokOAuthHasExplicitEntitlementDenial(body) {
			statusCode = http.StatusForbidden
			errorCode = "GROK_OAUTH_ENTITLEMENT_DENIED"
		}
	}
	return infraerrors.Newf(infraerrors.Category(statusCode), errorCode, "%s: status %d, body: %s", message, upstreamStatus, body)
}

// grokOAuthHasExplicitEntitlementDenial recognizes configured denial markers
// and phrases after lowercasing the body. Generic forbidden text returns
// false. Quota/spending exhaustion phrases are checked first and suppress
// denial even when access_denied or subscription-required markers also
// appear.
//
// The JSON-marker check removes whitespace before matching the configured
// error, code, or reason string values. This classifier is text-based and
// does not validate a JSON schema or credential status; grokOAuthStatusError
// additionally requires HTTP 403 before using the result for entitlement
// classification.
func grokOAuthHasExplicitEntitlementDenial(body string) bool {
	lower := strings.ToLower(body)
	// Exhaustion text can coexist with denial markers. Give it precedence so
	// status mapping leaves the exchange failure available to the caller's quota recovery rules.
	for _, phrase := range []string{
		"spending limit",
		"run out of credits",
		"out of credits",
		"credits exhausted",
		"included free usage",
	} {
		if strings.Contains(lower, phrase) {
			return false
		}
	}
	compact := strings.NewReplacer(" ", "", "\n", "", "\r", "", "\t", "").Replace(lower)
	for _, field := range []string{"error", "code", "reason"} {
		for _, value := range []string{"access_denied", "entitlement_denied", "subscription_required", "no_active_subscription"} {
			if strings.Contains(compact, `"`+field+`":"`+value+`"`) {
				return true
			}
		}
	}
	return strings.Contains(lower, "entitlement denied") ||
		strings.Contains(lower, "subscription required") ||
		strings.Contains(lower, "no active grok subscription")
}

// createGrokHTTPClient constructs a fresh HTTP client with a parsed optional
// proxy and a 120-second request timeout. noRedirect exposes redirect
// responses to the caller; when false, normal client redirect handling
// remains enabled for that client.
func createGrokHTTPClient(proxyURL string, noRedirect bool) (*http.Client, error) {
	transport := &http.Transport{}
	if strings.TrimSpace(proxyURL) != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	client := &http.Client{Timeout: 120 * time.Second, Transport: transport}
	if noRedirect {
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	return client, nil
}

// solveTurnstile creates a proxyless CAPTCHA task using the first nonblank
// the configured YesCaptcha client key. Missing keys and rejected task
// responses return application errors. Polling waits five seconds between
// attempts, honors ctx during waits, and retries transient poll build,
// transport, or decode failures.
//
// A local ninety-second deadline is checked before each polling wait after
// task creation. An admitted wait can start its request after that deadline;
// the deadline does not cancel http.DefaultClient exchanges. Callers provide
// a bounded context for network cancellation. A ready response needs nonblank
// solution text. Explicit poll ErrorID ends immediately, and loop exhaustion
// yields the CAPTCHA timeout error.
func solveTurnstile(ctx context.Context) (string, error) {
	clientKey := strings.TrimSpace(currentOverrides().YesCaptchaClientKey)
	if clientKey == "" {
		return "", infraerrors.New(infraerrors.Category(http.StatusBadRequest), "GROK_OAUTH_CAPTCHA_KEY_REQUIRED", "yescaptcha client key is required for Grok password authorization")
	}
	createBody, err := json.Marshal(map[string]any{
		"clientKey": clientKey,
		"task": map[string]any{
			"type":       "TurnstileTaskProxyless",
			"websiteURL": turnstileWebsiteURL,
			"websiteKey": turnstileWebsiteKey,
		},
	})
	if err != nil {
		return "", infraerrors.Newf(infraerrors.Category(http.StatusInternalServerError), "GROK_OAUTH_CAPTCHA_FAILED", "encode captcha create request failed: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, yesCaptchaCreateTask, bytes.NewReader(createBody))
	if err != nil {
		return "", infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_CAPTCHA_FAILED", "build captcha create request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_CAPTCHA_FAILED", "create captcha task failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Keep the task identity from creation for all later polls so readiness
	// and error observations cannot be attributed to another CAPTCHA task.
	var createResp struct {
		ErrorID          int    `json:"errorId"`          // CAPTCHA service error identifier
		TaskID           string `json:"taskId"`           // created CAPTCHA task identifier
		ErrorDescription string `json:"errorDescription"` // CAPTCHA service error text
	}
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		return "", infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_CAPTCHA_FAILED", "decode captcha create response failed: %v", err)
	}
	if createResp.ErrorID != 0 || strings.TrimSpace(createResp.TaskID) == "" {
		return "", infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_CAPTCHA_FAILED", "captcha create failed: %s", createResp.ErrorDescription)
	}
	// Check the local budget before each wait. A wait already admitted can
	// issue its next request after expiry; ctx still controls network cancellation.
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
		body, err := json.Marshal(map[string]any{"clientKey": clientKey, "taskId": createResp.TaskID})
		if err != nil {
			return "", infraerrors.Newf(infraerrors.Category(http.StatusInternalServerError), "GROK_OAUTH_CAPTCHA_FAILED", "encode captcha poll request failed: %v", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, yesCaptchaGetResult, bytes.NewReader(body))
		if err != nil {
			// Poll construction failure leaves the created task pending. Retry within
			// the local deadline because no solution or explicit task rejection was observed.
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			// A transient poll exchange supplies no task outcome. Keep polling until
			// readiness, explicit task error, caller cancellation, or the local deadline decides completion.
			continue
		}
		var pollResp struct {
			ErrorID int `json:"errorId"` // CAPTCHA service error identifier
			// Status, Solution, and its Token jointly describe readiness. ErrorID is
			// checked first; readiness returns a token only when Status is ready and Token
			// is nonblank, leaving other states available for another poll.
			Status           string   `json:"status"`           // CAPTCHA task progress status
			ErrorDescription string   `json:"errorDescription"` // CAPTCHA service error text
			Solution         struct { // CAPTCHA task solution object
				Token string `json:"token"` // solved Turnstile token text
			} `json:"solution"`
		}
		err = json.NewDecoder(resp.Body).Decode(&pollResp)
		_ = resp.Body.Close()
		if err != nil {
			// An undecodable poll body supplies no usable status; the task can still
			// be pending, so the next bounded poll may recover its completed solution.
			continue
		}
		if pollResp.ErrorID != 0 {
			return "", infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_CAPTCHA_FAILED", "captcha poll failed: %s", pollResp.ErrorDescription)
		}
		if pollResp.Status == "ready" && strings.TrimSpace(pollResp.Solution.Token) != "" {
			return pollResp.Solution.Token, nil
		}
	}
	return "", infraerrors.New(infraerrors.Category(http.StatusGatewayTimeout), "GROK_OAUTH_CAPTCHA_TIMEOUT", "captcha solve timed out")
}

// createGrokPasswordSession posts email/password and Turnstile credentials
// through the supplied client and ctx. Callers pass the redirect-suppressed
// password client. HTTP and decoded login errors are redacted; success
// requires a nonblank cookieSetterUrl for later validation.
func createGrokPasswordSession(ctx context.Context, client *http.Client, email, password, turnstileToken string) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"rpc": "createSession",
		"req": map[string]any{
			"createSessionRequest": map[string]any{
				"credentials": map[string]any{
					"case": "emailAndPassword",
					"value": map[string]any{
						"email":             email,
						"clearTextPassword": password,
					},
				},
			},
			"turnstileToken": turnstileToken,
		},
	})
	if err != nil {
		return "", infraerrors.Newf(infraerrors.Category(http.StatusInternalServerError), "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "encode password login request failed: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginRPCEndpoint, bytes.NewReader(payload))
	if err != nil {
		return "", infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "build password login request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", providersBaseURL)
	req.Header.Set("Referer", providersBaseURL+"/sign-in?redirect=grok-com&email=true")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "*/*")
	resp, err := client.Do(req)
	if err != nil {
		return "", infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "password login request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Discard read errors and retain available bytes for status diagnostics or
	// JSON decoding. A successful status still needs decodable login fields below.
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "password login returned status %d: %s", resp.StatusCode, logredact.RedactText(string(body)))
	}
	var loginResp struct {
		CookieSetterURL string `json:"cookieSetterUrl"` // accounts cookie setter URL
		Error           string `json:"error"`           // accounts login error text
	}
	if err := json.Unmarshal(body, &loginResp); err != nil {
		return "", infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "decode password login response failed: %v", err)
	}
	if strings.TrimSpace(loginResp.Error) != "" {
		return "", infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "password login error: %s", logredact.RedactText(loginResp.Error))
	}
	if strings.TrimSpace(loginResp.CookieSetterURL) == "" {
		return "", infraerrors.New(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "password login did not return cookieSetterUrl")
	}
	return loginResp.CookieSetterURL, nil
}

// extractGrokSSOToken validates the accounts cookie-setter URL and reads
// the first direct Set-Cookie value beginning sso=. Callers disable automatic
// redirects. The body closes on return, and no matching cookie returns a
// login failure. A matching cookie can yield empty trimmed text with nil
// error; the authorization caller rejects that empty SSO result.
func extractGrokSSOToken(ctx context.Context, client *http.Client, cookieSetterURL string) (string, error) {
	safeURL, err := validateGrokCookieSetterURL(cookieSetterURL)
	if err != nil {
		return "", infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "invalid cookie setter url: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, safeURL.String(), nil)
	if err != nil {
		return "", infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "build cookie setter request: %v", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Referer", providersBaseURL+"/")
	resp, err := client.Do(req)
	if err != nil {
		return "", infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "follow cookie setter url failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	for _, cookie := range resp.Header.Values("Set-Cookie") {
		if token, ok := strings.CutPrefix(cookie, "sso="); ok {
			if idx := strings.Index(token, ";"); idx > 0 {
				token = token[:idx]
			}
			return strings.TrimSpace(token), nil
		}
	}
	return "", infraerrors.Newf(infraerrors.Category(http.StatusBadGateway), "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "no sso cookie found in response (status=%d)", resp.StatusCode)
}

// validateGrokCookieSetterURL requires an HTTPS accounts.x.ai URL with no
// userinfo, nonempty port, fragment, or opaque form. It retains path and
// query for the cookie setter; transport callers separately apply request and
// DNS policy as needed.
func validateGrokCookieSetterURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	if parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "accounts.x.ai") {
		return nil, fmt.Errorf("url must use https://accounts.x.ai")
	}
	if parsed.User != nil || parsed.Port() != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return nil, fmt.Errorf("url contains disallowed authority or fragment components")
	}
	return parsed, nil
}
