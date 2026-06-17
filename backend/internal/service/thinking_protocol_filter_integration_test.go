package service

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

const passbackThinkingBody = `{
	"model":"deepseek-v4-pro",
	"thinking":{"type":"enabled","budget_tokens":1024},
	"messages":[
		{"role":"user","content":[{"type":"text","text":"Hi"}]},
		{"role":"assistant","content":[
			{"type":"thinking","thinking":"Let me think..."},
			{"type":"text","text":"Answer"}
		]}
	]
}`

func TestThinkingFilters_SkipForPassbackRequired(t *testing.T) {
	in := []byte(passbackThinkingBody)

	require.True(t, bytes.Equal(in, FilterThinkingBlocks(in, "deepseek-v4-pro")))
	require.True(t, bytes.Equal(in, FilterThinkingBlocksForRetry(in, "kimi-k2.6")))
	require.True(t, bytes.Equal(in, FilterSignatureSensitiveBlocksForRetry(in, "glm-5.1")))
}

func TestThinkingFilters_SkipForUnknownModel(t *testing.T) {
	in := []byte(passbackThinkingBody)

	require.True(t, bytes.Equal(in, FilterThinkingBlocks(in, "yi-large")))
	require.True(t, bytes.Equal(in, FilterThinkingBlocksForRetry(in, "gpt-5.5")))
	require.True(t, bytes.Equal(in, FilterSignatureSensitiveBlocksForRetry(in, "")))
}

func TestThinkingFilters_StillStripForAnthropicStrict(t *testing.T) {
	in := []byte(passbackThinkingBody)
	out := FilterThinkingBlocks(in, "claude-sonnet-4-5")

	require.False(t, bytes.Equal(in, out))
	require.NotContains(t, string(out), `"type":"thinking"`)
}
