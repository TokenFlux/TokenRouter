//go:build unit

package requeststate

import (
	"fmt"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestParseGatewayRequest(t *testing.T) {
	body := []byte(`{"model":"claude-3-7-sonnet","stream":true,"metadata":{"user_id":"session_123e4567-e89b-12d3-a456-426614174000"},"system":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}],"messages":[{"content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "")
	require.NoError(t, err)
	require.Equal(t, "claude-3-7-sonnet", parsed.Model)
	require.True(t, parsed.Stream)
	require.Equal(t, "session_123e4567-e89b-12d3-a456-426614174000", parsed.MetadataUserID)
	require.True(t, parsed.HasSystem)
	require.NotEmpty(t, parsed.SystemRaw())
	require.NotEmpty(t, parsed.MessagesRaw())
	require.False(t, parsed.ThinkingEnabled)
}

func TestParseGatewayRequest_ThinkingEnabled(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-5","thinking":{"type":"enabled"},"messages":[{"content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "")
	require.NoError(t, err)
	require.Equal(t, "claude-sonnet-4-5", parsed.Model)
	require.True(t, parsed.ThinkingEnabled)
}

func TestParseGatewayRequest_ThinkingAdaptiveEnabled(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-5","thinking":{"type":"adaptive"},"messages":[{"content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "")
	require.NoError(t, err)
	require.Equal(t, "claude-sonnet-4-5", parsed.Model)
	require.True(t, parsed.ThinkingEnabled)
}

func TestParseGatewayRequest_MaxTokens(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-4-5","max_tokens":1}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "")
	require.NoError(t, err)
	require.Equal(t, 1, parsed.MaxTokens)
}

func TestParseGatewayRequest_MaxTokensNonIntegralIgnored(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-4-5","max_tokens":1.5}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "")
	require.NoError(t, err)
	require.Equal(t, 0, parsed.MaxTokens)
}

func TestParseGatewayRequest_SystemNull(t *testing.T) {
	body := []byte(`{"model":"claude-3","system":null}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "")
	require.NoError(t, err)
	// 显式传入 system:null 也应视为“字段已存在”，避免默认 system 被注入。
	require.True(t, parsed.HasSystem)
	require.Equal(t, []byte("null"), parsed.SystemRaw())
}

func TestParseGatewayRequest_InvalidModelType(t *testing.T) {
	body := []byte(`{"model":123}`)
	_, err := ParseGatewayRequest(NewRequestBodyRef(body), "")
	require.Error(t, err)
}

func TestParseGatewayRequest_InvalidStreamType(t *testing.T) {
	body := []byte(`{"stream":"true"}`)
	_, err := ParseGatewayRequest(NewRequestBodyRef(body), "")
	require.Error(t, err)
}

func TestParseGatewayRequest_AnthropicNormalizesClaudeCodeLongContextModelSuffix(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		{name: "lowercase suffix", model: "claude-opus-4-8[1m]", want: "claude-opus-4-8"},
		{name: "uppercase suffix", model: "claude-opus-4-8[1M]", want: "claude-opus-4-8"},
		{name: "duplicated suffix", model: "claude-opus-4-8[1M][1m]", want: "claude-opus-4-8"},
		{name: "suffix in middle", model: "claude-opus-4-8[1m]-preview", want: "claude-opus-4-8[1m]-preview"},
		{name: "suffix only", model: "[1m]", want: "[1m]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"model":%q,"system":"test","messages":[{"role":"user","content":"hi"}]}`, tt.model))
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), capability.PlatformAnthropic)
			require.NoError(t, err)
			require.Equal(t, tt.want, parsed.Model)
			require.Equal(t, tt.want, gjson.GetBytes(parsed.Body.Bytes(), "model").String())
			require.Equal(t, `"test"`, string(parsed.SystemRaw()))
			require.NotEmpty(t, parsed.MessagesRaw())
		})
	}
}

func TestParseGatewayRequest_NonAnthropicPreservesClaudeCodeLongContextModelSuffix(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-8[1m]","input":"hi"}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "responses")
	require.NoError(t, err)
	require.Equal(t, "claude-opus-4-8[1m]", parsed.Model)
	require.Equal(t, "claude-opus-4-8[1m]", gjson.GetBytes(parsed.Body.Bytes(), "model").String())
}

func TestParseGatewayRequest_ResponsesInput(t *testing.T) {
	body := []byte(`{"model":"gpt-5.1","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "responses")
	require.NoError(t, err)
	require.NotEmpty(t, parsed.InputRaw())
	require.Nil(t, parsed.MessagesRaw())
	require.Equal(t, "hello", gjson.ParseBytes(parsed.InputRaw()).Get("0.content.0.text").String())
}

func TestParseGatewayRequest_GeminiContents(t *testing.T) {
	body := []byte(`{
		"contents": [
			{"role": "user", "parts": [{"text": "Hello"}]},
			{"role": "model", "parts": [{"text": "Hi there"}]},
			{"role": "user", "parts": [{"text": "How are you?"}]}
		]
	}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), capability.PlatformGemini)
	require.NoError(t, err)
	require.Len(t, gjson.ParseBytes(parsed.MessagesRaw()).Array(), 3, "should parse contents as Messages")
	require.False(t, parsed.HasSystem, "Gemini format should not set HasSystem")
	require.Nil(t, parsed.SystemRaw(), "no systemInstruction means nil System")
}

func TestParseGatewayRequest_GeminiSystemInstruction(t *testing.T) {
	body := []byte(`{
		"systemInstruction": {
			"parts": [{"text": "You are a helpful assistant."}]
		},
		"contents": [
			{"role": "user", "parts": [{"text": "Hello"}]}
		]
	}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), capability.PlatformGemini)
	require.NoError(t, err)
	system := gjson.ParseBytes(parsed.SystemRaw())
	require.True(t, system.IsArray(), "should parse systemInstruction.parts as System")
	require.Len(t, system.Array(), 1)
	require.Equal(t, "You are a helpful assistant.", system.Get("0.text").String())
	require.Len(t, gjson.ParseBytes(parsed.MessagesRaw()).Array(), 1)
}

func TestParseGatewayRequest_GeminiWithModel(t *testing.T) {
	body := []byte(`{
		"model": "gemini-2.5-pro",
		"contents": [{"role": "user", "parts": [{"text": "test"}]}]
	}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), capability.PlatformGemini)
	require.NoError(t, err)
	require.Equal(t, "gemini-2.5-pro", parsed.Model)
	require.Len(t, gjson.ParseBytes(parsed.MessagesRaw()).Array(), 1)
}

func TestParseGatewayRequest_GeminiIgnoresAnthropicFields(t *testing.T) {
	// Gemini 格式下 system/messages 字段应被忽略
	body := []byte(`{
		"system": "should be ignored",
		"messages": [{"role": "user", "content": "ignored"}],
		"contents": [{"role": "user", "parts": [{"text": "real content"}]}]
	}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), capability.PlatformGemini)
	require.NoError(t, err)
	require.False(t, parsed.HasSystem, "Gemini protocol should not parse Anthropic system field")
	require.Nil(t, parsed.SystemRaw(), "no systemInstruction = nil System")
	require.Len(t, gjson.ParseBytes(parsed.MessagesRaw()).Array(), 1, "should use contents, not messages")
}

func TestParseGatewayRequest_GeminiEmptyContents(t *testing.T) {
	body := []byte(`{"contents": []}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), capability.PlatformGemini)
	require.NoError(t, err)
	require.Empty(t, gjson.ParseBytes(parsed.MessagesRaw()).Array())
}

func TestParseGatewayRequest_GeminiNoContents(t *testing.T) {
	body := []byte(`{"model": "gemini-2.5-flash"}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), capability.PlatformGemini)
	require.NoError(t, err)
	require.Nil(t, parsed.MessagesRaw())
	require.Equal(t, "gemini-2.5-flash", parsed.Model)
}

func TestParseGatewayRequest_AnthropicIgnoresGeminiFields(t *testing.T) {
	// Anthropic 格式下 contents/systemInstruction 字段应被忽略
	body := []byte(`{
		"system": "real system",
		"messages": [{"role": "user", "content": "real content"}],
		"contents": [{"role": "user", "parts": [{"text": "ignored"}]}],
		"systemInstruction": {"parts": [{"text": "ignored"}]}
	}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), capability.PlatformAnthropic)
	require.NoError(t, err)
	require.True(t, parsed.HasSystem)
	require.Equal(t, "real system", gjson.ParseBytes(parsed.SystemRaw()).String())
	messages := gjson.ParseBytes(parsed.MessagesRaw()).Array()
	require.Len(t, messages, 1)
	require.Equal(t, "real content", messages[0].Get("content").String())
}

// Task 7.1 — 类型校验边界测试
func TestParseGatewayRequest_TypeValidation(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantErr   bool
		errSubstr string // 期望的错误信息子串（为空则不检查）
	}{
		{
			name:      "model 为 int",
			body:      `{"model":123}`,
			wantErr:   true,
			errSubstr: "invalid model field type",
		},
		{
			name:      "model 为 array",
			body:      `{"model":[]}`,
			wantErr:   true,
			errSubstr: "invalid model field type",
		},
		{
			name:      "model 为 bool",
			body:      `{"model":true}`,
			wantErr:   true,
			errSubstr: "invalid model field type",
		},
		{
			name:      "model 为 null — gjson Null 类型触发类型校验错误",
			body:      `{"model":null}`,
			wantErr:   true, // gjson: Exists()=true, Type=Null != String → 返回错误
			errSubstr: "invalid model field type",
		},
		{
			name:      "stream 为 string",
			body:      `{"stream":"true"}`,
			wantErr:   true,
			errSubstr: "invalid stream field type",
		},
		{
			name:      "stream 为 int",
			body:      `{"stream":1}`,
			wantErr:   true,
			errSubstr: "invalid stream field type",
		},
		{
			name:      "stream 为 null — gjson Null 类型触发类型校验错误",
			body:      `{"stream":null}`,
			wantErr:   true, // gjson: Exists()=true, Type=Null != True && != False → 返回错误
			errSubstr: "invalid stream field type",
		},
		{
			name:      "model 为 object",
			body:      `{"model":{}}`,
			wantErr:   true,
			errSubstr: "invalid model field type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseGatewayRequest(NewRequestBodyRef([]byte(tt.body)), "")
			if tt.wantErr {
				require.Error(t, err)
				if tt.errSubstr != "" {
					require.Contains(t, err.Error(), tt.errSubstr)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// Task 7.2 — 可选字段缺失测试
func TestParseGatewayRequest_OptionalFieldsMissing(t *testing.T) {
	tests := []struct {
		name            string
		body            string
		wantModel       string
		wantStream      bool
		wantMetadataUID string
		wantHasSystem   bool
		wantThinking    bool
		wantMaxTokens   int
		wantMessagesNil bool
		wantMessagesLen int
	}{
		{
			name:            "完全空 JSON — 所有字段零值",
			body:            `{}`,
			wantModel:       "",
			wantStream:      false,
			wantMetadataUID: "",
			wantHasSystem:   false,
			wantThinking:    false,
			wantMaxTokens:   0,
			wantMessagesNil: true,
		},
		{
			name:            "metadata 无 user_id",
			body:            `{"model":"test"}`,
			wantModel:       "test",
			wantMetadataUID: "",
			wantHasSystem:   false,
			wantThinking:    false,
		},
		{
			name:         "thinking 非 enabled（type=disabled）",
			body:         `{"model":"test","thinking":{"type":"disabled"}}`,
			wantModel:    "test",
			wantThinking: false,
		},
		{
			name:         "thinking 字段缺失",
			body:         `{"model":"test"}`,
			wantModel:    "test",
			wantThinking: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(tt.body)), "")
			require.NoError(t, err)

			require.Equal(t, tt.wantModel, parsed.Model)
			require.Equal(t, tt.wantStream, parsed.Stream)
			require.Equal(t, tt.wantMetadataUID, parsed.MetadataUserID)
			require.Equal(t, tt.wantHasSystem, parsed.HasSystem)
			require.Equal(t, tt.wantThinking, parsed.ThinkingEnabled)
			require.Equal(t, tt.wantMaxTokens, parsed.MaxTokens)

			if tt.wantMessagesNil {
				require.Nil(t, parsed.MessagesRaw())
			}
			if tt.wantMessagesLen > 0 {
				require.Len(t, gjson.ParseBytes(parsed.MessagesRaw()).Array(), tt.wantMessagesLen)
			}
		})
	}
}

// Task 7.4 — max_tokens 边界测试
func TestParseGatewayRequest_MaxTokensBoundary(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantMaxTokens int
		wantErr       bool
	}{
		{
			name:          "正常整数",
			body:          `{"max_tokens":1024}`,
			wantMaxTokens: 1024,
		},
		{
			name:          "浮点数（非整数）被忽略",
			body:          `{"max_tokens":10.5}`,
			wantMaxTokens: 0,
		},
		{
			name:          "负整数可以通过",
			body:          `{"max_tokens":-1}`,
			wantMaxTokens: -1,
		},
		{
			name:          "超大值不 panic",
			body:          `{"max_tokens":9999999999999999}`,
			wantMaxTokens: 10000000000000000, // float64 精度导致 9999999999999999 → 1e16
		},
		{
			name:          "null 值被忽略",
			body:          `{"max_tokens":null}`,
			wantMaxTokens: 0, // gjson Type=Null != Number → 条件不满足，跳过
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(tt.body)), "")
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantMaxTokens, parsed.MaxTokens)
		})
	}
}

func TestParseGatewayRequest_OutputEffort(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantEffort string
	}{
		{
			name:       "output_config.effort present",
			body:       `{"model":"claude-opus-4-6","output_config":{"effort":"medium"},"messages":[]}`,
			wantEffort: "medium",
		},
		{
			name:       "output_config.effort max",
			body:       `{"model":"claude-opus-4-6","output_config":{"effort":"max"},"messages":[]}`,
			wantEffort: "max",
		},
		{
			name:       "output_config.effort xhigh",
			body:       `{"model":"claude-opus-4-7","output_config":{"effort":"xhigh"},"messages":[]}`,
			wantEffort: "xhigh",
		},
		{
			name:       "output_config without effort",
			body:       `{"model":"claude-opus-4-6","output_config":{},"messages":[]}`,
			wantEffort: "",
		},
		{
			name:       "no output_config",
			body:       `{"model":"claude-opus-4-6","messages":[]}`,
			wantEffort: "",
		},
		{
			name:       "effort with whitespace trimmed",
			body:       `{"model":"claude-opus-4-6","output_config":{"effort":" high "},"messages":[]}`,
			wantEffort: "high",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(tt.body)), "")
			require.NoError(t, err)
			require.Equal(t, tt.wantEffort, parsed.OutputEffort)
		})
	}
}
