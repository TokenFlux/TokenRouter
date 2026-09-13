// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	errors "errors"
	fmt "fmt"
	strconv "strconv"
	strings "strings"
)

// ManualCredentialExchangeOptions 只绑定旧供应商 SDK，账号管理与持久化不由此适配器拥有。
type ManualCredentialExchangeOptions struct {
	Admin       AdminService
	Claude      *OAuthService
	OpenAI      *OpenAIOAuthService
	Gemini      *GeminiOAuthService
	Antigravity *AntigravityOAuthService
	Grok        GrokOAuthTokenService
	Qoder       func(context.Context, *Account) (map[string]any, error)
}
type manualQoderRefresher interface {
	Refresh(context.Context, *Account) (map[string]any, error)
}
type manualQoderRefreshFunc func(context.Context, *Account) (map[string]any, error)

func (f manualQoderRefreshFunc) Refresh(ctx context.Context, v *Account) (map[string]any, error) {
	return f(ctx, v)
}

type ManualCredentialExchange struct {
	options                 ManualCredentialExchangeOptions
	adminService            AdminService
	oauthService            *OAuthService
	openaiOAuthService      *OpenAIOAuthService
	geminiOAuthService      *GeminiOAuthService
	antigravityOAuthService *AntigravityOAuthService
	grokOAuthService        GrokOAuthTokenService
}

func NewManualCredentialExchange(o ManualCredentialExchangeOptions) *ManualCredentialExchange {
	return &ManualCredentialExchange{options: o, adminService: o.Admin, oauthService: o.Claude, openaiOAuthService: o.OpenAI, geminiOAuthService: o.Gemini, antigravityOAuthService: o.Antigravity, grokOAuthService: o.Grok}
}
func (h *ManualCredentialExchange) qoderRefresher() manualQoderRefresher {
	if h.options.Qoder != nil {
		return manualQoderRefreshFunc(h.options.Qoder)
	}
	return NewQoderTokenRefresherForAdmin(h.adminService, nil)
}

// Refresh 保留各平台响应转换、错误文本及 AG 项目缺失信号；S09 迁入具体平台 Adapter。
func (h *ManualCredentialExchange) Refresh(ctx context.Context, account *Account) (map[string]any, bool, error) {
	var newCredentials map[string]any
	if account.IsQoderCosy() {
		refresher := h.qoderRefresher()
		credentials, err := refresher.Refresh(ctx, account)
		if err != nil {
			return nil, false, err
		}
		newCredentials = credentials
	} else if account.IsOpenAI() {
		tokenInfo, err := h.openaiOAuthService.RefreshAccountToken(ctx, account)
		if err != nil {
			// 刷新失败但 access_token 可能仍有效，尝试设置隐私
			return nil, false, err
		}

		newCredentials = h.openaiOAuthService.BuildAccountCredentials(tokenInfo)
		for k, v := range account.Credentials {
			if _, exists := newCredentials[k]; !exists {
				newCredentials[k] = v
			}
		}
		newCredentials = NormalizeOpenAIPersonalAccessTokenCredentials(account, tokenInfo, newCredentials)
	} else if account.Platform == PlatformGemini {
		tokenInfo, err := h.geminiOAuthService.RefreshAccountToken(ctx, account)
		if err != nil {
			return nil, false, fmt.Errorf("failed to refresh credentials: %w", err)
		}

		newCredentials = h.geminiOAuthService.BuildAccountCredentials(tokenInfo)
		for k, v := range account.Credentials {
			if _, exists := newCredentials[k]; !exists {
				newCredentials[k] = v
			}
		}
	} else if account.Platform == PlatformAntigravity {
		tokenInfo, err := h.antigravityOAuthService.RefreshAccountToken(ctx, account)
		if err != nil {
			return nil, false, err
		}

		newCredentials = h.antigravityOAuthService.BuildAccountCredentials(tokenInfo)
		for k, v := range account.Credentials {
			if _, exists := newCredentials[k]; !exists {
				newCredentials[k] = v
			}
		}

		// 如果 project_id 获取失败，更新凭证但不标记为 error
		if tokenInfo.ProjectIDMissing {
			return newCredentials, true, nil
		}

	} else if account.Platform == PlatformGrok {
		if h.grokOAuthService == nil {
			return nil, false, errors.New("grok OAuth service is not configured")
		}
		tokenInfo, err := h.grokOAuthService.RefreshAccountToken(ctx, account)
		if err != nil {
			return nil, false, fmt.Errorf("failed to refresh Grok credentials: %w", err)
		}

		newCredentials = MergeCredentials(account.Credentials, h.grokOAuthService.BuildAccountCredentials(tokenInfo))
		if baseURL := strings.TrimSpace(account.GetCredential("base_url")); baseURL != "" {
			newCredentials["base_url"] = baseURL
		}
	} else {
		// Claude 交换保持原 token 字段形状及空白值过滤。
		tokenInfo, err := h.oauthService.RefreshAccountToken(ctx, account)
		if err != nil {
			return nil, false, err
		}

		// 保留非令牌配置，例如 intercept_warmup_requests。
		newCredentials = make(map[string]any)
		for k, v := range account.Credentials {
			newCredentials[k] = v
		}

		// 只更新原先认可的令牌字段。
		newCredentials["access_token"] = tokenInfo.AccessToken
		newCredentials["token_type"] = tokenInfo.TokenType
		newCredentials["expires_in"] = strconv.FormatInt(tokenInfo.ExpiresIn, 10)
		newCredentials["expires_at"] = strconv.FormatInt(tokenInfo.ExpiresAt, 10)
		if strings.TrimSpace(tokenInfo.RefreshToken) != "" {
			newCredentials["refresh_token"] = tokenInfo.RefreshToken
		}
		if strings.TrimSpace(tokenInfo.Scope) != "" {
			newCredentials["scope"] = tokenInfo.Scope
		}
	}

	return newCredentials, false, nil
}
