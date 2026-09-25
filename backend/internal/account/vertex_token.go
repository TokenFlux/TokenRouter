// Vertex 迁移保留原协议及取消边界，旧入口仅投影。
package account

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

func VertexServiceAccountCacheKey(accountID int64, email, keyID string, hasKey bool) string {
	fingerprint := ""
	if hasKey {
		sum := sha256.Sum256([]byte(email + "\x00" + keyID))
		fingerprint = hex.EncodeToString(sum[:8])
	}
	if fingerprint == "" {
		fingerprint = fmt.Sprintf("account:%d", accountID)
	}
	return "vertex:service_account:" + fingerprint
}

const VertexLockWaitTime = 200 * time.Millisecond

// VertexTokenOptions 固定一次请求的身份与交换端口；不复制缓存实例。
type VertexTokenOptions struct {
	AccountID int64
	CacheKey  string
	Cache     AccessTokenCache
	Exchange  func(context.Context) (string, time.Duration, error)
	Warn      func(string, ...any)
}

// GetVertexServiceAccountAccessToken 管理凭据缓存、刷新锁与故障降级，等锁响应 context 取消。
// @project-doc docs/interfaces/gemini_upstream.md#vertex_service_account_execution
func GetVertexServiceAccountAccessToken(ctx context.Context, options VertexTokenOptions) (string, error) {
	if options.Cache != nil {
		if token, err := options.Cache.GetAccessToken(ctx, options.CacheKey); err == nil && strings.TrimSpace(token) != "" {
			return token, nil
		}
	}
	locked := false
	if options.Cache != nil {
		var lockErr error
		locked, lockErr = options.Cache.AcquireRefreshLock(ctx, options.CacheKey, 30*time.Second)
		if lockErr == nil && locked {
			defer func() { _ = options.Cache.ReleaseRefreshLock(ctx, options.CacheKey) }()
		} else if lockErr != nil {
			if options.Warn != nil {
				options.Warn("vertex_service_account_token_lock_failed", "account_id", options.AccountID, "error", lockErr)
			}
		} else {
			timer := time.NewTimer(VertexLockWaitTime)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", ctx.Err()
			case <-timer.C:
			}
			// 即使定时器与取消同时就绪，也不能继续回读缓存或交换。
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if token, err := options.Cache.GetAccessToken(ctx, options.CacheKey); err == nil && strings.TrimSpace(token) != "" {
				return token, nil
			}
		}
	}

	accessToken, ttl, err := options.Exchange(ctx)
	if err != nil {
		return "", err
	}
	if options.Cache != nil {
		_ = options.Cache.SetAccessToken(ctx, options.CacheKey, accessToken, ttl)
	}
	return accessToken, nil
}
