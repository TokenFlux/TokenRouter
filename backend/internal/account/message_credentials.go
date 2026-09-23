package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// MessageCredentialSource 固定绑定原 Claude/Vertex 凭据能力，不拥有第二份缓存或刷新状态。
type MessageCredentialSource struct {
	Claude func(context.Context, *Record) (string, error)
}

// Resolve 在原调用时点选择凭据来源，保留 Messages 与其他入口的资格差异。
// @project-doc docs/interfaces/anthropic_upstream.md#anthropic_account_and_transport
func (s *MessageCredentialSource) Resolve(ctx context.Context, account *Record) (string, string, error) {
	switch account.Type {
	case capability.AccountTypeOAuth, capability.AccountTypeSetupToken:
		// OAuth 与 setup-token 保留原认证分支。
		return s.oauth(ctx, account)
	case capability.AccountTypeAPIKey:
		apiKey := account.GetCredential("api_key")
		if apiKey == "" {
			return "", "", errors.New("api_key not found in credentials")
		}
		return apiKey, "apikey", nil
	case capability.AccountTypeBedrock:
		return "", "bedrock", nil // Bedrock 使用 SigV4 签名或 API Key，由 forwardBedrock 处理
	case capability.AccountTypeServiceAccount:
		if account.Platform != capability.PlatformAnthropic {
			return "", "", fmt.Errorf("unsupported service account platform: %s", account.Platform)
		}
		if s == nil || s.Claude == nil {
			return "", "", errors.New("claude token provider not configured")
		}
		accessToken, err := s.Claude(ctx, account)
		if err != nil {
			return "", "", err
		}
		return accessToken, "service_account", nil
	default:
		return "", "", fmt.Errorf("unsupported account type: %s", account.Type)
	}
}

func (s *MessageCredentialSource) oauth(ctx context.Context, account *Record) (string, string, error) {
	// 对于 Anthropic OAuth 账号，使用 ClaudeTokenProvider 获取缓存的 token
	if account.Platform == capability.PlatformAnthropic && account.Type == capability.AccountTypeOAuth && s != nil && s.Claude != nil {
		accessToken, err := s.Claude(ctx, account)
		if err != nil {
			return "", "", err
		}
		return accessToken, "oauth", nil
	}

	// Grok OAuth 优先使用凭据中的 access_token，后台刷新器负责保持其有效。
	if account.Platform == capability.PlatformGrok && account.Type == capability.AccountTypeOAuth {
		accessToken, err := GrokStoredAccessToken(account)
		if err != nil {
			return "", "", err
		}
		return accessToken, "oauth", nil
	}

	// 其他情况（Gemini 有自己的 TokenProvider，setup-token 类型等）直接从账号读取
	accessToken := account.GetCredential("access_token")
	if accessToken == "" {
		return "", "", errors.New("access_token not found in credentials")
	}
	// Token刷新由后台 TokenRefreshService 处理，此处只返回当前token
	return accessToken, "oauth", nil
}
