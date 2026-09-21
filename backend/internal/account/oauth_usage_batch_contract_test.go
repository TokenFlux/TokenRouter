package account

import (
	"context"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

func TestAccountUsageService_GetUsageBatch_BestEffortByAccount(t *testing.T) {
	t.Parallel()

	resetAt := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)

	repo := &usageBatchRecordFixture{
		accounts: []Record{
			{
				ID:       7001,
				Platform: capability.PlatformAnthropic,
				Type:     capability.AccountTypeOAuth,
				Extra: map[string]any{
					"passive_usage_7d_utilization": 0.62,
				},
			},
			{
				ID:       7002,
				Platform: capability.PlatformOpenAI,
				Type:     capability.AccountTypeOAuth,
				Extra: map[string]any{
					"codex_usage_updated_at":  time.Now().UTC().Format(time.RFC3339),
					"codex_5h_used_percent":   18.0,
					"codex_5h_reset_at":       resetAt.Format(time.RFC3339),
					"codex_7d_used_percent":   34.0,
					"codex_7d_reset_at":       resetAt.Add(24 * time.Hour).Format(time.RFC3339),
					"workspace_id":            "org-test",
					"chatgpt_account_id":      "acct-test",
					"openai_snapshot_version": "test",
				},
			},
			{
				ID:       7003,
				Platform: capability.PlatformOpenAI,
				Type:     capability.AccountTypeAPIKey,
			},
		},
	}

	cache := NewOAuthUsageCache()
	stats := NewLocalUsageStatistics(usageBatchStatisticsFixture{}, cache, LocalUsageStatisticsOptions{Now: time.Now, Today: func() time.Time { return time.Now().Truncate(24 * time.Hour) }, Log: log.Printf})
	svc := NewOAuthUsageService(repo, cache, stats, OAuthUsageOptions{OpenAIQuotaPause: func(_ context.Context, value *Record, info *UsageInfo) {
		info.QuotaAutoPaused, _ = EvaluateQuotaAutoPause(value.Platform, value.Extra, QuotaAutoPauseSettings{}, time.Now())
	}})

	usageByAccount, errorsByAccount, err := svc.GetUsageBatch(context.Background(), []int64{7001, 7002, 7003, 7002}, false)
	if err != nil {
		t.Fatalf("GetUsageBatch() error = %v", err)
	}

	if usageByAccount[7001] == nil || usageByAccount[7001].Source != "passive" {
		t.Fatalf("expected anthropic passive usage, got %#v", usageByAccount[7001])
	}

	if usageByAccount[7002] == nil || usageByAccount[7002].FiveHour == nil || usageByAccount[7002].FiveHour.Utilization != 18.0 {
		t.Fatalf("expected openai snapshot usage, got %#v", usageByAccount[7002])
	}

	if !strings.Contains(strings.ToLower(errorsByAccount[7003]), "does not support usage query") {
		t.Fatalf("expected API key account error to be preserved, got %q", errorsByAccount[7003])
	}
}

// 批量读取替身仅返回请求中的原账号记录，不增加业务规则。
type usageBatchRecordFixture struct {
	OAuthUsageReader
	accounts []Record
}

func (r usageBatchRecordFixture) GetByIDs(_ context.Context, ids []int64) ([]*Record, error) {
	result := make([]*Record, 0, len(ids))
	for _, id := range ids {
		for index := range r.accounts {
			if r.accounts[index].ID == id {
				result = append(result, CloneRecord(&r.accounts[index]))
				break
			}
		}
	}
	return result, nil
}

// 保留原空统计夹具，主被动展示由生产组件执行。
type usageBatchStatisticsFixture struct{}

func (usageBatchStatisticsFixture) GetAccountWindowStats(context.Context, int64, time.Time) (*WindowStats, error) {
	return &WindowStats{}, nil
}
func (usageBatchStatisticsFixture) GetAccountTodayStats(context.Context, int64) (*WindowStats, error) {
	return &WindowStats{}, nil
}
