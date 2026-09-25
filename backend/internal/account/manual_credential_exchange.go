package account

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// ManualCredentialExchange 只合并本次交换结果，账号持久化与锁由管理用例拥有。
type ManualCredentialExchange struct {
	Claude      *ClaudeAuthorization
	OpenAI      *OpenAIAuthorization
	Gemini      *GeminiAuthorization
	Antigravity *AntigravityAuthorization
	Grok        GrokRefreshTokenService
	Qoder       func(context.Context, *Record) (map[string]any, error)
}

// Refresh 保留各平台字段类型、缺省值、错误文本与项目缺失信号。
func (h *ManualCredentialExchange) Refresh(ctx context.Context, account *Record) (map[string]any, bool, error) {
	var newCredentials map[string]any
	if account.IsQoderCosy() {
		credentials, err := h.Qoder(ctx, account)
		if err != nil {
			return nil, false, err
		}
		newCredentials = credentials
	} else if account.IsOpenAI() {
		tokenInfo, err := h.OpenAI.RefreshAccountToken(ctx, account)
		if err != nil {
			// 刷新失败但 access_token 可能仍有效，尝试设置隐私
			return nil, false, err
		}

		newCredentials = BuildOpenAIAccountCredentials(tokenInfo)
		for k, v := range account.Credentials {
			if _, exists := newCredentials[k]; !exists {
				newCredentials[k] = v
			}
		}
		newCredentials = NormalizeOpenAIPersonalAccessTokenCredentials(account, tokenInfo, newCredentials)
	} else if account.Platform == capability.PlatformGemini {
		tokenInfo, err := h.Gemini.RefreshAccountToken(ctx, account)
		if err != nil {
			return nil, false, fmt.Errorf("failed to refresh credentials: %w", err)
		}

		newCredentials = h.Gemini.BuildAccountCredentials(tokenInfo)
		for k, v := range account.Credentials {
			if _, exists := newCredentials[k]; !exists {
				newCredentials[k] = v
			}
		}
	} else if account.Platform == capability.PlatformAntigravity {
		tokenInfo, err := h.Antigravity.RefreshAccountToken(ctx, account)
		if err != nil {
			return nil, false, err
		}

		newCredentials = h.Antigravity.BuildAccountCredentials(tokenInfo)
		for k, v := range account.Credentials {
			if _, exists := newCredentials[k]; !exists {
				newCredentials[k] = v
			}
		}

		// 如果 project_id 获取失败，更新凭证但不标记为 error
		if tokenInfo.ProjectIDMissing {
			return newCredentials, true, nil
		}

	} else if account.Platform == capability.PlatformGrok {
		if h.Grok == nil {
			return nil, false, errors.New("grok OAuth service is not configured")
		}
		tokenInfo, err := h.Grok.RefreshAccountToken(ctx, account)
		if err != nil {
			return nil, false, fmt.Errorf("failed to refresh Grok credentials: %w", err)
		}

		newCredentials = MergeCredentials(account.Credentials, h.Grok.BuildAccountCredentials(tokenInfo))
		if baseURL := strings.TrimSpace(account.GetCredential("base_url")); baseURL != "" {
			newCredentials["base_url"] = baseURL
		}
	} else {
		// Claude 交换保持原 token 字段形状及空白值过滤。
		tokenInfo, err := h.Claude.RefreshAccountToken(ctx, account)
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
