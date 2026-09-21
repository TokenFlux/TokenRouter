package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// sparkShadowUsageTestRepo 是 spark 影子用量测试的最小 account.OAuthUsageReader stub。
// GetByID 从 map 返回影子/母账号，UpdateExtra 记录持久化内容用于断言。
type sparkShadowUsageTestRepo struct {
	account.OAuthUsageReader
	accounts      map[int64]*account.Record
	updateExtraCh chan map[string]any
}

func (r *sparkShadowUsageTestRepo) GetByID(_ context.Context, id int64) (*account.Record, error) {
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
	shadow := &account.Record{
		ID:              200,
		ParentAccountID: &pid,
		Platform:        capability.PlatformOpenAI,
		Type:            capability.AccountTypeOAuth,
		Status:          account.StatusActive,
		QuotaDimension:  account.QuotaDimensionSpark,
	}
	parent := &account.Record{
		ID:       100,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Status:   account.StatusActive,
		Credentials: map[string]any{
			"chatgpt_account_id": "org-spark-parent",
		},
	}

	// 同一个 repo 供 OpenAIQuotaService 解析母账号，也供 AccountUsageService 持久化 Extra。
	updateExtraCh := make(chan map[string]any, 1)
	repo := &sparkShadowUsageTestRepo{
		// 模拟数据库持有独立快照，不能与请求侧兼容投影写回共享可变对象。
		accounts: map[int64]*account.Record{
			200: account.CloneRecord(shadow),
			100: account.CloneRecord(parent),
		},
		updateExtraCh: updateExtraCh,
	}

	// Token cache 为母账号 cache key 返回假 token。
	tokenCache := &stubQuotaTokenCache{tokens: map[string]string{
		account.OpenAITokenCacheKey(parent): "fake-access-token",
	}}
	tokenProvider := newOpenAITokenSourceForTest(repo, tokenCache, nil)

	// HTTPUpstream stub 记录 chatgpt-account-id，并返回带 codex_bengalfox 5h+7d 窗口的用量。
	resp := openai.OpenAIQuotaUsage{
		AdditionalRateLimits: []openai.OpenAIAdditionalRateLimit{
			{
				MeteredFeature: "codex_bengalfox",
				RateLimit: &openai.OpenAIRateLimit{
					// 主窗口 -> 5h（18000 秒 = 300 分钟）。
					PrimaryWindow: &openai.OpenAIRateLimitWindow{
						UsedPercent:        42.5,
						ResetAfterSeconds:  3600,
						LimitWindowSeconds: 18000,
					},
					// 次窗口 -> 7d（604800 秒 = 10080 分钟）。
					SecondaryWindow: &openai.OpenAIRateLimitWindow{
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
	quotaFactory := &OpenAIQuotaFactory{Transport: upstream}
	quotaService := account.NewOpenAIQuotaService(account.OpenAIQuotaOptions{
		Configured: func() bool { return true },
		Read: func(ctx context.Context, id int64) (*account.Record, error) {
			value, err := repo.GetByID(ctx, id)
			return account.CloneRecord(value), err
		},
		Client: quotaFactory.Client, Token: tokenProvider.GetAccessToken, Warn: slog.Warn, Info: slog.Info,
	})
	svc := account.NewOAuthUsageService(repo, nil, nil, account.OAuthUsageOptions{OpenAI: account.OpenAIUsageOptions{
		Shadow: func(ctx context.Context, id int64, now time.Time) (map[string]any, error) {
			value, err := quotaService.QueryUsage(ctx, id)
			if err != nil {
				return nil, err
			}
			return account.BuildCodexSparkWindowExtraUpdates(value, now), nil
		},
	}})

	usage, err := svc.GetOpenAIUsage(ctx, shadow, true /*force*/)
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

// 影子写回必须定位本次读取的同一行与身份，不把母账号凭据写入影子行。
func (r *sparkShadowUsageTestRepo) UpdateUsageExtraIfUnchanged(ctx context.Context, version account.UsageObservationVersion, updates map[string]any) (bool, error) {
	current := r.accounts[version.ID]
	if current == nil || !reflect.DeepEqual(account.ObserveUsageVersion(current), version) {
		return false, nil
	}
	return true, r.UpdateExtra(ctx, version.ID, updates)
}
