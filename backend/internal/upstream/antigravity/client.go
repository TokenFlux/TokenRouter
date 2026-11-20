package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	proxyurl "github.com/TokenFlux/TokenRouter/internal/infra/httpclient/proxy"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	servertiming "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	googlewire "github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

// client.go exchanges Antigravity credentials and queries account projects,
// models, and user settings.
// Antigravity account operations exchange OAuth credentials and discover
// project, tier, model, and user-setting observations through Google
// endpoints. Account queries try configured endpoints in order, paid quota
// model queries can prioritize the daily endpoint before falling back to prod,
// and user-setting operations target the fixed daily destination.
//
// Completed HTTP bodies are consumed and closed before endpoint retries;
// onboarding polls named operations via GET /v1internal/{name} with cancellable
// waits and falls back to repeating POST :onboardUser when upstream omits an
// operation name. Model queries impose a caller-selected body bound and replace
// redirect policy on a shallow HTTP-client copy, accepting only configured hostnames.

const (
	// proxyDialTimeout bounds proxy connection establishment within the
	// constructed account client overall HTTP time budget, which also includes
	// response-body reading.
	proxyDialTimeout = 5 * time.Second
	// proxyTLSHandshakeTimeout bounds TLS negotiation in the explicit proxy
	// transport, separately from its dial and whole-request timeouts.
	proxyTLSHandshakeTimeout = 5 * time.Second
	// clientTimeout supplies the constructed account-client deadline across
	// dispatch and response-body reading unless a caller-supplied client replaces
	// it.
	clientTimeout = 10 * time.Second
	// defaultOnboardPollInterval sets the wait between pending onboarding checks
	// when ClientOptions.OnboardPollInterval is zero or negative.
	defaultOnboardPollInterval = 2 * time.Second

	// privacyBaseURL selects the fixed daily destination for user-settings
	// operations independently from configured account-query endpoint ordering.
	privacyBaseURL = antigravityDailyBaseURL
)

// ForbiddenError retains status and captured text from an account model-query
// permission rejection.
type ForbiddenError struct {
	StatusCode int    // upstream model-query HTTP status
	Body       string // captured upstream rejection text
}

// Client executes OAuth and project/account requests through its configured
// shared HTTP client.
type Client struct {
	// httpClient is shared by account operations; configure it before use and
	// keep transport/client settings stable while requests are running.
	httpClient          *http.Client
	onboardPollInterval time.Duration // wait between pending onboarding checks
}

// AvailableCredit represents shared supplier credit balance metadata carried
// in Antigravity account responses.
type AvailableCredit = googlewire.AntigravityAvailableCredit

// DeprecatedModelInfo represents shared supplier metadata describing a
// deprecated Antigravity model route.
type DeprecatedModelInfo = googlewire.AntigravityDeprecatedModelInfo

// FetchAvailableModelsRequest represents the shared project-scoped Antigravity
// model-query request envelope.
type FetchAvailableModelsRequest = googlewire.AntigravityFetchAvailableModelsRequest

// FetchAvailableModelsResponse represents shared Antigravity models, quota
// observations, and account-level response metadata.
type FetchAvailableModelsResponse = googlewire.AntigravityFetchAvailableModelsResponse

// RetrieveUserQuotaSummaryRequest represents the project-scoped Antigravity
// quota-summary request envelope.
type RetrieveUserQuotaSummaryRequest = googlewire.AntigravityRetrieveUserQuotaSummaryRequest

// RetrieveUserQuotaSummaryResponse represents family-grouped Antigravity weekly
// and 5-hour quota buckets returned by v1internal:retrieveUserQuotaSummary.
type RetrieveUserQuotaSummaryResponse = googlewire.AntigravityRetrieveUserQuotaSummaryResponse

// FetchUserInfoRequest represents the shared project-scoped request used to
// fetch Antigravity user settings.
type FetchUserInfoRequest = googlewire.AntigravityFetchUserInfoRequest

// FetchUserInfoResponse represents shared user-settings observations returned
// by the daily Antigravity endpoint.
type FetchUserInfoResponse = googlewire.AntigravityFetchUserInfoResponse

// LoadCodeAssistRequest represents shared IDE metadata sent during Antigravity
// account and project discovery.
type LoadCodeAssistRequest = googlewire.AntigravityLoadCodeAssistRequest

// LoadCodeAssistResponse represents shared project, tier, and credit
// observations returned by account discovery.
type LoadCodeAssistResponse = googlewire.AntigravityLoadCodeAssistResponse

// ModelInfo represents shared supplier model metadata and its optional quota
// observations.
type ModelInfo = googlewire.AntigravityModelInfo

// ModelQuotaInfo represents shared supplier remaining/reset observations for
// an Antigravity model quota.
type ModelQuotaInfo = googlewire.AntigravityModelQuotaInfo

// OnboardUserRequest represents shared tier and client metadata sent to
// Antigravity account onboarding.
type OnboardUserRequest = googlewire.AntigravityOnboardUserRequest

// OnboardUserResponse represents shared onboarding operation completion and
// its project-response data.
type OnboardUserResponse = googlewire.AntigravityOnboardUserResponse

// PaidTierInfo represents shared paid-account tier details selected by
// supplier response decoding.
type PaidTierInfo = googlewire.AntigravityPaidTierInfo

// SetUserSettingsRequest represents the shared user-settings object sent to
// the daily endpoint.
type SetUserSettingsRequest = googlewire.AntigravitySetUserSettingsRequest

// SetUserSettingsResponse represents shared supplier observations returned
// after a user-settings request.
type SetUserSettingsResponse = googlewire.AntigravitySetUserSettingsResponse

// TierInfo represents shared tier identity accepting supplier string and
// object JSON forms.
type TierInfo = googlewire.AntigravityTierInfo

// TokenResponse represents shared OAuth access, refresh, expiry, scope, and
// token-type observations.
type TokenResponse = googlewire.AntigravityTokenResponse

// UserInfo represents shared Google account identity observations returned by
// the OAuth user endpoint.
type UserInfo = googlewire.AntigravityUserInfo

// ClientOptions holds an optional HTTPClient and onboarding poll delay.
// Configure HTTPClient before use and keep its settings stable during account
// operations; nil keeps the constructed default client.
type ClientOptions struct {
	// HTTPClient replaces the constructed client when nonnil. It stays shared
	// with the caller, which configures it before concurrent account operations.
	HTTPClient *http.Client
	// OnboardPollInterval overrides the two-second wait between pending
	// onboarding polls when positive.
	OnboardPollInterval time.Duration
}

// FetchAvailableModelsOptions configures response size bounds and endpoint
// ordering for FetchAvailableModelsWithOptions.
type FetchAvailableModelsOptions struct {
	BodyLimit int64 // maximum response body bytes accepted
	// PreferDaily queries ForwardBaseURLs ([daily, prod]) first and permits HTTP
	// 401 on daily to fall back to prod so paid-tier quota reads match the
	// daily forwarding pool while remaining resilient to prod-only tokens.
	PreferDaily bool
}

// Error formats the model-query rejection status together with its captured
// upstream text.
func (e *ForbiddenError) Error() string {
	return fmt.Sprintf("fetchAvailableModels failed (HTTP %d): %s", e.StatusCode, e.Body)
}

// NewAPIRequestWithURL builds a POST using the supplied base URL, action,
// bearer token, and borrowed body bytes. Exact streamGenerateContent adds
// alt=sse; runtime User-Agent resolution supplies the identity header, and
// callers keep request bytes stable through dispatch.
func NewAPIRequestWithURL(ctx context.Context, baseURL, action, accessToken string, body []byte) (*http.Request, error) {
	// Select the SSE query only for the exact streaming action, because other
	// v1internal actions retain their plain JSON response request shape.
	apiURL := fmt.Sprintf("%s/v1internal:%s", baseURL, action)
	isStream := action == "streamGenerateContent"
	if isStream {
		apiURL += "?alt=sse"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// Resolve request-time client identity beside bearer authentication so
	// runtime version replacement affects each request without changing cached
	// OAuth credentials.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", GetUserAgentForContext(ctx))

	return req, nil
}

// NewAPIRequest delegates v1internal request construction using the package
// BaseURL. Body storage is borrowed by the request reader, and endpoint
// replacement requires caller synchronization with request construction.
func NewAPIRequest(ctx context.Context, action, accessToken string, body []byte) (*http.Request, error) {
	return NewAPIRequestWithURL(ctx, BaseURL, action, accessToken, body)
}

// NewClient constructs a ten-second account client and validates the supplied
// proxy URL. A configured proxy adds explicit dial/TLS limits; an absent proxy
// retains nil Transport and the standard default transport/environment
// behavior.
func NewClient(proxyURL string) (*Client, error) {
	client := &http.Client{
		Timeout: clientTimeout,
	}

	_, parsed, err := proxyurl.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	if parsed != nil {
		transport := &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: proxyDialTimeout,
			}).DialContext,
			TLSHandshakeTimeout: proxyTLSHandshakeTimeout,
		}
		if err := proxyurl.ConfigureTransportProxy(transport, parsed); err != nil {
			return nil, fmt.Errorf("configure proxy: %w", err)
		}
		client.Transport = transport
	}
	return &Client{
		httpClient:          client,
		onboardPollInterval: defaultOnboardPollInterval,
	}, nil
}

// IsConnectionError recognizes wrapped network timeouts, network operation
// errors, and URL errors. Nil or unrelated failures return false; account
// endpoint selection uses this broader shape set separately from forwarding
// retries.
func IsConnectionError(err error) bool {
	if err == nil {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	if _, ok := errors.AsType[*net.OpError](err); ok {
		return true
	}

	var urlErr *url.Error
	return errors.As(err, &urlErr)
}

// shouldFallbackToNextURL selects account-endpoint recovery for connection
// errors, 429, 408, 404, or server statuses. A true classification still
// requires another configured endpoint at the calling operation.
func shouldFallbackToNextURL(err error, statusCode int) bool {
	if IsConnectionError(err) {
		return true
	}
	return statusCode == http.StatusTooManyRequests ||
		statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusNotFound ||
		statusCode >= 500
}

// NewClientWithOptions validates proxy configuration by constructing the
// default client first. A nonnil HTTPClient then replaces that client
// wholesale, sharing caller settings and transport; nil retains constructed
// defaults.
func NewClientWithOptions(proxyURL string, options ClientOptions) (*Client, error) {
	client, err := NewClient(proxyURL)
	if err != nil {
		return nil, err
	}
	if options.HTTPClient != nil {
		client.httpClient = options.HTTPClient
	}
	if options.OnboardPollInterval > 0 {
		client.onboardPollInterval = options.OnboardPollInterval
	}
	return client, nil
}

// ExchangeCode submits the supplied code and PKCE verifier with cached OAuth
// client credentials. It consumes and closes the full response, requires
// status 200, and decodes token fields without validating their presence.
func (c *Client) ExchangeCode(ctx context.Context, code, codeVerifier string) (*TokenResponse, error) {
	clientSecret, err := getClientSecret()
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Set("client_id", ClientID)
	params.Set("client_secret", clientSecret)
	params.Set("code", code)
	params.Set("redirect_uri", RedirectURI)
	params.Set("grant_type", "authorization_code")
	params.Set("code_verifier", codeVerifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := servertiming.Do(c.httpClient, req)
	if err != nil {
		return nil, fmt.Errorf("send token exchange request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (HTTP %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(bodyBytes, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}

	return &tokenResp, nil
}

// RefreshToken submits the supplied refresh token with cached OAuth client
// credentials. It consumes and closes the full response, requires status 200,
// and leaves token-field validation and credential storage to the caller.
func (c *Client) RefreshToken(ctx context.Context, refreshToken string) (*TokenResponse, error) {
	clientSecret, err := getClientSecret()
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Set("client_id", ClientID)
	params.Set("client_secret", clientSecret)
	params.Set("refresh_token", refreshToken)
	params.Set("grant_type", "refresh_token")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TokenURL, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := servertiming.Do(c.httpClient, req)
	if err != nil {
		return nil, fmt.Errorf("send token refresh request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed (HTTP %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(bodyBytes, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}

	return &tokenResp, nil
}

// GetUserInfo retrieves Google account identity using the supplied bearer
// access token. It consumes and closes the full response, requires status 200,
// and returns decoded observations without deciding account authorization.
func (c *Client) GetUserInfo(ctx context.Context, accessToken string) (*UserInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, UserInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := servertiming.Do(c.httpClient, req)
	if err != nil {
		return nil, fmt.Errorf("send user info request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch user info failed (HTTP %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var userInfo UserInfo
	if err := json.Unmarshal(bodyBytes, &userInfo); err != nil {
		return nil, fmt.Errorf("parse user info: %w", err)
	}

	return &userInfo, nil
}

// LoadCodeAssist queries account tiers and project identity without an explicit
// project hint, delegating to LoadCodeAssistWithProject.
func (c *Client) LoadCodeAssist(ctx context.Context, accessToken string) (*LoadCodeAssistResponse, map[string]any, error) {
	return c.LoadCodeAssistWithProject(ctx, accessToken, "")
}

// LoadCodeAssistWithProject returns decoded account details and a separately
// decoded raw JSON object. Optional projectID populates cloudaicompanionProject
// and metadata.duetProject when nonblank, while BaseURLs are tried in
// configured order.
//
// Request-construction failures move to the next endpoint, and classified
// transport/status failures can move when another entry exists. Full-body read
// or decoding errors stop the operation. Each received body closes before
// another attempt, while status 200 and typed decoding precede success
// recording.
func (c *Client) LoadCodeAssistWithProject(ctx context.Context, accessToken, projectID string) (*LoadCodeAssistResponse, map[string]any, error) {
	trimmedProject := strings.TrimSpace(projectID)
	reqBody := LoadCodeAssistRequest{
		CloudAICompanionProject: trimmedProject,
	}
	reqBody.Metadata.IDEType = "ANTIGRAVITY"
	reqBody.Metadata.IDEVersion = EffectiveUserAgentVersion(GetUserAgentVersionForContext(ctx))
	reqBody.Metadata.IDEName = "antigravity"
	reqBody.Metadata.DuetProject = trimmedProject

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, nil, fmt.Errorf("encode request: %w", err)
	}

	// Keep configured account-query order instead of availability preference;
	// previous successes are recorded for other consumers after typed decoding.
	availableURLs := BaseURLs

	var lastErr error
	for urlIdx, baseURL := range availableURLs {
		apiURL := baseURL + "/v1internal:loadCodeAssist"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(string(bodyBytes)))
		if err != nil {
			// Skip this unusable endpoint after request construction fails; retain its
			// error while later configured entries can still produce an account result.
			lastErr = fmt.Errorf("create request: %w", err)
			continue
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", GetUserAgentForContext(ctx))

		resp, err := servertiming.Do(c.httpClient, req)
		if err != nil {
			lastErr = fmt.Errorf("loadCodeAssist request failed: %w", err)
			// Connection-shaped failure can advance configured account endpoints
			// while another entry remains; the final endpoint returns its own error.
			if shouldFallbackToNextURL(err, 0) && urlIdx < len(availableURLs)-1 {
				logging.LegacyPrintf("upstream.antigravity", "[antigravity] loadCodeAssist URL fallback: %s -> %s", baseURL, availableURLs[urlIdx+1])
				continue
			}
			return nil, nil, lastErr
		}

		// Consume and close each received body before endpoint recovery so a
		// subsequent attempt does not retain an unread earlier network response.
		respBodyBytes, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close() // release the consumed network response
		if err != nil {
			return nil, nil, fmt.Errorf("read response: %w", err)
		}

		// Eligible status recovery follows complete body capture and closing, so
		// advancing endpoints cannot leave this received stream owned by the loop.
		if shouldFallbackToNextURL(nil, resp.StatusCode) && urlIdx < len(availableURLs)-1 {
			logging.LegacyPrintf("upstream.antigravity", "[antigravity] loadCodeAssist URL fallback (HTTP %d): %s -> %s", resp.StatusCode, baseURL, availableURLs[urlIdx+1])
			continue
		}

		if resp.StatusCode != http.StatusOK {
			return nil, nil, fmt.Errorf("loadCodeAssist failed (HTTP %d): %s", resp.StatusCode, string(respBodyBytes))
		}

		var loadResp LoadCodeAssistResponse
		if err := json.Unmarshal(respBodyBytes, &loadResp); err != nil {
			return nil, nil, fmt.Errorf("parse response: %w", err)
		}

		var rawResp map[string]any
		// Typed decoding has already succeeded; retain best-effort raw observations
		// for account consumers while leaving ordinary float64 JSON numeric
		// representation in that map.
		_ = json.Unmarshal(respBodyBytes, &rawResp)

		// Publish success after typed decoding so availability consumers prefer an
		// endpoint that produced usable account data, while this query keeps its
		// configured ordering.
		DefaultURLAvailability.MarkSuccess(baseURL)
		return &loadResp, rawResp, nil
	}

	return nil, nil, lastErr
}

// OnboardUser submits tier onboarding without an explicit project hint,
// delegating to OnboardUserWithProject.
func (c *Client) OnboardUser(ctx context.Context, accessToken, tierID string) (string, error) {
	return c.OnboardUserWithProject(ctx, accessToken, tierID, "")
}

// OnboardUserWithProject submits tier onboarding with optional project hints
// and returns a trimmed project ID from a completed operation. Non-free tiers
// receive cloudaicompanionProject alongside metadata.duetProject, while
// free-tier omits cloudaicompanionProject to satisfy upstream preconditions.
//
// Each configured endpoint allows five operation observations: the first sends
// POST /v1internal:onboardUser with Hub control-plane headers, and pending
// responses with a valid operation name poll GET /v1internal/{name} on later
// attempts (or repeat POST :onboardUser when Name is blank).
func (c *Client) OnboardUserWithProject(ctx context.Context, accessToken, tierID, projectID string) (string, error) {
	tierID = strings.TrimSpace(tierID)
	if tierID == "" {
		return "", fmt.Errorf("tier_id is required")
	}
	trimmedProject := strings.TrimSpace(projectID)

	reqBody := OnboardUserRequest{TierID: tierID}
	if trimmedProject != "" && !strings.EqualFold(tierID, "free-tier") {
		reqBody.CloudAICompanionProject = trimmedProject
	}
	reqBody.Metadata.IDEType = "ANTIGRAVITY"
	reqBody.Metadata.Platform = "PLATFORM_UNSPECIFIED"
	reqBody.Metadata.PluginType = "GEMINI"
	reqBody.Metadata.DuetProject = trimmedProject

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("encode request: %w", err)
	}

	pollInterval := c.onboardPollInterval
	if pollInterval <= 0 {
		pollInterval = defaultOnboardPollInterval
	}

	availableURLs := BaseURLs
	var lastErr error

	for urlIdx, baseURL := range availableURLs {
		postURL := strings.TrimRight(baseURL, "/") + "/v1internal:onboardUser"
		var pendingOpName string

		// Bound each endpoint to five operation observations before considering the
		// next configured endpoint, polling GET /v1internal/{name} once upstream
		// assigns a long-running operation identifier.
		for attempt := 1; attempt <= 5; attempt++ {
			var req *http.Request
			if pendingOpName != "" {
				getURL := strings.TrimRight(baseURL, "/") + "/v1internal/" + pendingOpName
				req, err = http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
			} else {
				req, err = http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(bodyBytes))
			}
			if err != nil {
				lastErr = fmt.Errorf("create request: %w", err)
				break
			}
			req.Header.Set("Authorization", "Bearer "+accessToken)
			if pendingOpName == "" {
				req.Header.Set("Content-Type", "application/json")
			}
			req.Header.Set("Accept", "application/json")
			req.Header.Set("User-Agent", GetOnboardUserAgentForContext(ctx))
			req.Header.Set("X-Goog-Api-Client", GoogAPIClientHeader)

			resp, err := servertiming.Do(c.httpClient, req)
			if err != nil {
				lastErr = fmt.Errorf("onboardUser request failed: %w", err)
				// Connection-shaped failure can advance configured account endpoints
				// while another entry remains; the final endpoint returns its own error.
				if shouldFallbackToNextURL(err, 0) && urlIdx < len(availableURLs)-1 {
					logging.LegacyPrintf("upstream.antigravity", "[antigravity] onboardUser URL fallback: %s -> %s", baseURL, availableURLs[urlIdx+1])
					break
				}
				return "", lastErr
			}

			respBodyBytes, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				return "", fmt.Errorf("read response: %w", err)
			}

			// Eligible status recovery follows complete body capture and closing, so
			// advancing endpoints cannot leave this received stream owned by the loop.
			if shouldFallbackToNextURL(nil, resp.StatusCode) && urlIdx < len(availableURLs)-1 {
				logging.LegacyPrintf("upstream.antigravity", "[antigravity] onboardUser URL fallback (HTTP %d): %s -> %s", resp.StatusCode, baseURL, availableURLs[urlIdx+1])
				break
			}

			if resp.StatusCode != http.StatusOK {
				lastErr = fmt.Errorf("onboardUser failed (HTTP %d): %s", resp.StatusCode, string(respBodyBytes))
				return "", lastErr
			}

			var onboardResp OnboardUserResponse
			if err := json.Unmarshal(respBodyBytes, &onboardResp); err != nil {
				lastErr = fmt.Errorf("onboardUser response parsing failed: %w", err)
				return "", lastErr
			}

			if onboardResp.Done {
				if resolved := extractProjectIDFromOnboardResponse(onboardResp.Response); resolved != "" {
					DefaultURLAvailability.MarkSuccess(baseURL)
					return resolved, nil
				}
				if trimmedProject != "" {
					// Enterprise and BYOP accounts keep the caller-supplied project when
					// onboarding completes without allocating a new companion project.
					DefaultURLAvailability.MarkSuccess(baseURL)
					return trimmedProject, nil
				}
				lastErr = fmt.Errorf("onboardUser completed without project_id")
				return "", lastErr
			}

			if normalizedOp := normalizeAntigravityOperationName(onboardResp.Name); normalizedOp != "" {
				pendingOpName = normalizedOp
			}

			// Wait after each pending operation, including the fifth observation, so
			// endpoint advancement also follows the cancellable polling interval.
			timer := time.NewTimer(pollInterval)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return "", ctx.Err()
			}
		}
	}

	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("onboardUser did not return project_id")
}

// normalizeAntigravityOperationName strips optional leading slashes and
// v1internal/ prefixes and validates that name begins with operations/ without
// path-traversal or query characters.
func normalizeAntigravityOperationName(name string) string {
	trimmed := strings.TrimSpace(name)
	trimmed = strings.TrimPrefix(trimmed, "/")
	trimmed = strings.TrimPrefix(trimmed, "v1internal/")
	trimmed = strings.TrimPrefix(trimmed, "/")
	if !strings.HasPrefix(trimmed, "operations/") || len(trimmed) <= len("operations/") {
		return ""
	}
	if strings.Contains(trimmed, "..") || strings.ContainsAny(trimmed, "?#\\ \t\r\n") {
		return ""
	}
	return trimmed
}

// extractProjectIDFromOnboardResponse returns trimmed cloudaicompanionProject
// text or its object id. Empty maps, absent keys, nonstring IDs, and other
// shapes return empty so onboarding can reject missing project identity.
func extractProjectIDFromOnboardResponse(resp map[string]any) string {
	if len(resp) == 0 {
		return ""
	}

	if v, ok := resp["cloudaicompanionProject"]; ok {
		switch project := v.(type) {
		case string:
			return strings.TrimSpace(project)
		case map[string]any:
			if id, ok := project["id"].(string); ok {
				return strings.TrimSpace(id)
			}
		}
	}

	return ""
}

// FetchAvailableModels queries configured endpoints in BaseURLs order using
// bodyLimit, delegating to FetchAvailableModelsWithOptions.
func (c *Client) FetchAvailableModels(ctx context.Context, accessToken, projectID string, bodyLimit int64) (*FetchAvailableModelsResponse, map[string]any, error) {
	return c.FetchAvailableModelsWithOptions(ctx, accessToken, projectID, FetchAvailableModelsOptions{
		BodyLimit: bodyLimit,
	})
}

// FetchAvailableModelsWithOptions returns decoded models/quotas and best-effort
// raw JSON from configured endpoints. PreferDaily queries ForwardBaseURLs
// ([daily, prod]) first so paid Google One accounts read quota from the daily
// pool where their traffic executes, falling back to prod on HTTP 401 in
// addition to 404/408/429/5xx and connection errors.
//
// Reading one byte beyond BodyLimit distinguishes oversize before status
// recovery or decoding. A 403 becomes ForbiddenError with captured text, while
// status 200 and typed decoding precede success recording. A shallow client
// copy replaces redirect policy with configured-hostname checks.
func (c *Client) FetchAvailableModelsWithOptions(ctx context.Context, accessToken, projectID string, options FetchAvailableModelsOptions) (*FetchAvailableModelsResponse, map[string]any, error) {
	if c == nil || c.httpClient == nil {
		return nil, nil, errors.New("antigravity client is not configured")
	}
	if options.BodyLimit <= 0 {
		return nil, nil, errors.New("fetchAvailableModels body limit must be positive")
	}

	reqBody := FetchAvailableModelsRequest{Project: projectID}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, nil, fmt.Errorf("encode request: %w", err)
	}

	availableURLs := BaseURLs
	if options.PreferDaily {
		// Paid accounts consume quota on daily-cloudcode-pa; query daily first and
		// keep prod second so stale tier labels still recover on HTTP 401/404.
		availableURLs = ForwardBaseURLs()
	}

	fetchClient := c.fetchAvailableModelsHTTPClient()
	var lastErr error
	for urlIdx, baseURL := range availableURLs {
		apiURL := baseURL + "/v1internal:fetchAvailableModels"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(string(bodyBytes)))
		if err != nil {
			// Skip this unusable endpoint after request construction fails; retain its
			// error while later configured entries can still produce an account result.
			lastErr = fmt.Errorf("create request: %w", err)
			continue
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", GetUserAgentForContext(ctx))

		resp, err := servertiming.Do(fetchClient, req)
		if err != nil {
			lastErr = fmt.Errorf("fetchAvailableModels request failed: %w", err)
			// Connection-shaped failure can advance configured account endpoints
			// while another entry remains; the final endpoint returns its own error.
			if shouldFallbackToNextURL(err, 0) && urlIdx < len(availableURLs)-1 {
				logging.LegacyPrintf("upstream.antigravity", "[antigravity] fetchAvailableModels URL fallback: %s -> %s", baseURL, availableURLs[urlIdx+1])
				continue
			}
			return nil, nil, lastErr
		}

		// Read one extra byte before closing to detect oversize, so truncated
		// supplier JSON cannot enter typed decoding as a successful bounded
		// response.
		respBodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, options.BodyLimit+1))
		_ = resp.Body.Close() // release the consumed network response
		if err != nil {
			return nil, nil, fmt.Errorf("read response: %w", err)
		}
		if int64(len(respBodyBytes)) > options.BodyLimit {
			return nil, nil, fmt.Errorf("response exceeds %d bytes", options.BodyLimit)
		}

		// Eligible status recovery follows complete body capture and closing, plus
		// HTTP 401 fallback when PreferDaily tries daily before prod.
		canFallbackUnauthorized := options.PreferDaily && resp.StatusCode == http.StatusUnauthorized
		if (shouldFallbackToNextURL(nil, resp.StatusCode) || canFallbackUnauthorized) && urlIdx < len(availableURLs)-1 {
			logging.LegacyPrintf("upstream.antigravity", "[antigravity] fetchAvailableModels URL fallback (HTTP %d): %s -> %s", resp.StatusCode, baseURL, availableURLs[urlIdx+1])
			continue
		}

		if resp.StatusCode == http.StatusForbidden {
			return nil, nil, &ForbiddenError{
				StatusCode: resp.StatusCode,
				Body:       string(respBodyBytes),
			}
		}

		if resp.StatusCode != http.StatusOK {
			return nil, nil, fmt.Errorf("fetchAvailableModels failed (HTTP %d): %s", resp.StatusCode, string(respBodyBytes))
		}

		var modelsResp FetchAvailableModelsResponse
		if err := json.Unmarshal(respBodyBytes, &modelsResp); err != nil {
			return nil, nil, fmt.Errorf("parse response: %w", err)
		}

		var rawResp map[string]any
		// Keep best-effort raw values beside successful typed decoding; failure here
		// leaves those typed model observations usable while the map can be partial.
		_ = json.Unmarshal(respBodyBytes, &rawResp)

		// Publish success after typed decoding so availability consumers prefer an
		// endpoint that produced usable account data, while this query keeps its
		// configured ordering.
		DefaultURLAvailability.MarkSuccess(baseURL)
		return &modelsResp, rawResp, nil
	}

	return nil, nil, lastErr
}

// RetrieveUserQuotaSummary queries family-grouped weekly and 5-hour quota
// buckets across ForwardBaseURLs ([daily, prod]) using bodyLimit.
// Daily is preferred by default because v1internal:retrieveUserQuotaSummary
// tracks sliding windows on daily-cloudcode-pa, falling back to prod on
// connection errors or HTTP 401/404/408/429/5xx.
func (c *Client) RetrieveUserQuotaSummary(ctx context.Context, accessToken, projectID string, bodyLimit int64) (*RetrieveUserQuotaSummaryResponse, map[string]any, error) {
	return c.RetrieveUserQuotaSummaryWithOptions(ctx, accessToken, projectID, FetchAvailableModelsOptions{
		BodyLimit:   bodyLimit,
		PreferDaily: true,
	})
}

// RetrieveUserQuotaSummaryWithOptions posts v1internal:retrieveUserQuotaSummary
// across configured endpoints with bounded response reading and redirect checks.
// Placeholder "aicode-consumers" or blank project IDs omit the project field;
// HTTP 403 returns ForbiddenError, and typed JSON decoding precedes success
// publication.
func (c *Client) RetrieveUserQuotaSummaryWithOptions(ctx context.Context, accessToken, projectID string, options FetchAvailableModelsOptions) (*RetrieveUserQuotaSummaryResponse, map[string]any, error) {
	if c == nil || c.httpClient == nil {
		return nil, nil, errors.New("antigravity client is not configured")
	}
	if options.BodyLimit <= 0 {
		return nil, nil, errors.New("retrieveUserQuotaSummary body limit must be positive")
	}

	trimmedProject := strings.TrimSpace(projectID)
	if strings.EqualFold(trimmedProject, "aicode-consumers") {
		trimmedProject = ""
	}
	reqBody := RetrieveUserQuotaSummaryRequest{Project: trimmedProject}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, nil, fmt.Errorf("encode request: %w", err)
	}

	availableURLs := BaseURLs
	if options.PreferDaily {
		availableURLs = ForwardBaseURLs()
	}

	fetchClient := c.fetchAvailableModelsHTTPClient()
	var lastErr error
	for urlIdx, baseURL := range availableURLs {
		apiURL := baseURL + "/v1internal:retrieveUserQuotaSummary"
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(string(bodyBytes)))
		if err != nil {
			// Skip an unusable endpoint URL while later entries remain available.
			lastErr = fmt.Errorf("create request: %w", err)
			continue
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", GetUserAgentForContext(ctx))

		resp, err := servertiming.Do(fetchClient, req)
		if err != nil {
			lastErr = fmt.Errorf("retrieveUserQuotaSummary request failed: %w", err)
			// Advance to the next configured endpoint on connection failure when available.
			if shouldFallbackToNextURL(err, 0) && urlIdx < len(availableURLs)-1 {
				logging.LegacyPrintf("upstream.antigravity", "[antigravity] retrieveUserQuotaSummary URL fallback: %s -> %s", baseURL, availableURLs[urlIdx+1])
				continue
			}
			return nil, nil, lastErr
		}

		// Read one byte past BodyLimit to detect oversized payloads before JSON decoding.
		respBodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, options.BodyLimit+1))
		_ = resp.Body.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("read response: %w", err)
		}
		if int64(len(respBodyBytes)) > options.BodyLimit {
			return nil, nil, fmt.Errorf("response exceeds %d bytes", options.BodyLimit)
		}

		// Fall back from daily to prod on retryable status codes or HTTP 401 when PreferDaily is active.
		canFallbackUnauthorized := options.PreferDaily && resp.StatusCode == http.StatusUnauthorized
		if (shouldFallbackToNextURL(nil, resp.StatusCode) || canFallbackUnauthorized) && urlIdx < len(availableURLs)-1 {
			logging.LegacyPrintf("upstream.antigravity", "[antigravity] retrieveUserQuotaSummary URL fallback (HTTP %d): %s -> %s", resp.StatusCode, baseURL, availableURLs[urlIdx+1])
			continue
		}

		if resp.StatusCode == http.StatusForbidden {
			return nil, nil, &ForbiddenError{
				StatusCode: resp.StatusCode,
				Body:       string(respBodyBytes),
			}
		}

		if resp.StatusCode != http.StatusOK {
			return nil, nil, fmt.Errorf("retrieveUserQuotaSummary failed (HTTP %d): %s", resp.StatusCode, string(respBodyBytes))
		}

		var summaryResp RetrieveUserQuotaSummaryResponse
		if err := json.Unmarshal(respBodyBytes, &summaryResp); err != nil {
			return nil, nil, fmt.Errorf("parse response: %w", err)
		}

		var rawResp map[string]any
		_ = json.Unmarshal(respBodyBytes, &rawResp)

		DefaultURLAvailability.MarkSuccess(baseURL)
		return &summaryResp, rawResp, nil
	}

	return nil, nil, lastErr
}

// fetchAvailableModelsHTTPClient returns a shallow HTTP-client copy using the
// model-query redirect policy. Callers supply a configured client; transport
// and jar stay shared, while the copy replaces any supplied CheckRedirect
// callback.
func (c *Client) fetchAvailableModelsHTTPClient() *http.Client {
	fetchClient := *c.httpClient
	fetchClient.CheckRedirect = checkFetchAvailableModelsRedirect
	return &fetchClient
}

// checkFetchAvailableModelsRedirect rejects ten or more prior requests,
// missing redirect URLs, and unconfigured hostnames. Membership compares
// hostnames only, so permitted destinations do not require matching configured
// scheme or port.
func checkFetchAvailableModelsRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if req == nil || req.URL == nil {
		return errors.New("redirect url is nil")
	}
	if !isAllowedFetchAvailableModelsRedirectHost(req.URL.Hostname()) {
		return fmt.Errorf("redirect to unsupported host: %s", req.URL.Hostname())
	}
	return nil
}

// isAllowedFetchAvailableModelsRedirectHost trims and case-folds host before
// matching parsed BaseURLs hostnames. Blank input and malformed configured
// URLs do not match; callers serialize changes to that endpoint list with
// readers.
func isAllowedFetchAvailableModelsRedirectHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, baseURL := range append(slices.Clone(BaseURLs), ForwardBaseURLs()...) {
		parsed, err := url.Parse(baseURL)
		// A malformed configured URL contributes no hostname to this redirect
		// allowlist; continue checking other configured entries before rejecting.
		if err != nil {
			continue
		}
		if strings.EqualFold(host, parsed.Hostname()) {
			return true
		}
	}
	return false
}

// SetUserSettings sends an empty user_settings object to the fixed daily
// endpoint with its client headers. It consumes and closes the full response
// and requires status 200; provider orchestration interprets returned privacy
// observations.
func (c *Client) SetUserSettings(ctx context.Context, accessToken string) (*SetUserSettingsResponse, error) {
	// Send the configured empty settings object to the daily endpoint; returned
	// observations are interpreted by provider privacy orchestration rather than
	// this HTTP client.
	payload := SetUserSettingsRequest{UserSettings: map[string]any{}}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	apiURL := privacyBaseURL + "/v1internal:setUserSettings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", GetUserAgentForContext(ctx))
	req.Header.Set("X-Goog-Api-Client", GoogAPIClientHeader)
	req.Host = "daily-cloudcode-pa.googleapis.com"

	resp, err := servertiming.Do(c.httpClient, req)
	if err != nil {
		return nil, fmt.Errorf("setUserSettings request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("setUserSettings failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result SetUserSettingsResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &result, nil
}

// FetchUserInfo sends project identity to the fixed daily user-settings
// endpoint with its client headers. It consumes and closes the full response
// and requires status 200; callers decide how settings affect provider privacy
// state.
func (c *Client) FetchUserInfo(ctx context.Context, accessToken, projectID string) (*FetchUserInfoResponse, error) {
	reqBody := FetchUserInfoRequest{Project: projectID}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	apiURL := privacyBaseURL + "/v1internal:fetchUserInfo"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", GetUserAgentForContext(ctx))
	req.Header.Set("X-Goog-Api-Client", GoogAPIClientHeader)
	req.Host = "daily-cloudcode-pa.googleapis.com"

	resp, err := servertiming.Do(c.httpClient, req)
	if err != nil {
		return nil, fmt.Errorf("fetchUserInfo request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetchUserInfo failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result FetchUserInfoResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &result, nil
}
