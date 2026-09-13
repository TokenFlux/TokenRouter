// 本文件维护 rediscache 的所属能力；兼容入口复用唯一实现。
package rediscache

import (
	context "context"
	json "encoding/json"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	redis "github.com/redis/go-redis/v9"
	slog "log/slog"
	sync "sync"
	time "time"
)

const (
	tlsFPProfileCacheKey  = "tls_fingerprint_profiles"
	tlsFPProfilePubSubKey = "tls_fingerprint_profiles_updated"
	tlsFPProfileCacheTTL  = 24 * time.Hour
)

type tlsFingerprintProfileCache struct {
	rdb        *redis.Client
	localCache []*egress.TLSFingerprintProfile
	localMu    sync.RWMutex

	// 订阅协程由缓存自身持有取消句柄，关闭时必须在 Redis 之前完成退出。
	subscriptionMu      sync.Mutex
	subscriptionCancel  context.CancelFunc
	subscriptionWG      sync.WaitGroup
	subscriptionStopped bool
}

// NewTLSFingerprintProfileCache 创建 TLS 指纹模板缓存
func NewTLSFingerprintProfileCache(rdb *redis.Client) egress.TLSFingerprintProfileCache {
	return &tlsFingerprintProfileCache{
		rdb: rdb,
	}
}

// Get 从缓存获取模板列表
func (c *tlsFingerprintProfileCache) Get(ctx context.Context) ([]*egress.TLSFingerprintProfile, bool) {
	c.localMu.RLock()
	if c.localCache != nil {
		profiles := egress.CloneTLSProfiles(c.localCache)
		c.localMu.RUnlock()
		return profiles, true
	}
	c.localMu.RUnlock()

	data, err := c.rdb.Get(ctx, tlsFPProfileCacheKey).Bytes()
	if err != nil {
		if err != redis.Nil {
			slog.Warn("tls_fp_profile_cache_get_failed", "error", err)
		}
		return nil, false
	}

	var profiles []*egress.TLSFingerprintProfile
	if err := json.Unmarshal(data, &profiles); err != nil {
		slog.Warn("tls_fp_profile_cache_unmarshal_failed", "error", err)
		return nil, false
	}

	c.localMu.Lock()
	c.localCache = egress.CloneTLSProfiles(profiles)
	c.localMu.Unlock()

	return profiles, true
}

// Set 设置缓存
func (c *tlsFingerprintProfileCache) Set(ctx context.Context, profiles []*egress.TLSFingerprintProfile) error {
	data, err := json.Marshal(profiles)
	if err != nil {
		return err
	}

	if err := c.rdb.Set(ctx, tlsFPProfileCacheKey, data, tlsFPProfileCacheTTL).Err(); err != nil {
		return err
	}

	c.localMu.Lock()
	c.localCache = egress.CloneTLSProfiles(profiles)
	c.localMu.Unlock()

	return nil
}

// Invalidate 使缓存失效
func (c *tlsFingerprintProfileCache) Invalidate(ctx context.Context) error {
	c.localMu.Lock()
	c.localCache = nil
	c.localMu.Unlock()

	return c.rdb.Del(ctx, tlsFPProfileCacheKey).Err()
}

// NotifyUpdate 通知其他实例刷新缓存
func (c *tlsFingerprintProfileCache) NotifyUpdate(ctx context.Context) error {
	return c.rdb.Publish(ctx, tlsFPProfilePubSubKey, "refresh").Err()
}

// SubscribeUpdates 订阅缓存更新通知
func (c *tlsFingerprintProfileCache) SubscribeUpdates(ctx context.Context, handler func()) {
	subscriberCtx, cancel := context.WithCancel(ctx)
	c.subscriptionMu.Lock()
	if c.subscriptionCancel != nil || c.subscriptionStopped {
		c.subscriptionMu.Unlock()
		cancel()
		return
	}
	c.subscriptionCancel = cancel
	c.subscriptionWG.Add(1)
	c.subscriptionMu.Unlock()

	go func() {
		defer c.subscriptionWG.Done()
		sub := c.rdb.Subscribe(subscriberCtx, tlsFPProfilePubSubKey)
		defer func() { _ = sub.Close() }()

		ch := sub.Channel()
		for {
			select {
			case <-subscriberCtx.Done():
				slog.Debug("tls_fp_profile_cache_subscriber_stopped", "reason", "context_done")
				return
			case msg, ok := <-ch:
				if !ok || msg == nil {
					if subscriberCtx.Err() != nil {
						slog.Debug("tls_fp_profile_cache_subscriber_stopped", "reason", "context_done")
						return
					}
					slog.Warn("tls_fp_profile_cache_subscriber_stopped", "reason", "channel_closed")
					return
				}
				c.localMu.Lock()
				c.localCache = nil
				c.localMu.Unlock()

				handler()
			}
		}
	}()
}

// StopSubscription 取消并等待缓存订阅协程退出。
func (c *tlsFingerprintProfileCache) StopSubscription() {
	c.subscriptionMu.Lock()
	cancel := c.subscriptionCancel
	c.subscriptionStopped = true
	c.subscriptionMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	c.subscriptionWG.Wait()
}
