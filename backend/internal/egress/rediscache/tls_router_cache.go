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
	tlsFPRouterCacheKey  = "tls_fingerprint_routers"
	tlsFPRouterPubSubKey = "tls_fingerprint_routers_updated"
	tlsFPRouterCacheTTL  = 24 * time.Hour
)

type tlsFingerprintRouterCache struct {
	rdb        *redis.Client
	localCache []*egress.TLSFingerprintRouter
	localMu    sync.RWMutex

	// 订阅协程由缓存自身持有取消句柄，关闭时必须在 Redis 之前完成退出。
	subscriptionMu      sync.Mutex
	subscriptionCancel  context.CancelFunc
	subscriptionWG      sync.WaitGroup
	subscriptionStopped bool
}

// NewTLSFingerprintRouterCache 创建 TLS 路由器缓存。
func NewTLSFingerprintRouterCache(rdb *redis.Client) egress.TLSFingerprintRouterCache {
	return &tlsFingerprintRouterCache{rdb: rdb}
}

// Get 从缓存获取 TLS 路由器列表。
func (c *tlsFingerprintRouterCache) Get(ctx context.Context) ([]*egress.TLSFingerprintRouter, bool) {
	c.localMu.RLock()
	if c.localCache != nil {
		routers := egress.CloneTLSRouters(c.localCache)
		c.localMu.RUnlock()
		return routers, true
	}
	c.localMu.RUnlock()

	data, err := c.rdb.Get(ctx, tlsFPRouterCacheKey).Bytes()
	if err != nil {
		if err != redis.Nil {
			slog.Warn("tls_fp_router_cache_get_failed", "error", err)
		}
		return nil, false
	}

	var routers []*egress.TLSFingerprintRouter
	if err := json.Unmarshal(data, &routers); err != nil {
		slog.Warn("tls_fp_router_cache_unmarshal_failed", "error", err)
		return nil, false
	}

	c.localMu.Lock()
	c.localCache = egress.CloneTLSRouters(routers)
	c.localMu.Unlock()
	return routers, true
}

// Set 设置 TLS 路由器缓存。
func (c *tlsFingerprintRouterCache) Set(ctx context.Context, routers []*egress.TLSFingerprintRouter) error {
	data, err := json.Marshal(routers)
	if err != nil {
		return err
	}
	if err := c.rdb.Set(ctx, tlsFPRouterCacheKey, data, tlsFPRouterCacheTTL).Err(); err != nil {
		return err
	}
	c.localMu.Lock()
	c.localCache = egress.CloneTLSRouters(routers)
	c.localMu.Unlock()
	return nil
}

// Invalidate 使 TLS 路由器缓存失效。
func (c *tlsFingerprintRouterCache) Invalidate(ctx context.Context) error {
	c.localMu.Lock()
	c.localCache = nil
	c.localMu.Unlock()
	return c.rdb.Del(ctx, tlsFPRouterCacheKey).Err()
}

// NotifyUpdate 通知其他实例刷新 TLS 路由器缓存。
func (c *tlsFingerprintRouterCache) NotifyUpdate(ctx context.Context) error {
	return c.rdb.Publish(ctx, tlsFPRouterPubSubKey, "refresh").Err()
}

// SubscribeUpdates 订阅 TLS 路由器缓存更新通知。
func (c *tlsFingerprintRouterCache) SubscribeUpdates(ctx context.Context, handler func()) {
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
		sub := c.rdb.Subscribe(subscriberCtx, tlsFPRouterPubSubKey)
		defer func() { _ = sub.Close() }()
		ch := sub.Channel()
		for {
			select {
			case <-subscriberCtx.Done():
				slog.Debug("tls_fp_router_cache_subscriber_stopped", "reason", "context_done")
				return
			case msg, ok := <-ch:
				if !ok || msg == nil {
					if subscriberCtx.Err() != nil {
						slog.Debug("tls_fp_router_cache_subscriber_stopped", "reason", "context_done")
						return
					}
					slog.Warn("tls_fp_router_cache_subscriber_stopped", "reason", "channel_closed")
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
func (c *tlsFingerprintRouterCache) StopSubscription() {
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
