package account_test

import (
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/stretchr/testify/require"
)

// TestBuildCodexSparkWindowExtraUpdates_ContainsCodexKeys 验证:
//   - 产出包含 codex_5h_used_percent / codex_7d_used_percent
//   - 不含任何 codex_spark_ 前缀的 key（Method Z 前缀已禁止）
//   - 数值正确映射（primary 较短→5h，secondary 较长→7d）
func TestBuildCodexSparkWindowExtraUpdates_ContainsCodexKeys(t *testing.T) {
	now := time.Now().UTC()
	usage := &openai.OpenAIQuotaUsage{
		AdditionalRateLimits: []openai.OpenAIAdditionalRateLimit{
			{
				MeteredFeature: "codex_bengalfox",
				RateLimit: &openai.OpenAIRateLimit{
					PrimaryWindow: &openai.OpenAIRateLimitWindow{
						UsedPercent:        0.42,
						LimitWindowSeconds: 18000, // 300 min = 5 h
						ResetAfterSeconds:  3600,
					},
					SecondaryWindow: &openai.OpenAIRateLimitWindow{
						UsedPercent:        0.15,
						LimitWindowSeconds: 604800, // 7 d
						ResetAfterSeconds:  86400,
					},
				},
			},
		},
	}

	updates := accountcore.BuildCodexSparkWindowExtraUpdates(usage, now)
	require.NotNil(t, updates, "expected non-nil updates for valid codex_bengalfox entry")

	// 必须含有 codex_5h_* 和 codex_7d_* 键
	require.Contains(t, updates, "codex_5h_used_percent")
	require.Contains(t, updates, "codex_7d_used_percent")

	// 任何键不得含有 codex_spark_ 前缀（Method Z 已禁止）
	for k := range updates {
		require.False(t, strings.Contains(k, "codex_spark_"),
			"unexpected Method-Z prefix in key: %s", k)
	}

	// 数值验证（primary=5h, secondary=7d）
	require.InDelta(t, 0.42, updates["codex_5h_used_percent"], 1e-9)
	require.InDelta(t, 0.15, updates["codex_7d_used_percent"], 1e-9)
}

// TestBuildCodexSparkWindowExtraUpdates_NilUsage 验证 nil usage 返回 nil。
func TestBuildCodexSparkWindowExtraUpdates_NilUsage(t *testing.T) {
	require.Nil(t, accountcore.BuildCodexSparkWindowExtraUpdates(nil, time.Now()))
}

// TestBuildCodexSparkWindowExtraUpdates_NoBengalfox 验证无 codex_bengalfox 条目时返回 nil。
func TestBuildCodexSparkWindowExtraUpdates_NoBengalfox(t *testing.T) {
	usage := &openai.OpenAIQuotaUsage{
		AdditionalRateLimits: []openai.OpenAIAdditionalRateLimit{
			{MeteredFeature: "other_feature", RateLimit: &openai.OpenAIRateLimit{}},
		},
	}
	require.Nil(t, accountcore.BuildCodexSparkWindowExtraUpdates(usage, time.Now()))
}
