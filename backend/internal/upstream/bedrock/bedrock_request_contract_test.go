package bedrock_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 以下合同直接验证所属模块，保留原输入与断言。
func TestPrepareBedrockRequestBody_BasicFields(t *testing.T) {
	input := `{"model":"claude-opus-4-6","stream":true,"max_tokens":1024,"messages":[{"role":"user","content":"hi"}]}`
	result, err := bedrock.PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1", "")
	require.NoError(t, err)

	// anthropic_version 应被注入
	assert.Equal(t, "bedrock-2023-05-31", gjson.GetBytes(result, "anthropic_version").String())
	// model 和 stream 应被移除
	assert.False(t, gjson.GetBytes(result, "model").Exists())
	assert.False(t, gjson.GetBytes(result, "stream").Exists())
	// max_tokens 应保留
	assert.Equal(t, int64(1024), gjson.GetBytes(result, "max_tokens").Int())
}

func TestPrepareBedrockRequestBody_OutputFormatInlineSchema(t *testing.T) {
	t.Run("schema inlined into last user message array content", func(t *testing.T) {
		input := `{"model":"claude-sonnet-4-5","output_format":{"type":"json","schema":{"name":"string"}},"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`
		result, err := bedrock.PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-sonnet-4-5-v1", "")
		require.NoError(t, err)

		assert.False(t, gjson.GetBytes(result, "output_format").Exists())
		// schema 应内联到最后一条 user message 的 content 数组末尾
		contentArr := gjson.GetBytes(result, "messages.0.content").Array()
		require.Len(t, contentArr, 2)
		assert.Equal(t, "text", contentArr[1].Get("type").String())
		assert.Contains(t, contentArr[1].Get("text").String(), `"name":"string"`)
	})

	t.Run("schema inlined into string content", func(t *testing.T) {
		input := `{"model":"claude-sonnet-4-5","output_format":{"type":"json","schema":{"result":"number"}},"messages":[{"role":"user","content":"compute this"}]}`
		result, err := bedrock.PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-sonnet-4-5-v1", "")
		require.NoError(t, err)

		assert.False(t, gjson.GetBytes(result, "output_format").Exists())
		contentArr := gjson.GetBytes(result, "messages.0.content").Array()
		require.Len(t, contentArr, 2)
		assert.Equal(t, "compute this", contentArr[0].Get("text").String())
		assert.Contains(t, contentArr[1].Get("text").String(), `"result":"number"`)
	})

	t.Run("no schema field just removes output_format", func(t *testing.T) {
		input := `{"model":"claude-sonnet-4-5","output_format":{"type":"json"},"messages":[{"role":"user","content":"hi"}]}`
		result, err := bedrock.PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-sonnet-4-5-v1", "")
		require.NoError(t, err)

		assert.False(t, gjson.GetBytes(result, "output_format").Exists())
	})

	t.Run("no messages just removes output_format", func(t *testing.T) {
		input := `{"model":"claude-sonnet-4-5","output_format":{"type":"json","schema":{"name":"string"}}}`
		result, err := bedrock.PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-sonnet-4-5-v1", "")
		require.NoError(t, err)

		assert.False(t, gjson.GetBytes(result, "output_format").Exists())
	})
}

func TestPrepareBedrockRequestBody_RemoveOutputConfig(t *testing.T) {
	input := `{"model":"claude-sonnet-4-5","output_config":{"max_tokens":100},"messages":[]}`
	result, err := bedrock.PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-sonnet-4-5-v1", "")
	require.NoError(t, err)

	assert.False(t, gjson.GetBytes(result, "output_config").Exists())
}

func TestRemoveCustomFieldFromTools(t *testing.T) {
	input := `{
		"tools": [
			{"name":"tool1","custom":{"defer_loading":true},"description":"desc1"},
			{"name":"tool2","description":"desc2"},
			{"name":"tool3","custom":{"defer_loading":true,"other":123},"description":"desc3"}
		]
	}`
	result := bedrock.RemoveCustomFieldFromTools([]byte(input))

	tools := gjson.GetBytes(result, "tools").Array()
	require.Len(t, tools, 3)
	// custom 应被移除
	assert.False(t, tools[0].Get("custom").Exists())
	// name/description 应保留
	assert.Equal(t, "tool1", tools[0].Get("name").String())
	assert.Equal(t, "desc1", tools[0].Get("description").String())
	// 没有 custom 的工具不受影响
	assert.Equal(t, "tool2", tools[1].Get("name").String())
	// 第三个工具的 custom 也应被移除
	assert.False(t, tools[2].Get("custom").Exists())
	assert.Equal(t, "tool3", tools[2].Get("name").String())
}

func TestRemoveCustomFieldFromTools_NoTools(t *testing.T) {
	input := `{"messages":[{"role":"user","content":"hi"}]}`
	result := bedrock.RemoveCustomFieldFromTools([]byte(input))
	// 无 tools 时不改变原始数据
	assert.JSONEq(t, input, string(result))
}

func TestSanitizeBedrockCacheControl_RemoveScope(t *testing.T) {
	input := `{
		"system": [{"type":"text","text":"sys","cache_control":{"type":"ephemeral","scope":"global"}}],
		"messages": [{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","scope":"global"}}]}]
	}`
	result := bedrock.SanitizeBedrockCacheControl([]byte(input), "us.anthropic.claude-opus-4-6-v1")

	// scope 应被移除
	assert.False(t, gjson.GetBytes(result, "system.0.cache_control.scope").Exists())
	assert.False(t, gjson.GetBytes(result, "messages.0.content.0.cache_control.scope").Exists())
	// type 应保留
	assert.Equal(t, "ephemeral", gjson.GetBytes(result, "system.0.cache_control.type").String())
	assert.Equal(t, "ephemeral", gjson.GetBytes(result, "messages.0.content.0.cache_control.type").String())
}

func TestSanitizeBedrockCacheControl_TTL_OldModel(t *testing.T) {
	input := `{
		"system": [{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"5m"}}]
	}`
	// 旧模型（Claude 3.5）不支持 ttl
	result := bedrock.SanitizeBedrockCacheControl([]byte(input), "anthropic.claude-3-5-sonnet-20241022-v2:0")

	assert.False(t, gjson.GetBytes(result, "system.0.cache_control.ttl").Exists())
	assert.Equal(t, "ephemeral", gjson.GetBytes(result, "system.0.cache_control.type").String())
}

func TestSanitizeBedrockCacheControl_TTL_Claude45_Supported(t *testing.T) {
	input := `{
		"system": [{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"5m"}}]
	}`
	// Claude 4.5+ 支持 "5m" 和 "1h"
	result := bedrock.SanitizeBedrockCacheControl([]byte(input), "us.anthropic.claude-sonnet-4-5-20250929-v1:0")

	assert.True(t, gjson.GetBytes(result, "system.0.cache_control.ttl").Exists())
	assert.Equal(t, "5m", gjson.GetBytes(result, "system.0.cache_control.ttl").String())
}

func TestSanitizeBedrockCacheControl_TTL_Claude45_UnsupportedValue(t *testing.T) {
	input := `{
		"system": [{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"10m"}}]
	}`
	// Claude 4.5 不支持 "10m"
	result := bedrock.SanitizeBedrockCacheControl([]byte(input), "us.anthropic.claude-sonnet-4-5-20250929-v1:0")

	assert.False(t, gjson.GetBytes(result, "system.0.cache_control.ttl").Exists())
}

func TestSanitizeBedrockCacheControl_TTL_Claude46(t *testing.T) {
	input := `{
		"messages": [{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]
	}`
	result := bedrock.SanitizeBedrockCacheControl([]byte(input), "us.anthropic.claude-opus-4-6-v1")

	assert.True(t, gjson.GetBytes(result, "messages.0.content.0.cache_control.ttl").Exists())
	assert.Equal(t, "1h", gjson.GetBytes(result, "messages.0.content.0.cache_control.ttl").String())
}

func TestSanitizeBedrockCacheControl_NoCacheControl(t *testing.T) {
	input := `{"system":[{"type":"text","text":"sys"}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`
	result := bedrock.SanitizeBedrockCacheControl([]byte(input), "us.anthropic.claude-opus-4-6-v1")
	// 无 cache_control 时不改变原始数据
	assert.JSONEq(t, input, string(result))
}

func TestIsBedrockClaude45OrNewer(t *testing.T) {
	tests := []struct {
		modelID string
		expect  bool
	}{
		{"us.anthropic.claude-opus-4-6-v1", true},
		{"us.anthropic.claude-opus-4-8", true},
		{"anthropic.claude-fable-5", true},
		{"us.anthropic.claude-sonnet-4-6", true},
		{"us.anthropic.claude-sonnet-4-5-20250929-v1:0", true},
		{"us.anthropic.claude-opus-4-5-20251101-v1:0", true},
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", true},
		{"anthropic.claude-3-5-sonnet-20241022-v2:0", false},
		{"anthropic.claude-3-opus-20240229-v1:0", false},
		{"anthropic.claude-3-haiku-20240307-v1:0", false},
		// 未来版本应自动支持
		{"us.anthropic.claude-sonnet-5-0-v1", true},
		{"us.anthropic.claude-opus-4-7", true},
		// 旧版本
		{"anthropic.claude-opus-4-1-v1", false},
		{"anthropic.claude-sonnet-4-0-v1", false},
		// 非 Claude 模型
		{"amazon.nova-pro-v1", false},
		{"meta.llama3-70b", false},
	}
	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			assert.Equal(t, tt.expect, bedrock.IsBedrockClaude45OrNewer(tt.modelID))
		})
	}
}

func TestPrepareBedrockRequestBody_FullIntegration(t *testing.T) {
	// 模拟一个完整的 Claude Code 请求
	input := `{
		"model": "claude-opus-4-6",
		"stream": true,
		"max_tokens": 16384,
		"output_format": {"type": "json", "schema": {"result": "string"}},
		"output_config": {"max_tokens": 100},
		"system": [{"type": "text", "text": "You are helpful", "cache_control": {"type": "ephemeral", "scope": "global", "ttl": "5m"}}],
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "hello", "cache_control": {"type": "ephemeral", "ttl": "1h"}}]}
		],
		"tools": [
			{"name": "bash", "description": "Run bash", "custom": {"defer_loading": true}, "input_schema": {"type": "object"}},
			{"name": "read", "description": "Read file", "input_schema": {"type": "object"}}
		]
	}`

	betaHeader := "context-1m-2025-08-07, compact-2026-01-12"
	result, err := bedrock.PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1", betaHeader)
	require.NoError(t, err)

	// 基本字段
	assert.Equal(t, "bedrock-2023-05-31", gjson.GetBytes(result, "anthropic_version").String())
	assert.False(t, gjson.GetBytes(result, "model").Exists())
	assert.False(t, gjson.GetBytes(result, "stream").Exists())
	assert.Equal(t, int64(16384), gjson.GetBytes(result, "max_tokens").Int())

	// anthropic_beta 应包含所有 beta tokens
	betaArr := gjson.GetBytes(result, "anthropic_beta").Array()
	require.Len(t, betaArr, 2)
	assert.Equal(t, "context-1m-2025-08-07", betaArr[0].String())
	assert.Equal(t, "compact-2026-01-12", betaArr[1].String())

	// output_format 应被移除，schema 内联到最后一条 user message
	assert.False(t, gjson.GetBytes(result, "output_format").Exists())
	assert.False(t, gjson.GetBytes(result, "output_config").Exists())
	// content 数组：原始 text block + 内联 schema block
	contentArr := gjson.GetBytes(result, "messages.0.content").Array()
	require.Len(t, contentArr, 2)
	assert.Equal(t, "hello", contentArr[0].Get("text").String())
	assert.Contains(t, contentArr[1].Get("text").String(), `"result":"string"`)

	// tools 中的 custom 应被移除
	assert.False(t, gjson.GetBytes(result, "tools.0.custom").Exists())
	assert.Equal(t, "bash", gjson.GetBytes(result, "tools.0.name").String())
	assert.Equal(t, "read", gjson.GetBytes(result, "tools.1.name").String())

	// cache_control: scope 应被移除，ttl 在 Claude 4.6 上保留合法值
	assert.False(t, gjson.GetBytes(result, "system.0.cache_control.scope").Exists())
	assert.Equal(t, "ephemeral", gjson.GetBytes(result, "system.0.cache_control.type").String())
	assert.Equal(t, "5m", gjson.GetBytes(result, "system.0.cache_control.ttl").String())
	assert.Equal(t, "1h", gjson.GetBytes(result, "messages.0.content.0.cache_control.ttl").String())
}

func TestPrepareBedrockRequestBody_BetaHeader(t *testing.T) {
	input := `{"messages":[{"role":"user","content":"hi"}],"max_tokens":100}`

	t.Run("empty beta header", func(t *testing.T) {
		result, err := bedrock.PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1", "")
		require.NoError(t, err)
		assert.False(t, gjson.GetBytes(result, "anthropic_beta").Exists())
	})

	t.Run("single beta token", func(t *testing.T) {
		result, err := bedrock.PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1", "context-1m-2025-08-07")
		require.NoError(t, err)
		arr := gjson.GetBytes(result, "anthropic_beta").Array()
		require.Len(t, arr, 1)
		assert.Equal(t, "context-1m-2025-08-07", arr[0].String())
	})

	t.Run("multiple beta tokens with spaces", func(t *testing.T) {
		result, err := bedrock.PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1", "context-1m-2025-08-07 , compact-2026-01-12 ")
		require.NoError(t, err)
		arr := gjson.GetBytes(result, "anthropic_beta").Array()
		require.Len(t, arr, 2)
		assert.Equal(t, "context-1m-2025-08-07", arr[0].String())
		assert.Equal(t, "compact-2026-01-12", arr[1].String())
	})

	t.Run("json array beta header", func(t *testing.T) {
		result, err := bedrock.PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1", `["context-1m-2025-08-07","compact-2026-01-12"]`)
		require.NoError(t, err)
		arr := gjson.GetBytes(result, "anthropic_beta").Array()
		require.Len(t, arr, 2)
		assert.Equal(t, "context-1m-2025-08-07", arr[0].String())
		assert.Equal(t, "compact-2026-01-12", arr[1].String())
	})
}

func TestParseAnthropicBetaHeader(t *testing.T) {
	assert.Nil(t, bedrock.ParseAnthropicBetaHeader(""))
	assert.Equal(t, []string{"a"}, bedrock.ParseAnthropicBetaHeader("a"))
	assert.Equal(t, []string{"a", "b"}, bedrock.ParseAnthropicBetaHeader("a,b"))
	assert.Equal(t, []string{"a", "b"}, bedrock.ParseAnthropicBetaHeader("a , b "))
	assert.Equal(t, []string{"a", "b", "c"}, bedrock.ParseAnthropicBetaHeader("a,b,c"))
	assert.Equal(t, []string{"a", "b"}, bedrock.ParseAnthropicBetaHeader(`["a","b"]`))
}

func TestFilterBedrockBetaTokens(t *testing.T) {
	t.Run("supported tokens pass through", func(t *testing.T) {
		tokens := []string{"context-1m-2025-08-07", "compact-2026-01-12", "computer-use-2025-11-24"}
		result := bedrock.FilterBedrockBetaTokens(tokens)
		assert.Equal(t, tokens, result)
	})

	t.Run("unsupported tokens are filtered out", func(t *testing.T) {
		tokens := []string{"context-1m-2025-08-07", "output-128k-2025-02-19", "files-api-2025-04-14", "structured-outputs-2025-11-13"}
		result := bedrock.FilterBedrockBetaTokens(tokens)
		assert.Equal(t, []string{"context-1m-2025-08-07"}, result)
	})

	t.Run("advanced-tool-use transforms to tool-search-tool", func(t *testing.T) {
		tokens := []string{"advanced-tool-use-2025-11-20"}
		result := bedrock.FilterBedrockBetaTokens(tokens)
		assert.Contains(t, result, "tool-search-tool-2025-10-19")
		// tool-examples 自动关联
		assert.Contains(t, result, "tool-examples-2025-10-29")
	})

	t.Run("tool-search-tool auto-associates tool-examples", func(t *testing.T) {
		tokens := []string{"tool-search-tool-2025-10-19"}
		result := bedrock.FilterBedrockBetaTokens(tokens)
		assert.Contains(t, result, "tool-search-tool-2025-10-19")
		assert.Contains(t, result, "tool-examples-2025-10-29")
	})

	t.Run("no duplication when tool-examples already present", func(t *testing.T) {
		tokens := []string{"tool-search-tool-2025-10-19", "tool-examples-2025-10-29"}
		result := bedrock.FilterBedrockBetaTokens(tokens)
		count := 0
		for _, t := range result {
			if t == "tool-examples-2025-10-29" {
				count++
			}
		}
		assert.Equal(t, 1, count)
	})

	t.Run("empty input returns nil", func(t *testing.T) {
		result := bedrock.FilterBedrockBetaTokens(nil)
		assert.Nil(t, result)
	})

	t.Run("all unsupported returns nil", func(t *testing.T) {
		result := bedrock.FilterBedrockBetaTokens([]string{"output-128k-2025-02-19", "effort-2025-11-24"})
		assert.Nil(t, result)
	})

	t.Run("duplicate tokens are deduplicated", func(t *testing.T) {
		tokens := []string{"context-1m-2025-08-07", "context-1m-2025-08-07"}
		result := bedrock.FilterBedrockBetaTokens(tokens)
		assert.Equal(t, []string{"context-1m-2025-08-07"}, result)
	})
}

func TestPrepareBedrockRequestBody_BetaFiltering(t *testing.T) {
	input := `{"messages":[{"role":"user","content":"hi"}],"max_tokens":100}`

	t.Run("unsupported beta tokens are filtered", func(t *testing.T) {
		result, err := bedrock.PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1",
			"compact-2026-01-12, output-128k-2025-02-19, files-api-2025-04-14")
		require.NoError(t, err)
		arr := gjson.GetBytes(result, "anthropic_beta").Array()
		require.Len(t, arr, 1)
		assert.Equal(t, "compact-2026-01-12", arr[0].String())
	})

	t.Run("advanced-tool-use transformed in full pipeline", func(t *testing.T) {
		result, err := bedrock.PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1",
			"advanced-tool-use-2025-11-20")
		require.NoError(t, err)
		arr := gjson.GetBytes(result, "anthropic_beta").Array()
		require.Len(t, arr, 2)
		assert.Equal(t, "tool-search-tool-2025-10-19", arr[0].String())
		assert.Equal(t, "tool-examples-2025-10-29", arr[1].String())
	})
}

func TestPrepareBedrockRequestBodyWithTokens_ContextManagementRequiresSupportedBeta(t *testing.T) {
	modelID := "us.anthropic.claude-opus-4-6-v1"

	t.Run("strips context_management when final tokens omit context-management beta", func(t *testing.T) {
		input := `{
			"messages":[{"role":"user","content":"hi"}],
			"max_tokens":100,
			"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}
		}`
		betaTokens := []string{"context-1m-2025-08-07"}
		originalTokens := append([]string(nil), betaTokens...)

		result, err := bedrock.PrepareBedrockRequestBodyWithTokens([]byte(input), modelID, betaTokens, false)
		require.NoError(t, err)

		assert.False(t, gjson.GetBytes(result, "context_management").Exists())
		assert.Equal(t, originalTokens, betaTokens)
		assert.Equal(t, originalTokens, bedrockAnthropicBetaNames(result))
	})

	t.Run("leaves body without context_management otherwise intact", func(t *testing.T) {
		input := `{"messages":[{"role":"user","content":"hi"}],"max_tokens":100}`

		result, err := bedrock.PrepareBedrockRequestBodyWithTokens([]byte(input), modelID, nil, false)
		require.NoError(t, err)

		assert.False(t, gjson.GetBytes(result, "context_management").Exists())
		assert.False(t, gjson.GetBytes(result, "anthropic_beta").Exists())
		assert.Equal(t, "hi", gjson.GetBytes(result, "messages.0.content").String())
		assert.Equal(t, int64(100), gjson.GetBytes(result, "max_tokens").Int())
	})

	t.Run("keeps supported context-management beta and retains field", func(t *testing.T) {
		input := `{
			"messages":[{"role":"user","content":"hi"}],
			"max_tokens":100,
			"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}
		}`

		result, err := bedrock.PrepareBedrockRequestBodyWithTokens(
			[]byte(input),
			modelID,
			[]string{bedrock.BedrockContextManagementBetaToken, "context-1m-2025-08-07"},
			false,
		)
		require.NoError(t, err)

		assert.True(t, gjson.GetBytes(result, "context_management").Exists())
		assert.Equal(t, []string{bedrock.BedrockContextManagementBetaToken, "context-1m-2025-08-07"}, bedrockAnthropicBetaNames(result))
	})
}

func bedrockAnthropicBetaNames(body []byte) []string {
	arr := gjson.GetBytes(body, "anthropic_beta").Array()
	names := make([]string, len(arr))
	for i, token := range arr {
		names[i] = token.String()
	}
	return names
}

func TestAutoInjectBedrockBetaTokens(t *testing.T) {
	t.Run("no auto-inject for thinking (interleaved-thinking not supported)", func(t *testing.T) {
		body := []byte(`{"thinking":{"type":"enabled","budget_tokens":10000},"messages":[{"role":"user","content":"hi"}]}`)
		result := bedrock.AutoInjectBedrockBetaTokens(nil, body, "us.anthropic.claude-opus-4-6-v1")
		// interleaved-thinking-2025-05-14 已从白名单移除，不应自动注入
		assert.Empty(t, result)
	})

	t.Run("no duplicate when already present", func(t *testing.T) {
		body := []byte(`{"thinking":{"type":"enabled","budget_tokens":10000},"messages":[{"role":"user","content":"hi"}]}`)
		result := bedrock.AutoInjectBedrockBetaTokens([]string{"context-1m-2025-08-07"}, body, "us.anthropic.claude-opus-4-6-v1")
		assert.Equal(t, []string{"context-1m-2025-08-07"}, result)
	})

	t.Run("inject computer-use when computer tool present", func(t *testing.T) {
		body := []byte(`{"tools":[{"type":"computer_20250124","name":"computer","display_width_px":1024}],"messages":[{"role":"user","content":"hi"}]}`)
		result := bedrock.AutoInjectBedrockBetaTokens(nil, body, "us.anthropic.claude-opus-4-6-v1")
		assert.Contains(t, result, "computer-use-2025-11-24")
	})

	t.Run("inject advanced-tool-use for programmatic tool calling", func(t *testing.T) {
		body := []byte(`{"tools":[{"name":"bash","allowed_callers":["code_execution_20250825"]}],"messages":[{"role":"user","content":"hi"}]}`)
		result := bedrock.AutoInjectBedrockBetaTokens(nil, body, "us.anthropic.claude-opus-4-6-v1")
		assert.Contains(t, result, "advanced-tool-use-2025-11-20")
	})

	t.Run("inject advanced-tool-use for input examples", func(t *testing.T) {
		body := []byte(`{"tools":[{"name":"bash","input_examples":[{"cmd":"ls"}]}],"messages":[{"role":"user","content":"hi"}]}`)
		result := bedrock.AutoInjectBedrockBetaTokens(nil, body, "us.anthropic.claude-opus-4-6-v1")
		assert.Contains(t, result, "advanced-tool-use-2025-11-20")
	})

	t.Run("inject tool-search-tool directly for pure tool search (no programmatic/inputExamples)", func(t *testing.T) {
		body := []byte(`{"tools":[{"type":"tool_search_tool_regex_20251119","name":"search"}],"messages":[{"role":"user","content":"hi"}]}`)
		result := bedrock.AutoInjectBedrockBetaTokens(nil, body, "us.anthropic.claude-sonnet-4-6")
		// 纯 tool search 场景直接注入 Bedrock 特定头，不走 advanced-tool-use 转换
		assert.Contains(t, result, "tool-search-tool-2025-10-19")
		assert.NotContains(t, result, "advanced-tool-use-2025-11-20")
	})

	t.Run("inject advanced-tool-use when tool search combined with programmatic calling", func(t *testing.T) {
		body := []byte(`{"tools":[{"type":"tool_search_tool_regex_20251119","name":"search"},{"name":"bash","allowed_callers":["code_execution_20250825"]}],"messages":[{"role":"user","content":"hi"}]}`)
		result := bedrock.AutoInjectBedrockBetaTokens(nil, body, "us.anthropic.claude-sonnet-4-6")
		// 混合场景使用 advanced-tool-use（后续由 filter 转换为 tool-search-tool）
		assert.Contains(t, result, "advanced-tool-use-2025-11-20")
	})

	t.Run("do not inject tool-search beta for unsupported models", func(t *testing.T) {
		body := []byte(`{"tools":[{"type":"tool_search_tool_regex_20251119","name":"search"}],"messages":[{"role":"user","content":"hi"}]}`)
		result := bedrock.AutoInjectBedrockBetaTokens(nil, body, "anthropic.claude-3-5-sonnet-20241022-v2:0")
		assert.NotContains(t, result, "advanced-tool-use-2025-11-20")
		assert.NotContains(t, result, "tool-search-tool-2025-10-19")
	})

	t.Run("no injection for regular tools", func(t *testing.T) {
		body := []byte(`{"tools":[{"name":"bash","description":"run bash","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"hi"}]}`)
		result := bedrock.AutoInjectBedrockBetaTokens(nil, body, "us.anthropic.claude-opus-4-6-v1")
		assert.Empty(t, result)
	})

	t.Run("no injection when no features detected", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":"hi"}],"max_tokens":100}`)
		result := bedrock.AutoInjectBedrockBetaTokens(nil, body, "us.anthropic.claude-opus-4-6-v1")
		assert.Empty(t, result)
	})

	t.Run("preserves existing tokens", func(t *testing.T) {
		body := []byte(`{"thinking":{"type":"enabled"},"messages":[{"role":"user","content":"hi"}]}`)
		existing := []string{"context-1m-2025-08-07", "compact-2026-01-12"}
		result := bedrock.AutoInjectBedrockBetaTokens(existing, body, "us.anthropic.claude-opus-4-6-v1")
		assert.Contains(t, result, "context-1m-2025-08-07")
		assert.Contains(t, result, "compact-2026-01-12")
		// interleaved-thinking 不再自动注入
		assert.NotContains(t, result, "interleaved-thinking-2025-05-14")
	})
}

func TestResolveBedrockBetaTokens(t *testing.T) {
	t.Run("body-only tool features resolve to final bedrock tokens", func(t *testing.T) {
		body := []byte(`{"tools":[{"name":"bash","allowed_callers":["code_execution_20250825"]}],"messages":[{"role":"user","content":"hi"}]}`)
		result := bedrock.ResolveBedrockBetaTokens("", body, "us.anthropic.claude-opus-4-6-v1")
		assert.Contains(t, result, "tool-search-tool-2025-10-19")
		assert.Contains(t, result, "tool-examples-2025-10-29")
	})

	t.Run("unsupported client beta tokens are filtered out", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
		result := bedrock.ResolveBedrockBetaTokens("context-1m-2025-08-07,files-api-2025-04-14", body, "us.anthropic.claude-opus-4-6-v1")
		assert.Equal(t, []string{"context-1m-2025-08-07"}, result)
	})
}

func TestPrepareBedrockRequestBody_AutoBetaInjection(t *testing.T) {
	t.Run("thinking in body does not auto-inject beta (not supported)", func(t *testing.T) {
		input := `{"messages":[{"role":"user","content":"hi"}],"max_tokens":100,"thinking":{"type":"enabled","budget_tokens":10000}}`
		result, err := bedrock.PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1", "")
		require.NoError(t, err)
		// interleaved-thinking 已从白名单移除，不应自动注入
		assert.False(t, gjson.GetBytes(result, "anthropic_beta").Exists())
	})

	t.Run("header tokens preserved without auto-injection", func(t *testing.T) {
		input := `{"messages":[{"role":"user","content":"hi"}],"max_tokens":100,"thinking":{"type":"enabled","budget_tokens":10000}}`
		result, err := bedrock.PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1", "context-1m-2025-08-07")
		require.NoError(t, err)
		arr := gjson.GetBytes(result, "anthropic_beta").Array()
		names := make([]string, len(arr))
		for i, v := range arr {
			names[i] = v.String()
		}
		assert.Contains(t, names, "context-1m-2025-08-07")
		// interleaved-thinking 不再自动注入
		assert.NotContains(t, names, "interleaved-thinking-2025-05-14")
	})
}

func TestIsBedrockOpus47OrNewer(t *testing.T) {
	tests := []struct {
		modelID string
		expect  bool
	}{
		{"us.anthropic.claude-opus-4-8", true},
		{"us.anthropic.claude-opus-4-7", true},
		{"us.anthropic.claude-opus-4-6-v1", false},
		{"us.anthropic.claude-opus-4-5-20251101-v1:0", false},
		{"us.anthropic.claude-opus-5-0-v1", true},
		// Sonnet 4.7 不是 Opus，应返回 false。
		{"us.anthropic.claude-sonnet-4-7-v1", false},
		{"us.anthropic.claude-sonnet-4-6", false},
		// Haiku 不是 Opus。
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", false},
		// 非 Claude 模型。
		{"amazon.nova-pro-v1", false},
	}
	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			assert.Equal(t, tt.expect, bedrock.IsBedrockOpus47OrNewer(tt.modelID))
		})
	}
}

func TestSanitizeBedrockThinking(t *testing.T) {
	t.Run("Fable 5 将 enabled 转换为 adaptive 并移除预算", func(t *testing.T) {
		input := `{"thinking":{"type":"enabled","budget_tokens":10000},"messages":[]}`
		result := bedrock.SanitizeBedrockThinking([]byte(input), "anthropic.claude-fable-5")
		assert.Equal(t, "adaptive", gjson.GetBytes(result, "thinking.type").String())
		assert.False(t, gjson.GetBytes(result, "thinking.budget_tokens").Exists())
	})

	t.Run("Fable 5 adaptive 移除预算", func(t *testing.T) {
		input := `{"thinking":{"type":"adaptive","budget_tokens":10000},"messages":[]}`
		result := bedrock.SanitizeBedrockThinking([]byte(input), "claude-fable-5")
		assert.Equal(t, "adaptive", gjson.GetBytes(result, "thinking.type").String())
		assert.False(t, gjson.GetBytes(result, "thinking.budget_tokens").Exists())
	})

	t.Run("opus 4.7 converts enabled to adaptive", func(t *testing.T) {
		input := `{"thinking":{"type":"enabled","budget_tokens":10000},"messages":[]}`
		result := bedrock.SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-7")
		assert.Equal(t, "adaptive", gjson.GetBytes(result, "thinking.type").String())
		assert.False(t, gjson.GetBytes(result, "thinking.budget_tokens").Exists())
	})

	t.Run("opus 4.7 keeps adaptive unchanged", func(t *testing.T) {
		input := `{"thinking":{"type":"adaptive"},"messages":[]}`
		result := bedrock.SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-7")
		assert.Equal(t, "adaptive", gjson.GetBytes(result, "thinking.type").String())
	})

	t.Run("opus 4.6 enabled without budget_tokens gets default", func(t *testing.T) {
		input := `{"thinking":{"type":"enabled"},"messages":[]}`
		result := bedrock.SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-6-v1")
		assert.Equal(t, "enabled", gjson.GetBytes(result, "thinking.type").String())
		assert.Equal(t, int64(bedrock.DefaultThinkingBudgetTokens), gjson.GetBytes(result, "thinking.budget_tokens").Int())
	})

	t.Run("opus 4.6 enabled with budget_tokens unchanged", func(t *testing.T) {
		input := `{"thinking":{"type":"enabled","budget_tokens":20000},"messages":[]}`
		result := bedrock.SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-6-v1")
		assert.Equal(t, "enabled", gjson.GetBytes(result, "thinking.type").String())
		assert.Equal(t, int64(20000), gjson.GetBytes(result, "thinking.budget_tokens").Int())
	})

	t.Run("no thinking field unchanged", func(t *testing.T) {
		input := `{"messages":[]}`
		result := bedrock.SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-7")
		assert.JSONEq(t, input, string(result))
	})

	t.Run("sonnet 4.6 enabled without budget_tokens gets default", func(t *testing.T) {
		input := `{"thinking":{"type":"enabled"},"messages":[]}`
		result := bedrock.SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-sonnet-4-6")
		assert.Equal(t, "enabled", gjson.GetBytes(result, "thinking.type").String())
		assert.Equal(t, int64(bedrock.DefaultThinkingBudgetTokens), gjson.GetBytes(result, "thinking.budget_tokens").Int())
	})
}

func TestSanitizeBedrockToolUseIDs(t *testing.T) {
	t.Run("clean IDs unchanged", func(t *testing.T) {
		input := `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01AbCdEf","name":"bash","input":{}}]}]}`
		result := bedrock.SanitizeBedrockToolUseIDs([]byte(input))
		assert.Equal(t, "toolu_01AbCdEf", gjson.GetBytes(result, "messages.0.content.0.id").String())
	})

	t.Run("dots in tool_use ID replaced with underscores", func(t *testing.T) {
		input := `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu.01.Ab","name":"bash","input":{}}]}]}`
		result := bedrock.SanitizeBedrockToolUseIDs([]byte(input))
		assert.Equal(t, "toolu_01_Ab", gjson.GetBytes(result, "messages.0.content.0.id").String())
	})

	t.Run("special chars in tool_use ID sanitized", func(t *testing.T) {
		input := `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu:01@Ab#Cd","name":"bash","input":{}}]}]}`
		result := bedrock.SanitizeBedrockToolUseIDs([]byte(input))
		id := gjson.GetBytes(result, "messages.0.content.0.id").String()
		assert.Regexp(t, `^[a-zA-Z0-9_-]+$`, id)
	})

	t.Run("tool_result tool_use_id sanitized", func(t *testing.T) {
		input := `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu.01.Ab","content":"ok"}]}]}`
		result := bedrock.SanitizeBedrockToolUseIDs([]byte(input))
		assert.Equal(t, "toolu_01_Ab", gjson.GetBytes(result, "messages.0.content.0.tool_use_id").String())
	})

	t.Run("mixed clean and dirty IDs", func(t *testing.T) {
		input := `{"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"clean_id-123","name":"a","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"dirty.id@456","content":"ok"}]},
			{"role":"assistant","content":[{"type":"tool_use","id":"also.dirty","name":"b","input":{}}]}
		]}`
		result := bedrock.SanitizeBedrockToolUseIDs([]byte(input))
		assert.Equal(t, "clean_id-123", gjson.GetBytes(result, "messages.0.content.0.id").String())
		assert.Equal(t, "dirty_id_456", gjson.GetBytes(result, "messages.1.content.0.tool_use_id").String())
		assert.Equal(t, "also_dirty", gjson.GetBytes(result, "messages.2.content.0.id").String())
	})

	t.Run("no messages unchanged", func(t *testing.T) {
		input := `{"system":[{"type":"text","text":"hi"}]}`
		result := bedrock.SanitizeBedrockToolUseIDs([]byte(input))
		assert.JSONEq(t, input, string(result))
	})

	t.Run("string content skipped", func(t *testing.T) {
		input := `{"messages":[{"role":"user","content":"plain text"}]}`
		result := bedrock.SanitizeBedrockToolUseIDs([]byte(input))
		assert.JSONEq(t, input, string(result))
	})

	t.Run("empty ID skipped", func(t *testing.T) {
		input := `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"","name":"a","input":{}}]}]}`
		result := bedrock.SanitizeBedrockToolUseIDs([]byte(input))
		assert.Equal(t, "", gjson.GetBytes(result, "messages.0.content.0.id").String())
	})
}

func TestSanitizeBedrockThinking_EdgeCases(t *testing.T) {
	t.Run("opus 4.7 enabled without budget_tokens converts to adaptive", func(t *testing.T) {
		input := `{"thinking":{"type":"enabled"},"messages":[]}`
		result := bedrock.SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-7")
		assert.Equal(t, "adaptive", gjson.GetBytes(result, "thinking.type").String())
		assert.False(t, gjson.GetBytes(result, "thinking.budget_tokens").Exists())
	})

	t.Run("thinking type disabled unchanged", func(t *testing.T) {
		input := `{"thinking":{"type":"disabled"},"messages":[]}`
		result := bedrock.SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-7")
		assert.Equal(t, "disabled", gjson.GetBytes(result, "thinking.type").String())
	})

	t.Run("thinking type empty string unchanged", func(t *testing.T) {
		input := `{"thinking":{"type":""},"messages":[]}`
		result := bedrock.SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-7")
		assert.JSONEq(t, input, string(result))
	})

	t.Run("thinking is not an object unchanged", func(t *testing.T) {
		input := `{"thinking":true,"messages":[]}`
		result := bedrock.SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-7")
		assert.JSONEq(t, input, string(result))
	})

	t.Run("opus 4.7 adaptive with budget_tokens preserved", func(t *testing.T) {
		input := `{"thinking":{"type":"adaptive","budget_tokens":5000},"messages":[]}`
		result := bedrock.SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-7")
		assert.Equal(t, "adaptive", gjson.GetBytes(result, "thinking.type").String())
		assert.Equal(t, int64(5000), gjson.GetBytes(result, "thinking.budget_tokens").Int())
	})

	// Forward() 传入的是 parsed.Model（如 "claude-opus-4-7" 这类标准模型名）。
	t.Run("standard model name opus 4.7 converts enabled to adaptive", func(t *testing.T) {
		input := `{"thinking":{"type":"enabled","budget_tokens":10000},"messages":[]}`
		result := bedrock.SanitizeBedrockThinking([]byte(input), "claude-opus-4-7")
		assert.Equal(t, "adaptive", gjson.GetBytes(result, "thinking.type").String())
		assert.False(t, gjson.GetBytes(result, "thinking.budget_tokens").Exists())
	})

	t.Run("standard model name opus 4.6 keeps enabled", func(t *testing.T) {
		input := `{"thinking":{"type":"enabled","budget_tokens":10000},"messages":[]}`
		result := bedrock.SanitizeBedrockThinking([]byte(input), "claude-opus-4-6")
		assert.Equal(t, "enabled", gjson.GetBytes(result, "thinking.type").String())
		assert.Equal(t, int64(10000), gjson.GetBytes(result, "thinking.budget_tokens").Int())
	})
}

func TestIsBedrockOpus47OrNewer_EdgeCases(t *testing.T) {
	tests := []struct {
		modelID string
		expect  bool
	}{
		{"anthropic.claude-opus-4-8", true},
		{"anthropic.claude-opus-4-7", true},
		{"us.anthropic.claude-opus-4-7-20270101-v1:0", true},
		{"", false},
		// Forward() 传入的是 parsed.Model（标准模型名），不一定是 Bedrock 模型 ID。
		{"claude-opus-4-8", true},
		{"claude-opus-4-7", true},
		{"claude-opus-4-6", false},
		{"claude-sonnet-4-7", false},
	}
	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			assert.Equal(t, tt.expect, bedrock.IsBedrockOpus47OrNewer(tt.modelID))
		})
	}
}

func TestPrepareBedrockRequestBodyWithTokens_CCCompat(t *testing.T) {
	input := `{
		"model":"claude-opus-4-6",
		"stream":true,
		"max_tokens":16384,
		"thinking":{"type":"enabled"},
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu.01.Ab","name":"bash","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu.01.Ab","content":"ok"}]}
		]
	}`

	t.Run("ccCompat=false skips thinking and toolUseID sanitization", func(t *testing.T) {
		result, err := bedrock.PrepareBedrockRequestBodyWithTokens([]byte(input), "us.anthropic.claude-opus-4-6-v1", nil, false)
		require.NoError(t, err)
		assert.Equal(t, "enabled", gjson.GetBytes(result, "thinking.type").String())
		assert.False(t, gjson.GetBytes(result, "thinking.budget_tokens").Exists())
		assert.Equal(t, "toolu.01.Ab", gjson.GetBytes(result, "messages.0.content.0.id").String())
	})

	t.Run("ccCompat=true applies thinking fix and toolUseID sanitization (opus 4.6)", func(t *testing.T) {
		result, err := bedrock.PrepareBedrockRequestBodyWithTokens([]byte(input), "us.anthropic.claude-opus-4-6-v1", nil, true)
		require.NoError(t, err)
		assert.Equal(t, "enabled", gjson.GetBytes(result, "thinking.type").String())
		assert.Equal(t, int64(bedrock.DefaultThinkingBudgetTokens), gjson.GetBytes(result, "thinking.budget_tokens").Int())
		assert.Equal(t, "toolu_01_Ab", gjson.GetBytes(result, "messages.0.content.0.id").String())
		assert.Equal(t, "toolu_01_Ab", gjson.GetBytes(result, "messages.1.content.0.tool_use_id").String())
	})

	t.Run("ccCompat=true converts thinking to adaptive for opus 4.7", func(t *testing.T) {
		result, err := bedrock.PrepareBedrockRequestBodyWithTokens([]byte(input), "us.anthropic.claude-opus-4-7", nil, true)
		require.NoError(t, err)
		assert.Equal(t, "adaptive", gjson.GetBytes(result, "thinking.type").String())
		assert.False(t, gjson.GetBytes(result, "thinking.budget_tokens").Exists())
		assert.Equal(t, "toolu_01_Ab", gjson.GetBytes(result, "messages.0.content.0.id").String())
	})
}

func TestSanitizeBedrockCCFields(t *testing.T) {
	t.Run("removes service_tier and interface_geo", func(t *testing.T) {
		body := []byte(`{"model":"claude-opus-4-6","service_tier":"standard","interface_geo":"us","messages":[]}`)
		result := bedrock.SanitizeBedrockCCFields(body)
		assert.False(t, gjson.GetBytes(result, "service_tier").Exists())
		assert.False(t, gjson.GetBytes(result, "interface_geo").Exists())
		assert.True(t, gjson.GetBytes(result, "messages").Exists())
	})

	t.Run("removes context_management", func(t *testing.T) {
		body := []byte(`{"model":"claude-opus-4-6","context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},"messages":[]}`)
		result := bedrock.SanitizeBedrockCCFields(body)
		assert.False(t, gjson.GetBytes(result, "context_management").Exists())
		assert.True(t, gjson.GetBytes(result, "messages").Exists())
	})

	t.Run("injects max_tokens when missing", func(t *testing.T) {
		body := []byte(`{"model":"claude-opus-4-6","messages":[]}`)
		result := bedrock.SanitizeBedrockCCFields(body)
		assert.Equal(t, int64(bedrock.DefaultCCMaxTokens), gjson.GetBytes(result, "max_tokens").Int())
	})

	t.Run("preserves existing max_tokens", func(t *testing.T) {
		body := []byte(`{"model":"claude-opus-4-6","max_tokens":4096,"messages":[]}`)
		result := bedrock.SanitizeBedrockCCFields(body)
		assert.Equal(t, int64(4096), gjson.GetBytes(result, "max_tokens").Int())
	})

	t.Run("injects anthropic_version when missing", func(t *testing.T) {
		body := []byte(`{"model":"claude-opus-4-6","messages":[]}`)
		result := bedrock.SanitizeBedrockCCFields(body)
		assert.Equal(t, "bedrock-2023-05-31", gjson.GetBytes(result, "anthropic_version").String())
	})

	t.Run("preserves existing anthropic_version", func(t *testing.T) {
		body := []byte(`{"model":"claude-opus-4-6","anthropic_version":"bedrock-2023-05-31","messages":[]}`)
		result := bedrock.SanitizeBedrockCCFields(body)
		assert.Equal(t, "bedrock-2023-05-31", gjson.GetBytes(result, "anthropic_version").String())
	})

	t.Run("no-op when fields already clean", func(t *testing.T) {
		body := []byte(`{"model":"claude-opus-4-6","max_tokens":81920,"anthropic_version":"bedrock-2023-05-31","messages":[]}`)
		result := bedrock.SanitizeBedrockCCFields(body)
		assert.Equal(t, int64(bedrock.DefaultCCMaxTokens), gjson.GetBytes(result, "max_tokens").Int())
		assert.Equal(t, "bedrock-2023-05-31", gjson.GetBytes(result, "anthropic_version").String())
		assert.False(t, gjson.GetBytes(result, "service_tier").Exists())
		assert.False(t, gjson.GetBytes(result, "interface_geo").Exists())
		assert.False(t, gjson.GetBytes(result, "context_management").Exists())
	})

	t.Run("full CC request sanitization", func(t *testing.T) {
		body := []byte(`{
			"model":"claude-opus-4-6",
			"service_tier":"standard",
			"interface_geo":"us",
			"context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},
			"messages":[{"role":"user","content":"hello"}],
			"thinking":{"type":"enabled"}
		}`)
		result := bedrock.SanitizeBedrockCCFields(body)
		assert.False(t, gjson.GetBytes(result, "service_tier").Exists())
		assert.False(t, gjson.GetBytes(result, "interface_geo").Exists())
		assert.False(t, gjson.GetBytes(result, "context_management").Exists())
		assert.Equal(t, int64(bedrock.DefaultCCMaxTokens), gjson.GetBytes(result, "max_tokens").Int())
		assert.Equal(t, "bedrock-2023-05-31", gjson.GetBytes(result, "anthropic_version").String())
		assert.Equal(t, "enabled", gjson.GetBytes(result, "thinking.type").String())
	})
}

func TestSanitizeBedrockCCBetaTokens(t *testing.T) {
	t.Run("filters unsupported beta tokens", func(t *testing.T) {
		input := `{"anthropic_beta":["prompt-caching-2024-07-31","context-1m-2025-08-07","unsupported-feature"],"messages":[]}`
		result := bedrock.SanitizeBedrockCCBetaTokens([]byte(input), "claude-opus-4-6")
		beta := gjson.GetBytes(result, "anthropic_beta")
		assert.True(t, beta.Exists())
		assert.True(t, beta.IsArray())
		tokens := beta.Array()
		assert.Equal(t, 1, len(tokens))
		assert.Equal(t, "context-1m-2025-08-07", tokens[0].String())
	})

	t.Run("removes anthropic_beta if all tokens filtered", func(t *testing.T) {
		input := `{"anthropic_beta":["prompt-caching-2024-07-31","unsupported-feature"],"messages":[]}`
		result := bedrock.SanitizeBedrockCCBetaTokens([]byte(input), "claude-opus-4-6")
		assert.False(t, gjson.GetBytes(result, "anthropic_beta").Exists())
	})

	t.Run("thinking alone does not auto-inject beta tokens", func(t *testing.T) {
		input := `{"anthropic_beta":[],"thinking":{"type":"enabled"},"messages":[]}`
		result := bedrock.SanitizeBedrockCCBetaTokens([]byte(input), "claude-opus-4-6")
		assert.False(t, gjson.GetBytes(result, "anthropic_beta").Exists())
	})

	t.Run("auto-injects computer-use beta token", func(t *testing.T) {
		input := `{"anthropic_beta":[],"tools":[{"type":"computer_20250124","name":"computer"}],"messages":[]}`
		result := bedrock.SanitizeBedrockCCBetaTokens([]byte(input), "claude-opus-4-6")
		beta := gjson.GetBytes(result, "anthropic_beta")
		assert.True(t, beta.Exists())
		tokens := beta.Array()
		assert.Equal(t, 1, len(tokens))
		assert.Equal(t, "computer-use-2025-11-24", tokens[0].String())
	})

	t.Run("transforms advanced-tool-use to tool-search-tool", func(t *testing.T) {
		input := `{"anthropic_beta":["advanced-tool-use-2025-11-20"],"messages":[]}`
		result := bedrock.SanitizeBedrockCCBetaTokens([]byte(input), "claude-opus-4-6")
		beta := gjson.GetBytes(result, "anthropic_beta")
		tokens := beta.Array()
		assert.Equal(t, 2, len(tokens)) // tool-search-tool + 自动关联的 tool-examples
		assert.Contains(t, []string{tokens[0].String(), tokens[1].String()}, "tool-search-tool-2025-10-19")
		assert.Contains(t, []string{tokens[0].String(), tokens[1].String()}, "tool-examples-2025-10-29")
	})

	t.Run("no-op when anthropic_beta not present", func(t *testing.T) {
		input := `{"messages":[]}`
		result := bedrock.SanitizeBedrockCCBetaTokens([]byte(input), "claude-opus-4-6")
		assert.False(t, gjson.GetBytes(result, "anthropic_beta").Exists())
	})

	t.Run("preserves supported beta tokens", func(t *testing.T) {
		input := `{"anthropic_beta":["computer-use-2025-11-24","context-1m-2025-08-07"],"messages":[]}`
		result := bedrock.SanitizeBedrockCCBetaTokens([]byte(input), "claude-opus-4-6")
		beta := gjson.GetBytes(result, "anthropic_beta")
		tokens := beta.Array()
		assert.Equal(t, 2, len(tokens))
		assert.Contains(t, []string{tokens[0].String(), tokens[1].String()}, "computer-use-2025-11-24")
		assert.Contains(t, []string{tokens[0].String(), tokens[1].String()}, "context-1m-2025-08-07")
	})
}
