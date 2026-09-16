package service

import (
	"context"
	"log"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/model"
	"github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic/oauth"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

type OpenAIOAuthTokenRequestOptions = openai.OAuthTokenRequestOptions

// OpenAIOAuthTokenRouterReader 读取账号绑定的 TLS Router 运行时配置。
type OpenAIOAuthTokenRouterReader interface {
	GetRuntimeRouter(routerID int64) *model.TLSFingerprintRouter
}

// OpenAIOAuthTokenProfileResolver 解析 ChatGPT OAuth token 请求专用的 TLS 模板。
type OpenAIOAuthTokenProfileResolver interface {
	ResolveTokenTLSProfileByID(id int64) (*tlsfingerprint.Profile, bool)
}

// OpenAIOAuthClient interface for OpenAI OAuth operations
type OpenAIOAuthClient interface {
	ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI, proxyURL, clientID string, options ...OpenAIOAuthTokenRequestOptions) (*openai.TokenResponse, error)
	RefreshToken(ctx context.Context, refreshToken, proxyURL string, options ...OpenAIOAuthTokenRequestOptions) (*openai.TokenResponse, error)
	RefreshTokenWithClientID(ctx context.Context, refreshToken, proxyURL string, clientID string, options ...OpenAIOAuthTokenRequestOptions) (*openai.TokenResponse, error)
}

// GrokOAuthClient 定义 xAI/Grok OAuth 操作接口。
type GrokOAuthClient interface {
	ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI, proxyURL, clientID string) (*xai.TokenResponse, error)
	RefreshToken(ctx context.Context, refreshToken, proxyURL, clientID string) (*xai.TokenResponse, error)
	// LoginWithPassword 用邮箱密码兑换短期 Web SSO cookie；调用方必须继续通过 ConvertSSOToBuild
	// 兑换 OAuth token，且不得持久化密码或原始 SSO。
	LoginWithPassword(ctx context.Context, email, password, proxyURL string) (*GrokPasswordLoginResult, error)
	ConvertSSOToBuild(ctx context.Context, ssoToken, proxyURL string) (*xai.TokenResponse, error)
}

// GrokOAuthTokenService 是 Grok token provider 使用的窄刷新端口。
type GrokOAuthTokenService interface {
	RefreshAccountToken(ctx context.Context, account *Account) (*GrokTokenInfo, error)
	BuildAccountCredentials(tokenInfo *GrokTokenInfo) map[string]any
}

type ClaudeOAuthClient = accountcore.ClaudeOAuthClient
type GenerateAuthURLResult = accountcore.ClaudeGenerateAuthURLResult
type ExchangeCodeInput = accountcore.ClaudeExchangeCodeInput
type TokenInfo = accountcore.ClaudeTokenInfo
type CookieAuthInput = accountcore.ClaudeCookieAuthInput

// OAuthService 仅兼容旧构造和账号投影；授权会话与规则属于 account。
type OAuthService struct {
	*accountcore.ClaudeAuthorization
	sessionStore *accountcore.ClaudeAuthorizationSessions
	proxyRepo    ProxyRepository
	oauthClient  ClaudeOAuthClient
}

func NewOAuthService(proxyRepo ProxyRepository, client ClaudeOAuthClient) *OAuthService {
	s := &OAuthService{proxyRepo: proxyRepo, oauthClient: client}
	s.ClaudeAuthorization = accountcore.NewClaudeAuthorization(client, accountcore.ClaudeAuthorizationOptions{ScopeOAuth: oauth.ScopeOAuth, ScopeAPI: oauth.ScopeAPI, ScopeInference: oauth.ScopeInference, GenerateState: oauth.GenerateState, GenerateCodeVerifier: oauth.GenerateCodeVerifier, GenerateCodeChallenge: oauth.GenerateCodeChallenge, GenerateSessionID: oauth.GenerateSessionID, BuildAuthorizationURL: oauth.BuildAuthorizationURL, Logf: log.Printf, ResolveProxy: func(ctx context.Context, id int64) (string, bool) {
		proxy, err := proxyRepo.GetByID(ctx, id)
		if err != nil || proxy == nil {
			return "", false
		}
		return proxy.URL(), true
	}})
	s.sessionStore = s.Store
	return s
}
func (s *OAuthService) RefreshAccountToken(ctx context.Context, value *Account) (*TokenInfo, error) {
	return s.ClaudeAuthorization.RefreshAccountToken(ctx, AccountRecordView(value))
}
func (s *OAuthService) Stop() {
	if s != nil {
		_ = s.StopContext(context.Background())
	}
}
