package provider

import (
	"context"
	"errors"
	"sync"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// TestQoderTokenProviderConcurrent 验证 token provider 在并发访问下的缓存行为
func TestQoderTokenProviderConcurrent(t *testing.T) {
	provider := &QoderTokenProvider{Core: &qoderSessionState{Sessions: make(map[int64]qoderSessionCacheEntry)}}

	// 创建测试 account
	account := &accountcore.Record{
		ID:       12345,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "test_oauth_token",
			"machine_id":           "test_machine_id",
			"uid":                  "test_uid",
		},
	}

	const numGoroutines = 50
	const numRequestsPerGoroutine = 20

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	ctx := context.Background()
	successCount := sync.Map{}

	// 并发请求 session
	for i := 0; i < numGoroutines; i++ {
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < numRequestsPerGoroutine; j++ {
				session, err := provider.GetSession(ctx, account)
				if err != nil {
					// 与显式 Invalidate 竞争的旧世代构建必须被丢弃，这是预期的安全结果。
					if errors.Is(err, errQoderSessionBuildInvalidated) {
						successCount.Store(workerID*1000+j, true)
						continue
					}
					t.Logf("worker %d request %d failed: %v", workerID, j, err)
					continue
				}
				if session == nil {
					t.Errorf("worker %d request %d: got nil session", workerID, j)
					continue
				}
				successCount.Store(workerID*1000+j, true)

				// 模拟 invalidate 场景
				if workerID%3 == 0 && j%5 == 0 {
					provider.Invalidate(account.ID)
				}
			}
		}(i)
	}

	wg.Wait()

	// 统计成功率
	count := 0
	successCount.Range(func(key, value any) bool {
		count++
		return true
	})

	expectedTotal := numGoroutines * numRequestsPerGoroutine
	successRate := float64(count) / float64(expectedTotal)
	t.Logf("Success rate: %d/%d (%.2f%%)", count, expectedTotal, successRate*100)

	if successRate < 0.8 {
		t.Errorf("success rate too low: %.2f%%, expected >= 80%%", successRate*100)
	}
}

// TestQoderTokenProviderInvalidateRace 验证 GetSession 和 Invalidate 的竞态安全
func TestQoderTokenProviderInvalidateRace(t *testing.T) {
	provider := &QoderTokenProvider{Core: &qoderSessionState{Sessions: make(map[int64]qoderSessionCacheEntry)}}

	account := &accountcore.Record{
		ID:       999,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "token_999",
			"machine_id":           "machine_999",
			"aid":                  "aid_999",
		},
	}

	ctx := context.Background()
	const numIterations = 1000

	// 启动两个 goroutine：一个不断 GetSession，一个不断 Invalidate
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < numIterations; i++ {
			_, _ = provider.GetSession(ctx, account)
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < numIterations; i++ {
			provider.Invalidate(account.ID)
		}
	}()

	wg.Wait()

	// 不应该 panic 或死锁
	t.Log("GetSession/Invalidate race test completed without panic")
}
