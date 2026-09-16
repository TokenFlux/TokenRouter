//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWSPassthroughUsageMeta_InitFromFirstFrame_MappedModelCandidate(t *testing.T) {
	body := []byte(`{"type":"response.create","model":"sol","reasoning":{"effort":"max"}}`)

	meta := newOpenAIWSPassthroughUsageMeta("sol", body)
	meta.initFromFirstFrame(body, "gpt-5.6-sol")

	got := meta.ReasoningEffort.Load()
	require.NotNil(t, got, "reasoning effort should be set")
	require.Equal(t, "max", *got, "mapped model gpt-5.6-sol should preserve max")
}

func TestWSPassthroughUsageMeta_InitFromFirstFrame_NonGPT56RecordsExplicitMax(t *testing.T) {
	body := []byte(`{"type":"response.create","model":"deepseek-v4-flash","reasoning":{"effort":"max"}}`)

	meta := newOpenAIWSPassthroughUsageMeta("deepseek-v4-flash", body)
	meta.captureRequestedReasoningEffort(body, "deepseek-v4-flash")
	meta.initFromFirstFrame(body, "deepseek/deepseek-v4-flash-0731")

	got := meta.ReasoningEffort.Load()
	require.NotNil(t, got, "显式 max 应按实际请求记录")
	require.Equal(t, "max", *got)
	requested := meta.RequestedReasoningEffort.Load()
	require.NotNil(t, requested)
	require.Equal(t, "max", *requested, "策略改写前的档位必须单独保留")
}

func TestWSPassthroughUsageMeta_CaptureRequestedReasoningEffortPreservesNone(t *testing.T) {
	body := []byte(`{"type":"response.create","model":"gpt-6-astra","reasoning":{"effort":"none"}}`)

	meta := newOpenAIWSPassthroughUsageMeta("gpt-6-astra", body)
	meta.captureRequestedReasoningEffort(body, "gpt-6-astra")

	requested := meta.RequestedReasoningEffort.Load()
	require.NotNil(t, requested)
	require.Equal(t, "none", *requested, "映射前的 none 必须保留为客户端原始档位")
}

func TestWSPassthroughUsageMeta_UpdateFromResponseCreate_MappedModelCandidate(t *testing.T) {
	body := []byte(`{"type":"response.create","model":"sol","reasoning":{"effort":"max"}}`)

	meta := newOpenAIWSPassthroughUsageMeta("sol", body)
	meta.updateFromResponseCreate(body, "gpt-5.6-sol", "sol")

	got := meta.ReasoningEffort.Load()
	require.NotNil(t, got)
	require.Equal(t, "max", *got, "mapped model should preserve max on multi-turn update")
}
