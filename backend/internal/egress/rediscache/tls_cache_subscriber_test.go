package rediscache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type stoppableTLSFingerprintCache interface {
	SubscribeUpdates(context.Context, func())
	StopSubscription()
}

func TestTLSFingerprintCacheSubscriberStopsBeforeRedisClose(t *testing.T) {
	tests := []struct {
		name     string
		channel  string
		newCache func(*redis.Client) stoppableTLSFingerprintCache
	}{
		{
			name:    "profile",
			channel: tlsFPProfilePubSubKey,
			newCache: func(client *redis.Client) stoppableTLSFingerprintCache {
				return &tlsFingerprintProfileCache{rdb: client}
			},
		},
		{
			name:    "router",
			channel: tlsFPRouterPubSubKey,
			newCache: func(client *redis.Client) stoppableTLSFingerprintCache {
				return &tlsFingerprintRouterCache{rdb: client}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr()})
			defer func() { _ = client.Close() }()

			cache := tt.newCache(client)
			ctx := context.Background()
			notified := make(chan struct{}, 1)
			cache.SubscribeUpdates(ctx, func() { notified <- struct{}{} })

			require.Eventually(t, func() bool {
				if err := client.Publish(ctx, tt.channel, "refresh").Err(); err != nil {
					return false
				}
				select {
				case <-notified:
					return true
				default:
					return false
				}
			}, time.Second, 10*time.Millisecond)

			// 停止函数等待订阅协程退出，之后关闭 Redis 不会产生 channel_closed 告警。
			cache.StopSubscription()
			cache.StopSubscription()
		})
	}
}
