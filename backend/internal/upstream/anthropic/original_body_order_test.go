package anthropic_test

import (
	"strings"
	"testing"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeClaudeOAuthRequestBody_PreservesTopLevelFieldOrder(t *testing.T) {
	body := []byte(`{"alpha":1,"model":"claude-3-5-sonnet-latest","temperature":0.2,"system":"You are OpenCode, the best coding agent on the planet.","messages":[],"tool_choice":{"type":"auto"},"omega":2}`)

	result, modelID := claude.NormalizeClaudeOAuthRequestBody(body, "claude-3-5-sonnet-latest", claude.ClaudeOAuthNormalizeOptions{
		InjectMetadata: true,
		MetadataUserID: "user-1",
	})
	resultStr := string(result)

	require.Equal(t, claude.NormalizeModelID("claude-3-5-sonnet-latest"), modelID)
	assertJSONTokenOrder(t, resultStr, `"alpha"`, `"model"`, `"temperature"`, `"system"`, `"messages"`, `"omega"`, `"tools"`, `"metadata"`, `"max_tokens"`)
	require.Contains(t, resultStr, `"temperature":0.2`)
	require.NotContains(t, resultStr, `"tool_choice"`)
	require.Contains(t, resultStr, `"system":"`+claude.ClaudeCodeSystemPrompt+`"`)
	require.Contains(t, resultStr, `"tools":[]`)
	require.Contains(t, resultStr, `"metadata":{"user_id":"user-1"}`)
	require.Contains(t, resultStr, `"max_tokens":128000`)
}

func TestInjectClaudeCodePrompt_PreservesFieldOrder(t *testing.T) {
	body := []byte(`{"alpha":1,"system":[{"id":"block-1","type":"text","text":"Custom"}],"messages":[],"omega":2}`)

	result := claude.InjectClaudeCodePrompt(body, []any{
		map[string]any{"id": "block-1", "type": "text", "text": "Custom"},
	})
	resultStr := string(result)

	assertJSONTokenOrder(t, resultStr, `"alpha"`, `"system"`, `"messages"`, `"omega"`)
	require.Contains(t, resultStr, `{"id":"block-1","type":"text","text":"`+claude.ClaudeCodeSystemPrompt+`\n\nCustom"}`)
}

func TestEnforceCacheControlLimit_PreservesTopLevelFieldOrder(t *testing.T) {
	body := []byte(`{"alpha":1,"system":[{"type":"text","text":"s1","cache_control":{"type":"ephemeral"}},{"type":"text","text":"s2","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"m1","cache_control":{"type":"ephemeral"}},{"type":"text","text":"m2","cache_control":{"type":"ephemeral"}},{"type":"text","text":"m3","cache_control":{"type":"ephemeral"}}]}],"omega":2}`)

	result := claude.EnforceCacheControlLimit(body)
	resultStr := string(result)

	assertJSONTokenOrder(t, resultStr, `"alpha"`, `"system"`, `"messages"`, `"omega"`)
	require.Equal(t, 4, strings.Count(resultStr, `"cache_control"`))
}

func TestEnforceCacheControlLimit_CountsToolsAndPreservesMessageAnchorsFirst(t *testing.T) {
	body := []byte(`{"alpha":1,"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"m1","cache_control":{"type":"ephemeral"}},{"type":"text","text":"m2","cache_control":{"type":"ephemeral"}},{"type":"text","text":"m3","cache_control":{"type":"ephemeral"}}]}],"tools":[{"name":"a","input_schema":{},"cache_control":{"type":"ephemeral"}}],"omega":2}`)

	result := claude.EnforceCacheControlLimit(body)
	resultStr := string(result)

	assertJSONTokenOrder(t, resultStr, `"alpha"`, `"system"`, `"messages"`, `"tools"`, `"omega"`)
	require.Equal(t, 4, strings.Count(resultStr, `"cache_control"`))
	require.True(t, gjson.GetBytes(result, "system.0.cache_control").Exists())
	require.True(t, gjson.GetBytes(result, "messages.0.content.0.cache_control").Exists())
	require.True(t, gjson.GetBytes(result, "messages.0.content.1.cache_control").Exists())
	require.True(t, gjson.GetBytes(result, "messages.0.content.2.cache_control").Exists())
	require.False(t, gjson.GetBytes(result, "tools.0.cache_control").Exists())
}

func TestInjectAnthropicCacheControlTTL1h_OnlyUpdatesExistingEphemeralCacheControl(t *testing.T) {
	body := []byte(`{"alpha":1,"cache_control":{"type":"ephemeral"},"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"5m"}},{"type":"text","text":"plain"}],"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}},{"type":"text","text":"non","cache_control":{"type":"persistent","ttl":"5m"}}]}],"tools":[{"name":"a","input_schema":{},"cache_control":{"type":"ephemeral"}}],"omega":2}`)

	result := claude.InjectAnthropicCacheControlTTL1h(body)
	resultStr := string(result)

	assertJSONTokenOrder(t, resultStr, `"alpha"`, `"cache_control"`, `"system"`, `"messages"`, `"tools"`, `"omega"`)
	require.Equal(t, "1h", gjson.GetBytes(result, "cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(result, "system.0.cache_control.ttl").String())
	require.False(t, gjson.GetBytes(result, "system.1.cache_control").Exists())
	require.Equal(t, "1h", gjson.GetBytes(result, "messages.0.content.0.cache_control.ttl").String())
	require.Equal(t, "5m", gjson.GetBytes(result, "messages.0.content.1.cache_control.ttl").String())
	require.Equal(t, "1h", gjson.GetBytes(result, "tools.0.cache_control.ttl").String())
}
