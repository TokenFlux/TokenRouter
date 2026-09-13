// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	crsprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	slog "log/slog"
	time "time"
)

type CRSSyncService struct {
	core               *acctcore.CRSSync
	oauthService       *OAuthService
	openaiOAuthService *OpenAIOAuthService
	geminiOAuthService *GeminiOAuthService
}

func (s *CRSSyncService) Core() *acctcore.CRSSync         { return s.core }
func (s *CRSSyncService) BindCore(core *acctcore.CRSSync) { s.core = core }
func (s *CRSSyncService) RefreshCRSAccount(ctx context.Context, value *acctcore.Record) map[string]any {
	return s.refreshOAuthToken(ctx, AccountFromRecord(value))
}

// refreshOAuthToken 保留平台交换字段；失败或不适用时返回 nil。
func (s *CRSSyncService) refreshOAuthToken(ctx context.Context, account *Account) map[string]any {
	if account.Type != AccountTypeOAuth {
		return nil
	}

	var newCredentials map[string]any
	var err error

	switch account.Platform {
	case PlatformAnthropic:
		if s.oauthService == nil {
			return nil
		}
		tokenInfo, refreshErr := s.oauthService.RefreshAccountToken(ctx, account)
		if refreshErr != nil {
			err = refreshErr
		} else {
			// 保留现有非令牌配置。
			newCredentials = make(map[string]any)
			for k, v := range account.Credentials {
				newCredentials[k] = v
			}
			// 覆盖本次返回的令牌字段。
			newCredentials["access_token"] = tokenInfo.AccessToken
			newCredentials["token_type"] = tokenInfo.TokenType
			newCredentials["expires_in"] = tokenInfo.ExpiresIn
			newCredentials["expires_at"] = tokenInfo.ExpiresAt
			if tokenInfo.RefreshToken != "" {
				newCredentials["refresh_token"] = tokenInfo.RefreshToken
			}
			if tokenInfo.Scope != "" {
				newCredentials["scope"] = tokenInfo.Scope
			}
		}
	case PlatformOpenAI:
		if s.openaiOAuthService == nil {
			return nil
		}
		tokenInfo, refreshErr := s.openaiOAuthService.RefreshAccountToken(ctx, account)
		if refreshErr != nil {
			err = refreshErr
		} else {
			newCredentials = s.openaiOAuthService.BuildAccountCredentials(tokenInfo)
			// 保留响应未覆盖的非令牌配置。
			for k, v := range account.Credentials {
				if _, exists := newCredentials[k]; !exists {
					newCredentials[k] = v
				}
			}
		}
	case PlatformGemini:
		if s.geminiOAuthService == nil {
			return nil
		}
		tokenInfo, refreshErr := s.geminiOAuthService.RefreshAccountToken(ctx, account)
		if refreshErr != nil {
			err = refreshErr
		} else {
			newCredentials = s.geminiOAuthService.BuildAccountCredentials(tokenInfo)
			for k, v := range account.Credentials {
				if _, exists := newCredentials[k]; !exists {
					newCredentials[k] = v
				}
			}
		}
	default:
		return nil
	}

	if err != nil {
		// 刷新失败不改变已完成的同步结果。
		return nil
	}

	return newCredentials
}

type SyncFromCRSInput = acctcore.SyncFromCRSInput
type SyncFromCRSItemResult = acctcore.SyncFromCRSItemResult
type SyncFromCRSResult = acctcore.SyncFromCRSResult

func (s *CRSSyncService) SyncFromCRS(ctx context.Context, input SyncFromCRSInput) (*SyncFromCRSResult, error) {
	return s.core.SyncFromCRS(ctx, input)
}
func mergeMap(existing map[string]any, updates map[string]any) map[string]any {
	return acctcore.CRSMergeMap(existing, updates)
}
func reconcileCRSOllamaCloudUsageExtra(
	existing *Account,
	targetPlatform, targetType string,
	targetCredentials map[string]any,
	extra map[string]any,
) {
	acctcore.ReconcileCRSOllamaCloudUsageExtra(AccountRecordView(existing), targetPlatform, targetType, targetCredentials, extra)
}

func buildSelectedSet(ids []string) map[string]struct{} { return acctcore.CRSBuildSelectedSet(ids) }
func shouldCreateAccount(crsID string, selectedSet map[string]struct{}) bool {
	return acctcore.CRSShouldCreateAccount(crsID, selectedSet)
}

type PreviewFromCRSResult = acctcore.PreviewFromCRSResult
type CRSPreviewAccount = acctcore.CRSPreviewAccount

func (s *CRSSyncService) PreviewFromCRS(ctx context.Context, input SyncFromCRSInput) (*PreviewFromCRSResult, error) {
	return s.core.PreviewFromCRS(ctx, input)
}

// 旧独立构造只投影配置和记录；生产组合根直接传入唯一账号/代理存储。
func NewCRSSyncService(repo AccountRepository, proxies ProxyRepository, oauth *OAuthService, openai *OpenAIOAuthService, gemini *GeminiOAuthService, cfg *config.Config) *CRSSyncService {
	s := NewCRSPlatformExchange(oauth, openai, gemini)
	s.core = acctcore.NewCRSSync(legacyCRSStore{source: repo}, proxies, newCRSClientFromConfig(cfg), acctcore.CRSOptions{Now: time.Now, Warn: slog.Warn, Refresh: s.CoordinatedCRSRefresh(acctcore.NewOAuthRefreshAPI(legacyRefreshRepository(repo), nil, acctcore.RefreshOptions{Now: time.Now, Warn: slog.Warn}))})
	return s
}
func NewCRSPlatformExchange(oauth *OAuthService, openai *OpenAIOAuthService, gemini *GeminiOAuthService) *CRSSyncService {
	return &CRSSyncService{oauthService: oauth, openaiOAuthService: openai, geminiOAuthService: gemini}
}

// newCRSClientFromConfig 仅兼容旧构造的参数形状。
func newCRSClientFromConfig(cfg *config.Config) *crsprovider.CRSClient {
	options := crsprovider.CRSClientOptions{Configured: cfg != nil}
	if cfg != nil {
		v := cfg.Security.URLAllowlist
		options.AllowlistEnabled = v.Enabled
		options.Hosts = v.CRSHosts
		options.AllowInsecureHTTP = v.AllowInsecureHTTP
		options.AllowPrivateHosts = v.AllowPrivateHosts
	}
	return crsprovider.NewCRSClient(options)
}
