package service

import (
	"context"
	"log/slog"
	"strings"
	"sync"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// OpenAIOAuthService handles OpenAI OAuth authentication flows
type OpenAIOAuthService struct {
	coreOnce             sync.Once
	core                 *accountcore.OpenAIAuthorization
	sessionStore         *accountcore.OpenAISessionStore
	proxyRepo            ProxyRepository
	oauthClient          OpenAIOAuthClient
	privacyClientFactory PrivacyClientFactory // 用于调用 chatgpt.com/backend-api（ImpersonateChrome）
	settingService       *SettingService
	tlsFPRouterReader    OpenAIOAuthTokenRouterReader
	tlsFPProfileResolver OpenAIOAuthTokenProfileResolver
}

// NewOpenAIOAuthService creates a new OpenAI OAuth service
func NewOpenAIOAuthService(proxyRepo ProxyRepository, oauthClient OpenAIOAuthClient) *OpenAIOAuthService {
	return &OpenAIOAuthService{
		sessionStore: accountcore.NewOpenAISessionStore(),
		proxyRepo:    proxyRepo,
		oauthClient:  oauthClient,
	}
}

// SetPrivacyClientFactory 注入 ImpersonateChrome 客户端工厂，
// 用于调用 chatgpt.com/backend-api 获取账号信息（plan_type 等）。
func (s *OpenAIOAuthService) SetPrivacyClientFactory(factory PrivacyClientFactory) {
	s.privacyClientFactory = factory
}

// SetTokenTLSRouterDeps 注入 ChatGPT OAuth token 请求指纹配置所需的只读依赖。
func (s *OpenAIOAuthService) SetTokenTLSRouterDeps(settingService *SettingService, routerReader OpenAIOAuthTokenRouterReader, profileResolver OpenAIOAuthTokenProfileResolver) {
	s.settingService = settingService
	s.tlsFPRouterReader = routerReader
	s.tlsFPProfileResolver = profileResolver
}

type OpenAIAuthURLResult = accountcore.OpenAIAuthURLResult

func (s *OpenAIOAuthService) GenerateAuthURL(ctx context.Context, proxyID *int64, redirectURI, platform string) (*OpenAIAuthURLResult, error) {
	return s.Core().GenerateAuthURL(ctx, proxyID, redirectURI, platform)
}

type OpenAIExchangeCodeInput = accountcore.OpenAIExchangeCodeInput

type OpenAITokenInfo = accountcore.OpenAITokenInfo

func (s *OpenAIOAuthService) ExchangeCode(ctx context.Context, input *OpenAIExchangeCodeInput) (*OpenAITokenInfo, error) {
	return s.Core().ExchangeCode(ctx, input)
}

func (s *OpenAIOAuthService) RefreshToken(ctx context.Context, refreshToken string, proxyURL string) (*OpenAITokenInfo, error) {
	return s.Core().RefreshToken(ctx, refreshToken, proxyURL)
}

func (s *OpenAIOAuthService) RefreshTokenWithClientID(ctx context.Context, refreshToken string, proxyURL string, clientID string) (*OpenAITokenInfo, error) {
	return s.Core().RefreshTokenWithClientID(ctx, refreshToken, proxyURL, clientID)
}

func (s *OpenAIOAuthService) RefreshTokenWithClientIDAndRouter(ctx context.Context, refreshToken string, proxyURL string, clientID string, routerID *int64) (*OpenAITokenInfo, error) {
	return s.Core().RefreshTokenWithClientIDAndRouter(ctx, refreshToken, proxyURL, clientID, routerID)
}

func (s *OpenAIOAuthService) resolveChatGPTOAuthTokenRequestOptions(ctx context.Context, routerID int64, account *Account) []OpenAIOAuthTokenRequestOptions {
	if s == nil || routerID <= 0 || s.tlsFPRouterReader == nil {
		return nil
	}
	router := s.tlsFPRouterReader.GetRuntimeRouter(routerID)
	if router == nil || !router.Enabled {
		return nil
	}

	tokenUA := strings.TrimSpace(router.ChatGPTOAuthTokenUserAgent)
	tokenProfileID := router.ChatGPTOAuthTokenTLSFingerprintProfileID
	if tokenUA == "" && tokenProfileID == nil {
		return nil
	}

	option := OpenAIOAuthTokenRequestOptions{
		UserAgent: tokenUA,
	}
	if option.UserAgent == "" && s.settingService != nil {
		option.UserAgent = strings.TrimSpace(s.settingService.GetOpenAICodexUserAgent(ctx))
	}
	if account != nil {
		option.AccountID = account.ID
		option.AccountConcurrency = account.Concurrency
	}
	if tokenProfileID != nil && s.tlsFPProfileResolver != nil {
		if profile, ok := s.tlsFPProfileResolver.ResolveTokenTLSProfileByID(*tokenProfileID); ok {
			option.TLSProfile = profile
		}
	}
	if option.UserAgent == "" && option.TLSProfile == nil {
		return nil
	}
	return []OpenAIOAuthTokenRequestOptions{option}
}

func (s *OpenAIOAuthService) enrichTokenInfo(ctx context.Context, tokenInfo *OpenAITokenInfo, proxyURL string) {
	s.Core().EnrichTokenInfo(ctx, tokenInfo, proxyURL)
}

func shouldApplyChatGPTAccountInfoPlanType(current, candidate string) bool {
	return accountcore.ShouldApplyChatGPTAccountInfoPlanType(current, candidate)
}

func chatGPTAccountInfoBelongsToTokenAccount(tokenInfo *OpenAITokenInfo, info *ChatGPTAccountInfo) bool {
	return accountcore.ChatGPTAccountInfoBelongsToTokenAccount(tokenInfo, info)
}

func (s *OpenAIOAuthService) RefreshAccountToken(ctx context.Context, account *Account) (*OpenAITokenInfo, error) {
	return s.Core().RefreshAccountToken(ctx, AccountRecordView(account))
}

func (s *OpenAIOAuthService) BuildAccountCredentials(tokenInfo *OpenAITokenInfo) map[string]any {
	return accountcore.BuildOpenAIAccountCredentials(tokenInfo)
}

func (s *OpenAIOAuthService) Stop() { _ = s.StopContext(context.Background()) }

func (s *OpenAIOAuthService) Start() { s.Core().Start() }

// Core 绑定唯一账号授权实例；闭包按原时机读取旧装配设置与客户端。
func (s *OpenAIOAuthService) Core() *accountcore.OpenAIAuthorization {
	if s == nil {
		return nil
	}
	s.coreOnce.Do(func() {
		options := accountcore.OpenAIAuthOptions{
			ClientID: openai.ClientID, DefaultRedirectURI: openai.DefaultRedirectURI,
			GenerateState: openai.GenerateState, GenerateCodeVerifier: openai.GenerateCodeVerifier, GenerateSessionID: openai.GenerateSessionID,
			GenerateCodeChallenge: openai.GenerateCodeChallenge, OAuthClientConfigByPlatform: openai.OAuthClientConfigByPlatform, BuildAuthorizationURLForPlatform: openai.BuildAuthorizationURLForPlatform,
			ProxyAvailable: func() bool { return s.proxyRepo != nil },
			ProxyURL: func(ctx context.Context, id int64) (string, bool, error) {
				proxy, err := s.proxyRepo.GetByID(ctx, id)
				if err != nil || proxy == nil {
					return "", false, err
				}
				return proxy.URL(), true, nil
			},
			Exchange: func(ctx context.Context, code, verifier, redirect, proxy, clientID string, routerID int64) (*protocolopenai.OAuthTokenResponse, error) {
				opts := s.resolveChatGPTOAuthTokenRequestOptions(ctx, routerID, nil)
				return s.oauthClient.ExchangeCode(ctx, code, verifier, redirect, proxy, clientID, opts...)
			},
			Refresh: func(ctx context.Context, refresh, proxy, clientID string, routerID int64, record *accountcore.Record) (*protocolopenai.OAuthTokenResponse, error) {
				opts := s.resolveChatGPTOAuthTokenRequestOptions(ctx, routerID, AccountFromRecord(record))
				return s.oauthClient.RefreshTokenWithClientID(ctx, refresh, proxy, clientID, opts...)
			},
			ParseIDToken: openai.ParseIDToken, DecodeIDToken: openai.DecodeIDToken,
			PrivacyAvailable: func() bool { return s.privacyClientFactory != nil },
			FetchAccountInfo: func(ctx context.Context, token, proxy, org string) *protocolopenai.ChatGPTAccountInfo {
				return fetchChatGPTAccountInfo(ctx, s.privacyClientFactory, token, proxy, org)
			},
			FetchSubscription: func(ctx context.Context, token, proxy, id string) string {
				return fetchChatGPTSubscriptionExpiresAt(ctx, s.privacyClientFactory, token, proxy, id)
			},
			DisableTraining: func(ctx context.Context, token, proxy string) string {
				return disableOpenAITraining(ctx, s.privacyClientFactory, token, proxy)
			},
			ValidatePAT: s.ValidateCodexPersonalAccessToken, Warn: slog.Warn,
		}
		s.core = accountcore.NewOpenAIAuthorization(s.sessionStore, options)
	})
	return s.core
}

// StopContext 由组合根传入剩余清理预算。
func (s *OpenAIOAuthService) StopContext(ctx context.Context) error { return s.Core().StopContext(ctx) }
