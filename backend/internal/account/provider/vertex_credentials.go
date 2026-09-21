package provider

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
	"github.com/TokenFlux/TokenRouter/internal/upstream/vertex"
)

// GeminiTokenCacheKey 保留服务账号身份摘要与 OAuth 账号 ID 两种命名空间。
func GeminiTokenCacheKey(value *account.Record) string {
	if value != nil && value.Type == account.AccountTypeServiceAccount {
		if key, err := ParseVertexServiceAccountKey(value); err == nil {
			return VertexServiceAccountCacheKey(value, key)
		}
	}
	return account.GeminiOAuthTokenCacheKey(value)
}

// ParseVertexServiceAccountKey 将账号保存形状交给唯一平台解析器。
func ParseVertexServiceAccountKey(value *account.Record) (*google.ServiceAccountKey, error) {
	raw, err := account.VertexServiceAccountJSON(value)
	if err != nil {
		return nil, err
	}
	return vertex.ParseVertexServiceAccountJSON(raw)
}

// vertexServiceAccountProxyURL 保留显式代理绑定的判断顺序。
func vertexServiceAccountProxyURL(value *account.Record) string {
	if value == nil || value.ProxyID == nil || value.Proxy == nil {
		return ""
	}
	return value.Proxy.URL()
}

// VertexServiceAccountCacheKey 保留无账号及未解析密钥的旧键形状。
func VertexServiceAccountCacheKey(value *account.Record, key *google.ServiceAccountKey) string {
	var id int64
	if value != nil {
		id = value.ID
	}
	if key == nil {
		if value == nil {
			return "vertex:service_account:"
		}
		return account.VertexServiceAccountCacheKey(id, "", "", false)
	}
	return account.VertexServiceAccountCacheKey(id, key.ClientEmail, key.PrivateKeyID, true)
}

// VertexServiceAccountAccessToken 组合账号缓存协调与平台交换，不另建缓存或刷新锁。
func VertexServiceAccountAccessToken(ctx context.Context, cache account.AccessTokenCache, value *account.Record) (string, error) {
	key, err := ParseVertexServiceAccountKey(value)
	if err != nil {
		return "", err
	}
	return account.GetVertexServiceAccountAccessToken(ctx, account.VertexTokenOptions{
		AccountID: value.ID,
		CacheKey:  VertexServiceAccountCacheKey(value, key),
		Cache:     cache,
		Warn:      slog.Warn,
		Exchange: func(ctx context.Context) (string, time.Duration, error) {
			return vertex.ExchangeServiceAccountToken(ctx, key, vertexServiceAccountProxyURL(value))
		},
	})
}
