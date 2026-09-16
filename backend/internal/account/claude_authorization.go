// 本文件拥有 Claude 账号授权编排，平台调用与代理读取通过窄端口注入。
package account

import (
	"context"
	"errors"
	"fmt"
	"time"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
)

type ClaudeAuthorizationOptions struct {
	ScopeOAuth, ScopeAPI, ScopeInference                   string
	GenerateState, GenerateCodeVerifier, GenerateSessionID func() (string, error)
	GenerateCodeChallenge                                  func(string) string
	BuildAuthorizationURL                                  func(string, string, string) string
	ResolveProxy                                           func(context.Context, int64) (string, bool)
	Logf                                                   func(string, ...any)
}
type ClaudeAuthorization struct {
	Store    *ClaudeAuthorizationSessions
	Client   ClaudeOAuthClient
	Options  ClaudeAuthorizationOptions
	activity operationActivity
}

func NewClaudeAuthorization(client ClaudeOAuthClient, options ClaudeAuthorizationOptions) *ClaudeAuthorization {
	return &ClaudeAuthorization{Store: NewClaudeAuthorizationSessions(), Client: client, Options: options}
}
func (s *ClaudeAuthorization) Start() { s.Store.Start() }
func (s *ClaudeAuthorization) StopContext(ctx context.Context) error {
	s.Store.Stop()
	return s.activity.stop(ctx, "claude authorization")
}

// ClaudeOAuthClient handles HTTP requests for Claude OAuth flows
type ClaudeOAuthClient interface {
	GetOrganizationUUID(ctx context.Context, sessionKey, proxyURL string) (string, error)
	GetAuthorizationCode(ctx context.Context, sessionKey, orgUUID, scope, codeChallenge, state, proxyURL string) (string, error)
	ExchangeCodeForToken(ctx context.Context, code, codeVerifier, state, proxyURL string, isSetupToken bool) (*wire.OAuthTokenResponse, error)
	RefreshToken(ctx context.Context, refreshToken, proxyURL string) (*wire.OAuthTokenResponse, error)
}

// ClaudeGenerateAuthURLResult contains the authorization URL and session info
type ClaudeGenerateAuthURLResult struct {
	AuthURL   string `json:"auth_url"`
	SessionID string `json:"session_id"`
}

// GenerateAuthURL generates an OAuth authorization URL with full scope
func (s *ClaudeAuthorization) GenerateAuthURL(ctx context.Context, proxyID *int64) (*ClaudeGenerateAuthURLResult, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("claude authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	ctx = operation

	return s.generateAuthURLWithScope(ctx, s.Options.ScopeOAuth, proxyID)
}

// GenerateSetupTokenURL generates an OAuth authorization URL for setup token (inference only)
func (s *ClaudeAuthorization) GenerateSetupTokenURL(ctx context.Context, proxyID *int64) (*ClaudeGenerateAuthURLResult, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("claude authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	ctx = operation

	scope := s.Options.ScopeInference
	return s.generateAuthURLWithScope(ctx, scope, proxyID)
}
func (s *ClaudeAuthorization) generateAuthURLWithScope(ctx context.Context, scope string, proxyID *int64) (*ClaudeGenerateAuthURLResult, error) {
	// Generate PKCE values
	state, err := s.Options.GenerateState()
	if err != nil {
		return nil, fmt.Errorf("failed to generate state: %w", err)
	}

	codeVerifier, err := s.Options.GenerateCodeVerifier()
	if err != nil {
		return nil, fmt.Errorf("failed to generate code verifier: %w", err)
	}

	codeChallenge := s.Options.GenerateCodeChallenge(codeVerifier)

	// Generate session ID
	sessionID, err := s.Options.GenerateSessionID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate session ID: %w", err)
	}

	// Get proxy URL if specified
	var proxyURL string
	if proxyID != nil {
		if resolved, ok := s.Options.ResolveProxy(ctx, *proxyID); ok {
			proxyURL = resolved
		}
	}

	// Store session
	session := &ClaudeAuthorizationSession{
		State:        state,
		CodeVerifier: codeVerifier,
		Scope:        scope,
		ProxyURL:     proxyURL,
		CreatedAt:    time.Now(),
	}
	s.Store.Set(sessionID, session)

	// Build authorization URL
	authURL := s.Options.BuildAuthorizationURL(state, codeChallenge, scope)

	return &ClaudeGenerateAuthURLResult{
		AuthURL:   authURL,
		SessionID: sessionID,
	}, nil
}

// ClaudeExchangeCodeInput represents the input for code exchange
type ClaudeExchangeCodeInput struct {
	SessionID string
	Code      string
	ProxyID   *int64
}

// ClaudeTokenInfo represents the token information stored in credentials
type ClaudeTokenInfo struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	ExpiresAt    int64  `json:"expires_at"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
	OrgUUID      string `json:"org_uuid,omitempty"`
	AccountUUID  string `json:"account_uuid,omitempty"`
	EmailAddress string `json:"email_address,omitempty"`
}

// ExchangeCode exchanges authorization code for tokens
func (s *ClaudeAuthorization) ExchangeCode(ctx context.Context, input *ClaudeExchangeCodeInput) (*ClaudeTokenInfo, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("claude authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	ctx = operation

	// Get session
	session, ok := s.Store.Get(input.SessionID)
	if !ok {
		return nil, fmt.Errorf("session not found or expired")
	}

	// Get proxy URL
	proxyURL := session.ProxyURL
	if input.ProxyID != nil {
		if resolved, ok := s.Options.ResolveProxy(ctx, *input.ProxyID); ok {
			proxyURL = resolved
		}
	}

	// Determine if this is a setup token (scope is inference only)
	isSetupToken := session.Scope == s.Options.ScopeInference

	// Exchange code for token
	tokenInfo, err := s.exchangeCodeForToken(ctx, input.Code, session.CodeVerifier, session.State, proxyURL, isSetupToken)
	if err != nil {
		return nil, err
	}

	// Delete session after successful exchange
	s.Store.Delete(input.SessionID)

	return tokenInfo, nil
}

// ClaudeCookieAuthInput represents the input for cookie-based authentication
type ClaudeCookieAuthInput struct {
	SessionKey string
	ProxyID    *int64
	Scope      string // "full" or "inference"
}

// CookieAuth performs OAuth using sessionKey (cookie-based auto-auth)
func (s *ClaudeAuthorization) CookieAuth(ctx context.Context, input *ClaudeCookieAuthInput) (*ClaudeTokenInfo, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("claude authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	ctx = operation

	// Get proxy URL if specified
	var proxyURL string
	if input.ProxyID != nil {
		if resolved, ok := s.Options.ResolveProxy(ctx, *input.ProxyID); ok {
			proxyURL = resolved
		}
	}

	// Determine scope and if this is a setup token
	// Internal API call uses ScopeAPI (org:create_api_key not supported)
	scope := s.Options.ScopeAPI
	isSetupToken := false
	if input.Scope == "inference" {
		scope = s.Options.ScopeInference
		isSetupToken = true
	}

	// Step 1: Get organization info using sessionKey
	orgUUID, err := s.getOrganizationUUID(ctx, input.SessionKey, proxyURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get organization info: %w", err)
	}

	// Step 2: Generate PKCE values
	codeVerifier, err := s.Options.GenerateCodeVerifier()
	if err != nil {
		return nil, fmt.Errorf("failed to generate code verifier: %w", err)
	}
	codeChallenge := s.Options.GenerateCodeChallenge(codeVerifier)

	state, err := s.Options.GenerateState()
	if err != nil {
		return nil, fmt.Errorf("failed to generate state: %w", err)
	}

	// Step 3: Get authorization code using cookie
	authCode, err := s.getAuthorizationCode(ctx, input.SessionKey, orgUUID, scope, codeChallenge, state, proxyURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get authorization code: %w", err)
	}

	// Step 4: Exchange code for token
	tokenInfo, err := s.exchangeCodeForToken(ctx, authCode, codeVerifier, state, proxyURL, isSetupToken)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}

	// Ensure org_uuid is set (from step 1 if not from token response)
	if tokenInfo.OrgUUID == "" && orgUUID != "" {
		tokenInfo.OrgUUID = orgUUID
		s.logf("[OAuth] Set org_uuid from cookie auth")
	}

	return tokenInfo, nil
}

// getOrganizationUUID gets the organization UUID from claude.ai using sessionKey
func (s *ClaudeAuthorization) getOrganizationUUID(ctx context.Context, sessionKey, proxyURL string) (string, error) {
	return s.Client.GetOrganizationUUID(ctx, sessionKey, proxyURL)
}

// getAuthorizationCode gets the authorization code using sessionKey
func (s *ClaudeAuthorization) getAuthorizationCode(ctx context.Context, sessionKey, orgUUID, scope, codeChallenge, state, proxyURL string) (string, error) {
	return s.Client.GetAuthorizationCode(ctx, sessionKey, orgUUID, scope, codeChallenge, state, proxyURL)
}

// exchangeCodeForToken exchanges authorization code for tokens
func (s *ClaudeAuthorization) exchangeCodeForToken(ctx context.Context, code, codeVerifier, state, proxyURL string, isSetupToken bool) (*ClaudeTokenInfo, error) {
	tokenResp, err := s.Client.ExchangeCodeForToken(ctx, code, codeVerifier, state, proxyURL, isSetupToken)
	if err != nil {
		return nil, err
	}

	tokenInfo := &ClaudeTokenInfo{
		AccessToken:  tokenResp.AccessToken,
		TokenType:    tokenResp.TokenType,
		ExpiresIn:    tokenResp.ExpiresIn,
		ExpiresAt:    time.Now().Unix() + tokenResp.ExpiresIn,
		RefreshToken: tokenResp.RefreshToken,
		Scope:        tokenResp.Scope,
	}

	if tokenResp.Organization != nil && tokenResp.Organization.UUID != "" {
		tokenInfo.OrgUUID = tokenResp.Organization.UUID
		s.logf("[OAuth] Got org_uuid")
	}
	if tokenResp.Account != nil {
		if tokenResp.Account.UUID != "" {
			tokenInfo.AccountUUID = tokenResp.Account.UUID
			s.logf("[OAuth] Got account_uuid")
		}
		if tokenResp.Account.EmailAddress != "" {
			tokenInfo.EmailAddress = tokenResp.Account.EmailAddress
			s.logf("[OAuth] Got email_address")
		}
	}

	return tokenInfo, nil
}

// RefreshToken refreshes an OAuth token
func (s *ClaudeAuthorization) RefreshToken(ctx context.Context, refreshToken string, proxyURL string) (*ClaudeTokenInfo, error) {
	operation, done, err := s.activity.begin(ctx, errors.New("claude authorization is stopped"))
	if err != nil {
		return nil, err
	}
	defer done()
	ctx = operation

	tokenResp, err := s.Client.RefreshToken(ctx, refreshToken, proxyURL)
	if err != nil {
		return nil, err
	}

	return &ClaudeTokenInfo{
		AccessToken:  tokenResp.AccessToken,
		TokenType:    tokenResp.TokenType,
		ExpiresIn:    tokenResp.ExpiresIn,
		ExpiresAt:    time.Now().Unix() + tokenResp.ExpiresIn,
		RefreshToken: tokenResp.RefreshToken,
		Scope:        tokenResp.Scope,
	}, nil
}

// RefreshAccountToken refreshes token for an account
func (s *ClaudeAuthorization) RefreshAccountToken(ctx context.Context, account *Record) (*ClaudeTokenInfo, error) {
	refreshToken := account.GetCredential("refresh_token")
	if refreshToken == "" {
		return nil, fmt.Errorf("no refresh token available")
	}

	var proxyURL string
	if account.ProxyID != nil {
		if resolved, ok := s.Options.ResolveProxy(ctx, *account.ProxyID); ok {
			proxyURL = resolved
		}
	}

	return s.RefreshToken(ctx, refreshToken, proxyURL)
}
func (s *ClaudeAuthorization) logf(format string, args ...any) {
	if s.Options.Logf != nil {
		s.Options.Logf(format, args...)
	}
}
