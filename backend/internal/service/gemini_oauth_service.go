// 旧 Gemini OAuth 入口只投影配置和账号，同一授权状态由 account 拥有。
package service

import (
	"context"
	"fmt"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
)

type GeminiOAuthService struct {
	proxyRepo ProxyRepository
	*accountcore.GeminiAuthorization
	sessionStore *accountcore.GeminiAuthorizationSessions
}

func NewGeminiOAuthService(proxyRepo ProxyRepository, client GeminiOAuthClient, codeassist GeminiCliCodeAssistClient, drive geminicli.DriveClient, cfg *config.Config) *GeminiOAuthService {
	options := accountcore.GeminiAuthorizationOptions{Config: func() geminicli.OAuthConfig {
		return geminicli.OAuthConfig{ClientID: cfg.Gemini.OAuth.ClientID, ClientSecret: cfg.Gemini.OAuth.ClientSecret, Scopes: cfg.Gemini.OAuth.Scopes}
	}, BuiltinClientID: geminicli.GeminiCLIOAuthClientID, CLIRedirectURI: geminicli.GeminiCLIRedirectURI, AIStudioRedirectURI: geminicli.AIStudioOAuthRedirectURI, GenerateState: geminicli.GenerateState, GenerateCodeVerifier: geminicli.GenerateCodeVerifier, GenerateSessionID: geminicli.GenerateSessionID, GenerateCodeChallenge: geminicli.GenerateCodeChallenge, EffectiveOAuthConfig: geminicli.EffectiveOAuthConfig, BuildAuthorizationURL: geminicli.BuildAuthorizationURL, FetchProject: geminicli.FetchProjectIDFromResourceManager, ResolveProxy: func(ctx context.Context, id int64) (string, bool) {
		proxy, err := proxyRepo.GetByID(ctx, id)
		if err != nil || proxy == nil {
			return "", false
		}
		return proxy.URL(), true
	}, Logf: func(format string, args ...any) { logger.LegacyPrintf("service.gemini_oauth", format, args...) }, Printf: func(format string, args ...any) { _, _ = fmt.Printf(format, args...) }}
	core := accountcore.NewGeminiAuthorization(client, codeassist, drive, options)
	return &GeminiOAuthService{GeminiAuthorization: core, sessionStore: core.Store, proxyRepo: proxyRepo}
}
func (s *GeminiOAuthService) Stop() { _ = s.StopContext(context.Background()) }

const GeminiTierGoogleOneFree = accountcore.GeminiTierGoogleOneFree
const GeminiTierGoogleAIPro = accountcore.GeminiTierGoogleAIPro
const GeminiTierGoogleAIUltra = accountcore.GeminiTierGoogleAIUltra
const GeminiTierGCPStandard = accountcore.GeminiTierGCPStandard
const GeminiTierGCPEnterprise = accountcore.GeminiTierGCPEnterprise
const GeminiTierAIStudioFree = accountcore.GeminiTierAIStudioFree
const GeminiTierAIStudioPaid = accountcore.GeminiTierAIStudioPaid
const GeminiTierGoogleOneUnknown = accountcore.GeminiTierGoogleOneUnknown
const GB = accountcore.GB
const TB = accountcore.TB
const StorageTierUnlimited = accountcore.StorageTierUnlimited
const StorageTierAIPremium = accountcore.StorageTierAIPremium
const StorageTierStandard = accountcore.StorageTierStandard
const StorageTierBasic = accountcore.StorageTierBasic
const StorageTierFree = accountcore.StorageTierFree

type GeminiOAuthCapabilities = accountcore.GeminiOAuthCapabilities
type GeminiAuthURLResult = accountcore.GeminiAuthURLResult
type GeminiExchangeCodeInput = accountcore.GeminiExchangeCodeInput
type GeminiTokenInfo = accountcore.GeminiTokenInfo

func (s *GeminiOAuthService) RefreshAccountGoogleOneTier(
	ctx context.Context,
	account *Account,
) (tierID string, extra map[string]any, credentials map[string]any, err error) {
	return s.GeminiAuthorization.RefreshAccountGoogleOneTier(ctx, AccountRecordView(account))
}

func (s *GeminiOAuthService) RefreshAccountToken(ctx context.Context, account *Account) (*GeminiTokenInfo, error) {
	return s.GeminiAuthorization.RefreshAccountToken(ctx, AccountRecordView(account))
}
