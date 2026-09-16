// Claude 刷新资格与合并归账号，交换由授权端口完成。
package account

import (
	"context"
	strconv "strconv"
	"time"
)

// CanRefresh 检查是否能处理此账号
// 处理 anthropic 平台的 oauth 与 setup-token 类型账号。
// 两者的 access_token 均为短期令牌（expires_in=28800，即 8h），到期都需刷新；
// setup-token 之前被排除会导致其 access_token 过期后请求 401。
// 此处与手动刷新入口（account.IsOAuth()）保持一致，实际是否刷新由 NeedsRefresh
// 基于 expires_at 门控，并在分布式锁保护下执行，不会造成过度刷新。
func CanRefreshClaude(account *Record) bool {
	return account.Platform == PlatformAnthropic && account.IsOAuth()
}

// NeedsRefresh 检查token是否需要刷新
// 基于 expires_at 字段判断是否在刷新窗口内
func NeedsRefreshClaude(account *Record, refreshWindow time.Duration) bool {
	expiresAt := account.GetCredentialAsTime("expires_at")
	if expiresAt == nil {
		return false
	}
	return time.Until(*expiresAt) < refreshWindow
}

// Refresh 执行token刷新
// 保留原有credentials中的所有字段，只更新token相关字段
func RefreshClaudeCredentials(ctx context.Context, account *Record, exchange func(context.Context, *Record) (*ClaudeTokenInfo, error)) (map[string]any, error) {
	tokenInfo, err := exchange(ctx, account)
	if err != nil {
		return nil, err
	}

	newCredentials := BuildClaudeAccountCredentials(tokenInfo)
	newCredentials = MergeCredentials(account.Credentials, newCredentials)

	return newCredentials, nil
}

// BuildClaudeAccountCredentials 为 Claude 平台构建 OAuth credentials map
// 消除 Claude 平台没有 BuildAccountCredentials 方法的问题
func BuildClaudeAccountCredentials(tokenInfo *ClaudeTokenInfo) map[string]any {
	creds := map[string]any{
		"access_token": tokenInfo.AccessToken,
		"token_type":   tokenInfo.TokenType,
		"expires_in":   strconv.FormatInt(tokenInfo.ExpiresIn, 10),
		"expires_at":   strconv.FormatInt(tokenInfo.ExpiresAt, 10),
	}
	if tokenInfo.RefreshToken != "" {
		creds["refresh_token"] = tokenInfo.RefreshToken
	}
	if tokenInfo.Scope != "" {
		creds["scope"] = tokenInfo.Scope
	}
	return creds
}
