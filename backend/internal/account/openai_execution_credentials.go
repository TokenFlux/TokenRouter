package account

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// OpenAIExecutionCredentials 绑定受控母账号读取与既有 token 源，不缓存凭据或重建刷新协调器。
type OpenAIExecutionCredentials struct {
	Parent func(context.Context, int64) (*Record, error)
	OpenAI func(context.Context, *Record) (string, error)
	Grok   func(context.Context, *Record) (string, error)
}

// Resolve 保留影子读透、Agent Identity、Grok 与 setup-token 的原认证及降级顺序。
func (s *OpenAIExecutionCredentials) Resolve(ctx context.Context, account *Record) (string, string, error) {
	if account.IsShadow() {
		credAccount, err := ResolveCredentialRecord(ctx, s.Parent, account)
		if err != nil {
			return "", "", err
		}
		account = credAccount
	}
	switch account.Type {
	case capability.AccountTypeOAuth:
		if account.IsOpenAIAgentIdentity() {
			return "", OpenAIAuthModeAgentIdentity, nil
		}
		if account.Platform == capability.PlatformGrok {
			if s != nil && s.Grok != nil {
				accessToken, err := s.Grok(ctx, account)
				if err != nil {
					return "", "", err
				}
				return accessToken, "oauth", nil
			}
			accessToken := account.GetGrokAccessToken()
			if accessToken == "" {
				return "", "", errors.New("access_token not found in credentials")
			}
			return accessToken, "oauth", nil
		}
		// 使用 TokenProvider 获取缓存的 token
		if s != nil && s.OpenAI != nil {
			accessToken, err := s.OpenAI(ctx, account)
			if err != nil {
				return "", "", err
			}
			return accessToken, "oauth", nil
		}
		// 降级：TokenProvider 未配置时直接从账号读取
		accessToken := account.GetOpenAIAccessToken()
		if accessToken == "" {
			return "", "", errors.New("access_token not found in credentials")
		}
		return accessToken, "oauth", nil
	case capability.AccountTypeSetupToken:
		if !account.IsOpenAIOAuthLike() {
			return "", "", fmt.Errorf("unsupported account type: %s", account.Type)
		}
		// OpenAI setup-token 仅用于推理，使用原 Codex OAuth 转发协议但不进入刷新生命周期。
		accessToken := account.GetOpenAIAccessToken()
		if accessToken == "" {
			return "", "", errors.New("access_token not found in credentials")
		}
		return accessToken, "oauth", nil
	case capability.AccountTypeAPIKey:
		if account.Platform == capability.PlatformGrok {
			apiKey := strings.TrimSpace(account.GetCredential("api_key"))
			if apiKey == "" {
				return "", "", errors.New("api_key not found in credentials")
			}
			return apiKey, "apikey", nil
		}
		apiKey := strings.TrimSpace(account.GetOpenAIProtocolAPIKey())
		if apiKey == "" {
			return "", "", errors.New("api_key not found in credentials")
		}
		return apiKey, "apikey", nil
	default:
		return "", "", fmt.Errorf("unsupported account type: %s", account.Type)
	}
}
