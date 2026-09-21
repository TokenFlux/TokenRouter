package account

import (
	"context"
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// TokenCacheInvalidator 使用账号快照清理各平台原有凭据缓存。
type TokenCacheInvalidator interface {
	InvalidateToken(ctx context.Context, account *Record) error
}

// SessionInvalidator 只暴露账号会话失效，不依赖具体供应商构建器。
type SessionInvalidator interface {
	Invalidate(int64)
}

// CompositeTokenCacheInvalidator 保留各平台独立键及尽力删除顺序。
type CompositeTokenCacheInvalidator struct {
	warn               func(string, ...any)
	cache              AccessTokenCache // 统一使用一个缓存接口，通过缓存键前缀区分平台
	qoderTokenProvider SessionInvalidator
}

func NewCompositeTokenCacheInvalidator(cache AccessTokenCache, qoderTokenProvider SessionInvalidator, warn func(string, ...any),
) *CompositeTokenCacheInvalidator {
	return &CompositeTokenCacheInvalidator{
		warn:               warn,
		cache:              cache,
		qoderTokenProvider: qoderTokenProvider,
	}
}

func (c *CompositeTokenCacheInvalidator) InvalidateToken(ctx context.Context, account *Record) error {
	if c == nil || account == nil {
		return nil
	}
	if account.Type != capability.AccountTypeOAuth && !account.IsQoderCosy() {
		return nil
	}

	var keysToDelete []string
	accountIDKey := "account:" + strconv.FormatInt(account.ID, 10)

	switch account.Platform {
	case capability.PlatformGemini:
		// Gemini 可能有两种缓存键：project_id 或 account_id
		// 首次获取 token 时可能没有 project_id，之后自动检测到 project_id 后会使用新 key
		// 刷新时需要同时删除两种可能的 key，确保不会遗留旧缓存
		keysToDelete = append(keysToDelete, GeminiOAuthTokenCacheKey(account))
		keysToDelete = append(keysToDelete, "gemini:"+accountIDKey)
	case capability.PlatformAntigravity:
		// Antigravity 同样可能有两种缓存键
		keysToDelete = append(keysToDelete, AntigravityTokenCacheKey(account))
		keysToDelete = append(keysToDelete, "ag:"+accountIDKey)
	case capability.PlatformOpenAI:
		keysToDelete = append(keysToDelete, OpenAITokenCacheKey(account))
	case capability.PlatformGrok:
		keysToDelete = append(keysToDelete, GrokTokenCacheKey(account))
		keysToDelete = append(keysToDelete, "grok:"+accountIDKey)
	case capability.PlatformAnthropic:
		keysToDelete = append(keysToDelete, ClaudeTokenCacheKey(account))
	case capability.PlatformQoder:
		if account.IsQoderCosy() {
			keysToDelete = append(keysToDelete, QoderTokenCacheKey(account))
			if c.qoderTokenProvider != nil {
				c.qoderTokenProvider.Invalidate(account.ID)
			}
		}
	default:
		return nil
	}

	if c.cache == nil {
		return nil
	}

	// 删除所有可能的缓存键（去重后）
	seen := make(map[string]bool)
	for _, key := range keysToDelete {
		if seen[key] {
			continue
		}
		seen[key] = true
		if err := c.cache.DeleteAccessToken(ctx, key); err != nil {
			if c.warn != nil {
				c.warn("token_cache_delete_failed", "key", key, "account_id", account.ID, "error", err)
			}
		}
	}

	return nil
}
