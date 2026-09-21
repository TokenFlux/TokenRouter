package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateGroupRequestReasoningEffortMappingsTriState(t *testing.T) {
	t.Run("omitted means unchanged", func(t *testing.T) {
		var req UpdateGroupRequest
		require.NoError(t, json.Unmarshal([]byte(`{}`), &req))
		require.Nil(t, req.ReasoningEffortMappings)
	})

	t.Run("empty array means clear", func(t *testing.T) {
		var req UpdateGroupRequest
		require.NoError(t, json.Unmarshal([]byte(`{"reasoning_effort_mappings":[]}`), &req))
		require.NotNil(t, req.ReasoningEffortMappings)
		require.Empty(t, *req.ReasoningEffortMappings)
	})

	t.Run("non empty array means replace", func(t *testing.T) {
		var req UpdateGroupRequest
		require.NoError(t, json.Unmarshal([]byte(`{"reasoning_effort_mappings":[{"from":"max","to":"xhigh"}]}`), &req))
		require.NotNil(t, req.ReasoningEffortMappings)
		require.Len(t, *req.ReasoningEffortMappings, 1)
		require.Equal(t, "max", (*req.ReasoningEffortMappings)[0].From)
		require.Equal(t, "xhigh", (*req.ReasoningEffortMappings)[0].To)
	})

	t.Run("accepts model scoped mappings", func(t *testing.T) {
		var req UpdateGroupRequest
		require.NoError(t, json.Unmarshal([]byte(`{"reasoning_effort_mappings":[{"from":"max","to":"low","match_type":"prefix","model":"gpt"}]}`), &req))
		require.NotNil(t, req.ReasoningEffortMappings)
		require.Len(t, *req.ReasoningEffortMappings, 1)
		require.Equal(t, "max", (*req.ReasoningEffortMappings)[0].From)
		require.Equal(t, "low", (*req.ReasoningEffortMappings)[0].To)
		require.Equal(t, "prefix", (*req.ReasoningEffortMappings)[0].MatchType)
		require.Equal(t, "gpt", (*req.ReasoningEffortMappings)[0].Model)
	})
}

// TestGroupRequestReasoningEffortOverLimitAction 验证管理 API 保留显式超限动作。
func TestGroupRequestReasoningEffortOverLimitAction(t *testing.T) {
	var req UpdateGroupRequest
	require.NoError(t, json.Unmarshal([]byte(`{"max_reasoning_effort_over_limit":"deny"}`), &req))
	require.NotNil(t, req.MaxReasoningEffortOverLimit)
	require.Equal(t, "deny", *req.MaxReasoningEffortOverLimit)
}

func TestUpdateGroupRequestAdvancedSchedulerOverridesTriState(t *testing.T) {
	t.Run("omitted means unchanged", func(t *testing.T) {
		var req UpdateGroupRequest
		require.NoError(t, json.Unmarshal([]byte(`{}`), &req))
		require.Nil(t, req.AdvancedSchedulerOverrides)
	})

	t.Run("empty object means clear all overrides", func(t *testing.T) {
		var req UpdateGroupRequest
		require.NoError(t, json.Unmarshal([]byte(`{"advanced_scheduler_overrides":{}}`), &req))
		require.NotNil(t, req.AdvancedSchedulerOverrides)
		require.Zero(t, *req.AdvancedSchedulerOverrides)
	})

	t.Run("explicit false and zero are retained", func(t *testing.T) {
		var req UpdateGroupRequest
		require.NoError(t, json.Unmarshal([]byte(`{
			"advanced_scheduler_overrides":{
				"sticky_weighted_enabled":false,
				"lb_top_k":3,
				"weight_queue":0
			}
		}`), &req))
		require.NotNil(t, req.AdvancedSchedulerOverrides)
		require.NotNil(t, req.AdvancedSchedulerOverrides.StickyWeightedEnabled)
		require.False(t, *req.AdvancedSchedulerOverrides.StickyWeightedEnabled)
		require.Equal(t, 3, *req.AdvancedSchedulerOverrides.LBTopK)
		require.Equal(t, 0.0, *req.AdvancedSchedulerOverrides.WeightQueue)
	})
}
