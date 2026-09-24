package openai_test

import (
	"testing"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/stretchr/testify/require"
)

// ==================== P1-08 修复：model 替换性能优化测试 =============
func TestReplaceModelInSSELine(t *testing.T) {

	tests := []struct {
		name     string
		line     string
		from     string
		to       string
		expected string
	}{
		{
			name:     "顶层 model 字段替换",
			line:     `data: {"id":"chatcmpl-123","model":"gpt-4o","choices":[]}`,
			from:     "gpt-4o",
			to:       "my-custom-model",
			expected: `data: {"id":"chatcmpl-123","model":"my-custom-model","choices":[]}`,
		},
		{
			name:     "嵌套 response.model 替换",
			line:     `data: {"type":"response","response":{"id":"resp-1","model":"gpt-4o","output":[]}}`,
			from:     "gpt-4o",
			to:       "my-model",
			expected: `data: {"type":"response","response":{"id":"resp-1","model":"my-model","output":[]}}`,
		},
		{
			name:     "model 不匹配时不替换",
			line:     `data: {"id":"chatcmpl-123","model":"gpt-3.5-turbo","choices":[]}`,
			from:     "gpt-4o",
			to:       "my-model",
			expected: `data: {"id":"chatcmpl-123","model":"gpt-3.5-turbo","choices":[]}`,
		},
		{
			name:     "无 model 字段时不替换",
			line:     `data: {"id":"chatcmpl-123","choices":[]}`,
			from:     "gpt-4o",
			to:       "my-model",
			expected: `data: {"id":"chatcmpl-123","choices":[]}`,
		},
		{
			name:     "空 data 行",
			line:     `data: `,
			from:     "gpt-4o",
			to:       "my-model",
			expected: `data: `,
		},
		{
			name:     "[DONE] 行",
			line:     `data: [DONE]`,
			from:     "gpt-4o",
			to:       "my-model",
			expected: `data: [DONE]`,
		},
		{
			name:     "非 data: 前缀行",
			line:     `event: message`,
			from:     "gpt-4o",
			to:       "my-model",
			expected: `event: message`,
		},
		{
			name:     "非法 JSON 不替换",
			line:     `data: {invalid json}`,
			from:     "gpt-4o",
			to:       "my-model",
			expected: `data: {invalid json}`,
		},
		{
			name:     "无空格 data: 格式",
			line:     `data:{"id":"x","model":"gpt-4o"}`,
			from:     "gpt-4o",
			to:       "my-model",
			expected: `data: {"id":"x","model":"my-model"}`,
		},
		{
			name:     "model 名含特殊字符",
			line:     `data: {"model":"org/model-v2.1-beta"}`,
			from:     "org/model-v2.1-beta",
			to:       "custom/alias",
			expected: `data: {"model":"custom/alias"}`,
		},
		{
			name:     "空行",
			line:     "",
			from:     "gpt-4o",
			to:       "my-model",
			expected: "",
		},
		{
			name:     "保持其他字段不变",
			line:     `data: {"id":"abc","object":"chat.completion.chunk","model":"gpt-4o","created":1234567890,"choices":[{"index":0,"delta":{"content":"hi"}}]}`,
			from:     "gpt-4o",
			to:       "alias",
			expected: `data: {"id":"abc","object":"chat.completion.chunk","model":"alias","created":1234567890,"choices":[{"index":0,"delta":{"content":"hi"}}]}`,
		},
		{
			name:     "顶层优先于嵌套：同时存在两个 model",
			line:     `data: {"model":"gpt-4o","response":{"model":"gpt-4o"}}`,
			from:     "gpt-4o",
			to:       "replaced",
			expected: `data: {"model":"replaced","response":{"model":"gpt-4o"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := protocolopenai.ReplaceModelInSSELine(tt.line, tt.from, tt.to)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestReplaceModelInSSEBody(t *testing.T) {

	tests := []struct {
		name     string
		body     string
		from     string
		to       string
		expected string
	}{
		{
			name:     "多行 SSE body 替换",
			body:     "data: {\"model\":\"gpt-4o\",\"choices\":[]}\n\ndata: {\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n",
			from:     "gpt-4o",
			to:       "alias",
			expected: "data: {\"model\":\"alias\",\"choices\":[]}\n\ndata: {\"model\":\"alias\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n",
		},
		{
			name:     "无需替换的 body",
			body:     "data: {\"model\":\"gpt-3.5-turbo\"}\n\ndata: [DONE]\n",
			from:     "gpt-4o",
			to:       "alias",
			expected: "data: {\"model\":\"gpt-3.5-turbo\"}\n\ndata: [DONE]\n",
		},
		{
			name:     "混合 event 和 data 行",
			body:     "event: message\ndata: {\"model\":\"gpt-4o\"}\n\n",
			from:     "gpt-4o",
			to:       "alias",
			expected: "event: message\ndata: {\"model\":\"alias\"}\n\n",
		},
		{
			name:     "空 body",
			body:     "",
			from:     "gpt-4o",
			to:       "alias",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := protocolopenai.ReplaceModelInSSEBody(tt.body, tt.from, tt.to)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestParseSSEUsage_SelectiveParsing(t *testing.T) {
	usage := &protocolopenai.ForwardUsage{InputTokens: 9, OutputTokens: 8, CacheReadInputTokens: 7}

	// 非终态事件中的显式 usage 作为兼容 fallback，非零字段会被合并。
	protocolopenai.ParseSSEUsage(`{"type":"response.in_progress","response":{"usage":{"input_tokens":1,"output_tokens":2}}}`, usage)
	require.Equal(t, 1, usage.InputTokens)
	require.Equal(t, 2, usage.OutputTokens)
	require.Equal(t, 7, usage.CacheReadInputTokens)

	// completed 事件，应提取 usage
	protocolopenai.ParseSSEUsage(`{"type":"response.completed","response":{"usage":{"input_tokens":3,"output_tokens":5,"input_tokens_details":{"cached_tokens":2}}}}`, usage)
	require.Equal(t, 3, usage.InputTokens)
	require.Equal(t, 5, usage.OutputTokens)
	require.Equal(t, 2, usage.CacheReadInputTokens)

	// done 事件同样可能携带最终 usage
	protocolopenai.ParseSSEUsage(`{"type":"response.done","response":{"usage":{"input_tokens":13,"output_tokens":15,"input_tokens_details":{"cached_tokens":4}}}}`, usage)
	require.Equal(t, 13, usage.InputTokens)
	require.Equal(t, 15, usage.OutputTokens)
	require.Equal(t, 4, usage.CacheReadInputTokens)

	// failed 事件在部分上游路径也会携带已消耗 usage，应与 WS/passthrough 保持一致
	protocolopenai.ParseSSEUsage(`{"type":"response.failed","response":{"usage":{"input_tokens":17,"output_tokens":19,"input_tokens_details":{"cached_tokens":6}}}}`, usage)
	require.Equal(t, 17, usage.InputTokens)
	require.Equal(t, 19, usage.OutputTokens)
	require.Equal(t, 6, usage.CacheReadInputTokens)

	protocolopenai.ParseSSEUsage(`{"type":"response.completed","response":{"usage":{"prompt_tokens":21,"completion_tokens":8,"prompt_tokens_details":{"cached_tokens":6}}}}`, usage)
	require.Equal(t, 21, usage.InputTokens)
	require.Equal(t, 8, usage.OutputTokens)
	require.Equal(t, 6, usage.CacheReadInputTokens)
}

func TestParseSSEUsage_NonTerminalUsageMergesNonZeroFields(t *testing.T) {
	usage := &protocolopenai.ForwardUsage{}

	protocolopenai.ParseSSEUsage(`{"type":"response.in_progress","usage":{"input_tokens":17,"output_tokens":1,"input_tokens_details":{"cached_tokens":4}}}`, usage)
	protocolopenai.ParseSSEUsage(`{"type":"response.output_text.done","usage":{"input_tokens":0,"output_tokens":5,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":3}}}`, usage)

	require.Equal(t, 17, usage.InputTokens)
	require.Equal(t, 5, usage.OutputTokens)
	require.Equal(t, 4, usage.CacheReadInputTokens)
	require.Equal(t, 3, usage.CacheCreationInputTokens)
}

func TestParseSSEUsage_TerminalUsageReplacesFallback(t *testing.T) {
	usage := &protocolopenai.ForwardUsage{}

	protocolopenai.ParseSSEUsage(`{"type":"response.output_text.done","usage":{"input_tokens":17,"output_tokens":5,"input_tokens_details":{"cached_tokens":4}}}`, usage)
	protocolopenai.ParseSSEUsage(`{"type":"response.completed","response":{"usage":{"input_tokens":19,"output_tokens":7}}}`, usage)

	require.Equal(t, 19, usage.InputTokens)
	require.Equal(t, 7, usage.OutputTokens)
	require.Zero(t, usage.CacheReadInputTokens)
}

func TestParseSSEUsage_TerminalWithoutUsageKeepsFallback(t *testing.T) {
	usage := &protocolopenai.ForwardUsage{}

	protocolopenai.ParseSSEUsage(`{"type":"response.in_progress","usage":{"input_tokens":17,"output_tokens":5}}`, usage)
	protocolopenai.ParseSSEUsage(`{"type":"response.completed","response":{"id":"resp_1"}}`, usage)
	protocolopenai.ParseSSEUsage("  [DONE]\n", usage)

	require.Equal(t, 17, usage.InputTokens)
	require.Equal(t, 5, usage.OutputTokens)
}
