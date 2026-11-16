package bedrock

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	claude "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
)

// TestPrepareBedrockRequestBody_BasicFields supplies model and streaming
// fields in a direct Messages body. It checks Invoke version injection,
// URL-carried field removal, and retention of the requested output limit.
func TestPrepareBedrockRequestBody_BasicFields(t *testing.T) {
	input := `{"model":"claude-opus-4-6","stream":true,"max_tokens":1024,"messages":[{"role":"user","content":"hi"}]}`
	result, err := PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1", "")
	require.NoError(t, err)

	assert.Equal(t, "bedrock-2023-05-31", gjson.GetBytes(result, "anthropic_version").String())
	assert.False(t, gjson.GetBytes(result, "model").Exists())
	assert.False(t, gjson.GetBytes(result, "stream").Exists())
	assert.Equal(t, int64(1024), gjson.GetBytes(result, "max_tokens").Int())
}

// TestPrepareBedrockRequestBody_OutputFormatInlineSchema supplies array/string
// user content and missing insertion targets. It checks schema text follows
// the original prompt and object output_format is removed even when insertion
// cannot occur.
func TestPrepareBedrockRequestBody_OutputFormatInlineSchema(t *testing.T) {
	t.Run("schema inlined into last user message array content", func(t *testing.T) {
		input := `{"model":"claude-sonnet-4-5","output_format":{"type":"json","schema":{"name":"string"}},"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`
		result, err := PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-sonnet-4-5-v1", "")
		require.NoError(t, err)

		assert.False(t, gjson.GetBytes(result, "output_format").Exists())
		// The schema follows the original user text as another text block.
		contentArr := gjson.GetBytes(result, "messages.0.content").Array()
		require.Len(t, contentArr, 2)
		assert.Equal(t, "text", contentArr[1].Get("type").String())
		assert.Contains(t, contentArr[1].Get("text").String(), `"name":"string"`)
	})

	t.Run("schema inlined into string content", func(t *testing.T) {
		input := `{"model":"claude-sonnet-4-5","output_format":{"type":"json","schema":{"result":"number"}},"messages":[{"role":"user","content":"compute this"}]}`
		result, err := PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-sonnet-4-5-v1", "")
		require.NoError(t, err)

		assert.False(t, gjson.GetBytes(result, "output_format").Exists())
		contentArr := gjson.GetBytes(result, "messages.0.content").Array()
		require.Len(t, contentArr, 2)
		assert.Equal(t, "compute this", contentArr[0].Get("text").String())
		assert.Contains(t, contentArr[1].Get("text").String(), `"result":"number"`)
	})

	t.Run("no schema field just removes output_format", func(t *testing.T) {
		input := `{"model":"claude-sonnet-4-5","output_format":{"type":"json"},"messages":[{"role":"user","content":"hi"}]}`
		result, err := PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-sonnet-4-5-v1", "")
		require.NoError(t, err)

		assert.False(t, gjson.GetBytes(result, "output_format").Exists())
	})

	t.Run("no messages just removes output_format", func(t *testing.T) {
		input := `{"model":"claude-sonnet-4-5","output_format":{"type":"json","schema":{"name":"string"}}}`
		result, err := PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-sonnet-4-5-v1", "")
		require.NoError(t, err)

		assert.False(t, gjson.GetBytes(result, "output_format").Exists())
	})
}

// TestPrepareBedrockRequestBody_RemoveOutputConfig supplies direct
// output_config with ordinary messages. It checks that preparation removes
// that object before the body is sent using the Invoke dialect.
func TestPrepareBedrockRequestBody_RemoveOutputConfig(t *testing.T) {
	input := `{"model":"claude-sonnet-4-5","output_config":{"max_tokens":100},"messages":[]}`
	result, err := PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-sonnet-4-5-v1", "")
	require.NoError(t, err)

	assert.False(t, gjson.GetBytes(result, "output_config").Exists())
}

// TestRemoveCustomFieldFromTools mixes extended and ordinary tool definitions.
// It checks every custom extension is removed while tool names, descriptions,
// and the number of definitions remain available.
func TestRemoveCustomFieldFromTools(t *testing.T) {
	input := `{
		"tools": [
			{"name":"tool1","custom":{"defer_loading":true},"description":"desc1"},
			{"name":"tool2","description":"desc2"},
			{"name":"tool3","custom":{"defer_loading":true,"other":123},"description":"desc3"}
		]
	}`
	result := RemoveCustomFieldFromTools([]byte(input))

	tools := gjson.GetBytes(result, "tools").Array()
	require.Len(t, tools, 3)
	assert.False(t, tools[0].Get("custom").Exists())
	assert.Equal(t, "tool1", tools[0].Get("name").String())
	assert.Equal(t, "desc1", tools[0].Get("description").String())
	assert.Equal(t, "tool2", tools[1].Get("name").String())
	assert.False(t, tools[2].Get("custom").Exists())
	assert.Equal(t, "tool3", tools[2].Get("name").String())
}

// TestRemoveCustomFieldFromTools_NoTools supplies a Messages request
// containing no tools field. It checks the cleanup helper returns equivalent
// JSON rather than manufacturing a tool collection.
func TestRemoveCustomFieldFromTools_NoTools(t *testing.T) {
	input := `{"messages":[{"role":"user","content":"hi"}]}`
	result := RemoveCustomFieldFromTools([]byte(input))
	assert.JSONEq(t, input, string(result))
}

// TestSanitizeBedrockCacheControl_RemoveScope supplies cache controls in both
// system and message content. It checks scope removal while the ephemeral
// cache type remains at both locations.
func TestSanitizeBedrockCacheControl_RemoveScope(t *testing.T) {
	input := `{
		"system": [{"type":"text","text":"sys","cache_control":{"type":"ephemeral","scope":"global"}}],
		"messages": [{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","scope":"global"}}]}]
	}`
	result := SanitizeBedrockCacheControl([]byte(input), "us.anthropic.claude-opus-4-6-v1")

	assert.False(t, gjson.GetBytes(result, "system.0.cache_control.scope").Exists())
	assert.False(t, gjson.GetBytes(result, "messages.0.content.0.cache_control.scope").Exists())
	assert.Equal(t, "ephemeral", gjson.GetBytes(result, "system.0.cache_control.type").String())
	assert.Equal(t, "ephemeral", gjson.GetBytes(result, "messages.0.content.0.cache_control.type").String())
}

// TestSanitizeBedrockCacheControl_TTL_OldModel supplies a TTL to a catalogue
// entry without TTL capability. It checks removal of ttl while retaining the
// remaining cache-control type.
func TestSanitizeBedrockCacheControl_TTL_OldModel(t *testing.T) {
	input := `{
		"system": [{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"5m"}}]
	}`
	// This catalog entry has no cache TTL capability.
	result := SanitizeBedrockCacheControl([]byte(input), "anthropic.claude-3-5-sonnet-20241022-v2:0")

	assert.False(t, gjson.GetBytes(result, "system.0.cache_control.ttl").Exists())
	assert.Equal(t, "ephemeral", gjson.GetBytes(result, "system.0.cache_control.type").String())
}

// TestSanitizeBedrockCacheControl_TTL_Claude45_Supported supplies a recognized
// five-minute TTL in system cache control for the registered model. It checks
// capability-driven cleanup retains that exact duration field.
func TestSanitizeBedrockCacheControl_TTL_Claude45_Supported(t *testing.T) {
	input := `{
		"system": [{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"5m"}}]
	}`
	// The registered Sonnet 4.5 entry permits the recognized TTL values.
	result := SanitizeBedrockCacheControl([]byte(input), "us.anthropic.claude-sonnet-4-5-20250929-v1:0")

	assert.True(t, gjson.GetBytes(result, "system.0.cache_control.ttl").Exists())
	assert.Equal(t, "5m", gjson.GetBytes(result, "system.0.cache_control.ttl").String())
}

// TestSanitizeBedrockCacheControl_TTL_Claude45_UnsupportedValue supplies
// ten-minute TTL text to a TTL-capable model. It checks the unsupported
// spelling is removed even though the model supports other TTL values.
func TestSanitizeBedrockCacheControl_TTL_Claude45_UnsupportedValue(t *testing.T) {
	input := `{
		"system": [{"type":"text","text":"sys","cache_control":{"type":"ephemeral","ttl":"10m"}}]
	}`
	// Local cleanup removes unrecognized duration text for this TTL-capable entry.
	result := SanitizeBedrockCacheControl([]byte(input), "us.anthropic.claude-sonnet-4-5-20250929-v1:0")

	assert.False(t, gjson.GetBytes(result, "system.0.cache_control.ttl").Exists())
}

// TestSanitizeBedrockCacheControl_TTL_Claude46 supplies one-hour TTL in
// message cache control for the registered Opus model. It checks the field
// remains with its expected value.
func TestSanitizeBedrockCacheControl_TTL_Claude46(t *testing.T) {
	input := `{
		"messages": [{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]
	}`
	result := SanitizeBedrockCacheControl([]byte(input), "us.anthropic.claude-opus-4-6-v1")

	assert.True(t, gjson.GetBytes(result, "messages.0.content.0.cache_control.ttl").Exists())
	assert.Equal(t, "1h", gjson.GetBytes(result, "messages.0.content.0.cache_control.ttl").String())
}

// TestSanitizeBedrockCacheControl_TopLevelAndToolTTL supplies scope and
// one-hour TTL on top-level and tool cache-control markers across TTL-capable
// and older Bedrock models. It checks scope removal everywhere, one-hour TTL
// retention on Claude 4.6, and TTL removal on Claude 3.5.
func TestSanitizeBedrockCacheControl_TopLevelAndToolTTL(t *testing.T) {
	input := `{
		"cache_control": {"type":"ephemeral","scope":"global","ttl":"1h"},
		"tools": [
			{"name":"bash","cache_control":{"type":"ephemeral","scope":"global","ttl":"1h"},"input_schema":{"type":"object"}}
		],
		"messages": [{"role":"user","content":"hi"}]
	}`

	supported := SanitizeBedrockCacheControl([]byte(input), "us.anthropic.claude-opus-4-6-v1")
	assert.False(t, gjson.GetBytes(supported, "cache_control.scope").Exists())
	assert.Equal(t, "ephemeral", gjson.GetBytes(supported, "cache_control.type").String())
	assert.Equal(t, "1h", gjson.GetBytes(supported, "cache_control.ttl").String())
	assert.False(t, gjson.GetBytes(supported, "tools.0.cache_control.scope").Exists())
	assert.Equal(t, "ephemeral", gjson.GetBytes(supported, "tools.0.cache_control.type").String())
	assert.Equal(t, "1h", gjson.GetBytes(supported, "tools.0.cache_control.ttl").String())

	older := SanitizeBedrockCacheControl([]byte(input), "anthropic.claude-3-5-sonnet-20241022-v2:0")
	assert.False(t, gjson.GetBytes(older, "cache_control.scope").Exists())
	assert.False(t, gjson.GetBytes(older, "cache_control.ttl").Exists())
	assert.Equal(t, "ephemeral", gjson.GetBytes(older, "cache_control.type").String())
	assert.False(t, gjson.GetBytes(older, "tools.0.cache_control.scope").Exists())
	assert.False(t, gjson.GetBytes(older, "tools.0.cache_control.ttl").Exists())
	assert.Equal(t, "ephemeral", gjson.GetBytes(older, "tools.0.cache_control.type").String())
}

// TestSanitizeBedrockCacheControl_NoCacheControl supplies system and message
// text without cache objects. It checks cleanup returns equivalent JSON and
// leaves those ordinary blocks available.
func TestSanitizeBedrockCacheControl_NoCacheControl(t *testing.T) {
	input := `{"system":[{"type":"text","text":"sys"}],"messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`
	result := SanitizeBedrockCacheControl([]byte(input), "us.anthropic.claude-opus-4-6-v1")
	assert.JSONEq(t, input, string(result))
}

// TestIsBedrockClaude45OrNewer compares registered TTL-capable IDs with older,
// unknown, and other-vendor entries. It checks catalogue capability lookup
// rather than deriving support from apparent model version text.
func TestIsBedrockClaude45OrNewer(t *testing.T) {
	tests := []struct {
		modelID string // catalogue lookup model identifier
		expect  bool   // expected capability predicate result
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
		// An unregistered version must not inherit another model's capabilities.
		{"us.anthropic.claude-sonnet-5-0-v1", false},
		{"us.anthropic.claude-opus-4-7", true},
		{"anthropic.claude-opus-4-1-v1", false},
		{"anthropic.claude-sonnet-4-0-v1", false},
		{"amazon.nova-pro-v1", false},
		{"meta.llama3-70b", false},
	}
	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			assert.Equal(t, tt.expect, IsBedrockClaude45OrNewer(tt.modelID))
		})
	}
}

// TestPrepareBedrockRequestBody_FullIntegration combines schema, tool
// extensions, beta input, and cache controls in one body. It checks their
// combined preparation results and retained user text, output limit, recognized TTL
// values, and tool identities.
func TestPrepareBedrockRequestBody_FullIntegration(t *testing.T) {
	// Combine Claude Code extensions to exercise their interaction during preparation.
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
	result, err := PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1", betaHeader)
	require.NoError(t, err)

	assert.Equal(t, "bedrock-2023-05-31", gjson.GetBytes(result, "anthropic_version").String())
	assert.False(t, gjson.GetBytes(result, "model").Exists())
	assert.False(t, gjson.GetBytes(result, "stream").Exists())
	assert.Equal(t, int64(16384), gjson.GetBytes(result, "max_tokens").Int())

	betaArr := gjson.GetBytes(result, "anthropic_beta").Array()
	require.Len(t, betaArr, 2)
	assert.Equal(t, "context-1m-2025-08-07", betaArr[0].String())
	assert.Equal(t, "compact-2026-01-12", betaArr[1].String())

	assert.False(t, gjson.GetBytes(result, "output_format").Exists())
	assert.False(t, gjson.GetBytes(result, "output_config").Exists())
	// Keep the original text before the inlined schema.
	contentArr := gjson.GetBytes(result, "messages.0.content").Array()
	require.Len(t, contentArr, 2)
	assert.Equal(t, "hello", contentArr[0].Get("text").String())
	assert.Contains(t, contentArr[1].Get("text").String(), `"result":"string"`)

	assert.False(t, gjson.GetBytes(result, "tools.0.custom").Exists())
	assert.Equal(t, "bash", gjson.GetBytes(result, "tools.0.name").String())
	assert.Equal(t, "read", gjson.GetBytes(result, "tools.1.name").String())

	// Scope is removed while the catalog-supported TTL values survive.
	assert.False(t, gjson.GetBytes(result, "system.0.cache_control.scope").Exists())
	assert.Equal(t, "ephemeral", gjson.GetBytes(result, "system.0.cache_control.type").String())
	assert.Equal(t, "5m", gjson.GetBytes(result, "system.0.cache_control.ttl").String())
	assert.Equal(t, "1h", gjson.GetBytes(result, "messages.0.content.0.cache_control.ttl").String())
}

// TestPrepareBedrockRequestBody_BetaHeader supplies absent, single,
// comma-separated, and JSON-array beta input. It checks final body encoding
// and accepted token order for the supported fixture values.
func TestPrepareBedrockRequestBody_BetaHeader(t *testing.T) {
	input := `{"messages":[{"role":"user","content":"hi"}],"max_tokens":100}`

	t.Run("empty beta header", func(t *testing.T) {
		result, err := PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1", "")
		require.NoError(t, err)
		assert.False(t, gjson.GetBytes(result, "anthropic_beta").Exists())
	})

	t.Run("single beta token", func(t *testing.T) {
		result, err := PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1", "context-1m-2025-08-07")
		require.NoError(t, err)
		arr := gjson.GetBytes(result, "anthropic_beta").Array()
		require.Len(t, arr, 1)
		assert.Equal(t, "context-1m-2025-08-07", arr[0].String())
	})

	t.Run("multiple beta tokens with spaces", func(t *testing.T) {
		result, err := PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1", "context-1m-2025-08-07 , compact-2026-01-12 ")
		require.NoError(t, err)
		arr := gjson.GetBytes(result, "anthropic_beta").Array()
		require.Len(t, arr, 2)
		assert.Equal(t, "context-1m-2025-08-07", arr[0].String())
		assert.Equal(t, "compact-2026-01-12", arr[1].String())
	})

	t.Run("json array beta header", func(t *testing.T) {
		result, err := PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1", `["context-1m-2025-08-07","compact-2026-01-12"]`)
		require.NoError(t, err)
		arr := gjson.GetBytes(result, "anthropic_beta").Array()
		require.Len(t, arr, 2)
		assert.Equal(t, "context-1m-2025-08-07", arr[0].String())
		assert.Equal(t, "compact-2026-01-12", arr[1].String())
	})
}

// TestParseAnthropicBetaHeader compares empty, comma-separated, spaced, and
// JSON-array header forms. It checks delegation to the shared parser returns
// the expected ordered token values.
func TestParseAnthropicBetaHeader(t *testing.T) {
	assert.Nil(t, ParseAnthropicBetaHeader(""))
	assert.Equal(t, []string{"a"}, ParseAnthropicBetaHeader("a"))
	assert.Equal(t, []string{"a", "b"}, ParseAnthropicBetaHeader("a,b"))
	assert.Equal(t, []string{"a", "b"}, ParseAnthropicBetaHeader("a , b "))
	assert.Equal(t, []string{"a", "b", "c"}, ParseAnthropicBetaHeader("a,b,c"))
	assert.Equal(t, []string{"a", "b"}, ParseAnthropicBetaHeader(`["a","b"]`))
}

// TestFilterBedrockBetaTokens combines accepted, rejected, duplicate, and
// translated values. It checks encounter order, companion tool-examples
// injection, and nil output when no allowed beta remains.
func TestFilterBedrockBetaTokens(t *testing.T) {
	t.Run("supported tokens pass through", func(t *testing.T) {
		tokens := []string{"context-1m-2025-08-07", "compact-2026-01-12", "computer-use-2025-11-24"}
		result := FilterBedrockBetaTokens(tokens)
		assert.Equal(t, tokens, result)
	})

	t.Run("unsupported tokens are filtered out", func(t *testing.T) {
		tokens := []string{"context-1m-2025-08-07", "output-128k-2025-02-19", "files-api-2025-04-14", "structured-outputs-2025-11-13"}
		result := FilterBedrockBetaTokens(tokens)
		assert.Equal(t, []string{"context-1m-2025-08-07"}, result)
	})

	t.Run("advanced-tool-use transforms to tool-search-tool", func(t *testing.T) {
		tokens := []string{"advanced-tool-use-2025-11-20"}
		result := FilterBedrockBetaTokens(tokens)
		assert.Contains(t, result, "tool-search-tool-2025-10-19")
		// Tool search also enables tool examples.
		assert.Contains(t, result, "tool-examples-2025-10-29")
	})

	t.Run("tool-search-tool auto-associates tool-examples", func(t *testing.T) {
		tokens := []string{"tool-search-tool-2025-10-19"}
		result := FilterBedrockBetaTokens(tokens)
		assert.Contains(t, result, "tool-search-tool-2025-10-19")
		assert.Contains(t, result, "tool-examples-2025-10-29")
	})

	t.Run("no duplication when tool-examples already present", func(t *testing.T) {
		tokens := []string{"tool-search-tool-2025-10-19", "tool-examples-2025-10-29"}
		result := FilterBedrockBetaTokens(tokens)
		count := 0
		for _, t := range result {
			if t == "tool-examples-2025-10-29" {
				count++
			}
		}
		assert.Equal(t, 1, count)
	})

	t.Run("empty input returns nil", func(t *testing.T) {
		result := FilterBedrockBetaTokens(nil)
		assert.Nil(t, result)
	})

	t.Run("all unsupported returns nil", func(t *testing.T) {
		result := FilterBedrockBetaTokens([]string{"output-128k-2025-02-19", "effort-2025-11-24"})
		assert.Nil(t, result)
	})

	t.Run("duplicate tokens are deduplicated", func(t *testing.T) {
		tokens := []string{"context-1m-2025-08-07", "context-1m-2025-08-07"}
		result := FilterBedrockBetaTokens(tokens)
		assert.Equal(t, []string{"context-1m-2025-08-07"}, result)
	})
}

// TestPrepareBedrockRequestBody_BetaFiltering exercises allowlisting and
// advanced-tool translation through full body preparation. It checks
// unsupported tokens disappear and the final tool-search/examples pair is
// encoded.
func TestPrepareBedrockRequestBody_BetaFiltering(t *testing.T) {
	input := `{"messages":[{"role":"user","content":"hi"}],"max_tokens":100}`

	t.Run("unsupported beta tokens are filtered", func(t *testing.T) {
		result, err := PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1",
			"compact-2026-01-12, output-128k-2025-02-19, files-api-2025-04-14")
		require.NoError(t, err)
		arr := gjson.GetBytes(result, "anthropic_beta").Array()
		require.Len(t, arr, 1)
		assert.Equal(t, "compact-2026-01-12", arr[0].String())
	})

	t.Run("advanced-tool-use transformed in full pipeline", func(t *testing.T) {
		result, err := PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1",
			"advanced-tool-use-2025-11-20")
		require.NoError(t, err)
		arr := gjson.GetBytes(result, "anthropic_beta").Array()
		require.Len(t, arr, 2)
		assert.Equal(t, "tool-search-tool-2025-10-19", arr[0].String())
		assert.Equal(t, "tool-examples-2025-10-29", arr[1].String())
	})
}

// TestPrepareBedrockRequestBodyWithTokens_ContextManagementRequiresSupportedBeta
// supplies context edits with different final beta sets. It checks
// dependent-field stripping, retention when enabled, and unchanged caller
// token inputs.
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

		result, err := PrepareBedrockRequestBodyWithTokens([]byte(input), modelID, betaTokens, false)
		require.NoError(t, err)

		assert.False(t, gjson.GetBytes(result, "context_management").Exists())
		assert.Equal(t, originalTokens, betaTokens)
		assert.Equal(t, originalTokens, bedrockAnthropicBetaNames(result))
	})

	t.Run("leaves body without context_management otherwise intact", func(t *testing.T) {
		input := `{"messages":[{"role":"user","content":"hi"}],"max_tokens":100}`

		result, err := PrepareBedrockRequestBodyWithTokens([]byte(input), modelID, nil, false)
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

		result, err := PrepareBedrockRequestBodyWithTokens(
			[]byte(input),
			modelID,
			[]string{BedrockContextManagementBetaToken, "context-1m-2025-08-07"},
			false,
		)
		require.NoError(t, err)

		assert.True(t, gjson.GetBytes(result, "context_management").Exists())
		assert.Equal(t, []string{BedrockContextManagementBetaToken, "context-1m-2025-08-07"}, bedrockAnthropicBetaNames(result))
	})
}

// bedrockAnthropicBetaNames returns converted string values from the prepared
// body beta array for assertions.
func bedrockAnthropicBetaNames(body []byte) []string {
	arr := gjson.GetBytes(body, "anthropic_beta").Array()
	names := make([]string, len(arr))
	for i, token := range arr {
		names[i] = token.String()
	}
	return names
}

// TestAutoInjectBedrockBetaTokens combines computer, programmatic, example,
// search, and ordinary tool inputs. It checks body-feature selection, existing
// token retention, and absence of automatic beta injection for thinking alone.
func TestAutoInjectBedrockBetaTokens(t *testing.T) {
	t.Run("no auto-inject for thinking (interleaved-thinking not supported)", func(t *testing.T) {
		body := []byte(`{"thinking":{"type":"enabled","budget_tokens":10000},"messages":[{"role":"user","content":"hi"}]}`)
		result := AutoInjectBedrockBetaTokens(nil, body, "us.anthropic.claude-opus-4-6-v1")
		// Thinking alone must not inject the unlisted interleaved-thinking beta.
		assert.Empty(t, result)
	})

	t.Run("no duplicate when already present", func(t *testing.T) {
		body := []byte(`{"thinking":{"type":"enabled","budget_tokens":10000},"messages":[{"role":"user","content":"hi"}]}`)
		result := AutoInjectBedrockBetaTokens([]string{"context-1m-2025-08-07"}, body, "us.anthropic.claude-opus-4-6-v1")
		assert.Equal(t, []string{"context-1m-2025-08-07"}, result)
	})

	t.Run("inject computer-use when computer tool present", func(t *testing.T) {
		body := []byte(`{"tools":[{"type":"computer_20250124","name":"computer","display_width_px":1024}],"messages":[{"role":"user","content":"hi"}]}`)
		result := AutoInjectBedrockBetaTokens(nil, body, "us.anthropic.claude-opus-4-6-v1")
		assert.Contains(t, result, "computer-use-2025-11-24")
	})

	t.Run("inject advanced-tool-use for programmatic tool calling", func(t *testing.T) {
		body := []byte(`{"tools":[{"name":"bash","allowed_callers":["code_execution_20250825"]}],"messages":[{"role":"user","content":"hi"}]}`)
		result := AutoInjectBedrockBetaTokens(nil, body, "us.anthropic.claude-opus-4-6-v1")
		assert.Contains(t, result, "advanced-tool-use-2025-11-20")
	})

	t.Run("inject advanced-tool-use for input examples", func(t *testing.T) {
		body := []byte(`{"tools":[{"name":"bash","input_examples":[{"cmd":"ls"}]}],"messages":[{"role":"user","content":"hi"}]}`)
		result := AutoInjectBedrockBetaTokens(nil, body, "us.anthropic.claude-opus-4-6-v1")
		assert.Contains(t, result, "advanced-tool-use-2025-11-20")
	})

	t.Run("inject tool-search-tool directly for pure tool search (no programmatic/inputExamples)", func(t *testing.T) {
		body := []byte(`{"tools":[{"type":"tool_search_tool_regex_20251119","name":"search"}],"messages":[{"role":"user","content":"hi"}]}`)
		result := AutoInjectBedrockBetaTokens(nil, body, "us.anthropic.claude-sonnet-4-6")
		assert.Contains(t, result, "tool-search-tool-2025-10-19")
		assert.NotContains(t, result, "advanced-tool-use-2025-11-20")
	})

	t.Run("inject advanced-tool-use when tool search combined with programmatic calling", func(t *testing.T) {
		body := []byte(`{"tools":[{"type":"tool_search_tool_regex_20251119","name":"search"},{"name":"bash","allowed_callers":["code_execution_20250825"]}],"messages":[{"role":"user","content":"hi"}]}`)
		result := AutoInjectBedrockBetaTokens(nil, body, "us.anthropic.claude-sonnet-4-6")
		// The filter later translates advanced-tool-use to the Bedrock tool-search token.
		assert.Contains(t, result, "advanced-tool-use-2025-11-20")
	})

	t.Run("do not inject tool-search beta for unsupported models", func(t *testing.T) {
		body := []byte(`{"tools":[{"type":"tool_search_tool_regex_20251119","name":"search"}],"messages":[{"role":"user","content":"hi"}]}`)
		result := AutoInjectBedrockBetaTokens(nil, body, "anthropic.claude-3-5-sonnet-20241022-v2:0")
		assert.NotContains(t, result, "advanced-tool-use-2025-11-20")
		assert.NotContains(t, result, "tool-search-tool-2025-10-19")
	})

	t.Run("no injection for regular tools", func(t *testing.T) {
		body := []byte(`{"tools":[{"name":"bash","description":"run bash","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"hi"}]}`)
		result := AutoInjectBedrockBetaTokens(nil, body, "us.anthropic.claude-opus-4-6-v1")
		assert.Empty(t, result)
	})

	t.Run("no injection when no features detected", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":"hi"}],"max_tokens":100}`)
		result := AutoInjectBedrockBetaTokens(nil, body, "us.anthropic.claude-opus-4-6-v1")
		assert.Empty(t, result)
	})

	t.Run("preserves existing tokens", func(t *testing.T) {
		body := []byte(`{"thinking":{"type":"enabled"},"messages":[{"role":"user","content":"hi"}]}`)
		existing := []string{"context-1m-2025-08-07", "compact-2026-01-12"}
		result := AutoInjectBedrockBetaTokens(existing, body, "us.anthropic.claude-opus-4-6-v1")
		assert.Contains(t, result, "context-1m-2025-08-07")
		assert.Contains(t, result, "compact-2026-01-12")
		assert.NotContains(t, result, "interleaved-thinking-2025-05-14")
	})
}

// TestResolveBedrockBetaTokens supplies body-only tool features and
// unsupported client tokens. It checks the combined list is translated and
// filtered before becoming the final Invoke beta selection.
func TestResolveBedrockBetaTokens(t *testing.T) {
	t.Run("body-only tool features resolve to final bedrock tokens", func(t *testing.T) {
		body := []byte(`{"tools":[{"name":"bash","allowed_callers":["code_execution_20250825"]}],"messages":[{"role":"user","content":"hi"}]}`)
		result := ResolveBedrockBetaTokens("", body, "us.anthropic.claude-opus-4-6-v1")
		assert.Contains(t, result, "tool-search-tool-2025-10-19")
		assert.Contains(t, result, "tool-examples-2025-10-29")
	})

	t.Run("unsupported client beta tokens are filtered out", func(t *testing.T) {
		body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
		result := ResolveBedrockBetaTokens("context-1m-2025-08-07,files-api-2025-04-14", body, "us.anthropic.claude-opus-4-6-v1")
		assert.Equal(t, []string{"context-1m-2025-08-07"}, result)
	})
}

// TestPrepareBedrockRequestBody_AutoBetaInjection supplies thinking with
// absent or accepted header betas. It checks thinking alone does not add an
// unlisted beta and existing accepted header tokens survive.
func TestPrepareBedrockRequestBody_AutoBetaInjection(t *testing.T) {
	t.Run("thinking in body does not auto-inject beta (not supported)", func(t *testing.T) {
		input := `{"messages":[{"role":"user","content":"hi"}],"max_tokens":100,"thinking":{"type":"enabled","budget_tokens":10000}}`
		result, err := PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1", "")
		require.NoError(t, err)
		// No supported beta is inferred from thinking alone.
		assert.False(t, gjson.GetBytes(result, "anthropic_beta").Exists())
	})

	t.Run("header tokens preserved without auto-injection", func(t *testing.T) {
		input := `{"messages":[{"role":"user","content":"hi"}],"max_tokens":100,"thinking":{"type":"enabled","budget_tokens":10000}}`
		result, err := PrepareBedrockRequestBody([]byte(input), "us.anthropic.claude-opus-4-6-v1", "context-1m-2025-08-07")
		require.NoError(t, err)
		arr := gjson.GetBytes(result, "anthropic_beta").Array()
		names := make([]string, len(arr))
		for i, v := range arr {
			names[i] = v.String()
		}
		assert.Contains(t, names, "context-1m-2025-08-07")
		assert.NotContains(t, names, "interleaved-thinking-2025-05-14")
	})
}

// TestIsBedrockOpus47OrNewer compares adaptive Opus entries with budget,
// other-family, unknown-version, and other-vendor IDs. It checks both
// catalogue mode and model-family conditions determine the result.
func TestIsBedrockOpus47OrNewer(t *testing.T) {
	tests := []struct {
		modelID string // catalogue lookup model identifier
		expect  bool   // expected capability predicate result
	}{
		{"us.anthropic.claude-opus-4-8", true},
		{"us.anthropic.claude-opus-4-7", true},
		{"us.anthropic.claude-opus-4-6-v1", false},
		{"us.anthropic.claude-opus-4-5-20251101-v1:0", false},
		{"us.anthropic.claude-opus-5-0-v1", false},
		{"us.anthropic.claude-sonnet-4-7-v1", false},
		{"us.anthropic.claude-sonnet-4-6", false},
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", false},
		{"amazon.nova-pro-v1", false},
	}
	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			assert.Equal(t, tt.expect, IsBedrockOpus47OrNewer(tt.modelID))
		})
	}
}

// TestSanitizeBedrockThinking supplies Fable/Opus/Sonnet thinking shapes with
// absent or present budgets. It checks adaptive conversion, Fable budget
// removal, budget defaults, and retention of supported existing values.
func TestSanitizeBedrockThinking(t *testing.T) {
	t.Run("Fable 5 将 enabled 转换为 adaptive 并移除预算", func(t *testing.T) {
		input := `{"thinking":{"type":"enabled","budget_tokens":10000},"messages":[]}`
		result := SanitizeBedrockThinking([]byte(input), "anthropic.claude-fable-5")
		assert.Equal(t, "adaptive", gjson.GetBytes(result, "thinking.type").String())
		assert.False(t, gjson.GetBytes(result, "thinking.budget_tokens").Exists())
	})

	t.Run("Fable 5 adaptive 移除预算", func(t *testing.T) {
		input := `{"thinking":{"type":"adaptive","budget_tokens":10000},"messages":[]}`
		result := SanitizeBedrockThinking([]byte(input), "claude-fable-5")
		assert.Equal(t, "adaptive", gjson.GetBytes(result, "thinking.type").String())
		assert.False(t, gjson.GetBytes(result, "thinking.budget_tokens").Exists())
	})

	t.Run("opus 4.7 converts enabled to adaptive", func(t *testing.T) {
		input := `{"thinking":{"type":"enabled","budget_tokens":10000},"messages":[]}`
		result := SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-7")
		assert.Equal(t, "adaptive", gjson.GetBytes(result, "thinking.type").String())
		assert.False(t, gjson.GetBytes(result, "thinking.budget_tokens").Exists())
	})

	t.Run("opus 4.7 keeps adaptive unchanged", func(t *testing.T) {
		input := `{"thinking":{"type":"adaptive"},"messages":[]}`
		result := SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-7")
		assert.Equal(t, "adaptive", gjson.GetBytes(result, "thinking.type").String())
	})

	t.Run("opus 4.6 enabled without budget_tokens gets default", func(t *testing.T) {
		input := `{"thinking":{"type":"enabled"},"messages":[]}`
		result := SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-6-v1")
		assert.Equal(t, "enabled", gjson.GetBytes(result, "thinking.type").String())
		assert.Equal(t, int64(DefaultThinkingBudgetTokens), gjson.GetBytes(result, "thinking.budget_tokens").Int())
	})

	t.Run("opus 4.6 enabled with budget_tokens unchanged", func(t *testing.T) {
		input := `{"thinking":{"type":"enabled","budget_tokens":20000},"messages":[]}`
		result := SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-6-v1")
		assert.Equal(t, "enabled", gjson.GetBytes(result, "thinking.type").String())
		assert.Equal(t, int64(20000), gjson.GetBytes(result, "thinking.budget_tokens").Int())
	})

	t.Run("no thinking field unchanged", func(t *testing.T) {
		input := `{"messages":[]}`
		result := SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-7")
		assert.JSONEq(t, input, string(result))
	})

	t.Run("sonnet 4.6 enabled without budget_tokens gets default", func(t *testing.T) {
		input := `{"thinking":{"type":"enabled"},"messages":[]}`
		result := SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-sonnet-4-6")
		assert.Equal(t, "enabled", gjson.GetBytes(result, "thinking.type").String())
		assert.Equal(t, int64(DefaultThinkingBudgetTokens), gjson.GetBytes(result, "thinking.budget_tokens").Int())
	})
}

// TestSanitizeBedrockToolUseIDs supplies clean and disallowed characters in
// call/result identities. It checks the shared alphabet replacement and skips
// empty IDs, missing messages, and string content.
func TestSanitizeBedrockToolUseIDs(t *testing.T) {
	t.Run("clean IDs unchanged", func(t *testing.T) {
		input := `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_01AbCdEf","name":"bash","input":{}}]}]}`
		result := SanitizeBedrockToolUseIDs([]byte(input))
		assert.Equal(t, "toolu_01AbCdEf", gjson.GetBytes(result, "messages.0.content.0.id").String())
	})

	t.Run("dots in tool_use ID replaced with underscores", func(t *testing.T) {
		input := `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu.01.Ab","name":"bash","input":{}}]}]}`
		result := SanitizeBedrockToolUseIDs([]byte(input))
		assert.Equal(t, "toolu_01_Ab", gjson.GetBytes(result, "messages.0.content.0.id").String())
	})

	t.Run("special chars in tool_use ID sanitized", func(t *testing.T) {
		input := `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu:01@Ab#Cd","name":"bash","input":{}}]}]}`
		result := SanitizeBedrockToolUseIDs([]byte(input))
		id := gjson.GetBytes(result, "messages.0.content.0.id").String()
		assert.Regexp(t, `^[a-zA-Z0-9_-]+$`, id)
	})

	t.Run("tool_result tool_use_id sanitized", func(t *testing.T) {
		input := `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu.01.Ab","content":"ok"}]}]}`
		result := SanitizeBedrockToolUseIDs([]byte(input))
		assert.Equal(t, "toolu_01_Ab", gjson.GetBytes(result, "messages.0.content.0.tool_use_id").String())
	})

	t.Run("mixed clean and dirty IDs", func(t *testing.T) {
		input := `{"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"clean_id-123","name":"a","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"dirty.id@456","content":"ok"}]},
			{"role":"assistant","content":[{"type":"tool_use","id":"also.dirty","name":"b","input":{}}]}
		]}`
		result := SanitizeBedrockToolUseIDs([]byte(input))
		assert.Equal(t, "clean_id-123", gjson.GetBytes(result, "messages.0.content.0.id").String())
		assert.Equal(t, "dirty_id_456", gjson.GetBytes(result, "messages.1.content.0.tool_use_id").String())
		assert.Equal(t, "also_dirty", gjson.GetBytes(result, "messages.2.content.0.id").String())
	})

	t.Run("no messages unchanged", func(t *testing.T) {
		input := `{"system":[{"type":"text","text":"hi"}]}`
		result := SanitizeBedrockToolUseIDs([]byte(input))
		assert.JSONEq(t, input, string(result))
	})

	t.Run("string content skipped", func(t *testing.T) {
		input := `{"messages":[{"role":"user","content":"plain text"}]}`
		result := SanitizeBedrockToolUseIDs([]byte(input))
		assert.JSONEq(t, input, string(result))
	})

	t.Run("empty ID skipped", func(t *testing.T) {
		input := `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"","name":"a","input":{}}]}]}`
		result := SanitizeBedrockToolUseIDs([]byte(input))
		assert.Equal(t, "", gjson.GetBytes(result, "messages.0.content.0.id").String())
	})
}

// TestSanitizeBedrockThinking_EdgeCases supplies disabled, empty, nonobject,
// already adaptive, and standard-name inputs. It checks conditional adaptation
// retains explicit adaptive Opus budget and preserves unaffected thinking
// shapes.
func TestSanitizeBedrockThinking_EdgeCases(t *testing.T) {
	t.Run("opus 4.7 enabled without budget_tokens converts to adaptive", func(t *testing.T) {
		input := `{"thinking":{"type":"enabled"},"messages":[]}`
		result := SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-7")
		assert.Equal(t, "adaptive", gjson.GetBytes(result, "thinking.type").String())
		assert.False(t, gjson.GetBytes(result, "thinking.budget_tokens").Exists())
	})

	t.Run("thinking type disabled unchanged", func(t *testing.T) {
		input := `{"thinking":{"type":"disabled"},"messages":[]}`
		result := SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-7")
		assert.Equal(t, "disabled", gjson.GetBytes(result, "thinking.type").String())
	})

	t.Run("thinking type empty string unchanged", func(t *testing.T) {
		input := `{"thinking":{"type":""},"messages":[]}`
		result := SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-7")
		assert.JSONEq(t, input, string(result))
	})

	t.Run("thinking is not an object unchanged", func(t *testing.T) {
		input := `{"thinking":true,"messages":[]}`
		result := SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-7")
		assert.JSONEq(t, input, string(result))
	})

	t.Run("opus 4.7 adaptive with budget_tokens preserved", func(t *testing.T) {
		input := `{"thinking":{"type":"adaptive","budget_tokens":5000},"messages":[]}`
		result := SanitizeBedrockThinking([]byte(input), "us.anthropic.claude-opus-4-7")
		assert.Equal(t, "adaptive", gjson.GetBytes(result, "thinking.type").String())
		assert.Equal(t, int64(5000), gjson.GetBytes(result, "thinking.budget_tokens").Int())
	})

	// Accept standard Anthropic model names as well as Bedrock IDs.
	t.Run("standard model name opus 4.7 converts enabled to adaptive", func(t *testing.T) {
		input := `{"thinking":{"type":"enabled","budget_tokens":10000},"messages":[]}`
		result := SanitizeBedrockThinking([]byte(input), "claude-opus-4-7")
		assert.Equal(t, "adaptive", gjson.GetBytes(result, "thinking.type").String())
		assert.False(t, gjson.GetBytes(result, "thinking.budget_tokens").Exists())
	})

	t.Run("standard model name opus 4.6 keeps enabled", func(t *testing.T) {
		input := `{"thinking":{"type":"enabled","budget_tokens":10000},"messages":[]}`
		result := SanitizeBedrockThinking([]byte(input), "claude-opus-4-6")
		assert.Equal(t, "enabled", gjson.GetBytes(result, "thinking.type").String())
		assert.Equal(t, int64(10000), gjson.GetBytes(result, "thinking.budget_tokens").Int())
	})
}

// TestIsBedrockOpus47OrNewer_EdgeCases compares exact registered IDs and
// standard names with an unknown dated suffix. It checks empty and unsupported
// identities do not inherit another entry adaptive capability.
func TestIsBedrockOpus47OrNewer_EdgeCases(t *testing.T) {
	tests := []struct {
		modelID string // catalogue lookup model identifier
		expect  bool   // expected capability predicate result
	}{
		{"anthropic.claude-opus-4-8", true},
		{"anthropic.claude-opus-4-7", true},
		{"us.anthropic.claude-opus-4-7-20270101-v1:0", false},
		{"", false},
		// Standard model names are valid capability lookup inputs.
		{"claude-opus-4-8", true},
		{"claude-opus-4-7", true},
		{"claude-opus-4-6", false},
		{"claude-sonnet-4-7", false},
	}
	for _, tt := range tests {
		t.Run(tt.modelID, func(t *testing.T) {
			assert.Equal(t, tt.expect, IsBedrockOpus47OrNewer(tt.modelID))
		})
	}
}

// TestPrepareBedrockRequestBodyWithTokens_CCCompat supplies thinking and
// paired dirty tool IDs with compatibility disabled/enabled. It checks extra
// budget and identity normalization occurs only on the enabled path.
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
		result, err := PrepareBedrockRequestBodyWithTokens([]byte(input), "us.anthropic.claude-opus-4-6-v1", nil, false)
		require.NoError(t, err)
		assert.Equal(t, "enabled", gjson.GetBytes(result, "thinking.type").String())
		assert.False(t, gjson.GetBytes(result, "thinking.budget_tokens").Exists())
		assert.Equal(t, "toolu.01.Ab", gjson.GetBytes(result, "messages.0.content.0.id").String())
	})

	t.Run("ccCompat=true applies thinking fix and toolUseID sanitization (opus 4.6)", func(t *testing.T) {
		result, err := PrepareBedrockRequestBodyWithTokens([]byte(input), "us.anthropic.claude-opus-4-6-v1", nil, true)
		require.NoError(t, err)
		assert.Equal(t, "enabled", gjson.GetBytes(result, "thinking.type").String())
		assert.Equal(t, int64(DefaultThinkingBudgetTokens), gjson.GetBytes(result, "thinking.budget_tokens").Int())
		assert.Equal(t, "toolu_01_Ab", gjson.GetBytes(result, "messages.0.content.0.id").String())
		assert.Equal(t, "toolu_01_Ab", gjson.GetBytes(result, "messages.1.content.0.tool_use_id").String())
	})

	t.Run("ccCompat=true converts thinking to adaptive for opus 4.7", func(t *testing.T) {
		result, err := PrepareBedrockRequestBodyWithTokens([]byte(input), "us.anthropic.claude-opus-4-7", nil, true)
		require.NoError(t, err)
		assert.Equal(t, "adaptive", gjson.GetBytes(result, "thinking.type").String())
		assert.False(t, gjson.GetBytes(result, "thinking.budget_tokens").Exists())
		assert.Equal(t, "toolu_01_Ab", gjson.GetBytes(result, "messages.0.content.0.id").String())
	})
}

// TestSanitizeBedrockCCFields combines unsupported direct fields with absent
// or supplied defaults. It checks removals, default output/version insertion,
// and retention of explicit existing values.
func TestSanitizeBedrockCCFields(t *testing.T) {
	t.Run("removes service_tier and interface_geo", func(t *testing.T) {
		body := []byte(`{"model":"claude-opus-4-6","service_tier":"standard","interface_geo":"us","messages":[]}`)
		result := SanitizeBedrockCCFields(body)
		assert.False(t, gjson.GetBytes(result, "service_tier").Exists())
		assert.False(t, gjson.GetBytes(result, "interface_geo").Exists())
		assert.True(t, gjson.GetBytes(result, "messages").Exists())
	})

	t.Run("removes context_management", func(t *testing.T) {
		body := []byte(`{"model":"claude-opus-4-6","context_management":{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]},"messages":[]}`)
		result := SanitizeBedrockCCFields(body)
		assert.False(t, gjson.GetBytes(result, "context_management").Exists())
		assert.True(t, gjson.GetBytes(result, "messages").Exists())
	})

	t.Run("injects max_tokens when missing", func(t *testing.T) {
		body := []byte(`{"model":"claude-opus-4-6","messages":[]}`)
		result := SanitizeBedrockCCFields(body)
		assert.Equal(t, int64(DefaultCCMaxTokens), gjson.GetBytes(result, "max_tokens").Int())
	})

	t.Run("preserves existing max_tokens", func(t *testing.T) {
		body := []byte(`{"model":"claude-opus-4-6","max_tokens":4096,"messages":[]}`)
		result := SanitizeBedrockCCFields(body)
		assert.Equal(t, int64(4096), gjson.GetBytes(result, "max_tokens").Int())
	})

	t.Run("injects anthropic_version when missing", func(t *testing.T) {
		body := []byte(`{"model":"claude-opus-4-6","messages":[]}`)
		result := SanitizeBedrockCCFields(body)
		assert.Equal(t, "bedrock-2023-05-31", gjson.GetBytes(result, "anthropic_version").String())
	})

	t.Run("preserves existing anthropic_version", func(t *testing.T) {
		body := []byte(`{"model":"claude-opus-4-6","anthropic_version":"bedrock-2023-05-31","messages":[]}`)
		result := SanitizeBedrockCCFields(body)
		assert.Equal(t, "bedrock-2023-05-31", gjson.GetBytes(result, "anthropic_version").String())
	})

	t.Run("no-op when fields already clean", func(t *testing.T) {
		body := []byte(`{"model":"claude-opus-4-6","max_tokens":81920,"anthropic_version":"bedrock-2023-05-31","messages":[]}`)
		result := SanitizeBedrockCCFields(body)
		assert.Equal(t, int64(DefaultCCMaxTokens), gjson.GetBytes(result, "max_tokens").Int())
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
		result := SanitizeBedrockCCFields(body)
		assert.False(t, gjson.GetBytes(result, "service_tier").Exists())
		assert.False(t, gjson.GetBytes(result, "interface_geo").Exists())
		assert.False(t, gjson.GetBytes(result, "context_management").Exists())
		assert.Equal(t, int64(DefaultCCMaxTokens), gjson.GetBytes(result, "max_tokens").Int())
		assert.Equal(t, "bedrock-2023-05-31", gjson.GetBytes(result, "anthropic_version").String())
		assert.Equal(t, "enabled", gjson.GetBytes(result, "thinking.type").String())
	})
}

// TestSanitizeBedrockCCBetaTokens supplies existing beta fields in several
// feature configurations. It checks removal, translation, body-feature
// injection, and the absent-field path that performs no beta insertion.
func TestSanitizeBedrockCCBetaTokens(t *testing.T) {
	t.Run("filters unsupported beta tokens", func(t *testing.T) {
		input := `{"anthropic_beta":["prompt-caching-2024-07-31","context-1m-2025-08-07","unsupported-feature"],"messages":[]}`
		result := SanitizeBedrockCCBetaTokens([]byte(input), "claude-opus-4-6")
		beta := gjson.GetBytes(result, "anthropic_beta")
		assert.True(t, beta.Exists())
		assert.True(t, beta.IsArray())
		tokens := beta.Array()
		assert.Equal(t, 1, len(tokens))
		assert.Equal(t, "context-1m-2025-08-07", tokens[0].String())
	})

	t.Run("removes anthropic_beta if all tokens filtered", func(t *testing.T) {
		input := `{"anthropic_beta":["prompt-caching-2024-07-31","unsupported-feature"],"messages":[]}`
		result := SanitizeBedrockCCBetaTokens([]byte(input), "claude-opus-4-6")
		assert.False(t, gjson.GetBytes(result, "anthropic_beta").Exists())
	})

	t.Run("thinking alone does not auto-inject beta tokens", func(t *testing.T) {
		input := `{"anthropic_beta":[],"thinking":{"type":"enabled"},"messages":[]}`
		result := SanitizeBedrockCCBetaTokens([]byte(input), "claude-opus-4-6")
		assert.False(t, gjson.GetBytes(result, "anthropic_beta").Exists())
	})

	t.Run("auto-injects computer-use beta token", func(t *testing.T) {
		input := `{"anthropic_beta":[],"tools":[{"type":"computer_20250124","name":"computer"}],"messages":[]}`
		result := SanitizeBedrockCCBetaTokens([]byte(input), "claude-opus-4-6")
		beta := gjson.GetBytes(result, "anthropic_beta")
		assert.True(t, beta.Exists())
		tokens := beta.Array()
		assert.Equal(t, 1, len(tokens))
		assert.Equal(t, "computer-use-2025-11-24", tokens[0].String())
	})

	t.Run("transforms advanced-tool-use to tool-search-tool", func(t *testing.T) {
		input := `{"anthropic_beta":["advanced-tool-use-2025-11-20"],"messages":[]}`
		result := SanitizeBedrockCCBetaTokens([]byte(input), "claude-opus-4-6")
		beta := gjson.GetBytes(result, "anthropic_beta")
		tokens := beta.Array()
		assert.Equal(t, 2, len(tokens)) // tool-search-tool and its associated tool-examples token
		assert.Contains(t, []string{tokens[0].String(), tokens[1].String()}, "tool-search-tool-2025-10-19")
		assert.Contains(t, []string{tokens[0].String(), tokens[1].String()}, "tool-examples-2025-10-29")
	})

	t.Run("no-op when anthropic_beta not present", func(t *testing.T) {
		input := `{"messages":[]}`
		result := SanitizeBedrockCCBetaTokens([]byte(input), "claude-opus-4-6")
		assert.False(t, gjson.GetBytes(result, "anthropic_beta").Exists())
	})

	t.Run("preserves supported beta tokens", func(t *testing.T) {
		input := `{"anthropic_beta":["computer-use-2025-11-24","context-1m-2025-08-07"],"messages":[]}`
		result := SanitizeBedrockCCBetaTokens([]byte(input), "claude-opus-4-6")
		beta := gjson.GetBytes(result, "anthropic_beta")
		tokens := beta.Array()
		assert.Equal(t, 2, len(tokens))
		assert.Contains(t, []string{tokens[0].String(), tokens[1].String()}, "computer-use-2025-11-24")
		assert.Contains(t, []string{tokens[0].String(), tokens[1].String()}, "context-1m-2025-08-07")
	})
}

// TestPrepareBedrockRequestBodyWithTokens_FallbacksRequireSupportedBeta
// supplies fallback fields and beta inputs that the local allowlist rejects.
// It checks dependent fields are stripped while messages and output limits
// remain.
func TestPrepareBedrockRequestBodyWithTokens_FallbacksRequireSupportedBeta(t *testing.T) {
	modelID := "us.anthropic.claude-opus-4-6-v1"

	t.Run("strips fallbacks when final tokens omit server-side-fallback beta", func(t *testing.T) {
		input := `{
			"messages":[{"role":"user","content":"hi"}],
			"max_tokens":100,
			"fallbacks":"default",
			"fallback_credit_token":"tok_123"
		}`
		betaTokens := []string{"context-1m-2025-08-07"}

		result, err := PrepareBedrockRequestBodyWithTokens([]byte(input), modelID, betaTokens, false)
		require.NoError(t, err)

		assert.False(t, gjson.GetBytes(result, "fallbacks").Exists())
		assert.False(t, gjson.GetBytes(result, "fallback_credit_token").Exists())
		assert.Equal(t, "hi", gjson.GetBytes(result, "messages.0.content").String())
		assert.Equal(t, int64(100), gjson.GetBytes(result, "max_tokens").Int())
	})

	t.Run("strips fallbacks even when client passes server-side-fallback token (not whitelisted)", func(t *testing.T) {
		input := `{"messages":[{"role":"user","content":"hi"}],"max_tokens":100,"fallbacks":"default"}`

		result, err := PrepareBedrockRequestBodyWithTokens(
			[]byte(input), modelID, []string{claude.BetaServerSideFallback}, false,
		)
		require.NoError(t, err)

		assert.False(t, gjson.GetBytes(result, "fallbacks").Exists(),
			"Bedrock 白名单不含 fallback beta → 条件 strip 总会剥除（预期）")
		for _, name := range bedrockAnthropicBetaNames(result) {
			assert.NotEqual(t, claude.BetaServerSideFallback, name,
				"fallback beta 不得进入 Bedrock anthropic_beta 白名单输出")
		}
	})

	t.Run("leaves body without fallback fields otherwise intact", func(t *testing.T) {
		input := `{"messages":[{"role":"user","content":"hi"}],"max_tokens":100}`

		result, err := PrepareBedrockRequestBodyWithTokens([]byte(input), modelID, nil, false)
		require.NoError(t, err)

		assert.False(t, gjson.GetBytes(result, "fallbacks").Exists())
		assert.False(t, gjson.GetBytes(result, "fallback_credit_token").Exists())
		assert.False(t, gjson.GetBytes(result, "context_management").Exists())
		assert.Equal(t, "hi", gjson.GetBytes(result, "messages.0.content").String())
	})
}

// TestSanitizeBedrockCCFields_StripsFallbacksUnconditionally supplies context
// and fallback objects in a compatibility body. It checks every listed field
// is removed while the messages collection remains.
func TestSanitizeBedrockCCFields_StripsFallbacksUnconditionally(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-6","context_management":{"edits":[]},"fallbacks":"default","fallback_credit_token":"tok_123","messages":[]}`)
	result := SanitizeBedrockCCFields(body)

	assert.False(t, gjson.GetBytes(result, "fallbacks").Exists())
	assert.False(t, gjson.GetBytes(result, "fallback_credit_token").Exists())
	assert.False(t, gjson.GetBytes(result, "context_management").Exists())
	assert.True(t, gjson.GetBytes(result, "messages").Exists())
}

// TestBuildBedrockURL constructs streamed and buffered regional model actions
// with colon-bearing and suffix-free IDs. It checks exact action paths and
// encoded model-version colon placement.
func TestBuildBedrockURL(t *testing.T) {
	t.Run("stream URL with colon in model ID", func(t *testing.T) {
		url := BuildBedrockURL("us-east-1", "us.anthropic.claude-opus-4-5-20251101-v1:0", true)
		assert.Equal(t, "https://bedrock-runtime.us-east-1.amazonaws.com/model/us.anthropic.claude-opus-4-5-20251101-v1%3A0/invoke-with-response-stream", url)
	})

	t.Run("non-stream URL with colon in model ID", func(t *testing.T) {
		url := BuildBedrockURL("eu-west-1", "eu.anthropic.claude-sonnet-4-5-20250929-v1:0", false)
		assert.Equal(t, "https://bedrock-runtime.eu-west-1.amazonaws.com/model/eu.anthropic.claude-sonnet-4-5-20250929-v1%3A0/invoke", url)
	})

	t.Run("model ID without colon", func(t *testing.T) {
		url := BuildBedrockURL("us-east-1", "us.anthropic.claude-sonnet-4-6", true)
		assert.Equal(t, "https://bedrock-runtime.us-east-1.amazonaws.com/model/us.anthropic.claude-sonnet-4-6/invoke-with-response-stream", url)
	})
}
