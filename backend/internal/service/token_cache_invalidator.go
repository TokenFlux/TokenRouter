package service

import (
	"context"
	"log/slog"
	"strconv"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

type TokenCacheInvalidator interface {
	InvalidateToken(ctx context.Context, account *Account) error
}

type CompositeTokenCacheInvalidator struct {
	cache              GeminiTokenCache // 统一使用一个缓存接口，通过缓存键前缀区分平台
	qoderTokenProvider *QoderTokenProvider
}

func NewCompositeTokenCacheInvalidator(cache GeminiTokenCache, qoderTokenProvider *QoderTokenProvider) *CompositeTokenCacheInvalidator {
	return &CompositeTokenCacheInvalidator{
		cache:              cache,
		qoderTokenProvider: qoderTokenProvider,
	}
}

func (c *CompositeTokenCacheInvalidator) InvalidateToken(ctx context.Context, account *Account) error {
	if c == nil || account == nil {
		return nil
	}
	if account.Type != AccountTypeOAuth && !account.IsQoderCosy() {
		return nil
	}

	var keysToDelete []string
	accountIDKey := "account:" + strconv.FormatInt(account.ID, 10)

	switch account.Platform {
	case PlatformGemini:
		// Gemini 可能有两种缓存键：project_id 或 account_id
		// 首次获取 token 时可能没有 project_id，之后自动检测到 project_id 后会使用新 key
		// 刷新时需要同时删除两种可能的 key，确保不会遗留旧缓存
		keysToDelete = append(keysToDelete, GeminiTokenCacheKey(account))
		keysToDelete = append(keysToDelete, "gemini:"+accountIDKey)
	case PlatformAntigravity:
		// Antigravity 同样可能有两种缓存键
		keysToDelete = append(keysToDelete, AntigravityTokenCacheKey(account))
		keysToDelete = append(keysToDelete, "ag:"+accountIDKey)
	case PlatformOpenAI:
		keysToDelete = append(keysToDelete, OpenAITokenCacheKey(account))
	case PlatformGrok:
		keysToDelete = append(keysToDelete, GrokTokenCacheKey(account))
		keysToDelete = append(keysToDelete, "grok:"+accountIDKey)
	case PlatformAnthropic:
		keysToDelete = append(keysToDelete, ClaudeTokenCacheKey(account))
	case PlatformQoder:
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
			slog.Warn("token_cache_delete_failed", "key", key, "account_id", account.ID, "error", err)
		}
	}

	return nil
}

func CheckTokenVersion(ctx context.Context, account *Account, repo AccountRepository) (latestAccount *Account, isStale bool) {
	latest, stale := accountcore.CheckTokenVersion(ctx, AccountRecordView(account), legacyRefreshRepository(repo), slog.Debug)
	return AccountFromRecord(latest), stale
}
