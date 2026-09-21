//go:build unit

package provider

import (
	"time"

	acct "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"

	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// TestHandleUpstreamError_429_ModelRateLimit 测试 429 模型限流场景
func TestHandleUpstreamError_429_ModelRateLimit(t *testing.T) {
	repo := &antigravityErrorStoreFixture{}
	svc := newAntigravityErrorFixture(repo)
	account := &acct.Record{ID: 1, Name: "acc-1", Platform: capability.PlatformAntigravity}

	// 429 + RATE_LIMIT_EXCEEDED + 模型名 → 模型限流
	body := []byte(`{
		"error": {
			"status": "RESOURCE_EXHAUSTED",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "claude-sonnet-4-5"}, "reason": "RATE_LIMIT_EXCEEDED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "15s"}
			]
		}
	}`)

	result := svc.Observe(AntigravityErrorInput{Context: context.Background(), Prefix: "[test]", Account: account, Status: http.StatusTooManyRequests, Headers: http.Header{}, Body: body, RequestedModel: "claude-sonnet-4-5"})

	// 应该触发模型限流
	require.NotNil(t, result)
	require.True(t, result.Handled)
	require.NotNil(t, result.SwitchError)
	require.Equal(t, "claude-sonnet-4-5", result.SwitchError.RateLimitedModel)
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "claude-sonnet-4-5", repo.modelRateLimitCalls[0].modelKey)
}

// TestHandleUpstreamError_429_NonModelRateLimit 测试 429 非模型限流场景（走模型级限流兜底）
func TestHandleUpstreamError_429_NonModelRateLimit(t *testing.T) {
	repo := &antigravityErrorStoreFixture{}
	svc := newAntigravityErrorFixture(repo)
	account := &acct.Record{ID: 2, Name: "acc-2", Platform: capability.PlatformAntigravity}

	// 429 + 普通限流响应（无 RATE_LIMIT_EXCEEDED reason）→ 走模型级限流兜底
	body := buildGeminiRateLimitBody("5s")

	result := svc.Observe(AntigravityErrorInput{Context: context.Background(), Prefix: "[test]", Account: account, Status: http.StatusTooManyRequests, Headers: http.Header{}, Body: body, RequestedModel: "claude-sonnet-4-5"})

	// handleModelRateLimit 不会处理（因为没有 RATE_LIMIT_EXCEEDED），
	// 但 429 兜底逻辑会使用 requestedModel 设置模型级限流
	require.Nil(t, result)
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "claude-sonnet-4-5", repo.modelRateLimitCalls[0].modelKey)
}

// TestHandleUpstreamError_429_NonModelRateLimit_UsesMappedModelKey 测试 429 非模型限流场景
// 验证：requestedModel 会被映射到 Antigravity 最终模型（例如 claude-opus-4-6 -> claude-opus-4-6-thinking）
func TestHandleUpstreamError_429_NonModelRateLimit_UsesMappedModelKey(t *testing.T) {
	repo := &antigravityErrorStoreFixture{}
	svc := newAntigravityErrorFixture(repo)
	account := &acct.Record{ID: 20, Name: "acc-20", Platform: capability.PlatformAntigravity}

	body := buildGeminiRateLimitBody("5s")

	result := svc.Observe(AntigravityErrorInput{Context: context.Background(), Prefix: "[test]", Account: account, Status: http.StatusTooManyRequests, Headers: http.Header{}, Body: body, RequestedModel: "claude-opus-4-6"})

	require.Nil(t, result)
	require.Len(t, repo.modelRateLimitCalls, 1)
	require.Equal(t, "claude-opus-4-6-thinking", repo.modelRateLimitCalls[0].modelKey)
}

// TestHandleUpstreamError_503_ModelCapacityExhausted 测试 503 模型容量不足场景
// MODEL_CAPACITY_EXHAUSTED 时应等待重试，不切换账号
func TestHandleUpstreamError_503_ModelCapacityExhausted(t *testing.T) {
	repo := &antigravityErrorStoreFixture{}
	svc := newAntigravityErrorFixture(repo)
	account := &acct.Record{ID: 3, Name: "acc-3", Platform: capability.PlatformAntigravity}

	// 503 + MODEL_CAPACITY_EXHAUSTED → 等待重试，不切换账号
	body := []byte(`{
		"error": {
			"status": "UNAVAILABLE",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "metadata": {"model": "gemini-3-pro-high"}, "reason": "MODEL_CAPACITY_EXHAUSTED"},
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "30s"}
			]
		}
	}`)

	result := svc.Observe(AntigravityErrorInput{Context: context.Background(), Prefix: "[test]", Account: account, Status: http.StatusServiceUnavailable, Headers: http.Header{}, Body: body, RequestedModel: "gemini-3-pro-high"})

	// MODEL_CAPACITY_EXHAUSTED 应该标记为已处理，不切换账号，不设置模型限流
	// 实际重试由 handleSmartRetry 处理
	require.NotNil(t, result)
	require.True(t, result.Handled)
	require.False(t, result.ShouldRetry, "MODEL_CAPACITY_EXHAUSTED should not trigger retry from handleModelRateLimit path")
	require.Nil(t, result.SwitchError, "MODEL_CAPACITY_EXHAUSTED should not trigger account switch")
	require.Empty(t, repo.modelRateLimitCalls, "MODEL_CAPACITY_EXHAUSTED should not set model rate limit")
}

// TestHandleUpstreamError_503_NonModelRateLimit 测试 503 非模型限流场景（不处理）
func TestHandleUpstreamError_503_NonModelRateLimit(t *testing.T) {
	repo := &antigravityErrorStoreFixture{}
	svc := newAntigravityErrorFixture(repo)
	account := &acct.Record{ID: 4, Name: "acc-4", Platform: capability.PlatformAntigravity}

	// 503 + 普通错误（非 MODEL_CAPACITY_EXHAUSTED）→ 不做任何处理
	body := []byte(`{
		"error": {
			"status": "UNAVAILABLE",
			"message": "Service temporarily unavailable",
			"details": [
				{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": "SERVICE_UNAVAILABLE"}
			]
		}
	}`)

	result := svc.Observe(AntigravityErrorInput{Context: context.Background(), Prefix: "[test]", Account: account, Status: http.StatusServiceUnavailable, Headers: http.Header{}, Body: body, RequestedModel: "gemini-3-pro-high"})

	// 503 非模型限流不应该做任何处理
	require.Nil(t, result)
	require.Empty(t, repo.modelRateLimitCalls, "503 non-model rate limit should not trigger model rate limit")
	require.Empty(t, repo.rateCalls, "503 non-model rate limit should not trigger account rate limit")
}

// TestHandleUpstreamError_503_EmptyBody 测试 503 空响应体（不处理）
func TestHandleUpstreamError_503_EmptyBody(t *testing.T) {
	repo := &antigravityErrorStoreFixture{}
	svc := newAntigravityErrorFixture(repo)
	account := &acct.Record{ID: 5, Name: "acc-5", Platform: capability.PlatformAntigravity}

	// 503 + 空响应体 → 不做任何处理
	body := []byte(`{}`)

	result := svc.Observe(AntigravityErrorInput{Context: context.Background(), Prefix: "[test]", Account: account, Status: http.StatusServiceUnavailable, Headers: http.Header{}, Body: body, RequestedModel: "gemini-3-pro-high"})

	// 503 空响应不应该做任何处理
	require.Nil(t, result)
	require.Empty(t, repo.modelRateLimitCalls)
	require.Empty(t, repo.rateCalls)
}

func buildGeminiRateLimitBody(delay string) []byte {
	return []byte(fmt.Sprintf(`{"error":{"message":"too many requests","details":[{"metadata":{"quotaResetDelay":%q}}]}}`, delay))
}

// 此处只观测写入数量，模型映射及失败决策均来自生产 Adapter。
type antigravityErrorStoreFixture struct {
	acct.AntigravityHealthStore
	rateCalls           []int64
	modelRateLimitCalls []struct{ modelKey string }
}

func (s *antigravityErrorStoreFixture) SetModelRateLimit(_ context.Context, _ int64, key string, _ time.Time, _ ...string) error {
	s.modelRateLimitCalls = append(s.modelRateLimitCalls, struct{ modelKey string }{key})
	return nil
}
func (s *antigravityErrorStoreFixture) SetRateLimited(_ context.Context, id int64, _ time.Time) error {
	s.rateCalls = append(s.rateCalls, id)
	return nil
}
func newAntigravityErrorFixture(store *antigravityErrorStoreFixture) *AntigravityErrorObserver {
	return &AntigravityErrorObserver{
		Health:         &acct.AntigravityHealth{Store: store, ModelKeys: AntigravityModelLimitKeys, Logf: func(string, ...any) {}},
		SetRateLimited: store.SetRateLimited, LogConfig: func() (bool, int) { return false, 0 },
		ResetTime:       func(body []byte) *int64 { return gemini.ParseGeminiRateLimitResetTime(body, nil) },
		DefaultDuration: func() time.Duration { return antigravity.AntigravityDefaultRateLimitDuration },
	}
}
