// Gemini 授权、账号发现与凭据投影归账号；平台网络交换通过窄端口注入。
package account

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	geminiwire "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

type GeminiOAuthClient interface {
	ExchangeCode(context.Context, string, string, string, string, string) (*google.TokenResponse, error)
	RefreshToken(context.Context, string, string, string) (*google.TokenResponse, error)
}
type GeminiCodeAssistClient interface {
	LoadCodeAssist(context.Context, string, string, *geminiwire.LoadCodeAssistRequest) (*geminiwire.LoadCodeAssistResponse, error)
	OnboardUser(context.Context, string, string, *geminiwire.OnboardUserRequest) (*geminiwire.OnboardUserResponse, error)
}
type GeminiDriveClient interface {
	GetStorageQuota(context.Context, string, string) (*google.DriveStorageInfo, error)
}
type GeminiAuthorizationOptions struct {
	Config                                                 func() google.OAuthConfig
	BuiltinClientID, CLIRedirectURI, AIStudioRedirectURI   string
	GenerateState, GenerateCodeVerifier, GenerateSessionID func() (string, error)
	GenerateCodeChallenge                                  func(string) string
	EffectiveOAuthConfig                                   func(google.OAuthConfig, string) (google.OAuthConfig, error)
	BuildAuthorizationURL                                  func(google.OAuthConfig, string, string, string, string, string) (string, error)
	ResolveProxy                                           func(context.Context, int64) (string, bool)
	FetchProject                                           func(context.Context, string, string) (string, error)
	Logf, Printf                                           func(string, ...any)
}

// @project-doc docs/interfaces/gemini_upstream.md#gemini_native_execution
type GeminiAuthorization struct {
	Store      *GeminiAuthorizationSessions
	Client     GeminiOAuthClient
	CodeAssist GeminiCodeAssistClient
	Drive      GeminiDriveClient
	Options    GeminiAuthorizationOptions
	activity   operationActivity
}

func NewGeminiAuthorization(client GeminiOAuthClient, codeassist GeminiCodeAssistClient, drive GeminiDriveClient, options GeminiAuthorizationOptions) *GeminiAuthorization {
	return &GeminiAuthorization{Store: NewGeminiAuthorizationSessions(), Client: client, CodeAssist: codeassist, Drive: drive, Options: options}
}
func (s *GeminiAuthorization) Start() { s.Store.Start() }
func (s *GeminiAuthorization) StopContext(ctx context.Context) error {
	s.Store.Stop()
	return s.activity.stop(ctx, "gemini authorization")
}

const (
	GB = 1024 * 1024 * 1024
	TB = 1024 * GB

	StorageTierUnlimited = 100 * TB // 100TB
	StorageTierAIPremium = 2 * TB   // 2TB
	StorageTierStandard  = 200 * GB // 200GB
	StorageTierBasic     = 100 * GB // 100GB
	StorageTierFree      = 15 * GB  // 15GB
)

type GeminiOAuthCapabilities struct {
	AIStudioOAuthEnabled bool     `json:"ai_studio_oauth_enabled"`
	RequiredRedirectURIs []string `json:"required_redirect_uris"`
}

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

type GeminiAuthURLResult struct {
	AuthURL   string `json:"auth_url"`
	SessionID string `json:"session_id"`
	State     string `json:"state"`
}

func (s *GeminiAuthorization) GenerateAuthURL(ctx context.Context, proxyID *int64, redirectURI, projectID, oauthType, tierID string) (*GeminiAuthURLResult, error) {
	operation, done, startErr := s.activity.begin(ctx, errors.New("gemini authorization is stopped"))
	if startErr != nil {
		return nil, startErr
	}
	defer done()
	ctx = operation

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
	if proxyID != nil {
		if resolved, ok := s.Options.ResolveProxy(ctx, *proxyID); ok {
			proxyURL = resolved
		}
	}

	// OAuth client selection:
	// - code_assist: always use built-in Gemini CLI OAuth client (public)
	// - google_one: always use built-in Gemini CLI OAuth client (public)
	// - ai_studio: requires a user-provided OAuth client
	oauthCfg := s.Options.Config()
	if oauthType == "code_assist" || oauthType == "google_one" {
		// Force use of built-in Gemini CLI OAuth client
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

	// AI Studio OAuth requires a user-provided OAuth client (built-in Gemini CLI client is scope-restricted).
	if oauthType == "ai_studio" && isBuiltinClient {
		return nil, fmt.Errorf("AI Studio OAuth requires a custom OAuth Client (GEMINI_OAUTH_CLIENT_ID / GEMINI_OAUTH_CLIENT_SECRET). If you don't want to configure an OAuth client, please use an AI Studio API Key account instead")
	}

	// Redirect URI strategy:
	// - built-in Gemini CLI OAuth client: use upstream redirect URI (codeassist.google.com/authcode)
	// - custom OAuth client: use localhost callback for manual copy/paste flow
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

type GeminiExchangeCodeInput struct {
	SessionID string
	State     string
	Code      string
	ProxyID   *int64
	OAuthType string // "code_assist" 或 "ai_studio"
	// TierID is a user-selected tier to be used when auto detection is unavailable or fails.
	// If empty, the service will fall back to the tier stored in the OAuth session (if any).
	TierID string
}
type GeminiTokenInfo struct {
	AccessToken  string         `json:"access_token"`
	RefreshToken string         `json:"refresh_token"`
	ExpiresIn    int64          `json:"expires_in"`
	ExpiresAt    int64          `json:"expires_at"`
	TokenType    string         `json:"token_type"`
	Scope        string         `json:"scope,omitempty"`
	ProjectID    string         `json:"project_id,omitempty"`
	OAuthType    string         `json:"oauth_type,omitempty"` // "code_assist" 或 "ai_studio"
	TierID       string         `json:"tier_id,omitempty"`    // Canonical tier id (e.g. google_one_free, gcp_standard, aistudio_free)
	Extra        map[string]any `json:"extra,omitempty"`      // Drive metadata
}

// ValidateTierID validates tier_id format and length
func ValidateTierID(tierID string) error {
	if tierID == "" {
		return nil // Empty is allowed
	}
	if len(tierID) > 64 {
		return fmt.Errorf("tier_id exceeds maximum length of 64 characters")
	}
	// Allow alphanumeric, underscore, hyphen, and slash (for tier paths)
	if !regexp.MustCompile(`^[a-zA-Z0-9_/-]+$`).MatchString(tierID) {
		return fmt.Errorf("tier_id contains invalid characters")
	}
	return nil
}

// ExtractTierIDFromAllowedTiers extracts tierID from LoadCodeAssist response
// Prioritizes IsDefault tier, falls back to first non-empty tier
func ExtractTierIDFromAllowedTiers(allowedTiers []geminiwire.AllowedTier) string {
	tierID := "LEGACY"
	// First pass: look for default tier
	for _, tier := range allowedTiers {
		if tier.IsDefault && strings.TrimSpace(tier.ID) != "" {
			tierID = strings.TrimSpace(tier.ID)
			break
		}
	}
	// Second pass: if still LEGACY, take first non-empty tier
	if tierID == "LEGACY" {
		for _, tier := range allowedTiers {
			if strings.TrimSpace(tier.ID) != "" {
				tierID = strings.TrimSpace(tier.ID)
				break
			}
		}
	}
	return tierID
}

// inferGoogleOneTier infers Google One tier from Drive storage limit
func InferGoogleOneTier(storageBytes int64, logf func(string, ...any)) string {
	logf("[GeminiOAuth] inferGoogleOneTier - input: %d bytes (%.2f TB)", storageBytes, float64(storageBytes)/float64(TB))

	if storageBytes <= 0 {
		logf("[GeminiOAuth] inferGoogleOneTier - storageBytes <= 0, returning UNKNOWN")
		return GeminiTierGoogleOneUnknown
	}

	if storageBytes > StorageTierUnlimited {
		logf("[GeminiOAuth] inferGoogleOneTier - > %d bytes (100TB), returning UNLIMITED", StorageTierUnlimited)
		return GeminiTierGoogleAIUltra
	}
	if storageBytes >= StorageTierAIPremium {
		logf("[GeminiOAuth] inferGoogleOneTier - >= %d bytes (2TB), returning google_ai_pro", StorageTierAIPremium)
		return GeminiTierGoogleAIPro
	}
	if storageBytes >= StorageTierFree {
		logf("[GeminiOAuth] inferGoogleOneTier - >= %d bytes (15GB), returning FREE", StorageTierFree)
		return GeminiTierGoogleOneFree
	}

	logf("[GeminiOAuth] inferGoogleOneTier - < %d bytes (15GB), returning UNKNOWN", StorageTierFree)
	return GeminiTierGoogleOneUnknown
}

// FetchGoogleOneTier fetches Google One tier from Drive API.
// Note: LoadCodeAssist API is NOT called for Google One accounts because:
// 1. It's designed for GCP IAM (enterprise), not personal Google accounts
// 2. Personal accounts will get 403/404 from cloudaicompanion.googleapis.com
// 3. Google consumer (Google One) and enterprise (GCP) systems are physically isolated
func (s *GeminiAuthorization) fetchGoogleOneTier(ctx context.Context, accessToken, proxyURL string) (string, *google.DriveStorageInfo, error) {
	s.Options.Logf("[GeminiOAuth] Starting FetchGoogleOneTier (Google One personal account)")

	// Use Drive API to infer tier from storage quota (requires drive.readonly scope)
	s.Options.Logf("[GeminiOAuth] Calling Drive API for storage quota...")

	storageInfo, err := s.Drive.GetStorageQuota(ctx, accessToken, proxyURL)
	if err != nil {
		// Check if it's a 403 (scope not granted)
		if strings.Contains(err.Error(), "status 403") {
			s.Options.Logf("[GeminiOAuth] Drive API scope not available (403): %v", err)
			return GeminiTierGoogleOneUnknown, nil, err
		}
		// Other errors
		s.Options.Logf("[GeminiOAuth] Failed to fetch Drive storage: %v", err)
		return GeminiTierGoogleOneUnknown, nil, err
	}

	s.Options.Logf("[GeminiOAuth] Drive API response - Limit: %d bytes (%.2f TB), Usage: %d bytes (%.2f GB)",
		storageInfo.Limit, float64(storageInfo.Limit)/float64(TB),
		storageInfo.Usage, float64(storageInfo.Usage)/float64(GB))

	tierID := InferGoogleOneTier(storageInfo.Limit, s.Options.Logf)
	s.Options.Logf("[GeminiOAuth] Inferred tier from storage: %s", tierID)

	return tierID, storageInfo, nil
}

// RefreshAccountGoogleOneTier 刷新单个账号的 Google One Tier
func (s *GeminiAuthorization) RefreshAccountGoogleOneTier(
	ctx context.Context,
	account *Record,
) (tierID string, extra map[string]any, credentials map[string]any, err error) {
	operation, done, startErr := s.activity.begin(ctx, errors.New("gemini authorization is stopped"))
	if startErr != nil {
		return "", nil, nil, startErr
	}
	defer done()
	ctx = operation

	if account == nil {
		return "", nil, nil, fmt.Errorf("account is nil")
	}

	// 验证账号类型
	oauthType, ok := account.Credentials["oauth_type"].(string)
	if !ok || oauthType != "google_one" {
		return "", nil, nil, fmt.Errorf("not a google_one OAuth account")
	}

	// 获取 access_token
	accessToken, ok := account.Credentials["access_token"].(string)
	if !ok || accessToken == "" {
		return "", nil, nil, fmt.Errorf("missing access_token")
	}

	// 获取 proxy URL
	var proxyURL string
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	// 调用 Drive API
	tierID, storageInfo, err := s.fetchGoogleOneTier(ctx, accessToken, proxyURL)
	if err != nil {
		return "", nil, nil, err
	}

	observation := GoogleOneTierObservation{TierID: tierID}
	if storageInfo != nil {
		observation.Storage = &GoogleOneStorage{Limit: storageInfo.Limit, Usage: storageInfo.Usage}
		observation.ObservedAt = time.Now()
	}
	extra, credentials = ProjectGoogleOneTier(account, observation)
	return tierID, extra, credentials, nil
}
func (s *GeminiAuthorization) ExchangeCode(ctx context.Context, input *GeminiExchangeCodeInput) (*GeminiTokenInfo, error) {
	operation, done, startErr := s.activity.begin(ctx, errors.New("gemini authorization is stopped"))
	if startErr != nil {
		return nil, startErr
	}
	defer done()
	ctx = operation

	s.Options.Logf("[GeminiOAuth] ========== ExchangeCode START ==========")
	s.Options.Logf("[GeminiOAuth] SessionID: %s", input.SessionID)

	session, ok := s.Store.Get(input.SessionID)
	if !ok {
		s.Options.Logf("[GeminiOAuth] ERROR: Session not found or expired")
		return nil, fmt.Errorf("session not found or expired")
	}
	if strings.TrimSpace(input.State) == "" || input.State != session.State {
		s.Options.Logf("[GeminiOAuth] ERROR: Invalid state")
		return nil, fmt.Errorf("invalid state")
	}

	proxyURL := session.ProxyURL
	if input.ProxyID != nil {
		if resolved, ok := s.Options.ResolveProxy(ctx, *input.ProxyID); ok {
			proxyURL = resolved
		}
	}
	s.Options.Logf("[GeminiOAuth] ProxyURL: %s", proxyURL)

	redirectURI := session.RedirectURI

	// Resolve oauth_type early (defaults to code_assist for backward compatibility).
	oauthType := session.OAuthType
	if oauthType == "" {
		oauthType = "code_assist"
	}
	s.Options.Logf("[GeminiOAuth] OAuth Type: %s", oauthType)
	s.Options.Logf("[GeminiOAuth] Project ID from session: %s", session.ProjectID)

	// If the session was created for AI Studio OAuth, ensure a custom OAuth client is configured.
	if oauthType == "ai_studio" {
		effectiveCfg, err := s.Options.EffectiveOAuthConfig(s.Options.Config(), "ai_studio")
		if err != nil {
			return nil, err
		}
		isBuiltinClient := effectiveCfg.ClientID == s.Options.BuiltinClientID
		if isBuiltinClient {
			return nil, fmt.Errorf("AI Studio OAuth requires a custom OAuth Client. Please use an AI Studio API Key account, or configure GEMINI_OAUTH_CLIENT_ID / GEMINI_OAUTH_CLIENT_SECRET and re-authorize")
		}
	}

	// code_assist/google_one always uses the built-in client and its fixed redirect URI.
	if oauthType == "code_assist" || oauthType == "google_one" {
		redirectURI = s.Options.CLIRedirectURI
	}

	tokenResp, err := s.Client.ExchangeCode(ctx, oauthType, input.Code, session.CodeVerifier, redirectURI, proxyURL)
	if err != nil {
		s.Options.Logf("[GeminiOAuth] ERROR: Failed to exchange code: %v", err)
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	s.Options.Logf("[GeminiOAuth] Token exchange successful")
	s.Options.Logf("[GeminiOAuth] Token scope: %s", tokenResp.Scope)
	s.Options.Logf("[GeminiOAuth] Token expires_in: %d seconds", tokenResp.ExpiresIn)

	sessionProjectID := strings.TrimSpace(session.ProjectID)
	s.Store.Delete(input.SessionID)

	// 计算过期时间：减去 5 分钟安全时间窗口（考虑网络延迟和时钟偏差）
	// 同时设置下界保护，防止 expires_in 过小导致过去时间（引发刷新风暴）
	const safetyWindow = 300 // 5 minutes
	const minTTL = 30        // minimum 30 seconds
	expiresAt := time.Now().Unix() + tokenResp.ExpiresIn - safetyWindow
	minExpiresAt := time.Now().Unix() + minTTL
	if expiresAt < minExpiresAt {
		expiresAt = minExpiresAt
	}

	projectID := sessionProjectID
	var tierID string
	fallbackTierID := CanonicalGeminiTierIDForOAuthType(oauthType, input.TierID)
	if fallbackTierID == "" {
		fallbackTierID = CanonicalGeminiTierIDForOAuthType(oauthType, session.TierID)
	}

	s.Options.Logf("[GeminiOAuth] ========== Account Type Detection START ==========")
	s.Options.Logf("[GeminiOAuth] OAuth Type: %s", oauthType)

	// 对于 code_assist 模式，project_id 是必需的，需要调用 Code Assist API
	// 对于 google_one 模式，使用个人 Google 账号，不需要 project_id，配额由 Google 网关自动识别
	// 对于 ai_studio 模式，project_id 是可选的（不影响使用 AI Studio API）
	switch oauthType {
	case "code_assist":
		s.Options.Logf("[GeminiOAuth] Processing code_assist OAuth type")
		if projectID == "" {
			s.Options.Logf("[GeminiOAuth] No project_id provided, attempting to fetch from LoadCodeAssist API...")
			var err error
			projectID, tierID, err = s.fetchProjectID(ctx, tokenResp.AccessToken, proxyURL)
			if err != nil {
				// 记录警告但不阻断流程，允许后续补充 project_id
				s.Options.Printf("[GeminiOAuth] Warning: Failed to fetch project_id during token exchange: %v\n", err)
				s.Options.Logf("[GeminiOAuth] WARNING: Failed to fetch project_id: %v", err)
			} else {
				s.Options.Logf("[GeminiOAuth] Successfully fetched project_id: %s, tier_id: %s", projectID, tierID)
			}
		} else {
			s.Options.Logf("[GeminiOAuth] User provided project_id: %s, fetching tier_id...", projectID)
			// 用户手动填了 project_id，仍需调用 LoadCodeAssist 获取 tierID
			_, fetchedTierID, err := s.fetchProjectID(ctx, tokenResp.AccessToken, proxyURL)
			if err != nil {
				s.Options.Printf("[GeminiOAuth] Warning: Failed to fetch tierID: %v\n", err)
				s.Options.Logf("[GeminiOAuth] WARNING: Failed to fetch tier_id: %v", err)
			} else {
				tierID = fetchedTierID
				s.Options.Logf("[GeminiOAuth] Successfully fetched tier_id: %s", tierID)
			}
		}
		if strings.TrimSpace(projectID) == "" {
			s.Options.Logf("[GeminiOAuth] ERROR: Missing project_id for Code Assist OAuth")
			return nil, fmt.Errorf("missing project_id for Code Assist OAuth: please fill Project ID (optional field) and regenerate the auth URL, or ensure your Google account has an ACTIVE GCP project")
		}
		// Prefer auto-detected tier; fall back to user-selected tier.
		tierID = CanonicalGeminiTierIDForOAuthType(oauthType, tierID)
		if tierID == "" {
			if fallbackTierID != "" {
				tierID = fallbackTierID
				s.Options.Logf("[GeminiOAuth] Using fallback tier_id from user/session: %s", tierID)
			} else {
				tierID = GeminiTierGCPStandard
				s.Options.Logf("[GeminiOAuth] Using default tier_id: %s", tierID)
			}
		}
		s.Options.Logf("[GeminiOAuth] Final code_assist result - project_id: %s, tier_id: %s", projectID, tierID)

	case "google_one":
		s.Options.Logf("[GeminiOAuth] Processing google_one OAuth type")

		// Google One accounts use cloudaicompanion API, which requires a project_id.
		// For personal accounts, Google auto-assigns a project_id via the LoadCodeAssist API.
		if projectID == "" {
			s.Options.Logf("[GeminiOAuth] No project_id provided, attempting to fetch from LoadCodeAssist API...")
			var err error
			projectID, _, err = s.fetchProjectID(ctx, tokenResp.AccessToken, proxyURL)
			if err != nil {
				s.Options.Logf("[GeminiOAuth] ERROR: Failed to fetch project_id: %v", err)
				return nil, fmt.Errorf("google One accounts require a project_id, failed to auto-detect: %w", err)
			}
			s.Options.Logf("[GeminiOAuth] Successfully fetched project_id: %s", projectID)
		}

		s.Options.Logf("[GeminiOAuth] Attempting to fetch Google One tier from Drive API...")
		// Attempt to fetch Drive storage tier
		var storageInfo *google.DriveStorageInfo
		var err error
		tierID, storageInfo, err = s.fetchGoogleOneTier(ctx, tokenResp.AccessToken, proxyURL)
		if err != nil {
			// Log warning but don't block - use fallback
			s.Options.Printf("[GeminiOAuth] Warning: Failed to fetch Drive tier: %v\n", err)
			s.Options.Logf("[GeminiOAuth] WARNING: Failed to fetch Drive tier: %v", err)
			tierID = ""
		} else {
			s.Options.Logf("[GeminiOAuth] Successfully fetched Drive tier: %s", tierID)
			if storageInfo != nil {
				s.Options.Logf("[GeminiOAuth] Drive storage - Limit: %d bytes (%.2f TB), Usage: %d bytes (%.2f GB)",
					storageInfo.Limit, float64(storageInfo.Limit)/float64(TB),
					storageInfo.Usage, float64(storageInfo.Usage)/float64(GB))
			}
		}
		tierID = CanonicalGeminiTierIDForOAuthType(oauthType, tierID)
		if tierID == "" || tierID == GeminiTierGoogleOneUnknown {
			if fallbackTierID != "" {
				tierID = fallbackTierID
				s.Options.Logf("[GeminiOAuth] Using fallback tier_id from user/session: %s", tierID)
			} else {
				tierID = GeminiTierGoogleOneFree
				s.Options.Logf("[GeminiOAuth] Using default tier_id: %s", tierID)
			}
		}
		s.Options.Printf("[GeminiOAuth] Google One tierID after normalization: %s\n", tierID)

		// Store Drive info in extra field for caching
		if storageInfo != nil {
			tokenInfo := &GeminiTokenInfo{
				AccessToken:  tokenResp.AccessToken,
				RefreshToken: tokenResp.RefreshToken,
				TokenType:    tokenResp.TokenType,
				ExpiresIn:    tokenResp.ExpiresIn,
				ExpiresAt:    expiresAt,
				Scope:        tokenResp.Scope,
				ProjectID:    projectID,
				TierID:       tierID,
				OAuthType:    oauthType,
				Extra: map[string]any{
					"drive_storage_limit":   storageInfo.Limit,
					"drive_storage_usage":   storageInfo.Usage,
					"drive_tier_updated_at": time.Now().Format(time.RFC3339),
				},
			}
			s.Options.Logf("[GeminiOAuth] ========== ExchangeCode END (google_one with storage info) ==========")
			return tokenInfo, nil
		}

	case "ai_studio":
		// No automatic tier detection for AI Studio OAuth; rely on user selection.
		if fallbackTierID != "" {
			tierID = fallbackTierID
		} else {
			tierID = GeminiTierAIStudioFree
		}

	default:
		s.Options.Logf("[GeminiOAuth] Processing %s OAuth type (no tier detection)", oauthType)
	}

	s.Options.Logf("[GeminiOAuth] ========== Account Type Detection END ==========")

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
	s.Options.Logf("[GeminiOAuth] Final result - OAuth Type: %s, Project ID: %s, Tier ID: %s", result.OAuthType, result.ProjectID, result.TierID)
	s.Options.Logf("[GeminiOAuth] ========== ExchangeCode END ==========")
	return result, nil
}
func (s *GeminiAuthorization) refreshToken(ctx context.Context, oauthType, refreshToken, proxyURL string) (*GeminiTokenInfo, error) {
	var lastErr error

	for attempt := 0; attempt <= 3; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			time.Sleep(backoff)
		}

		tokenResp, err := s.Client.RefreshToken(ctx, oauthType, refreshToken, proxyURL)
		if err == nil {
			// 计算过期时间：减去 5 分钟安全时间窗口（考虑网络延迟和时钟偏差）
			// 同时设置下界保护，防止 expires_in 过小导致过去时间（引发刷新风暴）
			const safetyWindow = 300 // 5 minutes
			const minTTL = 30        // minimum 30 seconds
			expiresAt := time.Now().Unix() + tokenResp.ExpiresIn - safetyWindow
			minExpiresAt := time.Now().Unix() + minTTL
			if expiresAt < minExpiresAt {
				expiresAt = minExpiresAt
			}
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
func (s *GeminiAuthorization) RefreshAccountToken(ctx context.Context, account *Record) (*GeminiTokenInfo, error) {
	operation, done, startErr := s.activity.begin(ctx, errors.New("gemini authorization is stopped"))
	if startErr != nil {
		return nil, startErr
	}
	defer done()
	ctx = operation

	if account.Platform != PlatformGemini || account.Type != AccountTypeOAuth {
		return nil, fmt.Errorf("account is not a Gemini OAuth account")
	}

	refreshToken := account.GetCredential("refresh_token")
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("no refresh token available")
	}

	// Preserve oauth_type from the account (defaults to code_assist for backward compatibility).
	oauthType := strings.TrimSpace(account.GetCredential("oauth_type"))
	if oauthType == "" {
		oauthType = "code_assist"
	}

	var proxyURL string
	if account.ProxyID != nil {
		if resolved, ok := s.Options.ResolveProxy(ctx, *account.ProxyID); ok {
			proxyURL = resolved
		}
	}

	tokenInfo, err := s.refreshToken(ctx, oauthType, refreshToken, proxyURL)
	// Backward compatibility:
	// Older versions could refresh Code Assist tokens using a user-provided OAuth client when configured.
	// If the refresh token was originally issued to that custom client, forcing the built-in client will
	// fail with "unauthorized_client". In that case, retry with the custom client (ai_studio path) when available.
	if err != nil && oauthType == "code_assist" && strings.Contains(err.Error(), "unauthorized_client") && s.GetOAuthConfig().AIStudioOAuthEnabled {
		if alt, altErr := s.refreshToken(ctx, "ai_studio", refreshToken, proxyURL); altErr == nil {
			tokenInfo = alt
			err = nil
		}
	}
	// Backward compatibility for google_one:
	// - New behavior: when a custom OAuth client is configured, google_one will use it.
	// - Old behavior: google_one always used the built-in Gemini CLI OAuth client.
	// If an existing account was authorized with the built-in client, refreshing with the custom client
	// will fail with "unauthorized_client". Retry with the built-in client (code_assist path forces it).
	if err != nil && oauthType == "google_one" && strings.Contains(err.Error(), "unauthorized_client") && s.GetOAuthConfig().AIStudioOAuthEnabled {
		if alt, altErr := s.refreshToken(ctx, "code_assist", refreshToken, proxyURL); altErr == nil {
			tokenInfo = alt
			err = nil
		}
	}
	if err != nil {
		// Provide a more actionable error for common OAuth client mismatch issues.
		if strings.Contains(err.Error(), "unauthorized_client") {
			return nil, fmt.Errorf("%w (OAuth client mismatch: the refresh_token is bound to the OAuth client used during authorization; please re-authorize this account or restore the original GEMINI_OAUTH_CLIENT_ID/SECRET)", err)
		}
		return nil, err
	}

	tokenInfo.OAuthType = oauthType

	// Preserve account's project_id when present.
	existingProjectID := strings.TrimSpace(account.GetCredential("project_id"))
	if existingProjectID != "" {
		tokenInfo.ProjectID = existingProjectID
	}

	// 尝试从账号凭证获取 tierID（向后兼容）
	existingTierID := strings.TrimSpace(account.GetCredential("tier_id"))

	// For Code Assist, project_id is required. Auto-detect if missing.
	// For AI Studio OAuth, project_id is optional and should not block refresh.
	switch oauthType {
	case "code_assist":
		// 先设置默认值或保留旧值，确保 tier_id 始终有值
		if existingTierID != "" {
			tokenInfo.TierID = CanonicalGeminiTierIDForOAuthType(oauthType, existingTierID)
		}
		if tokenInfo.TierID == "" {
			tokenInfo.TierID = GeminiTierGCPStandard
		}

		// 尝试自动探测 project_id 和 tier_id
		needDetect := strings.TrimSpace(tokenInfo.ProjectID) == "" || tokenInfo.TierID == ""
		if needDetect {
			projectID, tierID, err := s.fetchProjectID(ctx, tokenInfo.AccessToken, proxyURL)
			if err != nil {
				s.Options.Printf("[GeminiOAuth] Warning: failed to auto-detect project/tier: %v\n", err)
			} else {
				if strings.TrimSpace(tokenInfo.ProjectID) == "" && projectID != "" {
					tokenInfo.ProjectID = projectID
				}
				if tierID != "" {
					if canonical := CanonicalGeminiTierIDForOAuthType(oauthType, tierID); canonical != "" {
						tokenInfo.TierID = canonical
					}
				}
			}
		}

		if strings.TrimSpace(tokenInfo.ProjectID) == "" {
			return nil, fmt.Errorf("failed to auto-detect project_id: empty result")
		}
	case "google_one":
		canonicalExistingTier := CanonicalGeminiTierIDForOAuthType(oauthType, existingTierID)
		// Check if tier cache is stale (> 24 hours)
		needsRefresh := true
		if account.Extra != nil {
			if updatedAtStr, ok := account.Extra["drive_tier_updated_at"].(string); ok {
				if updatedAt, err := time.Parse(time.RFC3339, updatedAtStr); err == nil {
					if time.Since(updatedAt) <= 24*time.Hour {
						needsRefresh = false
						// Use cached tier
						tokenInfo.TierID = canonicalExistingTier
					}
				}
			}
		}

		if tokenInfo.TierID == "" {
			tokenInfo.TierID = canonicalExistingTier
		}

		if needsRefresh {
			tierID, storageInfo, err := s.fetchGoogleOneTier(ctx, tokenInfo.AccessToken, proxyURL)
			if err == nil {
				if canonical := CanonicalGeminiTierIDForOAuthType(oauthType, tierID); canonical != "" && canonical != GeminiTierGoogleOneUnknown {
					tokenInfo.TierID = canonical
				}
				if storageInfo != nil {
					tokenInfo.Extra = map[string]any{
						"drive_storage_limit":   storageInfo.Limit,
						"drive_storage_usage":   storageInfo.Usage,
						"drive_tier_updated_at": time.Now().Format(time.RFC3339),
					}
				}
			}
		}

		if tokenInfo.TierID == "" || tokenInfo.TierID == GeminiTierGoogleOneUnknown {
			if canonicalExistingTier != "" {
				tokenInfo.TierID = canonicalExistingTier
			} else {
				tokenInfo.TierID = GeminiTierGoogleOneFree
			}
		}
	}

	return tokenInfo, nil
}
func (s *GeminiAuthorization) BuildAccountCredentials(tokenInfo *GeminiTokenInfo) map[string]any {
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
		// Validate tier_id before storing
		if err := ValidateTierID(tokenInfo.TierID); err == nil {
			creds["tier_id"] = tokenInfo.TierID
			s.Options.Printf("[GeminiOAuth] Storing tier_id: %s\n", tokenInfo.TierID)
		} else {
			s.Options.Printf("[GeminiOAuth] Invalid tier_id %s: %v\n", tokenInfo.TierID, err)
		}
		// Silently skip invalid tier_id (don't block account creation)
	}
	if tokenInfo.OAuthType != "" {
		creds["oauth_type"] = tokenInfo.OAuthType
	}
	// Store extra metadata (Drive info) if present
	if len(tokenInfo.Extra) > 0 {
		for k, v := range tokenInfo.Extra {
			creds[k] = v
		}
	}
	return creds
}
func (s *GeminiAuthorization) fetchProjectID(ctx context.Context, accessToken, proxyURL string) (string, string, error) {
	if s.CodeAssist == nil {
		return "", "", errors.New("code assist client not configured")
	}

	loadResp, loadErr := s.CodeAssist.LoadCodeAssist(ctx, accessToken, proxyURL, nil)

	// Extract tierID from response (works whether CloudAICompanionProject is set or not)
	tierID := "LEGACY"
	if loadResp != nil {
		// First try to get tier from currentTier/paidTier fields
		if tier := loadResp.GetTier(); tier != "" {
			tierID = tier
		} else {
			// Fallback to extracting from allowedTiers
			tierID = ExtractTierIDFromAllowedTiers(loadResp.AllowedTiers)
		}
	}

	// If LoadCodeAssist returned a project, use it
	if loadErr == nil && loadResp != nil && strings.TrimSpace(loadResp.CloudAICompanionProject) != "" {
		return strings.TrimSpace(loadResp.CloudAICompanionProject), tierID, nil
	}

	// 关键逻辑：对齐 Gemini CLI 对“已注册用户”的处理方式。
	// 当 LoadCodeAssist 返回了 currentTier / paidTier（表示账号已注册）但没有返回 cloudaicompanionProject 时：
	// - 不要再调用 onboardUser（通常不会再分配 project_id，且可能触发 INVALID_ARGUMENT）
	// - 先尝试从 Cloud Resource Manager 获取可用项目；仍失败则提示用户手动填写 project_id
	if loadResp != nil {
		registeredTierID := strings.TrimSpace(loadResp.GetTier())
		if registeredTierID != "" {
			// 已注册但未返回 cloudaicompanionProject，这在 Google One 用户中较常见：需要用户自行提供 project_id。
			s.Options.Logf("[GeminiOAuth] User has tier (%s) but no cloudaicompanionProject, trying Cloud Resource Manager...", registeredTierID)

			// Try to get project from Cloud Resource Manager
			fallback, fbErr := s.Options.FetchProject(ctx, accessToken, proxyURL)
			if fbErr == nil && strings.TrimSpace(fallback) != "" {
				s.Options.Logf("[GeminiOAuth] Found project from Cloud Resource Manager: %s", fallback)
				return strings.TrimSpace(fallback), tierID, nil
			}

			// No project found - user must provide project_id manually
			s.Options.Logf("[GeminiOAuth] No project found from Cloud Resource Manager, user must provide project_id manually")
			return "", tierID, fmt.Errorf("user is registered (tier: %s) but no project_id available. Please provide Project ID manually in the authorization form, or create a project at https://console.cloud.google.com", registeredTierID)
		}
	}

	// 未检测到 currentTier/paidTier，视为新用户，继续调用 onboardUser
	s.Options.Logf("[GeminiOAuth] No currentTier/paidTier found, proceeding with onboardUser (tierID: %s)", tierID)

	req := &geminiwire.OnboardUserRequest{
		TierID: tierID,
		Metadata: geminiwire.LoadCodeAssistMetadata{
			IDEType:    "ANTIGRAVITY",
			Platform:   "PLATFORM_UNSPECIFIED",
			PluginType: "GEMINI",
		},
	}

	maxAttempts := 5
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		resp, err := s.CodeAssist.OnboardUser(ctx, accessToken, proxyURL, req)
		if err != nil {
			// If Code Assist onboarding fails (e.g. INVALID_ARGUMENT), fallback to Cloud Resource Manager projects.
			fallback, fbErr := s.Options.FetchProject(ctx, accessToken, proxyURL)
			if fbErr == nil && strings.TrimSpace(fallback) != "" {
				return strings.TrimSpace(fallback), tierID, nil
			}
			return "", tierID, err
		}
		if resp.Done {
			if resp.Response != nil && resp.Response.CloudAICompanionProject != nil {
				switch v := resp.Response.CloudAICompanionProject.(type) {
				case string:
					return strings.TrimSpace(v), tierID, nil
				case map[string]any:
					if id, ok := v["id"].(string); ok {
						return strings.TrimSpace(id), tierID, nil
					}
				}
			}

			fallback, fbErr := s.Options.FetchProject(ctx, accessToken, proxyURL)
			if fbErr == nil && strings.TrimSpace(fallback) != "" {
				return strings.TrimSpace(fallback), tierID, nil
			}
			return "", tierID, errors.New("onboardUser completed but no project_id returned")
		}
		time.Sleep(2 * time.Second)
	}

	fallback, fbErr := s.Options.FetchProject(ctx, accessToken, proxyURL)
	if fbErr == nil && strings.TrimSpace(fallback) != "" {
		return strings.TrimSpace(fallback), tierID, nil
	}
	if loadErr != nil {
		return "", tierID, fmt.Errorf("loadCodeAssist failed (%v) and onboardUser timeout after %d attempts", loadErr, maxAttempts)
	}
	return "", tierID, fmt.Errorf("onboardUser timeout after %d attempts", maxAttempts)
}

// 单独刷新和 Drive 查询也纳入统一停止等待；嵌套调用复用所属外层操作。
func (s *GeminiAuthorization) RefreshToken(ctx context.Context, kind, token, proxy string) (*GeminiTokenInfo, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("gemini authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	return s.refreshToken(operation, kind, token, proxy)
}
func (s *GeminiAuthorization) FetchGoogleOneTier(ctx context.Context, token, proxy string) (string, *google.DriveStorageInfo, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("gemini authorization is stopped"))
	if err != nil {
		return "", nil, err
	}
	defer done()
	return s.fetchGoogleOneTier(operation, token, proxy)
}

// FetchProject 供请求侧自动发现使用，仍由同一授权拥有者停止和等待。
func (s *GeminiAuthorization) FetchProject(ctx context.Context, token, proxy string) (string, string, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("gemini authorization is stopped"))
	if err != nil {
		return "", "", err
	}
	defer done()
	return s.fetchProjectID(operation, token, proxy)
}
