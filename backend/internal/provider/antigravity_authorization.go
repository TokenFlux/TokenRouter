package provider

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

// antigravity_authorization.go exchanges and refreshes Antigravity credentials
// and discovers account metadata using stored authorization proof.
// Antigravity authorization binds a Google code to stored state, verifier, and
// proxy before credential exchange. Interactive ExchangeCode caches exchanged
// tokens in the session when project discovery requires operator input or
// account verification, allowing Complete Auth retries without a second Google
// login and preventing empty-project accounts from being created silently.
//
// Runtime RefreshProviderToken keeps non-destructive renewal semantics so
// transient LoadCodeAssist failures preserve existing saved project_id values.
// Public operations register separately with activity for shutdown cancellation
// and waiting.

// AntigravityAuthorizationClient executes token, project, account, and privacy
// protocol requests.
type AntigravityAuthorizationClient interface {
	// ExchangeCode exchanges the supplied code and verifier for credentials.
	ExchangeCode(context.Context, string, string) (*google.AntigravityTokenResponse, error)
	// RefreshToken renews the supplied refresh credential through this client.
	RefreshToken(context.Context, string) (*google.AntigravityTokenResponse, error)
	// GetUserInfo queries email metadata with the supplied access token.
	GetUserInfo(context.Context, string) (*google.AntigravityUserInfo, error)
	// LoadCodeAssist reads project and subscription data with its raw response.
	LoadCodeAssist(context.Context, string) (*google.AntigravityLoadCodeAssistResponse, map[string]any, error)
	// OnboardUser requests a project using an access token and selected default tier.
	OnboardUser(context.Context, string, string) (string, error)
	// SetUserSettings clears account settings for privacy setup using the token.
	SetUserSettings(context.Context, string) (*google.AntigravitySetUserSettingsResponse, error)
	// FetchUserInfo queries settings for the access token and project identity.
	FetchUserInfo(context.Context, string, string) (*google.AntigravityFetchUserInfoResponse, error)
}

// antigravityProjectHintClient is an optional extension implemented by
// antigravity.Client to forward caller-supplied project hints into discovery
// and onboarding requests without breaking simpler test doubles.
type antigravityProjectHintClient interface {
	// LoadCodeAssistWithProject queries discovery with an optional project hint.
	LoadCodeAssistWithProject(context.Context, string, string) (*google.AntigravityLoadCodeAssistResponse, map[string]any, error)
	// OnboardUserWithProject submits onboarding with an optional project hint.
	OnboardUserWithProject(context.Context, string, string, string) (string, error)
}

// AntigravityAuthorizationOptions binds PKCE generation, transport selection,
// client creation, Resource Manager fallback, and diagnostics.
type AntigravityAuthorizationOptions struct {
	// NewClient constructs a usable client for each selected proxy URL.
	NewClient func(string) (AntigravityAuthorizationClient, error)
	// ResolveProxy resolves selected ProxyID values; false retains saved proxy or empty.
	ResolveProxy func(context.Context, int64) (string, bool)
	// FetchProject optionally queries Cloud Resource Manager for an ACTIVE GCP
	// project when LoadCodeAssist and OnboardUser return no companion project.
	FetchProject func(context.Context, string, string) (string, error)
	// RetryBackoff optionally overrides the exponential retry spacing between
	// LoadCodeAssist discovery attempts (one-based attempt index).
	RetryBackoff func(int) time.Duration
	// GenerateState, GenerateCodeVerifier, and GenerateSessionID prepare one
	// paired proof before publication.
	GenerateState, GenerateCodeVerifier, GenerateSessionID func() (string, error)
	// GenerateCodeChallenge derives the S256 PKCE challenge from CodeVerifier.
	GenerateCodeChallenge func(string) string
	// BuildAuthorizationURL constructs the Google consent URL.
	BuildAuthorizationURL func(string, string) string
	// IsConnectionError classifies proxy/network errors during token refresh.
	IsConnectionError func(error) bool
	// Printf logs discovery and renewal diagnostics.
	Printf func(string, ...any)
	// Warn and Info record privacy setup diagnostics.
	Warn, Info func(string, ...any)
}

// AntigravityAuthorization owns session proof and tracked Antigravity account
// authorization operations.
// @project-doc docs/interfaces/antigravity_upstream.md#antigravity_native_execution
type AntigravityAuthorization struct {
	// Store holds borrowed proof pointers and owns cleanup lifecycle.
	Store *AntigravityAuthorizationSessions
	// Options copies callback values at construction.
	Options  AntigravityAuthorizationOptions
	activity operationActivity // registered operation cancellation state
}

// AntigravityAuthURLResult pairs a browser destination with stored proof
// identifiers.
type AntigravityAuthURLResult struct {
	AuthURL   string `json:"auth_url"`   // generated browser authorization URL
	SessionID string `json:"session_id"` // stored proof lookup identifier
	State     string `json:"state"`      // returned OAuth state proof
}

// AntigravityExchangeCodeInput supplies saved state proof, the upstream
// authorization code, and an optional GCP Project ID hint.
type AntigravityExchangeCodeInput struct {
	SessionID string // stored proof lookup identifier
	State     string // supplied OAuth state proof
	Code      string // supplied authorization code text
	// ProjectID supplies an optional GCP Project ID when automatic companion
	// project discovery returns no project (e.g. standard-tier BYOP accounts).
	ProjectID string
	// ProxyID overrides saved transport only when its lookup succeeds.
	ProxyID *int64
}

// AntigravityTokenInfo carries refreshed credentials and independently
// observed account metadata.
type AntigravityTokenInfo struct {
	AccessToken  string `json:"access_token"`         // exchanged OAuth access token
	RefreshToken string `json:"refresh_token"`        // optional token renewal credential
	ExpiresIn    int64  `json:"expires_in"`           // upstream token lifetime seconds
	ExpiresAt    int64  `json:"expires_at"`           // absolute Unix-second refresh expiry
	TokenType    string `json:"token_type"`           // upstream token type name
	Email        string `json:"email,omitempty"`      // discovered upstream account email
	ProjectID    string `json:"project_id,omitempty"` // discovered project execution identifier
	// ProjectIDMissing marks incomplete project discovery during non-destructive
	// RefreshProviderToken calls so callers can retain existing credentials.
	ProjectIDMissing bool   `json:"-"`
	PlanType         string `json:"plan_type,omitempty"`    // normalized subscription plan label
	PrivacyMode      string `json:"privacy_mode,omitempty"` // privacy setup observation marker
}

// loadCodeAssistResult retains a project, subscription metadata, and any
// ineligible-tier observations from discovery.
type loadCodeAssistResult struct {
	ProjectID       string                              // discovered project execution identifier
	Subscription    *AntigravitySubscriptionResult      // latest observed subscription metadata
	IneligibleTiers []*google.AntigravityIneligibleTier // latest observed ineligible tiers
}

// NewAntigravityAuthorization copies option callbacks and creates idle session
// storage.
func NewAntigravityAuthorization(options AntigravityAuthorizationOptions) *AntigravityAuthorization {
	return &AntigravityAuthorization{Store: NewAntigravityAuthorizationSessions(), Options: options}
}

// Start enables the authorization store cleanup worker through its lifecycle gate.
func (s *AntigravityAuthorization) Start() { s.Store.Start() }

// StopContext stops and joins session cleanup before canceling registered
// authorization operations.
func (s *AntigravityAuthorization) StopContext(ctx context.Context) error {
	s.Store.Stop()
	return s.activity.stop(ctx, "antigravity authorization")
}

// generateAuthURL prepares and stores state, verifier, and proxy for one
// returned authorization URL.
func (s *AntigravityAuthorization) generateAuthURL(ctx context.Context, proxyID *int64) (*AntigravityAuthURLResult, error) {
	state, err := s.Options.GenerateState()
	if err != nil {
		return nil, fmt.Errorf("failed to generate state: %w", err)
	}

	codeVerifier, err := s.Options.GenerateCodeVerifier()
	if err != nil {
		return nil, fmt.Errorf("failed to generate code_verifier: %w", err)
	}

	sessionID, err := s.Options.GenerateSessionID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate session_id: %w", err)
	}

	var proxyURL string
	if proxyID != nil && s.Options.ResolveProxy != nil {
		if proxy, ok := s.Options.ResolveProxy(ctx, *proxyID); ok {
			proxyURL = proxy
		}
	}

	session := &AntigravityAuthorizationSession{
		State:        state,
		CodeVerifier: codeVerifier,
		ProxyURL:     proxyURL,
		CreatedAt:    time.Now(),
	}
	s.Store.Set(sessionID, session)

	codeChallenge := s.Options.GenerateCodeChallenge(codeVerifier)
	authURL := s.Options.BuildAuthorizationURL(state, codeChallenge)

	return &AntigravityAuthURLResult{
		AuthURL:   authURL,
		SessionID: sessionID,
		State:     state,
	}, nil
}

// exchangeCode validates input against saved state, exchanges or reuses the
// cached Google token, and requires a nonblank ProjectID before consuming the
// session so standard-tier accounts without auto-provisioned projects can
// complete Project ID entry in place without re-authorizing.
func (s *AntigravityAuthorization) exchangeCode(ctx context.Context, input *AntigravityExchangeCodeInput) (*AntigravityTokenInfo, error) {
	session, ok := s.Store.Get(input.SessionID)
	if !ok {
		return nil, fmt.Errorf("session not found or expired")
	}

	if strings.TrimSpace(input.State) == "" || input.State != session.State {
		return nil, fmt.Errorf("invalid state")
	}
	trimmedCode := strings.TrimSpace(input.Code)
	if trimmedCode == "" {
		return nil, fmt.Errorf("authorization code is required")
	}

	effectiveProjectID := strings.TrimSpace(input.ProjectID)
	if effectiveProjectID == "" {
		effectiveProjectID = strings.TrimSpace(session.ProjectID)
	}
	if err := ValidateGoogleProjectID(effectiveProjectID); err != nil {
		return nil, err
	}

	proxyURL := session.ProxyURL
	if input.ProxyID != nil && s.Options.ResolveProxy != nil {
		if proxy, ok := s.Options.ResolveProxy(ctx, *input.ProxyID); ok {
			proxyURL = proxy
		}
	}

	client, err := s.Options.NewClient(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("create antigravity client failed: %w", err)
	}

	tokenResp, err := s.obtainOrReuseAntigravitySessionToken(ctx, client, session, input.SessionID, trimmedCode)
	if err != nil {
		return nil, err
	}
	if effectiveProjectID != "" {
		session.ProjectID = effectiveProjectID
		s.Store.Set(input.SessionID, session)
	}

	expiresAt := time.Now().Unix() + tokenResp.ExpiresIn - 300

	result := &AntigravityTokenInfo{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresIn:    tokenResp.ExpiresIn,
		ExpiresAt:    expiresAt,
		TokenType:    tokenResp.TokenType,
	}

	userInfo, err := client.GetUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		s.printf("[AntigravityOAuth] Warning: failed to get user information: %v\n", err)
	} else if userInfo != nil {
		result.Email = userInfo.Email
	}

	loadResult, loadErr := s.loadProjectIDWithRetry(ctx, tokenResp.AccessToken, proxyURL, effectiveProjectID, 2)
	if loadResult != nil {
		result.ProjectID = strings.TrimSpace(loadResult.ProjectID)
		if loadResult.Subscription != nil {
			result.PlanType = loadResult.Subscription.PlanType
		}
	}

	if result.ProjectID == "" {
		diagErr, unrecoverable := formatAntigravityMissingProjectError(loadResult, loadErr)
		if unrecoverable {
			s.Store.Delete(input.SessionID)
		}
		s.printf("[AntigravityOAuth] Warning: project_id unresolved during exchange (unrecoverable=%t): %v\n", unrecoverable, diagErr)
		return nil, diagErr
	}

	// Delete the session only after a usable ProjectID is confirmed so standard
	// accounts never create broken projectless providers.
	s.Store.Delete(input.SessionID)

	result.PrivacyMode = s.SetPrivacy(ctx, result.AccessToken, result.ProjectID, proxyURL)

	return result, nil
}

// obtainOrReuseAntigravitySessionToken reuses a session-cached token when the
// submitted code matches ExchangedCodeHash in constant time (refreshing near
// expiry), or exchanges trimmedCode and caches the response on session.
func (s *AntigravityAuthorization) obtainOrReuseAntigravitySessionToken(
	ctx context.Context,
	client AntigravityAuthorizationClient,
	session *AntigravityAuthorizationSession,
	sessionID, trimmedCode string,
) (*google.AntigravityTokenResponse, error) {
	codeHash := hashAuthorizationCode(trimmedCode)
	if session.ExchangedToken != nil && matchesCachedCodeHash(session.ExchangedCodeHash, codeHash) {
		cached := *session.ExchangedToken
		if !session.ExchangedAt.IsZero() && cached.ExpiresIn > 0 && cached.RefreshToken != "" {
			remaining := time.Until(session.ExchangedAt.Add(time.Duration(cached.ExpiresIn) * time.Second))
			if remaining < cachedTokenRefreshHorizon {
				refreshed, err := client.RefreshToken(ctx, cached.RefreshToken)
				if err == nil && refreshed != nil && strings.TrimSpace(refreshed.AccessToken) != "" {
					if strings.TrimSpace(refreshed.RefreshToken) == "" {
						refreshed.RefreshToken = cached.RefreshToken
					}
					session.ExchangedToken = refreshed
					session.ExchangedAt = time.Now()
					s.Store.Set(sessionID, session)
					return refreshed, nil
				}
			}
		}
		return &cached, nil
	}

	tokenResp, err := client.ExchangeCode(ctx, trimmedCode, session.CodeVerifier)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange token: %w", err)
	}
	session.ExchangedToken = tokenResp
	session.ExchangedCodeHash = codeHash
	session.ExchangedAt = time.Now()
	s.Store.Set(sessionID, session)
	return tokenResp, nil
}

// refreshToken attempts token renewal at most four times with one-, two-, and
// four-second sleeps after failed attempts.
func (s *AntigravityAuthorization) refreshToken(ctx context.Context, refreshToken, proxyURL string) (*AntigravityTokenInfo, error) {
	var lastErr error

	for attempt := range 4 {
		if attempt > 0 {
			backoff := min(time.Duration(1<<uint(attempt-1))*time.Second, 30*time.Second)
			// Retry after a failed attempt with finite spacing. The sleep ignores
			// cancellation, so shutdown joins actual completion after this delay returns.
			time.Sleep(backoff)
		}

		client, err := s.Options.NewClient(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("create antigravity client failed: %w", err)
		}
		tokenResp, err := client.RefreshToken(ctx, refreshToken)
		if err == nil {
			now := time.Now()
			expiresAt := now.Unix() + tokenResp.ExpiresIn - 300
			s.printf("[AntigravityOAuth] Token refreshed: expires_in=%d, expires_at=%d (%s)\n",
				tokenResp.ExpiresIn, expiresAt, time.Unix(expiresAt, 0).Format("2006-01-02 15:04:05"))
			return &AntigravityTokenInfo{
				AccessToken:  tokenResp.AccessToken,
				RefreshToken: tokenResp.RefreshToken,
				ExpiresIn:    tokenResp.ExpiresIn,
				ExpiresAt:    expiresAt,
				TokenType:    tokenResp.TokenType,
			}, nil
		}

		if IsNonRetryableAntigravityOAuthError(err) {
			return nil, err
		}
		if s.Options.IsConnectionError != nil && s.Options.IsConnectionError(err) {
			return nil, fmt.Errorf("proxy unavailable: %w", err)
		}
		lastErr = err
	}

	return nil, fmt.Errorf("failed to refresh token after retries: %w", lastErr)
}

// validateRefreshToken renews a credential with an optional project hint before
// querying email, project, subscription, and privacy state. Interactive callers
// receive a 400 BadRequest error when neither discovery nor projectID yields a
// usable GCP project identifier.
func (s *AntigravityAuthorization) validateRefreshToken(ctx context.Context, refreshToken, projectID string, proxyID *int64) (*AntigravityTokenInfo, error) {
	if err := ValidateGoogleProjectID(projectID); err != nil {
		return nil, apperror.BadRequest("INVALID_PROJECT_ID", err.Error())
	}
	trimmedProject := strings.TrimSpace(projectID)

	var proxyURL string
	if proxyID != nil && s.Options.ResolveProxy != nil {
		if proxy, ok := s.Options.ResolveProxy(ctx, *proxyID); ok {
			proxyURL = proxy
		}
	}

	tokenInfo, err := s.refreshToken(ctx, refreshToken, proxyURL)
	if err != nil {
		return nil, err
	}

	client, err := s.Options.NewClient(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("create antigravity client failed: %w", err)
	}
	userInfo, err := client.GetUserInfo(ctx, tokenInfo.AccessToken)
	if err != nil {
		s.printf("[AntigravityOAuth] Warning: failed to get user information: %v\n", err)
	} else if userInfo != nil {
		tokenInfo.Email = userInfo.Email
	}

	loadResult, loadErr := s.loadProjectIDWithRetry(ctx, tokenInfo.AccessToken, proxyURL, trimmedProject, 2)
	if loadResult != nil {
		tokenInfo.ProjectID = strings.TrimSpace(loadResult.ProjectID)
		if loadResult.Subscription != nil {
			tokenInfo.PlanType = loadResult.Subscription.PlanType
		}
	}
	if tokenInfo.ProjectID == "" {
		diagErr, _ := formatAntigravityMissingProjectError(loadResult, loadErr)
		return nil, apperror.BadRequest("ANTIGRAVITY_PROJECT_ID_REQUIRED", diagErr.Error())
	}

	tokenInfo.PrivacyMode = s.SetPrivacy(ctx, tokenInfo.AccessToken, tokenInfo.ProjectID, proxyURL)

	return tokenInfo, nil
}

// IsNonRetryableAntigravityOAuthError checks four case-sensitive OAuth error
// substrings.
func IsNonRetryableAntigravityOAuthError(err error) bool {
	msg := err.Error()
	nonRetryable := []string{
		"invalid_grant",
		"invalid_client",
		"unauthorized_client",
		"access_denied",
	}
	for _, needle := range nonRetryable {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// refreshProviderToken renews a nonnil Antigravity OAuth record with a
// nonblank refresh token while preserving existing stored project_id on
// transient discovery errors.
func (s *AntigravityAuthorization) refreshProviderToken(ctx context.Context, provider *Record) (*AntigravityTokenInfo, error) {
	if provider.Platform != PlatformAntigravity || provider.Type != ProviderTypeOAuth {
		return nil, fmt.Errorf("provider is not an Antigravity OAuth provider")
	}

	refreshToken := provider.GetCredential("refresh_token")
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("no refresh_token available")
	}

	var proxyURL string
	if provider.ProxyID != nil && s.Options.ResolveProxy != nil {
		if proxy, ok := s.Options.ResolveProxy(ctx, *provider.ProxyID); ok {
			proxyURL = proxy
		}
	}

	tokenInfo, err := s.refreshToken(ctx, refreshToken, proxyURL)
	if err != nil {
		return nil, err
	}

	existingEmail := strings.TrimSpace(provider.GetCredential("email"))
	if existingEmail != "" {
		tokenInfo.Email = existingEmail
	}

	existingProjectID := strings.TrimSpace(provider.GetCredential("project_id"))
	loadResult, loadErr := s.loadProjectIDWithRetry(ctx, tokenInfo.AccessToken, proxyURL, existingProjectID, 2)

	if loadErr != nil {
		tokenInfo.ProjectID = existingProjectID
		if existingProjectID == "" {
			tokenInfo.ProjectIDMissing = true
		}
	}
	if loadResult != nil {
		if loadResult.ProjectID != "" {
			tokenInfo.ProjectID = loadResult.ProjectID
		} else if existingProjectID != "" {
			tokenInfo.ProjectID = existingProjectID
		}
		if loadResult.Subscription != nil {
			tokenInfo.PlanType = loadResult.Subscription.PlanType
		}
	}

	return tokenInfo, nil
}

// loadProjectIDWithRetry queries LoadCodeAssist, OnboardUser, and optional
// Cloud Resource Manager fallback across an initial attempt plus maxRetries
// additional attempts.
func (s *AntigravityAuthorization) loadProjectIDWithRetry(
	ctx context.Context,
	accessToken, proxyURL, hintProject string,
	maxRetries int,
) (*loadCodeAssistResult, error) {
	hintProject = strings.TrimSpace(hintProject)
	var lastErr error
	var lastSubscription *AntigravitySubscriptionResult
	var lastIneligible []*google.AntigravityIneligibleTier

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			backoff := min(time.Duration(1<<uint(attempt-1))*time.Second, 8*time.Second)
			if s.Options.RetryBackoff != nil {
				backoff = s.Options.RetryBackoff(attempt)
			}
			timer := time.NewTimer(backoff)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return &loadCodeAssistResult{
					Subscription:    lastSubscription,
					IneligibleTiers: lastIneligible,
				}, ctx.Err()
			}
		}

		client, err := s.Options.NewClient(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("create antigravity client failed: %w", err)
		}
		loadResp, loadRaw, err := callAntigravityLoadCodeAssist(ctx, client, accessToken, hintProject)

		if loadResp != nil {
			sub := NormalizeAntigravitySubscription(loadResp)
			lastSubscription = &sub
			if len(loadResp.IneligibleTiers) > 0 {
				lastIneligible = loadResp.IneligibleTiers
			}
		}

		if err == nil && loadResp != nil && strings.TrimSpace(loadResp.CloudAICompanionProject) != "" {
			return &loadCodeAssistResult{
				ProjectID:       strings.TrimSpace(loadResp.CloudAICompanionProject),
				Subscription:    lastSubscription,
				IneligibleTiers: lastIneligible,
			}, nil
		}

		if err == nil {
			// Registered tiers without a companion project accept an explicit operator
			// GCP Project ID directly without repeating onboarding.
			if loadResp != nil && strings.TrimSpace(loadResp.GetTier()) != "" && hintProject != "" {
				return &loadCodeAssistResult{
					ProjectID:       hintProject,
					Subscription:    lastSubscription,
					IneligibleTiers: lastIneligible,
				}, nil
			}
			if projectID, onboardErr := tryOnboardProjectID(ctx, client, accessToken, hintProject, loadRaw); onboardErr == nil && projectID != "" {
				return &loadCodeAssistResult{
					ProjectID:       projectID,
					Subscription:    lastSubscription,
					IneligibleTiers: lastIneligible,
				}, nil
			} else if onboardErr != nil {
				lastErr = onboardErr
				// Stop retrying immediately when upstream returned no onboarding tier
				// because repeating identical loadCodeAssist calls cannot invent one.
				if loadResp != nil && len(loadResp.AllowedTiers) == 0 {
					break
				}
				continue
			}
		}

		if err != nil {
			lastErr = err
		} else if loadResp == nil {
			lastErr = fmt.Errorf("LoadCodeAssist returned an empty response")
		} else {
			lastErr = fmt.Errorf("LoadCodeAssist returned an empty project_id")
		}
	}

	if hintProject != "" && lastSubscription != nil {
		return &loadCodeAssistResult{
			ProjectID:       hintProject,
			Subscription:    lastSubscription,
			IneligibleTiers: lastIneligible,
		}, nil
	}
	if s.Options.FetchProject != nil {
		if rmProject, rmErr := s.Options.FetchProject(ctx, accessToken, proxyURL); rmErr == nil && strings.TrimSpace(rmProject) != "" {
			return &loadCodeAssistResult{
				ProjectID:       strings.TrimSpace(rmProject),
				Subscription:    lastSubscription,
				IneligibleTiers: lastIneligible,
			}, nil
		}
	}

	if lastSubscription != nil || len(lastIneligible) > 0 {
		return &loadCodeAssistResult{
			Subscription:    lastSubscription,
			IneligibleTiers: lastIneligible,
		}, fmt.Errorf("failed to get project_id after %d retries: %w", maxRetries, lastErr)
	}
	return nil, fmt.Errorf("failed to get project_id after %d retries: %w", maxRetries, lastErr)
}

// callAntigravityLoadCodeAssist invokes LoadCodeAssistWithProject when client
// supports project hints and hintProject is nonblank, otherwise LoadCodeAssist.
func callAntigravityLoadCodeAssist(
	ctx context.Context,
	client AntigravityAuthorizationClient,
	accessToken, hintProject string,
) (*google.AntigravityLoadCodeAssistResponse, map[string]any, error) {
	if hintProject != "" {
		if hc, ok := client.(antigravityProjectHintClient); ok {
			return hc.LoadCodeAssistWithProject(ctx, accessToken, hintProject)
		}
	}
	return client.LoadCodeAssist(ctx, accessToken)
}

// tryOnboardProjectID delegates onboarding with the raw response default tier
// (or first nonblank allowed tier), forwarding hintProject when supported.
func tryOnboardProjectID(
	ctx context.Context,
	client AntigravityAuthorizationClient,
	accessToken, hintProject string,
	loadRaw map[string]any,
) (string, error) {
	tierID := ResolveAntigravityDefaultTierID(loadRaw)
	if tierID == "" {
		return "", fmt.Errorf("loadCodeAssist returned no usable default tier")
	}

	var projectID string
	var err error
	if hintProject != "" {
		if hc, ok := client.(antigravityProjectHintClient); ok {
			projectID, err = hc.OnboardUserWithProject(ctx, accessToken, tierID, hintProject)
		} else {
			projectID, err = client.OnboardUser(ctx, accessToken, tierID)
		}
	} else {
		projectID, err = client.OnboardUser(ctx, accessToken, tierID)
	}
	if err != nil {
		return "", fmt.Errorf("onboardUser failed (tier=%s): %w", tierID, err)
	}
	return strings.TrimSpace(projectID), nil
}

// formatAntigravityMissingProjectError builds an actionable diagnostic from
// ineligible-tier observations or missing-project discovery errors and reports
// whether the failure is unrecoverable.
func formatAntigravityMissingProjectError(result *loadCodeAssistResult, loadErr error) (error, bool) {
	if result != nil {
		for _, tier := range result.IneligibleTiers {
			if tier == nil {
				continue
			}
			code := strings.ToUpper(strings.TrimSpace(tier.ReasonCode))
			msg := firstNonEmptyText(strings.TrimSpace(tier.ReasonMessage), strings.TrimSpace(tier.ValidationErrorMessage))
			switch code {
			case "TOS_VIOLATION":
				return fmt.Errorf("account rejected by Antigravity (%s): %s", code, firstNonEmptyText(msg, "terms of service violation")), true
			case "VALIDATION_REQUIRED":
				if url := strings.TrimSpace(tier.ValidationURL); url != "" {
					return fmt.Errorf("account verification required (%s): complete verification at %s and click Complete Auth again (no re-login needed)", firstNonEmptyText(msg, code), url), false
				}
				return fmt.Errorf("account verification required (%s): complete verification in your Google account and click Complete Auth again", firstNonEmptyText(msg, code)), false
			case "UNSUPPORTED_LOCATION", "INELIGIBLE_ACCOUNT":
				if msg != "" {
					return fmt.Errorf("account eligibility check failed on Antigravity (%s: %s): if this account uses a GCP project, enter your GCP Project ID above and click Complete Auth again", code, msg), false
				}
			}
		}
	}
	if loadErr != nil && isServiceDisabledGuidance(loadErr) {
		return loadErr, false
	}
	return errors.New("missing project_id for Antigravity account (no managed companion project was returned): enter an active GCP Project ID in the Project ID field above and click Complete Auth again (no re-login needed)"), false
}

// ResolveAntigravityDefaultTierID returns the first nonblank trimmed ID from a
// default entry in the raw allowedTiers array. Missing or differently typed
// containers return empty, and paid/current tier metadata does not substitute
// for that default.
func ResolveAntigravityDefaultTierID(loadRaw map[string]any) string {
	if len(loadRaw) == 0 {
		return ""
	}

	rawTiers, ok := loadRaw["allowedTiers"]
	if !ok {
		return ""
	}

	tiers, ok := rawTiers.([]any)
	if !ok {
		return ""
	}

	for _, rawTier := range tiers {
		tier, ok := rawTier.(map[string]any)
		if !ok {
			continue
		}
		if isDefault, _ := tier["isDefault"].(bool); !isDefault {
			continue
		}
		if id, ok := tier["id"].(string); ok {
			id = strings.TrimSpace(id)
			if id != "" {
				return id
			}
		}
	}

	return ""
}

// fillProjectID discovers project metadata with the supplied access token and
// resolved record proxy.
func (s *AntigravityAuthorization) fillProjectID(ctx context.Context, provider *Record, accessToken string) (string, error) {
	var proxyURL string
	if provider.ProxyID != nil && s.Options.ResolveProxy != nil {
		if proxy, ok := s.Options.ResolveProxy(ctx, *provider.ProxyID); ok {
			proxyURL = proxy
		}
	}
	result, err := s.loadProjectIDWithRetry(ctx, accessToken, proxyURL, strings.TrimSpace(provider.GetCredential("project_id")), 2)
	if result != nil {
		return result.ProjectID, err
	}
	return "", err
}

// BuildProviderCredentials creates stored credentials from nonnil token data.
func (s *AntigravityAuthorization) BuildProviderCredentials(tokenInfo *AntigravityTokenInfo) map[string]any {
	creds := map[string]any{
		"access_token": tokenInfo.AccessToken,
		"expires_at":   strconv.FormatInt(tokenInfo.ExpiresAt, 10),
	}
	if tokenInfo.RefreshToken != "" {
		creds["refresh_token"] = tokenInfo.RefreshToken
	}
	if tokenInfo.TokenType != "" {
		creds["token_type"] = tokenInfo.TokenType
	}
	if tokenInfo.Email != "" {
		creds["email"] = tokenInfo.Email
	}
	if tokenInfo.ProjectID != "" {
		creds["project_id"] = tokenInfo.ProjectID
	}
	if tokenInfo.PlanType != "" {
		creds["plan_type"] = tokenInfo.PlanType
	}
	return creds
}

// printf invokes Options.Printf when configured.
func (s *AntigravityAuthorization) printf(format string, args ...any) {
	if s.Options.Printf != nil {
		s.Options.Printf(format, args...)
	}
}

// GenerateAuthURL registers PKCE URL setup for shutdown waiting and releases
// that registration on return.
func (s *AntigravityAuthorization) GenerateAuthURL(ctx context.Context, proxyID *int64) (*AntigravityAuthURLResult, error) {
	operation, done, err := s.activity.begin(ctx, fmt.Errorf("antigravity authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	return s.generateAuthURL(operation, proxyID)
}

// ExchangeCode registers a nonnil code input for authorization shutdown waiting.
func (s *AntigravityAuthorization) ExchangeCode(ctx context.Context, input *AntigravityExchangeCodeInput) (*AntigravityTokenInfo, error) {
	operation, done, err := s.activity.begin(ctx, fmt.Errorf("antigravity authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	return s.exchangeCode(operation, input)
}

// RefreshToken registers credential renewal and releases its activity on return.
func (s *AntigravityAuthorization) RefreshToken(ctx context.Context, refreshToken, proxyURL string) (*AntigravityTokenInfo, error) {
	operation, done, err := s.activity.begin(ctx, fmt.Errorf("antigravity authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	return s.refreshToken(operation, refreshToken, proxyURL)
}

// ValidateRefreshToken registers renewal plus account validation without an
// explicit project hint, delegating to ValidateRefreshTokenWithProject.
func (s *AntigravityAuthorization) ValidateRefreshToken(ctx context.Context, refreshToken string, proxyID *int64) (*AntigravityTokenInfo, error) {
	return s.ValidateRefreshTokenWithProject(ctx, refreshToken, "", proxyID)
}

// ValidateRefreshTokenWithProject registers renewal plus account validation
// with an optional operator-supplied GCP Project ID hint.
func (s *AntigravityAuthorization) ValidateRefreshTokenWithProject(ctx context.Context, refreshToken, projectID string, proxyID *int64) (*AntigravityTokenInfo, error) {
	operation, done, err := s.activity.begin(ctx, fmt.Errorf("antigravity authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	return s.validateRefreshToken(operation, refreshToken, projectID, proxyID)
}

// RefreshProviderToken registers renewal for a stable nonnil provider record.
func (s *AntigravityAuthorization) RefreshProviderToken(ctx context.Context, provider *Record) (*AntigravityTokenInfo, error) {
	operation, done, err := s.activity.begin(ctx, fmt.Errorf("antigravity authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	return s.refreshProviderToken(operation, provider)
}

// FillProjectID registers project discovery using a stable record and supplied token.
func (s *AntigravityAuthorization) FillProjectID(ctx context.Context, provider *Record, accessToken string) (string, error) {
	operation, done, err := s.activity.begin(ctx, fmt.Errorf("antigravity authorization is stopped"))
	if err != nil {
		return "", err
	}
	defer done()
	return s.fillProjectID(operation, provider, accessToken)
}
