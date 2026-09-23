package session

import (
	"context"
	"strings"
	"time"
)

// legacyCyberSessionBlockStore 兼容迁移前缓存接口，避免旧部署在升级后静默失去会话屏蔽。
type legacyCyberSessionBlockStore interface {
	SetCyberSessionBlocked(ctx context.Context, key string, ttl time.Duration) error
	IsCyberSessionBlocked(ctx context.Context, key string) (bool, error)
}

type legacyCyberSessionBlockStoreAdapter struct{ legacy legacyCyberSessionBlockStore }

func (a legacyCyberSessionBlockStoreAdapter) SetCyberSessionBlocked(ctx context.Context, scopeKey string, keys []string, ttl time.Duration) error {
	key := strings.TrimSpace(scopeKey)
	if key == "" && len(keys) > 0 {
		key = strings.TrimSpace(keys[0])
	}
	if key == "" {
		return nil
	}
	return a.legacy.SetCyberSessionBlocked(ctx, key, ttl)
}

func (a legacyCyberSessionBlockStoreAdapter) IsCyberSessionScopeActive(ctx context.Context, scopeKey string) (bool, error) {
	if strings.TrimSpace(scopeKey) == "" {
		return false, nil
	}
	return a.legacy.IsCyberSessionBlocked(ctx, scopeKey)
}

func (a legacyCyberSessionBlockStoreAdapter) FindCyberSessionBlocked(ctx context.Context, keys []string) (string, error) {
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		blocked, err := a.legacy.IsCyberSessionBlocked(ctx, key)
		if err != nil {
			return "", err
		}
		if blocked {
			return key, nil
		}
	}
	return "", nil
}

const cyberSessionTranscriptLookupOverflowBlockKey = "transcript_lookup_limit_exceeded"

// AdaptCyberSessionBlockStore 保留可选能力与旧缓存的读取/写入语义，不创建缓存。
func AdaptCyberSessionBlockStore(cache GatewayCache) CyberSessionBlockStore {
	if cache == nil {
		return nil
	}
	if store, ok := cache.(CyberSessionBlockStore); ok {
		return store
	}
	if legacy, ok := cache.(legacyCyberSessionBlockStore); ok {
		return legacyCyberSessionBlockStoreAdapter{legacy: legacy}
	}
	return nil
}
