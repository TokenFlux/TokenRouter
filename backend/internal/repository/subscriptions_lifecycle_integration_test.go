//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	egressredis "github.com/TokenFlux/TokenRouter/internal/egress/rediscache"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// 真实 Redis 订阅的最后一个回调返回前，Stop 不得允许关闭共享连接。
func TestSubscriptionsWaitForInFlightCallback(t *testing.T) {
	for _, factory := range []struct {
		name, channel string
		make          func(*redis.Client) stoppableTLSFingerprintCache
	}{
		{"error", "error_passthrough_rules_updated", func(c *redis.Client) stoppableTLSFingerprintCache {
			value, ok := NewErrorPassthroughCache(c).(stoppableTLSFingerprintCache)
			require.True(t, ok)
			return value
		}},
		{"profile", "tls_fingerprint_profiles_updated", func(c *redis.Client) stoppableTLSFingerprintCache {
			value, ok := egressredis.NewTLSFingerprintProfileCache(c).(stoppableTLSFingerprintCache)
			require.True(t, ok)
			return value
		}},
		{"router", "tls_fingerprint_routers_updated", func(c *redis.Client) stoppableTLSFingerprintCache {
			value, ok := egressredis.NewTLSFingerprintRouterCache(c).(stoppableTLSFingerprintCache)
			require.True(t, ok)
			return value
		}},
	} {
		t.Run(factory.name, func(t *testing.T) {
			client := testRedis(t)
			cache := factory.make(client)
			entered, release := make(chan struct{}, 1), make(chan struct{})
			cache.SubscribeUpdates(context.Background(), func() {
				select {
				case entered <- struct{}{}:
				default:
				}
				<-release
			})
			require.Eventually(t, func() bool {
				require.NoError(t, client.Publish(context.Background(), factory.channel, "refresh").Err())
				select {
				case <-entered:
					return true
				default:
					return false
				}
			}, 2*time.Second, 10*time.Millisecond)
			stopped := make(chan struct{})
			go func() { cache.StopSubscription(); close(stopped) }()
			select {
			case <-stopped:
				t.Fatal("订阅回调尚未完成")
			case <-time.After(20 * time.Millisecond):
			}
			close(release)
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("订阅没有退出")
			}
			cache.StopSubscription()
		})
	}
}

// 订阅停止能力由缓存公开实现提供，不绑定旧包的私有类型。
type stoppableTLSFingerprintCache interface {
	SubscribeUpdates(context.Context, func())
	StopSubscription()
}
