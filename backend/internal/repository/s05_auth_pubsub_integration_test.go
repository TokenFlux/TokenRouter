//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	keyredis "github.com/TokenFlux/TokenRouter/internal/apikey/rediscache"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

// TestS05AuthPubSubReconnect 验证现有双实例发布订阅协议、真实断线重订阅及停止顺序。
// 两个实例仅用于既有认证缓存兼容性，不扩大平台额度的单进程协调边界。
func TestS05AuthPubSubReconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	client := testEntClient(t)
	rdb := testRedis(t)
	user := mustCreateUser(t, client, &service.User{})
	store := keypostgres.NewKeyStore(client, integrationDB, nil)
	key := &apikey.APIKey{UserID: user.ID, Key: fmt.Sprintf("sk-s05-pubsub-%d", time.Now().UnixNano()), Name: "before", Status: "active", ModelMapping: map[string]string{"client": "upstream"}}
	require.NoError(t, store.Create(ctx, key))
	cache := keyredis.NewAPIKeyCache(rdb)
	options := &apikey.Options{APIKeyAuth: apikey.APIKeyAuthCacheConfig{L1Size: 1024, L1TTLSeconds: 60, L2TTLSeconds: 60, Singleflight: true}}
	one := apikey.NewAPIKeyService(store, identitypostgres.NewUserStore(client, integrationDB), nil, nil, nil, cache, options)
	two := apikey.NewAPIKeyService(store, identitypostgres.NewUserStore(client, integrationDB), nil, nil, nil, cache, options)
	t.Cleanup(one.Stop)
	t.Cleanup(two.Stop)
	one.Start()
	two.Start()
	subscribers := func() int64 {
		v, e := rdb.PubSubNumSub(ctx, keyredis.AuthCacheInvalidateChannel).Result()
		if e != nil {
			return -1
		}
		return v[keyredis.AuthCacheInvalidateChannel]
	}
	require.Eventually(t, func() bool { return subscribers() == 2 }, 5*time.Second, 10*time.Millisecond)
	for _, core := range []*apikey.APIKeyService{one, two} {
		v, e := core.GetByKey(ctx, key.Key)
		require.NoError(t, e)
		require.Equal(t, "before", v.Name)
		v.ModelMapping["client"] = "request-only"
	}
	for _, core := range []*apikey.APIKeyService{one, two} {
		v, e := core.GetByKey(ctx, key.Key)
		require.NoError(t, e)
		require.Equal(t, "upstream", v.ModelMapping["client"])
	}
	cacheKey := one.KeyAuthCacheKey(key.Key)
	publish := func(name string) {
		_, e := client.APIKey.UpdateOneID(key.ID).SetName(name).Save(ctx)
		require.NoError(t, e)
		require.NoError(t, cache.DeleteAuthCache(ctx, cacheKey))
		require.NoError(t, cache.PublishAuthCacheInvalidation(ctx, cacheKey))
		require.Eventually(t, func() bool {
			for _, core := range []*apikey.APIKeyService{one, two} {
				v, e := core.GetByKey(ctx, key.Key)
				if e != nil || v.Name != name {
					return false
				}
			}
			return true
		}, 5*time.Second, 10*time.Millisecond)
	}
	publish("after-publish")
	// 仅操作本测试隔离 Redis 容器中的订阅连接，验证实际网络重连。
	killed, e := rdb.ClientKillByFilter(ctx, "TYPE", "pubsub").Result()
	require.NoError(t, e)
	require.Equal(t, int64(2), killed)
	require.Eventually(t, func() bool { return subscribers() == 2 }, 8*time.Second, 10*time.Millisecond)
	publish("after-reconnect")
	stopCtx, stopCancel := context.WithTimeout(ctx, 2*time.Second)
	defer stopCancel()
	require.NoError(t, one.StopContext(stopCtx))
	require.NoError(t, two.StopContext(stopCtx))
	require.Eventually(t, func() bool { return subscribers() == 0 }, time.Second, 10*time.Millisecond)
	require.NoError(t, rdb.Ping(ctx).Err(), "认证资源先于 Redis 关闭")
	_, e = one.GetByKey(ctx, key.Key)
	require.ErrorIs(t, e, apikey.ErrAuthenticationStopped)
}
