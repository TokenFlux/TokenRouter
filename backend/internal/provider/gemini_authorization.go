package provider

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strconv"
	"strings"
	"time"

	geminiwire "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

// gemini_authorization.go exchanges and refreshes Gemini OAuth credentials
// with project and account-tier discovery.
// Gemini authorization stores PKCE proof with its OAuth variant, proxy,
// project, and selected tier. Code Assist uses the built-in Gemini CLI client
// for GCP and Workspace projects, while AI Studio requires a custom Google
// OAuth client and uses its dedicated loopback callback address.
//
// Recoverable project-discovery failures (missing project_id, SERVICE_DISABLED,
// or VALIDATION_REQUIRED) retain the exchanged Google token in the session
// keyed by constant-time SHA-256 code comparison, letting operators supply a
// GCP Project ID or finish verification and click Complete Auth again without
// repeating browser login. Unrecoverable client or terms violations delete the
// session immediately, and successful completion deletes the session before
// returning credentials.

const (
	// defaultGeminiOnboardPollInterval spaces GetOperation polls when
	// GeminiAuthorizationOptions.OnboardPollInterval is unset.
	defaultGeminiOnboardPollInterval = 2 * time.Second

	// cachedTokenRefreshHorizon triggers an automatic RefreshToken call before
	// reusing a session-cached access token that is within sixty seconds of expiry.
	cachedTokenRefreshHorizon = 60 * time.Second
)

var (
	// tierIDPattern accepts up to 64 ASCII alphanumerics, underscores, hyphens, and slashes.
	tierIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_/-]+$`)

	// numericProjectNumberPattern detects numeric GCP Project Numbers supplied where
	// a string GCP Project ID is required.
	numericProjectNumberPattern = regexp.MustCompile(`^\d+$`)

	// googleProjectIDPattern accepts standard and domain-scoped GCP Project IDs
	// beginning with an ASCII letter.
	googleProjectIDPattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9._:-]*$`)
)

// GeminiOAuthClient exchanges authorization codes and renews variant-specific
// Google credentials.
type GeminiOAuthClient interface {
	// ExchangeCode exchanges variant-specific code and saved PKCE proof. Return
	// nonnil token data on success so authorization can cache or finalize
	// credentials and account metadata.
	ExchangeCode(context.Context, string, string, string, string, string) (*google.TokenResponse, error)
	// RefreshToken renews the supplied variant credential through its proxy.
	// Successful data must be nonnil because authorization reads lifetime and
	// optional renewal fields before returning the credential result.
	RefreshToken(context.Context, string, string, string) (*google.TokenResponse, error)
}

// GeminiCodeAssistClient reads project eligibility, submits onboarding, and
// polls pending onboarding operations.
type GeminiCodeAssistClient interface {
	// LoadCodeAssist reads project and tier observations with optional metadata.
	// Nil response can trigger onboarding; a nonnil response may retain useful
	// tier and ineligibility information.
	LoadCodeAssist(context.Context, string, string, *geminiwire.LoadCodeAssistRequest) (*geminiwire.LoadCodeAssistResponse, error)
	// OnboardUser submits selected-tier onboarding and returns an operation state.
	OnboardUser(context.Context, string, string, *geminiwire.OnboardUserRequest) (*geminiwire.OnboardUserResponse, error)
	// GetOperation polls a pending onboarding operation by its resource name.
	GetOperation(context.Context, string, string, string) (*geminiwire.OnboardUserResponse, error)
}

// GeminiAuthorizationOptions binds dynamic OAuth configuration, proof
// generation, discovery, and diagnostics.
type GeminiAuthorizationOptions struct {
	// Config is required during capability checks and variant setup. Keep outputs
	// coherent within a flow; changing client configuration can invalidate
	// compatibility with a stored refresh token.
	Config func() google.OAuthConfig
	// BuiltinClientID, CLIRedirectURI, and AIStudioRedirectURI pair client
	// identity with callback selection. Built-in clients use the CLI address;
	// custom clients use the AI Studio address, replacing the input redirect.
	BuiltinClientID, CLIRedirectURI, AIStudioRedirectURI string
	// GenerateState, GenerateCodeVerifier, and GenerateSessionID prepare one
	// paired proof before publication. Their errors abort setup before storing
	// state and verifier under the session identifier.
	GenerateState, GenerateCodeVerifier, GenerateSessionID func() (string, error)
	// GenerateCodeChallenge is required after verifier generation and derives the
	// URL challenge from the exact verifier retained for code exchange.
	GenerateCodeChallenge func(string) string
	// EffectiveOAuthConfig is required during setup and AI Studio exchange checks.
	// Errors leave any already stored proof available until expiry or cleanup.
	EffectiveOAuthConfig func(google.OAuthConfig, string) (google.OAuthConfig, error)
	// BuildAuthorizationURL is required after redirect selection. Its failure
	// retains stored proof, including the callback address chosen for that client.
	BuildAuthorizationURL func(google.OAuthConfig, string, string, string, string, string) (string, error)
	// ResolveProxy is required when ProxyID is supplied. False availability
	// retains saved exchange transport or leaves a new flow proxy URL empty.
	ResolveProxy func(context.Context, int64) (string, bool)
	// FetchProject queries Cloud Resource Manager when Code Assist discovery
	// returns no companion project.
	FetchProject func(context.Context, string, string) (string, error)
	// OnboardPollInterval overrides the two-second wait between pending
	// onboarding operation polls when positive.
	OnboardPollInterval time.Duration
	// Logf and Printf are required by reached metadata and failure paths. Logf
	// records discovery decisions and details; Printf reports recoverable
	// discovery or credential-validation diagnostics to the operator.
	Logf, Printf func(string, ...any)
}

// GeminiAuthorization owns saved proof and tracked variant-specific Google
// authorization operations.
// @project-doc docs/interfaces/gemini_upstream.md#gemini_native_execution
type GeminiAuthorization struct {
	// Store owns temporary proof and cleanup lifecycle. Construction initializes
	// it; completed exchanges and unrecoverable errors delete the session, while
	// recoverable missing-project errors retain cached tokens for same-session retry.
	Store *GeminiAuthorizationSessions
	// Client and CodeAssist borrow shared protocol clients and remain usable
	// until registered operations return.
	Client     GeminiOAuthClient      // shared Google token client
	CodeAssist GeminiCodeAssistClient // Code Assist discovery and onboarding client
	// Options copies callback values and borrows captured dependencies. Configure
	// the shared service before operations and keep callbacks stable through
	// registered work.
	Options  GeminiAuthorizationOptions
	activity operationActivity // registered authorization operation lifetime
}

// GeminiOAuthCapabilities reports custom-client eligibility and required AI
// Studio callback addresses.
type GeminiOAuthCapabilities struct {
	AIStudioOAuthEnabled bool     `json:"ai_studio_oauth_enabled"` // custom client capability indicator
	RequiredRedirectURIs []string `json:"required_redirect_uris"`  // configured custom callback addresses
}

// GeminiAuthURLResult pairs the browser destination with saved session state
// identifiers.
type GeminiAuthURLResult struct {
	AuthURL   string `json:"auth_url"`   // generated browser authorization URL
	SessionID string `json:"session_id"` // stored proof lookup identifier
	State     string `json:"state"`      // returned OAuth state proof
}

// GeminiExchangeCodeInput supplies code proof and optional project, proxy, and
// tier selections.
type GeminiExchangeCodeInput struct {
	SessionID string // stored proof lookup identifier
	State     string // supplied OAuth state proof
	Code      string // supplied authorization code text
	// ProjectID overrides the stored session project hint when nonblank after
	// trimming, allowing Project ID completion without generating a new auth URL.
	ProjectID string
	// ProxyID overrides saved transport only when selected lookup succeeds.
	// An unavailable selection retains the proxy associated with this stored session.
	ProxyID *int64
	// OAuthType is accepted metadata, while ExchangeCode uses the stored session
	// variant. An empty saved variant selects the Code Assist exchange path.
	OAuthType string
	// TierID supplies a compatible fallback after automatic discovery. Empty
	// or incompatible input uses the saved session tier before its variant default.
	TierID string
}

// GeminiTokenInfo carries renewed credentials, adjusted expiry, and account
// discovery hints.
type GeminiTokenInfo struct {
	AccessToken  string `json:"access_token"`         // exchanged OAuth access token
	RefreshToken string `json:"refresh_token"`        // optional token renewal credential
	ExpiresIn    int64  `json:"expires_in"`           // upstream token lifetime seconds
	ExpiresAt    int64  `json:"expires_at"`           // adjusted stored Unix expiry
	TokenType    string `json:"token_type"`           // upstream token type name
	Scope        string `json:"scope,omitempty"`      // upstream authorization scope text
	ProjectID    string `json:"project_id,omitempty"` // selected project execution identifier
	OAuthType    string `json:"oauth_type,omitempty"` // selected OAuth variant label
	TierID       string `json:"tier_id,omitempty"`    // selected tier identifier text
	// Extra merges into credentials last and can override token or tier keys.
	// Nested values remain borrowed through that shallow metadata copy.
	Extra map[string]any `json:"extra,omitempty"`
}

// NewGeminiAuthorization retains protocol clients, copies options, and creates
// idle session storage. Configure reached callbacks and clients before use;
// Start enables cleanup, and StopContext owns authorization cancellation and
// completion waiting.
func NewGeminiAuthorization(client GeminiOAuthClient, codeassist GeminiCodeAssistClient, options GeminiAuthorizationOptions) *GeminiAuthorization {
	return &GeminiAuthorization{
		Store:      NewGeminiAuthorizationSessions(),
		Client:     client,
		CodeAssist: codeassist,
		Options:    options,
	}
}

// Start enables periodic expiry cleanup through the authorization session
// store. Repeated startup follows its lifecycle gate, and StopContext
// permanently closes cleanup admission before waiting for tracked
// authorization work.
func (s *GeminiAuthorization) Start() { s.Store.Start() }

// StopContext stops and joins session cleanup before canceling authorization
// activity. A nonnil context budgets the activity wait; its first completed
// result, including unfinished-work timeout, remains available to subsequent
// calls.
func (s *GeminiAuthorization) StopContext(ctx context.Context) error {
	s.Store.Stop()
	return s.activity.stop(ctx, "gemini authorization")
}

// ValidateGoogleProjectID checks optional operator-supplied GCP Project IDs.
// Blank values pass; numeric GCP Project Numbers and malformed identifiers
// return descriptive validation errors before upstream calls run.
func ValidateGoogleProjectID(projectID string) error {
	trimmed := strings.TrimSpace(projectID)
	if trimmed == "" {
		return nil
	}
	if len(trimmed) > 128 {
		return fmt.Errorf("invalid project_id: exceeds maximum length of 128 characters")
	}
	if numericProjectNumberPattern.MatchString(trimmed) {
		return fmt.Errorf("invalid project_id %q: appears to be a numeric GCP Project Number rather than a Project ID string (use the Project ID such as \"my-gcp-project-123\" from Google Cloud Console)", trimmed)
	}
	if !googleProjectIDPattern.MatchString(trimmed) {
		return fmt.Errorf("invalid project_id %q: must start with a letter and contain only letters, digits, hyphens, underscores, dots, or colons", trimmed)
	}
	return nil
}

// hashAuthorizationCode computes the lowercase hex SHA-256 digest of a trimmed
// OAuth authorization code for session-bound idempotent retry verification.
func hashAuthorizationCode(code string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(code)))
	return hex.EncodeToString(sum[:])
}

// matchesCachedCodeHash compares two hex SHA-256 digests in constant time and
// rejects empty hashes.
func matchesCachedCodeHash(savedHash, candidateHash string) bool {
	if savedHash == "" || len(savedHash) != len(candidateHash) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(savedHash), []byte(candidateHash)) == 1
}

// GetOAuthConfig reports whether custom client configuration enables AI Studio
// OAuth. Config is required; trimmed ID and secret must both be nonempty, and
// the ID must differ from the configured built-in identifier.
func (s *GeminiAuthorization) GetOAuthConfig() *GeminiOAuthCapabilities {
	// AI Studio OAuth is only enabled when the operator configures a custom OAuth client.
	clientID := strings.TrimSpace(s.Options.Config().ClientID)
	clientSecret := strings.TrimSpace(s.Options.Config().ClientSecret)
	enabled := clientID != "" && clientSecret != "" && clientID != s.Options.BuiltinClientID

	return &GeminiOAuthCapabilities{
		AIStudioOAuthEnabled: enabled,
		RequiredRedirectURIs: []string{s.Options.AIStudioRedirectURI},
	}
}

// GenerateAuthURL registers setup and stores state, verifier, proxy, project,
// tier, and variant for its URL. Only code_assist and ai_studio are supported,
// and nonblank projectID inputs are validated before session creation.
// Pass a nonnil context for independent activity registration.
func (s *GeminiAuthorization) GenerateAuthURL(ctx context.Context, proxyID *int64, redirectURI, projectID, oauthType, tierID string) (*GeminiAuthURLResult, error) {
	operation, done, startErr := s.activity.begin(ctx, errors.New("gemini authorization is stopped"))
	if startErr != nil {
		return nil, startErr
	}
	defer done()
	ctx = operation

	oauthType = strings.TrimSpace(oauthType)
	if oauthType == "" {
		oauthType = "code_assist"
	}
	if oauthType != "code_assist" && oauthType != "ai_studio" {
		return nil, fmt.Errorf("invalid oauth_type %q: must be 'code_assist' or 'ai_studio' (personal and Google One accounts must use Antigravity)", oauthType)
	}
	if err := ValidateGoogleProjectID(projectID); err != nil {
		return nil, err
	}

	// Prepare state, verifier, challenge, and identifier before publishing proof.
	state, err := s.Options.GenerateState()
	if err != nil {
		return nil, fmt.Errorf("failed to generate state: %w", err)
	}
	codeVerifier, err := s.Options.GenerateCodeVerifier()
	if err != nil {
		return nil, fmt.Errorf("failed to generate code verifier: %w", err)
	}
	codeChallenge := s.Options.GenerateCodeChallenge(codeVerifier)
	sessionID, err := s.Options.GenerateSessionID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate session ID: %w", err)
	}

	var proxyURL string
	if proxyID != nil && s.Options.ResolveProxy != nil {
		if resolved, ok := s.Options.ResolveProxy(ctx, *proxyID); ok {
			proxyURL = resolved
		}
	}

	// Clear custom credentials for Code Assist before effective client selection.
	// AI Studio retains configured credentials so setup can validate its custom-client requirement.
	oauthCfg := s.Options.Config()
	if oauthType == "code_assist" {
		oauthCfg.ClientID = ""
		oauthCfg.ClientSecret = ""
	}

	session := &GeminiOAuthSession{
		State:        state,
		CodeVerifier: codeVerifier,
		ProxyURL:     proxyURL,
		RedirectURI:  redirectURI,
		ProjectID:    strings.TrimSpace(projectID),
		TierID:       CanonicalGeminiTierIDForOAuthType(oauthType, tierID),
		OAuthType:    oauthType,
		CreatedAt:    time.Now(),
	}
	s.Store.Set(sessionID, session)

	effectiveCfg, err := s.Options.EffectiveOAuthConfig(oauthCfg, oauthType)
	if err != nil {
		return nil, err
	}

	isBuiltinClient := effectiveCfg.ClientID == s.Options.BuiltinClientID

	// AI Studio OAuth requires a user-provided OAuth client (built-in Gemini CLI
	// client is scope-restricted).
	if oauthType == "ai_studio" && isBuiltinClient {
		return nil, fmt.Errorf("AI Studio OAuth requires a custom OAuth Client (GEMINI_OAUTH_CLIENT_ID / GEMINI_OAUTH_CLIENT_SECRET). If you don't want to configure an OAuth client, please use an AI Studio API Key provider instead")
	}

	if isBuiltinClient {
		redirectURI = s.Options.CLIRedirectURI
	} else {
		redirectURI = s.Options.AIStudioRedirectURI
	}
	session.RedirectURI = redirectURI
	s.Store.Set(sessionID, session)

	authURL, err := s.Options.BuildAuthorizationURL(effectiveCfg, state, codeChallenge, redirectURI, session.ProjectID, oauthType)
	if err != nil {
		return nil, err
	}

	return &GeminiAuthURLResult{
		AuthURL:   authURL,
		SessionID: sessionID,
		State:     state,
	}, nil
}

// ValidateTierID accepts empty or up to sixty-four ASCII letters, digits,
// underscores, hyphens, and slashes. Validation is structural: a permitted
// string need not be a recognized canonical tier.
func ValidateTierID(tierID string) error {
	if tierID == "" {
		return nil
	}
	if len(tierID) > 64 {
		return fmt.Errorf("tier_id exceeds maximum length of 64 characters")
	}
	if !tierIDPattern.MatchString(tierID) {
		return fmt.Errorf("tier_id contains invalid characters")
	}
	return nil
}

// ExtractTierIDFromAllowedTiers selects a trimmed nonempty default tier, then
// the first nonempty tier when its sentinel remains. Missing and blank IDs
// cannot displace a usable candidate; empty input returns legacy-tier.
func ExtractTierIDFromAllowedTiers(allowedTiers []geminiwire.AllowedTier) string {
	// Use legacy-tier as the unresolved marker before default selection. Its
	// literal identity also triggers the later first-nonempty pass, so
	// explicit legacy-tier defaults need that same handling.
	tierID := "legacy-tier"
	for _, tier := range allowedTiers {
		if tier.IsDefault && strings.TrimSpace(tier.ID) != "" {
			tierID = strings.TrimSpace(tier.ID)
			break
		}
	}
	if tierID == "legacy-tier" {
		for _, tier := range allowedTiers {
			if strings.TrimSpace(tier.ID) != "" {
				tierID = strings.TrimSpace(tier.ID)
				break
			}
		}
	}
	return tierID
}

// ExchangeCode validates saved state, exchanges or reuses the session-cached
// Google token, and resolves required Code Assist project and tier metadata.
//
// When token exchange succeeds but Code Assist project discovery fails for a
// recoverable reason (missing project_id, SERVICE_DISABLED, or
// VALIDATION_REQUIRED), the exchanged token remains cached on the session so
// the operator can enter a GCP Project ID or finish verification and retry
// Complete Auth without re-running browser login. Unrecoverable client or terms
// rejections and successful exchanges delete the session immediately.
func (s *GeminiAuthorization) ExchangeCode(ctx context.Context, input *GeminiExchangeCodeInput) (*GeminiTokenInfo, error) {
	operation, done, startErr := s.activity.begin(ctx, errors.New("gemini authorization is stopped"))
	if startErr != nil {
		return nil, startErr
	}
	defer done()
	ctx = operation

	s.logf("[GeminiOAuth] ========== ExchangeCode START ==========")
	s.logf("[GeminiOAuth] SessionID: %s", input.SessionID)

	session, ok := s.Store.Get(input.SessionID)
	if !ok {
		s.logf("[GeminiOAuth] ERROR: Session not found or expired")
		return nil, fmt.Errorf("session not found or expired")
	}
	if strings.TrimSpace(input.State) == "" || input.State != session.State {
		s.logf("[GeminiOAuth] ERROR: Invalid state")
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
	// Validate Project ID before consuming the authorization code so a numeric
	// Project Number typo keeps the session intact for immediate correction.
	if err := ValidateGoogleProjectID(effectiveProjectID); err != nil {
		return nil, err
	}

	proxyURL := session.ProxyURL
	if input.ProxyID != nil && s.Options.ResolveProxy != nil {
		if resolved, ok := s.Options.ResolveProxy(ctx, *input.ProxyID); ok {
			proxyURL = resolved
		}
	}

	redirectURI := session.RedirectURI
	oauthType := strings.TrimSpace(session.OAuthType)
	if oauthType == "" {
		oauthType = "code_assist"
	}
	if oauthType != "code_assist" && oauthType != "ai_studio" {
		s.Store.Delete(input.SessionID)
		return nil, fmt.Errorf("unsupported oauth_type %q: personal and Google One accounts must use Antigravity", oauthType)
	}

	if oauthType == "ai_studio" {
		effectiveCfg, err := s.Options.EffectiveOAuthConfig(s.Options.Config(), "ai_studio")
		if err != nil {
			return nil, err
		}
		if effectiveCfg.ClientID == s.Options.BuiltinClientID {
			return nil, fmt.Errorf("AI Studio OAuth requires a custom OAuth Client. Please use an AI Studio API Key provider, or configure GEMINI_OAUTH_CLIENT_ID / GEMINI_OAUTH_CLIENT_SECRET and re-authorize")
		}
	}
	if oauthType == "code_assist" {
		redirectURI = s.Options.CLIRedirectURI
	}

	tokenResp, err := s.obtainOrReuseSessionToken(ctx, session, input.SessionID, oauthType, trimmedCode, redirectURI, proxyURL)
	if err != nil {
		return nil, err
	}
	if effectiveProjectID != "" {
		session.ProjectID = effectiveProjectID
		s.Store.Set(input.SessionID, session)
	}

	const safetyWindow = 300 // 5 minutes
	const minTTL = 30        // minimum 30 seconds
	expiresAt := max(time.Now().Unix()+tokenResp.ExpiresIn-safetyWindow, time.Now().Unix()+minTTL)

	fallbackTierID := CanonicalGeminiTierIDForOAuthType(oauthType, input.TierID)
	if fallbackTierID == "" {
		fallbackTierID = CanonicalGeminiTierIDForOAuthType(oauthType, session.TierID)
	}

	var projectID string
	var tierID string

	switch oauthType {
	case "code_assist":
		resolvedProj, discoveredTier, unrecoverable, discoverErr := s.discoverCodeAssistProjectAndTier(ctx, tokenResp.AccessToken, proxyURL, effectiveProjectID)
		if discoverErr != nil {
			if unrecoverable {
				s.Store.Delete(input.SessionID)
			}
			s.printf("[GeminiOAuth] Warning: Code Assist project discovery failed: %v\n", discoverErr)
			s.logf("[GeminiOAuth] ERROR: Code Assist project discovery failed (unrecoverable=%t): %v", unrecoverable, discoverErr)
			return nil, discoverErr
		}
		projectID = strings.TrimSpace(resolvedProj)
		if projectID == "" {
			return nil, fmt.Errorf("missing project_id for Code Assist OAuth: enter your GCP Project ID in the Project ID field above and click Complete Auth again (no re-login needed), or create an active project at https://console.cloud.google.com")
		}
		tierID = CanonicalGeminiTierIDForOAuthType(oauthType, discoveredTier)
		if tierID == "" {
			if fallbackTierID != "" {
				tierID = fallbackTierID
			} else {
				tierID = GeminiTierGCPStandard
			}
		}

	case "ai_studio":
		projectID = effectiveProjectID
		if fallbackTierID != "" {
			tierID = fallbackTierID
		} else {
			tierID = GeminiTierAIStudioFree
		}
	}

	// Delete the authorization session only after project and tier resolution
	// succeeds so completed credentials cannot be replayed.
	s.Store.Delete(input.SessionID)

	result := &GeminiTokenInfo{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    tokenResp.TokenType,
		ExpiresIn:    tokenResp.ExpiresIn,
		ExpiresAt:    expiresAt,
		Scope:        tokenResp.Scope,
		ProjectID:    projectID,
		TierID:       tierID,
		OAuthType:    oauthType,
	}
	s.logf("[GeminiOAuth] Final result - OAuth Type: %s, Project ID: %s, Tier ID: %s", result.OAuthType, result.ProjectID, result.TierID)
	s.logf("[GeminiOAuth] ========== ExchangeCode END ==========")
	return result, nil
}

// obtainOrReuseSessionToken returns a cached Google token when the same session
// submits the exact authorization code again, refreshing it if near expiry, or
// performs a fresh ExchangeCode and caches the result on session.
func (s *GeminiAuthorization) obtainOrReuseSessionToken(
	ctx context.Context,
	session *GeminiOAuthSession,
	sessionID, oauthType, trimmedCode, redirectURI, proxyURL string,
) (*google.TokenResponse, error) {
	codeHash := hashAuthorizationCode(trimmedCode)
	if session.ExchangedToken != nil && matchesCachedCodeHash(session.ExchangedCodeHash, codeHash) {
		cached := *session.ExchangedToken
		// Refresh the cached access token when within sixty seconds of expiry so a
		// delayed Project ID retry still calls Code Assist with a live bearer token.
		if !session.ExchangedAt.IsZero() && cached.ExpiresIn > 0 && cached.RefreshToken != "" {
			remaining := time.Until(session.ExchangedAt.Add(time.Duration(cached.ExpiresIn) * time.Second))
			if remaining < cachedTokenRefreshHorizon {
				refreshed, err := s.Client.RefreshToken(ctx, oauthType, cached.RefreshToken, proxyURL)
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
		s.logf("[GeminiOAuth] Reusing session-cached token for retry on session %s", sessionID)
		return &cached, nil
	}

	tokenResp, err := s.Client.ExchangeCode(ctx, oauthType, trimmedCode, session.CodeVerifier, redirectURI, proxyURL)
	if err != nil {
		s.logf("[GeminiOAuth] ERROR: Failed to exchange code: %v", err)
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	session.ExchangedToken = tokenResp
	session.ExchangedCodeHash = codeHash
	session.ExchangedAt = time.Now()
	s.Store.Set(sessionID, session)
	return tokenResp, nil
}

// refreshToken renews the supplied variant credential with at most four calls
// and nonnil successful token data. Permanent OAuth substrings stop
// immediately; other errors use one-, two-, and four-second unconditional
// sleeps, while ctx reaches cooperative requests.
func (s *GeminiAuthorization) refreshToken(ctx context.Context, oauthType, refreshToken, proxyURL string) (*GeminiTokenInfo, error) {
	var lastErr error

	for attempt := range 4 {
		if attempt > 0 {
			backoff := min(time.Duration(1<<uint(attempt-1))*time.Second, 30*time.Second)
			// Space transient exchange attempts after failure. Sleep ignores
			// cancellation, so registered work completes only after this fixed delay
			// returns.
			time.Sleep(backoff)
		}

		tokenResp, err := s.Client.RefreshToken(ctx, oauthType, refreshToken, proxyURL)
		if err == nil {
			const safetyWindow = 300 // 5 minutes
			const minTTL = 30        // minimum 30 seconds
			expiresAt := max(time.Now().Unix()+tokenResp.ExpiresIn-safetyWindow, time.Now().Unix()+minTTL)
			return &GeminiTokenInfo{
				AccessToken:  tokenResp.AccessToken,
				RefreshToken: tokenResp.RefreshToken,
				TokenType:    tokenResp.TokenType,
				ExpiresIn:    tokenResp.ExpiresIn,
				ExpiresAt:    expiresAt,
				Scope:        tokenResp.Scope,
			}, nil
		}

		if IsNonRetryableGeminiOAuthError(err) {
			return nil, err
		}
		lastErr = err
	}

	return nil, fmt.Errorf("token refresh failed after retries: %w", lastErr)
}

// IsNonRetryableGeminiOAuthError checks four case-sensitive OAuth substrings
// in a nonnil error. invalid_grant, invalid_client, unauthorized_client, and
// access_denied stop retries; other diagnostic text remains subject to the
// caller retry schedule.
func IsNonRetryableGeminiOAuthError(err error) bool {
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

// RefreshProviderToken registers renewal for a stable nonnil Gemini OAuth
// record with a nonblank refresh token. Empty stored type defaults to Code
// Assist; unresolved selected proxy lookup uses an empty URL.
func (s *GeminiAuthorization) RefreshProviderToken(ctx context.Context, provider *Record) (*GeminiTokenInfo, error) {
	operation, done, startErr := s.activity.begin(ctx, errors.New("gemini authorization is stopped"))
	if startErr != nil {
		return nil, startErr
	}
	defer done()
	ctx = operation

	if provider.Platform != PlatformGemini || provider.Type != ProviderTypeOAuth {
		return nil, fmt.Errorf("provider is not a Gemini OAuth provider")
	}

	refreshToken := provider.GetCredential("refresh_token")
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("no refresh token available")
	}

	oauthType := strings.TrimSpace(provider.GetCredential("oauth_type"))
	if oauthType == "" {
		oauthType = "code_assist"
	}

	var proxyURL string
	if provider.ProxyID != nil && s.Options.ResolveProxy != nil {
		if resolved, ok := s.Options.ResolveProxy(ctx, *provider.ProxyID); ok {
			proxyURL = resolved
		}
	}

	tokenInfo, err := s.refreshToken(ctx, oauthType, refreshToken, proxyURL)
	// A Code Assist unauthorized_client error can retry through AI Studio when
	// custom credentials are configured. Preserve the initial error if that alternate fails.
	if err != nil && oauthType == "code_assist" && strings.Contains(err.Error(), "unauthorized_client") && s.GetOAuthConfig().AIStudioOAuthEnabled {
		if alt, altErr := s.refreshToken(ctx, "ai_studio", refreshToken, proxyURL); altErr == nil {
			tokenInfo = alt
			err = nil
		}
	}
	if err != nil {
		if strings.Contains(err.Error(), "unauthorized_client") {
			return nil, fmt.Errorf("%w (OAuth client mismatch: the refresh_token is bound to the OAuth client used during authorization; please re-authorize this provider or restore the original GEMINI_OAUTH_CLIENT_ID/SECRET)", err)
		}
		return nil, err
	}

	tokenInfo.OAuthType = oauthType

	existingProjectID := strings.TrimSpace(provider.GetCredential("project_id"))
	if existingProjectID != "" {
		tokenInfo.ProjectID = existingProjectID
	}

	existingTierID := strings.TrimSpace(provider.GetCredential("tier_id"))

	if oauthType == "code_assist" {
		if existingTierID != "" {
			tokenInfo.TierID = CanonicalGeminiTierIDForOAuthType(oauthType, existingTierID)
		}
		if tokenInfo.TierID == "" {
			tokenInfo.TierID = GeminiTierGCPStandard
		}

		if strings.TrimSpace(tokenInfo.ProjectID) == "" {
			projectID, tierID, _, discoverErr := s.discoverCodeAssistProjectAndTier(ctx, tokenInfo.AccessToken, proxyURL, "")
			if discoverErr != nil {
				s.printf("[GeminiOAuth] Warning: failed to auto-detect project/tier: %v\n", discoverErr)
			} else {
				if projectID != "" {
					tokenInfo.ProjectID = projectID
				}
				if canonical := CanonicalGeminiTierIDForOAuthType(oauthType, tierID); canonical != "" {
					tokenInfo.TierID = canonical
				}
			}
		}

		if strings.TrimSpace(tokenInfo.ProjectID) == "" {
			return nil, fmt.Errorf("failed to auto-detect project_id: empty result")
		}
	}

	return tokenInfo, nil
}

// BuildProviderCredentials constructs credentials from nonnil token
// information. Access token and decimal Unix expiry are included; nonempty
// optional fields are copied, and structurally invalid tier is diagnosed and
// omitted.
func (s *GeminiAuthorization) BuildProviderCredentials(tokenInfo *GeminiTokenInfo) map[string]any {
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
	if tokenInfo.Scope != "" {
		creds["scope"] = tokenInfo.Scope
	}
	if tokenInfo.ProjectID != "" {
		creds["project_id"] = tokenInfo.ProjectID
	}
	if tokenInfo.TierID != "" {
		if err := ValidateTierID(tokenInfo.TierID); err == nil {
			creds["tier_id"] = tokenInfo.TierID
			s.printf("[GeminiOAuth] Storing tier_id: %s\n", tokenInfo.TierID)
		} else {
			s.printf("[GeminiOAuth] Invalid tier_id %s: %v\n", tokenInfo.TierID, err)
		}
	}
	if tokenInfo.OAuthType != "" {
		creds["oauth_type"] = tokenInfo.OAuthType
	}
	// Merge metadata after token fields. A colliding Extra key wins, and nested
	// references remain shared with tokenInfo because this map copy is shallow.
	if len(tokenInfo.Extra) > 0 {
		maps.Copy(creds, tokenInfo.Extra)
	}
	return creds
}

// fetchProjectID runs Code Assist discovery without an operator project hint.
func (s *GeminiAuthorization) fetchProjectID(ctx context.Context, accessToken, proxyURL string) (string, string, error) {
	projectID, tierID, _, err := s.discoverCodeAssistProjectAndTier(ctx, accessToken, proxyURL, "")
	return projectID, tierID, err
}

// discoverCodeAssistProjectAndTier follows the official gemini-cli setupUser
// flow: it sends optional hintProject in cloudaicompanionProject and
// metadata.duetProject, checks ineligibleTiers when enrollment is blocked,
// polls GetOperation for pending onboarding, and falls back to hintProject or
// Cloud Resource Manager without ever returning a blank project as success.
func (s *GeminiAuthorization) discoverCodeAssistProjectAndTier(
	ctx context.Context,
	accessToken, proxyURL, hintProject string,
) (projectID string, tierID string, unrecoverable bool, err error) {
	if s.CodeAssist == nil {
		if strings.TrimSpace(hintProject) != "" {
			return strings.TrimSpace(hintProject), GeminiTierGCPStandard, false, nil
		}
		return "", "", false, errors.New("code assist client not configured")
	}

	hintProject = strings.TrimSpace(hintProject)
	loadReq := &geminiwire.LoadCodeAssistRequest{
		CloudAICompanionProject: hintProject,
		Metadata: geminiwire.LoadCodeAssistMetadata{
			IDEType:     "IDE_UNSPECIFIED",
			Platform:    "PLATFORM_UNSPECIFIED",
			PluginType:  "GEMINI",
			DuetProject: hintProject,
		},
	}

	loadResp, loadErr := s.CodeAssist.LoadCodeAssist(ctx, accessToken, proxyURL, loadReq)
	if loadErr != nil && isServiceDisabledGuidance(loadErr) {
		// Propagate Cloud Console API activation links directly while keeping the
		// cached session token alive for post-activation retry.
		return "", "", false, loadErr
	}

	var registeredTier string
	candidateTier := "legacy-tier"
	if loadResp != nil {
		registeredTier = strings.TrimSpace(loadResp.GetTier())
		if registeredTier != "" {
			candidateTier = registeredTier
		} else if allowed := ExtractTierIDFromAllowedTiers(loadResp.AllowedTiers); allowed != "" && allowed != "legacy-tier" {
			candidateTier = allowed
		}
	}

	if loadErr == nil && loadResp != nil && strings.TrimSpace(loadResp.CloudAICompanionProject) != "" {
		return strings.TrimSpace(loadResp.CloudAICompanionProject), candidateTier, false, nil
	}

	// Surface explicit upstream eligibility blocks before attempting onboarding
	// when upstream returned neither a registered tier nor any allowed onboarding tier.
	if loadResp != nil && registeredTier == "" && len(loadResp.AllowedTiers) == 0 && len(loadResp.IneligibleTiers) > 0 {
		diagErr, fatal := formatCodeAssistIneligibleError(loadResp.IneligibleTiers)
		return "", candidateTier, fatal, diagErr
	}

	// Already-registered accounts without a companion project require a user GCP
	// project (or an ACTIVE Resource Manager project).
	if registeredTier != "" {
		if loadErr == nil && hintProject != "" {
			return hintProject, registeredTier, false, nil
		}
		if fallback := s.tryFetchResourceManagerProject(ctx, accessToken, proxyURL); fallback != "" {
			return fallback, registeredTier, false, nil
		}
		if loadResp != nil && len(loadResp.IneligibleTiers) > 0 {
			diagErr, fatal := formatCodeAssistIneligibleError(loadResp.IneligibleTiers)
			return "", registeredTier, fatal, diagErr
		}
		return "", registeredTier, false, fmt.Errorf("account is enrolled in Code Assist (tier: %s) without a managed project_id: enter your GCP Project ID in the Project ID field above and click Complete Auth again (no re-login needed), or create an active project at https://console.cloud.google.com", registeredTier)
	}

	// Unregistered accounts proceed through onboardUser and GetOperation polling.
	onboardReq := &geminiwire.OnboardUserRequest{
		TierID: candidateTier,
		Metadata: geminiwire.LoadCodeAssistMetadata{
			IDEType:     "IDE_UNSPECIFIED",
			Platform:    "PLATFORM_UNSPECIFIED",
			PluginType:  "GEMINI",
			DuetProject: hintProject,
		},
	}
	if hintProject != "" && !strings.EqualFold(candidateTier, "free-tier") {
		onboardReq.CloudAICompanionProject = hintProject
	}

	pollInterval := s.Options.OnboardPollInterval
	if pollInterval <= 0 {
		pollInterval = defaultGeminiOnboardPollInterval
	}

	const maxAttempts = 5
	var pendingOpName string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var resp *geminiwire.OnboardUserResponse
		var opErr error
		if attempt == 1 || pendingOpName == "" {
			resp, opErr = s.CodeAssist.OnboardUser(ctx, accessToken, proxyURL, onboardReq)
		} else {
			resp, opErr = s.CodeAssist.GetOperation(ctx, accessToken, proxyURL, pendingOpName)
		}
		if opErr != nil {
			if isServiceDisabledGuidance(opErr) {
				return "", candidateTier, false, opErr
			}
			if loadErr == nil && hintProject != "" {
				return hintProject, candidateTier, false, nil
			}
			if fallback := s.tryFetchResourceManagerProject(ctx, accessToken, proxyURL); fallback != "" {
				return fallback, candidateTier, false, nil
			}
			return "", candidateTier, false, opErr
		}

		if resp != nil && resp.Done {
			if managedProj := resp.ProjectID(); managedProj != "" {
				return managedProj, candidateTier, false, nil
			}
			if hintProject != "" {
				return hintProject, candidateTier, false, nil
			}
			if fallback := s.tryFetchResourceManagerProject(ctx, accessToken, proxyURL); fallback != "" {
				return fallback, candidateTier, false, nil
			}
			return "", candidateTier, false, errors.New("code assist onboarding completed without a managed project_id: if this is a GCP/Workspace account, enter your GCP Project ID above and click Complete Auth again (no re-login needed); if this is a personal or Google One account, use the Antigravity platform instead")
		}

		if resp != nil && strings.TrimSpace(resp.Name) != "" {
			pendingOpName = strings.TrimSpace(resp.Name)
		}

		timer := time.NewTimer(pollInterval)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return "", candidateTier, false, ctx.Err()
		}
	}

	if hintProject != "" && loadErr == nil {
		return hintProject, candidateTier, false, nil
	}
	if fallback := s.tryFetchResourceManagerProject(ctx, accessToken, proxyURL); fallback != "" {
		return fallback, candidateTier, false, nil
	}
	if loadErr != nil {
		return "", candidateTier, false, fmt.Errorf("loadCodeAssist failed (%v) and onboardUser timed out after %d attempts", loadErr, maxAttempts)
	}
	return "", candidateTier, false, fmt.Errorf("onboardUser timed out after %d attempts: enter a GCP Project ID above and click Complete Auth again", maxAttempts)
}

// tryFetchResourceManagerProject returns a trimmed ACTIVE GCP project ID from
// FetchProject or an empty string when unavailable.
func (s *GeminiAuthorization) tryFetchResourceManagerProject(ctx context.Context, accessToken, proxyURL string) string {
	if s.Options.FetchProject == nil {
		return ""
	}
	fallback, err := s.Options.FetchProject(ctx, accessToken, proxyURL)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(fallback)
}

// formatCodeAssistIneligibleError converts upstream ineligibleTiers into an
// operator-facing diagnostic and reports whether the session is unrecoverable.
func formatCodeAssistIneligibleError(tiers []geminiwire.IneligibleTier) (error, bool) {
	for _, tier := range tiers {
		code := strings.ToUpper(strings.TrimSpace(tier.ReasonCode))
		msg := strings.TrimSpace(tier.ReasonMessage)
		if msg == "" {
			msg = strings.TrimSpace(tier.ValidationErrorMessage)
		}
		switch code {
		case "UNSUPPORTED_CLIENT", "SUBSCRIPTION_REQUIRED":
			if msg != "" {
				return fmt.Errorf("personal and Google One accounts are not supported on Gemini CLI OAuth (%s: %s); please add this account using the Antigravity platform instead", code, msg), true
			}
			return errors.New("personal and Google One accounts are not supported on Gemini CLI OAuth; please add this account using the Antigravity platform instead"), true
		case "TOS_VIOLATION":
			if msg != "" {
				return fmt.Errorf("account rejected by Code Assist (%s): %s", code, msg), true
			}
			return errors.New("account rejected by Code Assist due to a terms-of-service violation"), true
		case "VALIDATION_REQUIRED":
			validationURL := strings.TrimSpace(tier.ValidationURL)
			if validationURL != "" {
				return fmt.Errorf("account verification required (%s): complete verification at %s and then click Complete Auth again (no re-login needed)", firstNonEmptyText(msg, code), validationURL), false
			}
			return fmt.Errorf("account verification required (%s): complete verification in your Google account and click Complete Auth again", firstNonEmptyText(msg, code)), false
		}
	}

	first := tiers[0]
	reason := firstNonEmptyText(strings.TrimSpace(first.ReasonMessage), strings.TrimSpace(first.ValidationErrorMessage), strings.TrimSpace(first.ReasonCode), "account ineligible for Code Assist")
	return fmt.Errorf("account eligibility rejected by Code Assist: %s (for personal or Google One accounts, use the Antigravity platform instead)", reason), false
}

// isServiceDisabledGuidance reports whether err carries the formatted Cloud
// Console API-activation prompt produced by codeassist.Client.
func isServiceDisabledGuidance(err error) bool {
	return err != nil && strings.Contains(err.Error(), "gemini API not enabled for this project")
}

// firstNonEmptyText returns the first nonblank string in values or empty.
func firstNonEmptyText(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// logf invokes Options.Logf when configured.
func (s *GeminiAuthorization) logf(format string, args ...any) {
	if s.Options.Logf != nil {
		s.Options.Logf(format, args...)
	}
}

// printf invokes Options.Printf when configured.
func (s *GeminiAuthorization) printf(format string, args ...any) {
	if s.Options.Printf != nil {
		s.Options.Printf(format, args...)
	}
}

// RefreshToken registers renewal separately with authorization activity and
// releases it after refreshToken returns.
func (s *GeminiAuthorization) RefreshToken(ctx context.Context, kind, token, proxy string) (*GeminiTokenInfo, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("gemini authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	return s.refreshToken(operation, kind, token, proxy)
}

// FetchProject registers project discovery for shutdown waiting using the
// supplied token and proxy.
func (s *GeminiAuthorization) FetchProject(ctx context.Context, token, proxy string) (string, string, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("gemini authorization is stopped"))
	if err != nil {
		return "", "", err
	}
	defer done()
	return s.fetchProjectID(operation, token, proxy)
}
