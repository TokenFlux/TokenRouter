package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// sparkShadowUsageTestRepo 是 spark 影子用量测试的最小 AccountRepository stub。
// GetByID 从 map 返回影子/母账号，UpdateExtra 记录持久化内容用于断言。
type sparkShadowUsageTestRepo struct {
	AccountRepository
	accounts      map[int64]*Account
	updateExtraCh chan map[string]any
}

func (r *sparkShadowUsageTestRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	if acc, ok := r.accounts[id]; ok {
		return acc, nil
	}
	return nil, fmt.Errorf("account %d not found", id)
}

func (r *sparkShadowUsageTestRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	if r.updateExtraCh != nil {
		copied := make(map[string]any, len(updates))
		for k, v := range updates {
			copied[k] = v
		}
		r.updateExtraCh <- copied
	}
	return nil
}

// TestGetOpenAIUsage_SparkShadow_WritesExtraAndReturnsNonEmptyWindows 覆盖:
// A) spark 影子账号会持久化自身 codex_5h_used_percent，且上游请求携带母账号 chatgpt-account-id。
// B) 同一次调用返回的 UsageInfo 已从 Extra 重建 5h/7d 窗口，而不是只写数据库。
func TestGetOpenAIUsage_SparkShadow_WritesExtraAndReturnsNonEmptyWindows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	pid := int64(100)
	shadow := &Account{
		ID:              200,
		ParentAccountID: &pid,
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		Status:          StatusActive,
		QuotaDimension:  QuotaDimensionSpark,
	}
	parent := &Account{
		ID:       100,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"chatgpt_account_id": "org-spark-parent",
		},
	}

	// 同一个 repo 供 OpenAIQuotaService 解析母账号，也供 AccountUsageService 持久化 Extra。
	updateExtraCh := make(chan map[string]any, 1)
	repo := &sparkShadowUsageTestRepo{
		accounts:      map[int64]*Account{200: shadow, 100: parent},
		updateExtraCh: updateExtraCh,
	}

	// Token cache 为母账号 cache key 返回假 token。
	tokenCache := &stubQuotaTokenCache{tokens: map[string]string{
		OpenAITokenCacheKey(parent): "fake-access-token",
	}}
	tokenProvider := NewOpenAITokenProvider(repo, tokenCache, nil)

	// HTTPUpstream stub 记录 chatgpt-account-id，并返回带 codex_bengalfox 5h+7d 窗口的用量。
	resp := OpenAIQuotaUsage{
		AdditionalRateLimits: []OpenAIAdditionalRateLimit{
			{
				MeteredFeature: "codex_bengalfox",
				RateLimit: &OpenAIRateLimit{
					// 主窗口 -> 5h（18000 秒 = 300 分钟）。
					PrimaryWindow: &OpenAIRateLimitWindow{
						UsedPercent:        42.5,
						ResetAfterSeconds:  3600,
						LimitWindowSeconds: 18000,
					},
					// 次窗口 -> 7d（604800 秒 = 10080 分钟）。
					SecondaryWindow: &OpenAIRateLimitWindow{
						UsedPercent:        10.0,
						ResetAfterSeconds:  86400,
						LimitWindowSeconds: 604800,
					},
				},
			},
		},
	}
	payload, err := json.Marshal(resp)
	require.NoError(t, err)

	upstream := &stubQuotaHTTPUpstream{responseBody: string(payload)}
	quotaService := NewOpenAIQuotaService(stubQuotaAdminService{repo: repo}, upstream, tokenProvider, nil, nil)
	svc := &AccountUsageService{
		accountRepo:        repo,
		openAIQuotaService: quotaService,
	}

	usage, err := svc.getOpenAIUsage(ctx, shadow, true /*force*/)
	require.NoError(t, err)

	// 断言 A-1: 上游收到母账号的 chatgpt-account-id。
	require.Equal(t, "org-spark-parent", upstream.capturedAccountID,
		"QueryUsage must use parent's chatgpt-account-id for spark shadow accounts")

	// 断言 A-2: 影子账号 Extra 持久化了 codex_5h_used_percent。
	select {
	case updates := <-updateExtraCh:
		require.Contains(t, updates, "codex_5h_used_percent",
			"persisted extra must contain codex_5h_used_percent")
		require.InDelta(t, 42.5, updates["codex_5h_used_percent"], 0.01,
			"codex_5h_used_percent must match the upstream value")
	case <-time.After(2 * time.Second):
		t.Fatal("UpdateExtra was not called within timeout — spark shadow persist did not happen")
	}

	// Assertion B：返回的 UsageInfo 必须有非空窗口，避免只写 Extra 不重建返回值。
	require.NotNil(t, usage.FiveHour,
		"returned UsageInfo.FiveHour must be non-nil (rebuild from merged Extra must happen)")
	require.NotNil(t, usage.SevenDay,
		"returned UsageInfo.SevenDay must be non-nil (rebuild from merged Extra must happen)")
}
