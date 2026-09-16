// 旧授权入口只投影已迁账号用例；唯一会话与活动状态在 core 中。
package service

import (
	"context"
	"errors"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

type GrokOAuthService struct {
	core         *acctcore.GrokAuthorization
	sessionStore *acctcore.GrokSessionStore
}

func NewGrokOAuthService(proxyRepo ProxyRepository, client GrokOAuthClient, configs ...*config.Config) *GrokOAuthService {
	var cfg *config.Config
	if len(configs) > 0 {
		cfg = configs[0]
	}
	options := acctcore.GrokAuthorizationOptions{PasswordAuthEnabled: func() bool { return cfg != nil && cfg.Gateway.Grok.PasswordAuthEnabled }, GenerateState: xai.GenerateState, GenerateNonce: xai.GenerateNonce, GenerateCodeVerifier: xai.GenerateCodeVerifier, GenerateSessionID: xai.GenerateSessionID, EffectiveRedirectURI: xai.EffectiveRedirectURI, GenerateCodeChallenge: xai.GenerateCodeChallenge, BuildAuthorizationURL: xai.BuildAuthorizationURL, EffectiveClientID: xai.EffectiveClientID, EffectiveScope: xai.EffectiveScope, ParseAuthorizationInput: xai.ParseAuthorizationInput, DecodeJWTClaims: xai.DecodeJWTClaims, JWTClaimString: xai.JWTClaimString, SubscriptionTierFromJWT: xai.SubscriptionTierFromJWT, DefaultClientID: xai.DefaultClientID, DefaultCLIBaseURL: xai.DefaultCLIBaseURL}
	if proxyRepo != nil {
		options.LookupProxy = func(ctx context.Context, id int64) (string, bool, error) {
			p, err := proxyRepo.GetByID(ctx, id)
			if errors.Is(err, ErrProxyNotFound) {
				return "", false, nil
			}
			if err != nil {
				return "", false, err
			}
			if p == nil {
				return "", false, nil
			}
			return p.URL(), true, nil
		}
	}
	core := acctcore.NewGrokAuthorization(client, options)
	return &GrokOAuthService{core: core, sessionStore: core.Store}
}
func (s *GrokOAuthService) WithSessionStore(store *acctcore.GrokSessionStore) *GrokOAuthService {
	if s != nil && store != nil {
		if s.sessionStore != nil {
			s.sessionStore.Stop()
		}
		s.core.Store = store
		s.sessionStore = store
	}
	return s
}
func (s *GrokOAuthService) Core() *acctcore.GrokAuthorization     { return s.core }
func (s *GrokOAuthService) Start()                                { s.core.Start() }
func (s *GrokOAuthService) Stop()                                 { _ = s.core.StopContext(context.Background()) }
func (s *GrokOAuthService) StopContext(ctx context.Context) error { return s.core.StopContext(ctx) }

type GrokOAuthCapabilities = acctcore.GrokOAuthCapabilities

func (s *GrokOAuthService) GetCapabilities() GrokOAuthCapabilities { return s.core.GetCapabilities() }

type GrokAuthURLResult = acctcore.GrokAuthURLResult

func (s *GrokOAuthService) GenerateAuthURL(ctx context.Context, proxyID *int64, redirectURI string) (*GrokAuthURLResult, error) {
	return s.core.GenerateAuthURL(ctx, proxyID, redirectURI)
}

type GrokExchangeCodeInput = acctcore.GrokExchangeCodeInput
type GrokTokenInfo = acctcore.GrokTokenInfo
type GrokPasswordLoginResult = acctcore.GrokPasswordLoginResult

func (s *GrokOAuthService) ExchangeCode(ctx context.Context, input *GrokExchangeCodeInput) (*GrokTokenInfo, error) {
	return s.core.ExchangeCode(ctx, input)
}
func (s *GrokOAuthService) RefreshToken(ctx context.Context, refreshToken, proxyURL, clientID string) (*GrokTokenInfo, error) {
	return s.core.RefreshToken(ctx, refreshToken, proxyURL, clientID)
}
func (s *GrokOAuthService) ValidateRefreshToken(ctx context.Context, refreshToken string, proxyID *int64) (*GrokTokenInfo, error) {
	return s.core.ValidateRefreshToken(ctx, refreshToken, proxyID)
}
func (s *GrokOAuthService) ValidateSSOToken(ctx context.Context, ssoToken string, proxyID *int64) (*GrokTokenInfo, error) {
	return s.core.ValidateSSOToken(ctx, ssoToken, proxyID)
}
func (s *GrokOAuthService) ConvertFromSSO(ctx context.Context, ssoToken string, proxyID *int64) (*GrokTokenInfo, error) {
	return s.core.ConvertFromSSO(ctx, ssoToken, proxyID)
}
func (s *GrokOAuthService) AuthorizePassword(ctx context.Context, email, password string, proxyID *int64) (*GrokTokenInfo, error) {
	return s.core.AuthorizePassword(ctx, email, password, proxyID)
}
func (s *GrokOAuthService) RefreshAccountToken(ctx context.Context, account *Account) (*GrokTokenInfo, error) {
	return s.core.RefreshAccountToken(ctx, AccountRecordView(account))
}
func (s *GrokOAuthService) BuildAccountCredentials(tokenInfo *GrokTokenInfo) map[string]any {
	return s.core.BuildAccountCredentials(tokenInfo)
}
