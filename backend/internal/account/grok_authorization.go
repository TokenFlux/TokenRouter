// Grok 的会话消费、授权校验和凭据投影归账号；供应商交换通过显式端口注入。
package account

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	wiregrok "github.com/TokenFlux/TokenRouter/internal/protocol/grok"
)

type GrokAuthorizationClient interface {
	ExchangeCode(context.Context, string, string, string, string, string) (*wiregrok.TokenResponse, error)
	RefreshToken(context.Context, string, string, string) (*wiregrok.TokenResponse, error)
	LoginWithPassword(context.Context, string, string, string) (*wiregrok.PasswordLoginResult, error)
	ConvertSSOToBuild(context.Context, string, string) (*wiregrok.TokenResponse, error)
}
type GrokAuthorizationOptions struct {
	PasswordAuthEnabled                                                   func() bool
	LookupProxy                                                           func(context.Context, int64) (string, bool, error)
	GenerateState, GenerateNonce, GenerateCodeVerifier, GenerateSessionID func() (string, error)
	EffectiveRedirectURI, GenerateCodeChallenge                           func(string) string
	BuildAuthorizationURL                                                 func(string, string, string, string) (string, error)
	EffectiveClientID, EffectiveScope                                     func() string
	ParseAuthorizationInput                                               func(string) wiregrok.AuthorizationInput
	DecodeJWTClaims                                                       func(string) map[string]any
	JWTClaimString                                                        func(map[string]any, string) string
	SubscriptionTierFromJWT                                               func(string) string
	DefaultClientID, DefaultCLIBaseURL                                    string
}
type GrokAuthorization struct {
	Store    *GrokSessionStore
	Client   GrokAuthorizationClient
	Options  GrokAuthorizationOptions
	activity operationActivity
}

func NewGrokAuthorization(client GrokAuthorizationClient, options GrokAuthorizationOptions) *GrokAuthorization {
	return &GrokAuthorization{Store: NewGrokSessionStore(nil), Client: client, Options: options}
}
func (s *GrokAuthorization) Start() { s.Store.Start() }
func (s *GrokAuthorization) StopContext(ctx context.Context) error {
	s.Store.Stop()
	return s.activity.stop(ctx, "grok authorization")
}
func (s *GrokAuthorization) proxyURL(ctx context.Context, id *int64) (string, error) {
	if id == nil {
		return "", nil
	}
	if s.Options.LookupProxy == nil {
		return "", infraerrors.New(infraerrors.CategoryBadRequest, "GROK_OAUTH_PROXY_NOT_AVAILABLE", "proxy repository is not available")
	}
	value, found, err := s.Options.LookupProxy(ctx, *id)
	if err != nil {
		return "", infraerrors.New(infraerrors.CategoryServiceUnavailable, "GROK_OAUTH_PROXY_LOOKUP_FAILED", "proxy lookup is temporarily unavailable")
	}
	if !found {
		return "", infraerrors.New(infraerrors.CategoryBadRequest, "GROK_OAUTH_PROXY_NOT_FOUND", "configured proxy was not found")
	}
	return value, nil
}

const grokDefaultAccessTokenTTL = 6 * time.Hour

type GrokOAuthCapabilities struct {
	PasswordAuthEnabled bool `json:"password_auth_enabled"`
}

func (s *GrokAuthorization) GetCapabilities() GrokOAuthCapabilities {
	return GrokOAuthCapabilities{PasswordAuthEnabled: s.Options.PasswordAuthEnabled()}
}

type GrokAuthURLResult struct {
	AuthURL   string `json:"auth_url"`
	SessionID string `json:"session_id"`
	State     string `json:"state"`
}

func (s *GrokAuthorization) generateAuthURL(ctx context.Context, proxyID *int64, redirectURI string) (*GrokAuthURLResult, error) {
	state, err := s.Options.GenerateState()
	if err != nil {
		return nil, infraerrors.Newf(infraerrors.CategoryInternalServer, "GROK_OAUTH_STATE_FAILED", "failed to generate state: %v", err)
	}
	nonce, err := s.Options.GenerateNonce()
	if err != nil {
		return nil, infraerrors.Newf(infraerrors.CategoryInternalServer, "GROK_OAUTH_NONCE_FAILED", "failed to generate nonce: %v", err)
	}
	codeVerifier, err := s.Options.GenerateCodeVerifier()
	if err != nil {
		return nil, infraerrors.Newf(infraerrors.CategoryInternalServer, "GROK_OAUTH_VERIFIER_FAILED", "failed to generate code verifier: %v", err)
	}
	sessionID, err := s.Options.GenerateSessionID()
	if err != nil {
		return nil, infraerrors.Newf(infraerrors.CategoryInternalServer, "GROK_OAUTH_SESSION_FAILED", "failed to generate session ID: %v", err)
	}

	proxyURL, err := s.proxyURL(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	redirectURI = s.Options.EffectiveRedirectURI(redirectURI)
	codeChallenge := s.Options.GenerateCodeChallenge(codeVerifier)

	authURL, err := s.Options.BuildAuthorizationURL(state, codeChallenge, redirectURI, nonce)
	if err != nil {
		return nil, infraerrors.Newf(infraerrors.CategoryBadRequest, "GROK_OAUTH_INVALID_AUTHORIZE_URL", "%v", err)
	}

	s.Store.Set(sessionID, &GrokOAuthSession{

		State: state,

		CodeVerifier: codeVerifier,

		CodeChallenge: codeChallenge,

		ClientID: s.Options.EffectiveClientID(),

		Scope: s.Options.EffectiveScope(),

		ProxyURL: proxyURL,

		RedirectURI: redirectURI,

		CreatedAt: time.Now(),
	})

	return &GrokAuthURLResult{
		AuthURL:   authURL,
		SessionID: sessionID,
		State:     state,
	}, nil
}

type GrokExchangeCodeInput struct {
	SessionID   string
	Code        string
	State       string
	RedirectURI string
	ProxyID     *int64
}
type GrokTokenInfo struct {
	AccessToken       string `json:"access_token"`
	RefreshToken      string `json:"refresh_token,omitempty"`
	IDToken           string `json:"id_token,omitempty"`
	TokenType         string `json:"token_type,omitempty"`
	ExpiresIn         int64  `json:"expires_in"`
	ExpiresAt         int64  `json:"expires_at"`
	ClientID          string `json:"client_id,omitempty"`
	Scope             string `json:"scope,omitempty"`
	Email             string `json:"email,omitempty"`
	Subject           string `json:"sub,omitempty"`
	TeamID            string `json:"team_id,omitempty"`
	SubscriptionTier  string `json:"subscription_tier,omitempty"`
	EntitlementStatus string `json:"entitlement_status,omitempty"`
}
type GrokPasswordLoginResult = wiregrok.PasswordLoginResult

func (s *GrokAuthorization) exchangeCode(ctx context.Context, input *GrokExchangeCodeInput) (*GrokTokenInfo, error) {
	if input == nil {
		return nil, infraerrors.New(infraerrors.CategoryBadRequest, "GROK_OAUTH_INVALID_INPUT", "input is required")
	}
	session, ok := s.Store.Get(input.SessionID)
	if !ok {
		return nil, infraerrors.New(infraerrors.CategoryBadRequest, "GROK_OAUTH_SESSION_NOT_FOUND", "session not found or expired")
	}

	parsed := s.Options.ParseAuthorizationInput(input.Code)
	code := strings.TrimSpace(parsed.Code)
	if code == "" {
		return nil, infraerrors.New(infraerrors.CategoryBadRequest, "GROK_OAUTH_CODE_REQUIRED", "authorization code is required")
	}
	state := strings.TrimSpace(input.State)
	if state == "" {
		state = strings.TrimSpace(parsed.State)
	}
	if state == "" {
		return nil, infraerrors.New(infraerrors.CategoryBadRequest, "GROK_OAUTH_STATE_REQUIRED", "oauth state is required")
	}
	if subtle.ConstantTimeCompare([]byte(state), []byte(session.State)) != 1 {
		return nil, infraerrors.New(infraerrors.CategoryBadRequest, "GROK_OAUTH_INVALID_STATE", "invalid oauth state")
	}
	if redirectURI := strings.TrimSpace(input.RedirectURI); redirectURI != "" &&
		redirectURI != strings.TrimSpace(session.RedirectURI) {
		return nil, infraerrors.New(infraerrors.CategoryBadRequest, "GROK_OAUTH_REDIRECT_URI_MISMATCH", "redirect_uri does not match the OAuth session")
	}

	proxyURL := session.ProxyURL
	if input.ProxyID != nil {
		var err error
		proxyURL, err = s.proxyURL(ctx, input.ProxyID)
		if err != nil {
			return nil, err
		}
	}
	if err := s.requireOAuthClient(); err != nil {
		return nil, err
	}
	if !s.Store.TryConsumeSession(input.SessionID) {
		return nil, infraerrors.New(infraerrors.CategoryBadRequest, "GROK_OAUTH_SESSION_ALREADY_USED", "oauth session has already been used")
	}
	defer s.Store.Delete(input.SessionID)
	tokenResp, err := s.Client.ExchangeCode(ctx, code, session.CodeVerifier, session.RedirectURI, proxyURL, session.ClientID)
	if err != nil {
		return nil, err
	}
	if err := validateGrokTokenResponse(tokenResp); err != nil {
		return nil, err
	}
	return s.tokenInfoFromResponse(tokenResp, session.ClientID, nil), nil
}
func (s *GrokAuthorization) requireOAuthClient() error {
	if s == nil || s.Client == nil {
		return infraerrors.New(infraerrors.CategoryInternalServer, "GROK_OAUTH_CLIENT_NOT_CONFIGURED", "oauth client is not configured")
	}
	return nil
}
func (s *GrokAuthorization) refreshToken(ctx context.Context, refreshToken, proxyURL, clientID string) (*GrokTokenInfo, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, infraerrors.New(infraerrors.CategoryBadRequest, "GROK_OAUTH_NO_REFRESH_TOKEN", "refresh_token is required")
	}
	if err := s.requireOAuthClient(); err != nil {
		return nil, err
	}
	tokenResp, err := s.Client.RefreshToken(ctx, refreshToken, proxyURL, clientID)
	if err != nil {
		return nil, err
	}
	if err := validateGrokTokenResponse(tokenResp); err != nil {
		return nil, err
	}
	tokenInfo := s.tokenInfoFromResponse(tokenResp, clientID, nil)
	if tokenInfo.RefreshToken == "" {
		tokenInfo.RefreshToken = refreshToken
	}
	return tokenInfo, nil
}
func (s *GrokAuthorization) validateRefreshToken(ctx context.Context, refreshToken string, proxyID *int64) (*GrokTokenInfo, error) {
	proxyURL, err := s.proxyURL(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	return s.refreshToken(ctx, refreshToken, proxyURL, s.Options.EffectiveClientID())
}

// ValidateSSOToken 将 Web SSO Cookie 转换为 Build OAuth 令牌。
// 原始 sso_token 绝不写入 GrokTokenInfo 或账号凭证。
func (s *GrokAuthorization) validateSSOToken(ctx context.Context, ssoToken string, proxyID *int64) (*GrokTokenInfo, error) {
	ssoToken = strings.TrimSpace(ssoToken)
	if ssoToken == "" {
		return nil, infraerrors.New(infraerrors.CategoryBadRequest, "GROK_OAUTH_NO_SSO_TOKEN", "sso_token is required")
	}
	if err := s.requireOAuthClient(); err != nil {
		return nil, err
	}
	proxyURL, err := s.proxyURL(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	tokenResp, err := s.Client.ConvertSSOToBuild(ctx, ssoToken, proxyURL)
	if err != nil {
		return nil, err
	}
	if err := validateGrokTokenResponse(tokenResp); err != nil {
		return nil, err
	}
	return s.tokenInfoFromResponse(tokenResp, s.Options.DefaultClientID, nil), nil
}

// ConvertFromSSO 是批量导入入口，语义与 ValidateSSOToken 相同。
func (s *GrokAuthorization) convertFromSSO(ctx context.Context, ssoToken string, proxyID *int64) (*GrokTokenInfo, error) {
	return s.validateSSOToken(ctx, ssoToken, proxyID)
}

// AuthorizePassword 使用邮箱和密码登录，将所得 SSO Cookie 转换为 Build OAuth，
// 并且只返回 OAuth 令牌；密码与原始 SSO 数据绝不持久化。
func (s *GrokAuthorization) authorizePassword(ctx context.Context, email, password string, proxyID *int64) (*GrokTokenInfo, error) {
	if !s.Options.PasswordAuthEnabled() {
		return nil, infraerrors.New(infraerrors.CategoryForbidden, "GROK_OAUTH_PASSWORD_AUTH_DISABLED", "Grok password authorization is disabled")
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, infraerrors.New(infraerrors.CategoryBadRequest, "GROK_OAUTH_EMAIL_REQUIRED", "email is required")
	}
	if strings.TrimSpace(password) == "" {
		return nil, infraerrors.New(infraerrors.CategoryBadRequest, "GROK_OAUTH_PASSWORD_REQUIRED", "password is required")
	}
	if err := s.requireOAuthClient(); err != nil {
		return nil, err
	}
	proxyURL, err := s.proxyURL(ctx, proxyID)
	if err != nil {
		return nil, err
	}
	loginResult, err := s.Client.LoginWithPassword(ctx, email, password, proxyURL)
	if err != nil {
		return nil, err
	}
	if loginResult == nil || strings.TrimSpace(loginResult.SSOToken) == "" {
		return nil, infraerrors.New(infraerrors.CategoryBadGateway, "GROK_OAUTH_PASSWORD_LOGIN_FAILED", "grok password login did not return sso_token")
	}
	info, err := s.validateSSOToken(ctx, loginResult.SSOToken, proxyID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(info.Email) == "" {
		info.Email = loginResult.Email
	}
	return info, nil
}
func validateGrokTokenResponse(tokenResp *wiregrok.TokenResponse) error {
	if tokenResp == nil || strings.TrimSpace(tokenResp.AccessToken) == "" {
		return infraerrors.New(infraerrors.CategoryBadGateway, "GROK_OAUTH_INVALID_TOKEN_RESPONSE", "grok oauth token response missing access_token")
	}
	return nil
}
func (s *GrokAuthorization) refreshAccountToken(ctx context.Context, account *Record) (*GrokTokenInfo, error) {
	if account == nil || account.Platform != PlatformGrok {
		return nil, infraerrors.New(infraerrors.CategoryBadRequest, "GROK_OAUTH_INVALID_ACCOUNT", "account is not a Grok account")
	}
	if account.Type != AccountTypeOAuth {
		return nil, infraerrors.New(infraerrors.CategoryBadRequest, "GROK_OAUTH_INVALID_ACCOUNT_TYPE", "account is not an OAuth account")
	}

	proxyURL, err := s.proxyURL(ctx, account.ProxyID)
	if err != nil {
		return nil, err
	}
	refreshToken := account.GetCredential("refresh_token")
	if strings.TrimSpace(refreshToken) == "" {
		return nil, infraerrors.New(infraerrors.CategoryBadRequest, "GROK_OAUTH_NO_REFRESH_TOKEN", "no refresh token available")
	}

	clientID := account.GetCredential("client_id")
	tokenInfo, err := s.refreshToken(ctx, refreshToken, proxyURL, clientID)
	if err != nil {
		return nil, err
	}
	// 新访问令牌 JWT 是权威来源；刷新后的令牌没有档位声明（不透明令牌或字段缺失）时，
	// 才保留已存储值。
	if strings.TrimSpace(tokenInfo.SubscriptionTier) == "" {
		tokenInfo.SubscriptionTier = account.GetCredential("subscription_tier")
	}
	if strings.TrimSpace(tokenInfo.EntitlementStatus) == "" {
		tokenInfo.EntitlementStatus = account.GetCredential("entitlement_status")
	}
	return tokenInfo, nil
}
func (s *GrokAuthorization) BuildAccountCredentials(tokenInfo *GrokTokenInfo) map[string]any {
	if tokenInfo == nil {
		return nil
	}
	expiresAt := time.Unix(tokenInfo.ExpiresAt, 0).UTC().Format(time.RFC3339)
	creds := map[string]any{
		"access_token": tokenInfo.AccessToken,
		"expires_at":   expiresAt,
	}
	if tokenInfo.RefreshToken != "" {
		creds["refresh_token"] = tokenInfo.RefreshToken
	}
	if tokenInfo.TokenType != "" {
		creds["token_type"] = tokenInfo.TokenType
	}
	if tokenInfo.IDToken != "" {
		creds["id_token"] = tokenInfo.IDToken
	}
	if tokenInfo.ClientID != "" {
		creds["client_id"] = tokenInfo.ClientID
	}
	if tokenInfo.Scope != "" {
		creds["scope"] = tokenInfo.Scope
	}
	if tokenInfo.Email != "" {
		creds["email"] = tokenInfo.Email
	}
	if tokenInfo.Subject != "" {
		creds["sub"] = tokenInfo.Subject
	}
	if tokenInfo.TeamID != "" {
		creds["team_id"] = tokenInfo.TeamID
	}
	if tokenInfo.SubscriptionTier != "" {
		creds["subscription_tier"] = tokenInfo.SubscriptionTier
	}
	if tokenInfo.EntitlementStatus != "" {
		creds["entitlement_status"] = tokenInfo.EntitlementStatus
	}
	creds["base_url"] = s.Options.DefaultCLIBaseURL
	return creds
}
func (s *GrokAuthorization) tokenInfoFromResponse(tokenResp *wiregrok.TokenResponse, clientID string, existing map[string]any) *GrokTokenInfo {
	now := time.Now()
	expiresIn := tokenResp.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = int64(grokDefaultAccessTokenTTL.Seconds())
	}
	info := &GrokTokenInfo{

		AccessToken: tokenResp.AccessToken,

		RefreshToken: tokenResp.RefreshToken,

		IDToken: tokenResp.IDToken,

		TokenType: tokenResp.TokenType,

		ExpiresIn: expiresIn,

		ExpiresAt: now.Add(time.Duration(expiresIn) * time.Second).Unix(),

		ClientID: strings.TrimSpace(clientID),

		Scope: tokenResp.Scope,
	}
	if info.ClientID == "" {
		info.ClientID = s.Options.EffectiveClientID()
	}
	if info.TokenType == "" {
		info.TokenType = "Bearer"
	}
	s.applyGrokTokenClaims(info, tokenResp.IDToken, false)
	s.applyGrokTokenClaims(info, tokenResp.AccessToken, true)
	if existing != nil {
		if info.Email == "" {
			if email, _ := existing["email"].(string); email != "" {
				info.Email = email
			}
		}
		if info.Subject == "" {
			if subject, _ := existing["sub"].(string); subject != "" {
				info.Subject = subject
			}
		}
		if info.TeamID == "" {
			if teamID, _ := existing["team_id"].(string); teamID != "" {
				info.TeamID = teamID
			}
		}
	}
	return info
}
func (s *GrokAuthorization) applyGrokTokenClaims(info *GrokTokenInfo, token string, includeTier bool) {
	if info == nil || strings.TrimSpace(token) == "" {
		return
	}
	claims := s.Options.DecodeJWTClaims(token)
	if claims == nil {
		return
	}
	if info.Email == "" {
		info.Email = s.Options.JWTClaimString(claims, "email")
	}
	if info.Subject == "" {
		info.Subject = s.Options.JWTClaimString(claims, "sub")
	}
	if info.TeamID == "" {
		info.TeamID = s.Options.JWTClaimString(claims, "team_id")
	}
	if includeTier {
		if tier := s.Options.SubscriptionTierFromJWT(token); tier != "" {
			info.SubscriptionTier = tier
		}
	}
}
func (s *GrokAuthorization) GenerateAuthURL(ctx context.Context, proxyID *int64, redirectURI string) (*GrokAuthURLResult, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("grok authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	return s.generateAuthURL(operation, proxyID, redirectURI)
}
func (s *GrokAuthorization) ExchangeCode(ctx context.Context, input *GrokExchangeCodeInput) (*GrokTokenInfo, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("grok authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	return s.exchangeCode(operation, input)
}
func (s *GrokAuthorization) RefreshToken(ctx context.Context, refreshToken, proxyURL, clientID string) (*GrokTokenInfo, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("grok authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	return s.refreshToken(operation, refreshToken, proxyURL, clientID)
}
func (s *GrokAuthorization) ValidateRefreshToken(ctx context.Context, refreshToken string, proxyID *int64) (*GrokTokenInfo, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("grok authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	return s.validateRefreshToken(operation, refreshToken, proxyID)
}
func (s *GrokAuthorization) ValidateSSOToken(ctx context.Context, ssoToken string, proxyID *int64) (*GrokTokenInfo, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("grok authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	return s.validateSSOToken(operation, ssoToken, proxyID)
}
func (s *GrokAuthorization) ConvertFromSSO(ctx context.Context, ssoToken string, proxyID *int64) (*GrokTokenInfo, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("grok authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	return s.convertFromSSO(operation, ssoToken, proxyID)
}
func (s *GrokAuthorization) AuthorizePassword(ctx context.Context, email, password string, proxyID *int64) (*GrokTokenInfo, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("grok authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	return s.authorizePassword(operation, email, password, proxyID)
}
func (s *GrokAuthorization) RefreshAccountToken(ctx context.Context, account *Record) (*GrokTokenInfo, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("grok authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	return s.refreshAccountToken(operation, account)
}
