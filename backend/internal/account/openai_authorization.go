// OpenAI 授权、刷新结果补全与状态校验由账号用例拥有；交换和 TLS 只通过端口调用。
package account

import (
	"context"
	"crypto/subtle"
	"strings"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

type OpenAIAuthOptions struct {
	ClientID, DefaultRedirectURI                           string
	GenerateState, GenerateCodeVerifier, GenerateSessionID func() (string, error)
	GenerateCodeChallenge                                  func(string) string
	OAuthClientConfigByPlatform                            func(string) (string, bool)
	BuildAuthorizationURLForPlatform                       func(string, string, string, string) string
	ProxyAvailable                                         func() bool
	ProxyURL                                               func(context.Context, int64) (string, bool, error)
	Exchange                                               func(context.Context, string, string, string, string, string, int64) (*wire.OAuthTokenResponse, error)
	Refresh                                                func(context.Context, string, string, string, int64, *Record) (*wire.OAuthTokenResponse, error)
	ParseIDToken, DecodeIDToken                            func(string) (*wire.OAuthIDTokenClaims, error)
	PrivacyAvailable                                       func() bool
	FetchAccountInfo                                       func(context.Context, string, string, string) *wire.ChatGPTAccountInfo
	FetchSubscription                                      func(context.Context, string, string, string) string
	DisableTraining                                        func(context.Context, string, string) string
	ValidatePAT                                            func(context.Context, string, string) (*OpenAITokenInfo, error)
	Warn                                                   func(string, ...any)
}
type OpenAIAuthorization struct {
	activity operationActivity
	Sessions *OpenAISessionStore
	Options  OpenAIAuthOptions
}

func NewOpenAIAuthorization(sessions *OpenAISessionStore, options OpenAIAuthOptions) *OpenAIAuthorization {
	return &OpenAIAuthorization{Sessions: sessions, Options: options}
}
func openAIInt64Value(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

// 生成授权 URL，保持随机值与会话登记顺序。
func (s *OpenAIAuthorization) generateAuthURL(ctx context.Context, proxyID *int64, redirectURI, platform string) (*OpenAIAuthURLResult, error) {
	// 按原顺序生成 PKCE 值。
	state, err := s.Options.GenerateState()
	if err != nil {
		return nil, infraerrors.Newf(500, "OPENAI_OAUTH_STATE_FAILED", "failed to generate state: %v", err)
	}

	codeVerifier, err := s.Options.GenerateCodeVerifier()
	if err != nil {
		return nil, infraerrors.Newf(500, "OPENAI_OAUTH_VERIFIER_FAILED", "failed to generate code verifier: %v", err)
	}

	codeChallenge := s.Options.GenerateCodeChallenge(codeVerifier)

	// 生成本次授权会话 ID。
	sessionID, err := s.Options.GenerateSessionID()
	if err != nil {
		return nil, infraerrors.Newf(500, "OPENAI_OAUTH_SESSION_FAILED", "failed to generate session ID: %v", err)
	}

	// 使用显式指定的代理。
	var proxyURL string
	if proxyID != nil {
		resolvedProxyURL, proxyFound, err := s.Options.ProxyURL(ctx, *proxyID)
		if err != nil {
			return nil, infraerrors.Newf(400, "OPENAI_OAUTH_PROXY_NOT_FOUND", "proxy not found: %v", err)
		}
		if proxyFound {
			proxyURL = resolvedProxyURL
		}
	}

	// 未指定时沿用默认回调地址。
	if redirectURI == "" {
		redirectURI = s.Options.DefaultRedirectURI
	}
	normalizedPlatform := NormalizeOpenAIOAuthPlatform(platform)
	clientID, _ := s.Options.OAuthClientConfigByPlatform(normalizedPlatform)

	// 记录原会话值。
	session := &OpenAIOAuthSession{
		State:        state,
		CodeVerifier: codeVerifier,
		ClientID:     clientID,
		RedirectURI:  redirectURI,
		ProxyURL:     proxyURL,
		CreatedAt:    time.Now(),
	}
	s.Sessions.Set(sessionID, session)

	// 构造供应商授权地址。
	authURL := s.Options.BuildAuthorizationURLForPlatform(state, codeChallenge, redirectURI, normalizedPlatform)

	return &OpenAIAuthURLResult{
		AuthURL:   authURL,
		SessionID: sessionID,
	}, nil
}

// 交换授权码并在成功后消费原会话。
func (s *OpenAIAuthorization) exchangeCode(ctx context.Context, input *OpenAIExchangeCodeInput) (*OpenAITokenInfo, error) {
	// 读取原授权会话。
	session, ok := s.Sessions.Get(input.SessionID)
	if !ok {
		return nil, infraerrors.New(400, "OPENAI_OAUTH_SESSION_NOT_FOUND", "session not found or expired")
	}
	if input.State == "" {
		return nil, infraerrors.New(400, "OPENAI_OAUTH_STATE_REQUIRED", "oauth state is required")
	}
	if subtle.ConstantTimeCompare([]byte(input.State), []byte(session.State)) != 1 {
		return nil, infraerrors.New(400, "OPENAI_OAUTH_INVALID_STATE", "invalid oauth state")
	}

	// 输入代理优先，未指定时沿用会话代理。
	proxyURL := session.ProxyURL
	if input.ProxyID != nil {
		resolvedProxyURL, proxyFound, err := s.Options.ProxyURL(ctx, *input.ProxyID)
		if err != nil {
			return nil, infraerrors.Newf(400, "OPENAI_OAUTH_PROXY_NOT_FOUND", "proxy not found: %v", err)
		}
		if proxyFound {
			proxyURL = resolvedProxyURL
		}
	}

	// 输入回调地址覆盖会话回调地址。
	redirectURI := session.RedirectURI
	if input.RedirectURI != "" {
		redirectURI = input.RedirectURI
	}
	clientID := strings.TrimSpace(session.ClientID)
	if clientID == "" {
		clientID = s.Options.ClientID
	}

	// 执行单次授权码交换。
	tokenResp, err := s.Options.Exchange(ctx, input.Code, session.CodeVerifier, redirectURI, proxyURL, clientID, openAIInt64Value(input.TLSFingerprintRouterID))
	if err != nil {
		return nil, err
	}

	// 解码 ID token 的现有账号元数据。
	var userInfo *wire.OAuthUserInfo
	if tokenResp.IDToken != "" {
		claims, parseErr := s.Options.ParseIDToken(tokenResp.IDToken)
		if parseErr != nil {
			s.Options.Warn("openai_oauth_id_token_parse_failed", "error", parseErr)
		} else {
			userInfo = claims.GetUserInfo()
		}
	}

	// 交换成功后删除会话。
	s.Sessions.Delete(input.SessionID)

	tokenInfo := &OpenAITokenInfo{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		IDToken:      tokenResp.IDToken,
		ExpiresIn:    int64(tokenResp.ExpiresIn),
		ExpiresAt:    time.Now().Unix() + int64(tokenResp.ExpiresIn),
		ClientID:     clientID,
	}

	if userInfo != nil {
		tokenInfo.Email = userInfo.Email
		tokenInfo.ChatGPTAccountID = userInfo.ChatGPTAccountID
		tokenInfo.ChatGPTUserID = userInfo.ChatGPTUserID
		tokenInfo.OrganizationID = userInfo.OrganizationID
		tokenInfo.PlanType = userInfo.PlanType
	}

	s.enrichTokenInfo(ctx, tokenInfo, proxyURL)

	return tokenInfo, nil
}

// 使用原刷新入口和 client_id 回退。
func (s *OpenAIAuthorization) refreshToken(ctx context.Context, refreshToken string, proxyURL string) (*OpenAITokenInfo, error) {
	return s.refreshTokenWithClientID(ctx, refreshToken, proxyURL, "")
}

// 按显式 client_id 刷新，缺省行为保持。
func (s *OpenAIAuthorization) refreshTokenWithClientID(ctx context.Context, refreshToken string, proxyURL string, clientID string) (*OpenAITokenInfo, error) {
	return s.refreshTokenWithParameters(ctx, refreshToken, proxyURL, clientID, 0, nil)
}

// RefreshTokenWithClientIDAndRouter 使用指定 TLS 路由器配置刷新导入态 ChatGPT OAuth token。
func (s *OpenAIAuthorization) refreshTokenWithClientIDAndRouter(ctx context.Context, refreshToken string, proxyURL string, clientID string, routerID *int64) (*OpenAITokenInfo, error) {
	return s.refreshTokenWithParameters(ctx, refreshToken, proxyURL, clientID, openAIInt64Value(routerID), nil)
}
func (s *OpenAIAuthorization) refreshTokenWithParameters(ctx context.Context, refreshToken string, proxyURL string, clientID string, routerID int64, account *Record) (*OpenAITokenInfo, error) {
	if account != nil && routerID <= 0 {
		routerID = account.GetTLSFingerprintRouterID()
	}
	tokenResp, err := s.Options.Refresh(ctx, refreshToken, proxyURL, clientID, routerID, account)
	if err != nil {
		return nil, err
	}

	// 解码 ID token 的现有账号元数据。
	var userInfo *wire.OAuthUserInfo
	if tokenResp.IDToken != "" {
		claims, parseErr := s.Options.ParseIDToken(tokenResp.IDToken)
		if parseErr != nil {
			s.Options.Warn("openai_oauth_id_token_parse_failed", "error", parseErr)
		} else {
			userInfo = claims.GetUserInfo()
		}
	}

	tokenInfo := &OpenAITokenInfo{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		IDToken:      tokenResp.IDToken,
		ExpiresIn:    int64(tokenResp.ExpiresIn),
		ExpiresAt:    time.Now().Unix() + int64(tokenResp.ExpiresIn),
	}
	if trimmed := strings.TrimSpace(clientID); trimmed != "" {
		tokenInfo.ClientID = trimmed
	}

	if userInfo != nil {
		tokenInfo.Email = userInfo.Email
		tokenInfo.ChatGPTAccountID = userInfo.ChatGPTAccountID
		tokenInfo.ChatGPTUserID = userInfo.ChatGPTUserID
		tokenInfo.OrganizationID = userInfo.OrganizationID
		tokenInfo.PlanType = userInfo.PlanType
	}

	s.enrichTokenInfo(ctx, tokenInfo, proxyURL)

	return tokenInfo, nil
}

// EnrichTokenInfo 通过 ChatGPT backend-api 补全 tokenInfo 并设置隐私（best-effort）。
// 从 accounts/check 获取最新 plan_type、subscription_expires_at、email，
// 然后尝试关闭训练数据共享。适用于所有获取/刷新 token 的路径。
func (s *OpenAIAuthorization) enrichTokenInfo(ctx context.Context, tokenInfo *OpenAITokenInfo, proxyURL string) {
	if tokenInfo.AccessToken == "" || !s.Options.PrivacyAvailable() {
		return
	}

	// 从 access_token JWT 中提取 orgID（poid），用于匹配正确的账号
	orgID := tokenInfo.OrganizationID
	if orgID == "" {
		if atClaims, err := s.Options.DecodeIDToken(tokenInfo.AccessToken); err == nil && atClaims.OpenAIAuth != nil {
			orgID = atClaims.OpenAIAuth.POID
		}
	}
	// accounts/check 命中的记录不属于个人账号时，必须改用个人订阅端点拿到期时间，
	// 否则会把 workspace 权益的 expires_at 当成个人订阅到期日展示。
	forcePersonalSubscriptionLookup := false
	if info := s.Options.FetchAccountInfo(ctx, tokenInfo.AccessToken, proxyURL, orgID); info != nil {
		// ID token 里的 chatgpt_plan_type 是个人订阅的权威值。
		// accounts/check 是多账号/工作区接口，已失效的团队或商业工作区可能会用内部计费计划名
		// 覆盖 Pro/Free，例如 self_serve_business_usage_based。
		appliedAccountInfoPlanType := ShouldApplyChatGPTAccountInfoPlanType(tokenInfo.PlanType, info.PlanType)
		if appliedAccountInfoPlanType {
			tokenInfo.PlanType = info.PlanType
		}
		// 套餐与到期时间必须描述同一份订阅。套餐保留 JWT 个人值时，只有
		// accounts/check 命中的记录属于该个人账号，才采用其 entitlement.expires_at。
		if info.SubscriptionExpiresAt != "" {
			if appliedAccountInfoPlanType || ChatGPTAccountInfoBelongsToTokenAccount(tokenInfo, info) {
				tokenInfo.SubscriptionExpiresAt = info.SubscriptionExpiresAt
			} else {
				forcePersonalSubscriptionLookup = true
			}
		}
		if tokenInfo.Email == "" && info.Email != "" {
			tokenInfo.Email = info.Email
		}
	}
	if forcePersonalSubscriptionLookup || strings.TrimSpace(tokenInfo.SubscriptionExpiresAt) == "" {
		if expiresAt := s.Options.FetchSubscription(ctx, tokenInfo.AccessToken, proxyURL, ResolveChatGPTSubscriptionAccountID(tokenInfo, orgID)); expiresAt != "" {
			tokenInfo.SubscriptionExpiresAt = expiresAt
		}
	}

	// 尝试设置隐私（关闭训练数据共享），best-effort
	tokenInfo.PrivacyMode = s.Options.DisableTraining(ctx, tokenInfo.AccessToken, proxyURL)
}
func ShouldApplyChatGPTAccountInfoPlanType(current, candidate string) bool {
	return strings.TrimSpace(candidate) != "" && strings.TrimSpace(current) == ""
}

// ChatGPTAccountInfoBelongsToTokenAccount 判断 accounts/check 命中的记录是否属于
// token 的个人 ChatGPT 账号。任一侧缺少 ID 时无法区分，沿用既有行为。
func ChatGPTAccountInfoBelongsToTokenAccount(tokenInfo *OpenAITokenInfo, info *wire.ChatGPTAccountInfo) bool {
	personalID := strings.TrimSpace(tokenInfo.ChatGPTAccountID)
	sourceID := strings.TrimSpace(info.AccountID)
	if personalID == "" || sourceID == "" {
		return true
	}
	return strings.EqualFold(personalID, sourceID)
}
func ResolveChatGPTSubscriptionAccountID(tokenInfo *OpenAITokenInfo, orgID string) string {
	for _, candidate := range []string{
		tokenInfo.ChatGPTAccountID,
		tokenInfo.OrganizationID,
		orgID,
	} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// 根据账号当前认证方式取得刷新结果。
func (s *OpenAIAuthorization) refreshAccountToken(ctx context.Context, account *Record) (*OpenAITokenInfo, error) {
	if account.Platform != PlatformOpenAI {
		return nil, infraerrors.New(400, "OPENAI_OAUTH_INVALID_ACCOUNT", "account is not an OpenAI account")
	}
	if account.Type != AccountTypeOAuth {
		return nil, infraerrors.New(400, "OPENAI_OAUTH_INVALID_ACCOUNT_TYPE", "account is not an OAuth account")
	}

	var proxyURL string
	if account.ProxyID != nil && s.Options.ProxyAvailable() {
		resolvedProxyURL, proxyFound, err := s.Options.ProxyURL(ctx, *account.ProxyID)
		if err == nil && proxyFound {
			proxyURL = resolvedProxyURL
		}
	}

	accessToken := account.GetCredential("access_token")
	if account.IsOpenAIPersonalAccessToken() {
		if accessToken == "" {
			return nil, infraerrors.New(400, "OPENAI_CODEX_PAT_REQUIRED", "access token is required")
		}
		return s.Options.ValidatePAT(ctx, accessToken, proxyURL)
	}

	refreshToken := account.GetCredential("refresh_token")
	if refreshToken == "" {
		if accessToken != "" {
			tokenInfo := &OpenAITokenInfo{
				AccessToken:           accessToken,
				RefreshToken:          "",
				IDToken:               account.GetCredential("id_token"),
				ClientID:              account.GetCredential("client_id"),
				Email:                 account.GetCredential("email"),
				ChatGPTAccountID:      account.GetCredential("chatgpt_account_id"),
				ChatGPTUserID:         account.GetCredential("chatgpt_user_id"),
				OrganizationID:        account.GetCredential("organization_id"),
				PlanType:              account.GetCredential("plan_type"),
				SubscriptionExpiresAt: account.GetCredential("subscription_expires_at"),
			}
			if expiresAt := account.GetCredentialAsTime("expires_at"); expiresAt != nil {
				tokenInfo.ExpiresAt = expiresAt.Unix()
				tokenInfo.ExpiresIn = int64(time.Until(*expiresAt).Seconds())
			}
			s.enrichTokenInfo(ctx, tokenInfo, proxyURL)
			return tokenInfo, nil
		}
		return nil, infraerrors.New(400, "OPENAI_OAUTH_NO_REFRESH_TOKEN", "no refresh token available")
	}

	clientID := account.GetCredential("client_id")
	return s.refreshTokenWithParameters(ctx, refreshToken, proxyURL, clientID, account.GetTLSFingerprintRouterID(), account)
}
func NormalizeOpenAIOAuthPlatform(platform string) string {
	return PlatformOpenAI
}

// Start 只启动账号已有会话清理，不提前执行供应商交换。
func (s *OpenAIAuthorization) Start() {
	if s == nil || s.Sessions == nil {
		return
	}
	s.activity.mu.Lock()
	defer s.activity.mu.Unlock()
	if s.activity.stopped {
		return
	}
	s.Sessions.Start()
}

// StopContext 取消并等待本实例在途授权，然后关闭其会话清理。
func (s *OpenAIAuthorization) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if err := s.activity.stop(ctx, "OpenAI authorization"); err != nil {
		return err
	}
	if s.Sessions != nil {
		s.Sessions.Stop()
	}
	return nil
}

// GenerateAuthURL 将当前操作登记到唯一授权实例的生命周期。
func (s *OpenAIAuthorization) GenerateAuthURL(ctx context.Context, proxyID *int64, redirectURI, platform string) (*OpenAIAuthURLResult, error) {
	ctx, finish, err := s.activity.begin(ctx, ErrProbeStopped)
	if err != nil {
		return nil, err
	}
	defer finish()
	return s.generateAuthURL(ctx, proxyID, redirectURI, platform)
}

// ExchangeCode 将当前操作登记到唯一授权实例的生命周期。
func (s *OpenAIAuthorization) ExchangeCode(ctx context.Context, input *OpenAIExchangeCodeInput) (*OpenAITokenInfo, error) {
	ctx, finish, err := s.activity.begin(ctx, ErrProbeStopped)
	if err != nil {
		return nil, err
	}
	defer finish()
	return s.exchangeCode(ctx, input)
}

// RefreshToken 将当前操作登记到唯一授权实例的生命周期。
func (s *OpenAIAuthorization) RefreshToken(ctx context.Context, refreshToken string, proxyURL string) (*OpenAITokenInfo, error) {
	ctx, finish, err := s.activity.begin(ctx, ErrProbeStopped)
	if err != nil {
		return nil, err
	}
	defer finish()
	return s.refreshToken(ctx, refreshToken, proxyURL)
}

// RefreshTokenWithClientID 将当前操作登记到唯一授权实例的生命周期。
func (s *OpenAIAuthorization) RefreshTokenWithClientID(ctx context.Context, refreshToken string, proxyURL string, clientID string) (*OpenAITokenInfo, error) {
	ctx, finish, err := s.activity.begin(ctx, ErrProbeStopped)
	if err != nil {
		return nil, err
	}
	defer finish()
	return s.refreshTokenWithClientID(ctx, refreshToken, proxyURL, clientID)
}

// RefreshTokenWithClientIDAndRouter 将当前操作登记到唯一授权实例的生命周期。
func (s *OpenAIAuthorization) RefreshTokenWithClientIDAndRouter(ctx context.Context, refreshToken string, proxyURL string, clientID string, routerID *int64) (*OpenAITokenInfo, error) {
	ctx, finish, err := s.activity.begin(ctx, ErrProbeStopped)
	if err != nil {
		return nil, err
	}
	defer finish()
	return s.refreshTokenWithClientIDAndRouter(ctx, refreshToken, proxyURL, clientID, routerID)
}

// RefreshTokenWithParameters 将当前操作登记到唯一授权实例的生命周期。
func (s *OpenAIAuthorization) RefreshTokenWithParameters(ctx context.Context, refreshToken string, proxyURL string, clientID string, routerID int64, account *Record) (*OpenAITokenInfo, error) {
	ctx, finish, err := s.activity.begin(ctx, ErrProbeStopped)
	if err != nil {
		return nil, err
	}
	defer finish()
	return s.refreshTokenWithParameters(ctx, refreshToken, proxyURL, clientID, routerID, account)
}

// RefreshAccountToken 将当前操作登记到唯一授权实例的生命周期。
func (s *OpenAIAuthorization) RefreshAccountToken(ctx context.Context, account *Record) (*OpenAITokenInfo, error) {
	ctx, finish, err := s.activity.begin(ctx, ErrProbeStopped)
	if err != nil {
		return nil, err
	}
	defer finish()
	return s.refreshAccountToken(ctx, account)
}

// EnrichTokenInfo 将当前操作登记到唯一授权实例的生命周期。
func (s *OpenAIAuthorization) EnrichTokenInfo(ctx context.Context, tokenInfo *OpenAITokenInfo, proxyURL string) {
	ctx, finish, err := s.activity.begin(ctx, ErrProbeStopped)
	if err != nil {
		return
	}
	defer finish()
	s.enrichTokenInfo(ctx, tokenInfo, proxyURL)
}

// ValidatePersonalAccessToken 复用原验证端口，并登记到账号授权的唯一活动拥有者。
func (s *OpenAIAuthorization) ValidatePersonalAccessToken(ctx context.Context, token, proxyURL string) (*OpenAITokenInfo, error) {
	ctx, done, err := s.activity.begin(ctx, ErrProbeStopped)
	if err != nil {
		return nil, err
	}
	defer done()
	return s.Options.ValidatePAT(ctx, token, proxyURL)
}
