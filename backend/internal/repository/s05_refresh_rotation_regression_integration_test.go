//go:build integration

package repository

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

// s05RefreshReadBarrier 让两个请求都读取到原凭据，再同时进入轮换，验证真实 Redis 原子消费。
type s05RefreshReadBarrier struct {
	service.RefreshTokenCache
	arrived chan struct{}
	release chan struct{}
}

func (c *s05RefreshReadBarrier) GetRefreshToken(ctx context.Context, key string) (*service.RefreshTokenData, error) {
	value, err := c.RefreshTokenCache.GetRefreshToken(ctx, key)
	if err != nil {
		return nil, err
	}
	select {
	case c.arrived <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-c.release:
		return value, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// s05RefreshDeleteFailure 同时覆盖旧删除入口与新消费入口，不模拟 Redis 的成功行为。
type s05RefreshDeleteFailure struct {
	service.RefreshTokenCache
	failure error
}

func (c s05RefreshDeleteFailure) DeleteRefreshToken(context.Context, string) error { return c.failure }
func (c s05RefreshDeleteFailure) ConsumeRefreshToken(context.Context, string) (bool, error) {
	return false, c.failure
}

func TestS05RefreshRotationConsumesOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := testEntClient(t)
	users := NewUserRepository(client, integrationDB)
	user := mustCreateUser(t, client, &service.User{})
	cache := NewRefreshTokenCache(testRedis(t))
	barrier := &s05RefreshReadBarrier{RefreshTokenCache: cache, arrived: make(chan struct{}, 2), release: make(chan struct{})}
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(barrier.release) }) }
	t.Cleanup(unblock)
	cfg := &config.Config{}
	cfg.JWT.Secret = "s05-local-fixture-only"
	cfg.JWT.ExpireHour = 1
	cfg.JWT.RefreshTokenExpireDays = 1
	auth := service.NewAuthService(client, users, nil, barrier, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	pair, err := auth.GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() { defer workers.Done(); _, e := auth.RefreshTokenPair(ctx, pair.RefreshToken); results <- e }()
	}
	for range 2 {
		select {
		case <-barrier.arrived:
		case <-ctx.Done():
			t.Fatal("两个请求未同时到达读取屏障", ctx.Err())
		}
	}
	unblock()
	workers.Wait()
	close(results)
	successes := 0
	invalid := 0
	for e := range results {
		if e == nil {
			successes++
		} else if errors.Is(e, service.ErrRefreshTokenInvalid) {
			invalid++
		} else {
			t.Errorf("轮换出现非预期错误: %v", e)
		}
	}
	require.Equal(t, 1, successes, "同一 refresh token 只能签发一次后继凭据")
	require.Equal(t, 1, invalid, "已被并发请求消费的 token 必须拒绝")
}

func TestS05RefreshRotationStorageFailureDoesNotIssueTokens(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	users := NewUserRepository(client, integrationDB)
	user := mustCreateUser(t, client, &service.User{})
	cache := NewRefreshTokenCache(testRedis(t))
	failing := s05RefreshDeleteFailure{RefreshTokenCache: cache, failure: errors.New("s05 injected token consume failure")}
	cfg := &config.Config{}
	cfg.JWT.Secret = "s05-local-fixture-only"
	cfg.JWT.ExpireHour = 1
	cfg.JWT.RefreshTokenExpireDays = 1
	auth := service.NewAuthService(client, users, nil, failing, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	pair, err := auth.GenerateTokenPair(ctx, user, "")
	require.NoError(t, err)
	before, err := cache.GetUserTokenHashes(ctx, user.ID)
	require.NoError(t, err)
	next, err := auth.RefreshTokenPair(ctx, pair.RefreshToken)
	require.ErrorIs(t, err, service.ErrServiceUnavailable, "无法使旧凭据失效时不得继续签发")
	require.Nil(t, next)
	after, err := cache.GetUserTokenHashes(ctx, user.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, before, after)
}
