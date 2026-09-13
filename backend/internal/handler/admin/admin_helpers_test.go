package admin

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/handler/dto"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPickThroughputBucketSeconds(t *testing.T) {
	require.Equal(t, 60, pickThroughputBucketSeconds(30*time.Minute))
	require.Equal(t, 300, pickThroughputBucketSeconds(6*time.Hour))
	require.Equal(t, 3600, pickThroughputBucketSeconds(48*time.Hour))
}

func TestOpsAlertRuleValidation(t *testing.T) {
	raw := map[string]json.RawMessage{
		"name":        json.RawMessage(`"High error rate"`),
		"metric_type": json.RawMessage(`"error_rate"`),
		"operator":    json.RawMessage(`">"`),
		"threshold":   json.RawMessage(`90`),
	}

	validated, err := validateOpsAlertRulePayload(raw)
	require.NoError(t, err)
	require.Equal(t, "High error rate", validated.Name)

	_, err = validateOpsAlertRulePayload(map[string]json.RawMessage{})
	require.Error(t, err)

	require.True(t, isPercentOrRateMetric("error_rate"))
	require.True(t, isPercentOrRateMetric("disk_usage_percent"))
	require.False(t, isPercentOrRateMetric("concurrency_queue_depth"))
}

// TestOpenAIFastPolicySettingsFromDTO_NormalizesServiceTier 验证 admin
// 写入路径会把 ServiceTier 的空字符串/空白/大小写归一化为
// service.OpenAIFastTierAny ("all")，避免落盘时 "" 与 "all" 双语义。
func TestOpenAIFastPolicySettingsFromDTO_NormalizesServiceTier(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		require.Nil(t, openaiFastPolicySettingsFromDTO(nil))
	})

	t.Run("empty service_tier becomes 'all'", func(t *testing.T) {
		in := &dto.OpenAIFastPolicySettings{
			Rules: []dto.OpenAIFastPolicyRule{{
				ServiceTier: "",
				Action:      "filter",
				Scope:       "all",
			}},
		}
		out := openaiFastPolicySettingsFromDTO(in)
		require.NotNil(t, out)
		require.Len(t, out.Rules, 1)
		require.Equal(t, service.OpenAIFastTierAny, out.Rules[0].ServiceTier)
		require.Equal(t, "all", out.Rules[0].ServiceTier)
	})

	t.Run("whitespace-only service_tier becomes 'all'", func(t *testing.T) {
		in := &dto.OpenAIFastPolicySettings{
			Rules: []dto.OpenAIFastPolicyRule{{
				ServiceTier: "   ",
				Action:      "pass",
				Scope:       "all",
			}},
		}
		out := openaiFastPolicySettingsFromDTO(in)
		require.Equal(t, service.OpenAIFastTierAny, out.Rules[0].ServiceTier)
	})

	t.Run("uppercase service_tier is lowercased", func(t *testing.T) {
		in := &dto.OpenAIFastPolicySettings{
			Rules: []dto.OpenAIFastPolicyRule{{
				ServiceTier: "PRIORITY",
				Action:      "filter",
				Scope:       "all",
				UserIDs:     []int64{42},
			}},
		}
		out := openaiFastPolicySettingsFromDTO(in)
		require.Equal(t, service.OpenAIFastTierPriority, out.Rules[0].ServiceTier)
		require.Equal(t, []int64{42}, out.Rules[0].UserIDs)
	})

	t.Run("non-empty values pass through (lowercased)", func(t *testing.T) {
		in := &dto.OpenAIFastPolicySettings{
			Rules: []dto.OpenAIFastPolicyRule{
				{ServiceTier: "priority", Action: "filter", Scope: "all"},
				{ServiceTier: "flex", Action: "block", Scope: "oauth"},
				{ServiceTier: "ultrafast", Action: "pass", Scope: "all"},
				{ServiceTier: "all", Action: "pass", Scope: "apikey"},
			},
		}
		out := openaiFastPolicySettingsFromDTO(in)
		require.Len(t, out.Rules, 4)
		require.Equal(t, service.OpenAIFastTierPriority, out.Rules[0].ServiceTier)
		require.Equal(t, service.OpenAIFastTierFlex, out.Rules[1].ServiceTier)
		require.Equal(t, service.OpenAIFastTierUltrafast, out.Rules[2].ServiceTier)
		require.Equal(t, service.OpenAIFastTierAny, out.Rules[3].ServiceTier)
	})
}
