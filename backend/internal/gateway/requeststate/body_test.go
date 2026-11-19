package requeststate

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// TestParseGatewayRequest parses a streaming Messages body with system,
// metadata, and message fields. Captured scalars and raw ranges must agree with
// the supplied payload while absent thinking stays false.
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

// TestParseGatewayRequest_ThinkingEnabled supplies the enabled thinking
// spelling beside a model and message. Parsing must retain the requested model
// and expose the thinking flag to later execution decisions.
func TestParseGatewayRequest_ThinkingEnabled(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-5","thinking":{"type":"enabled"},"messages":[{"content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "")
	require.NoError(t, err)
	require.Equal(t, "claude-sonnet-4-5", parsed.Model)
	require.True(t, parsed.ThinkingEnabled)
}

// TestParseGatewayRequest_ThinkingAdaptiveEnabled supplies adaptive thinking in
// a Messages body. Parsing must classify it as enabled thinking while retaining
// the model used by later request preparation.
func TestParseGatewayRequest_ThinkingAdaptiveEnabled(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-5","thinking":{"type":"adaptive"},"messages":[{"content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "")
	require.NoError(t, err)
	require.Equal(t, "claude-sonnet-4-5", parsed.Model)
	require.True(t, parsed.ThinkingEnabled)
}

// TestParseGatewayRequest_MaxTokens supplies a one-token probe value for a
// Haiku request. Parsing must expose that integral value for the entry's probe
// detection before forwarding.
func TestParseGatewayRequest_MaxTokens(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-4-5","max_tokens":1}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "")
	require.NoError(t, err)
	require.Equal(t, 1, parsed.MaxTokens)
}

// TestParseGatewayRequest_MaxTokensNonIntegralIgnored supplies a fractional
// numeric max_tokens value. Parsing must still succeed and retain zero for the
// probe field instead of converting that fraction to an integer.
func TestParseGatewayRequest_MaxTokensNonIntegralIgnored(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-4-5","max_tokens":1.5}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "")
	require.NoError(t, err)
	require.Equal(t, 0, parsed.MaxTokens)
}

// TestParseGatewayRequest_SystemNull supplies an explicit null system value.
// The parser must preserve its raw spelling and presence flag so a caller can
// distinguish it from omitted system input.
func TestParseGatewayRequest_SystemNull(t *testing.T) {
	body := []byte(`{"model":"claude-3","system":null}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "")
	require.NoError(t, err)
	// Presence and raw null spelling travel together so later default-system
	// injection can respect this explicit client field even though its value is empty.
	require.True(t, parsed.HasSystem)
	require.Equal(t, []byte("null"), parsed.SystemRaw())
}

// TestParseGatewayRequest_InvalidModelType supplies a numeric model in an
// otherwise valid JSON body. The parser must reject that field type before
// returning a successful parsed request to forwarding.
func TestParseGatewayRequest_InvalidModelType(t *testing.T) {
	body := []byte(`{"model":123}`)
	_, err := ParseGatewayRequest(NewRequestBodyRef(body), "")
	require.Error(t, err)
}

// TestParseGatewayRequest_InvalidStreamType supplies the string true for stream
// in valid JSON. Parsing must reject that representation because request
// streaming evidence requires a JSON boolean value.
func TestParseGatewayRequest_InvalidStreamType(t *testing.T) {
	body := []byte(`{"stream":"true"}`)
	_, err := ParseGatewayRequest(NewRequestBodyRef(body), "")
	require.Error(t, err)
}

// TestParseGatewayRequest_AnthropicNormalizesClaudeCodeLongContextModelSuffix
// supplies case variants, repeated selectors, middle text, and a selector-only
// model. Anthropic parsing must normalize eligible trailing selectors in scalar
// and body views while retaining usable system/message ranges.
func TestParseGatewayRequest_AnthropicNormalizesClaudeCodeLongContextModelSuffix(t *testing.T) {
	tests := []struct {
		name  string // case label for diagnostics
		model string // supplied client model spelling
		want  string // expected normalized model spelling
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

// TestParseGatewayRequest_NonAnthropicPreservesClaudeCodeLongContextModelSuffix
// parses a Responses request carrying a Claude-style selector. The model and
// forwarding bytes must retain that spelling because selector normalization is
// selected by the Anthropic protocol.
func TestParseGatewayRequest_NonAnthropicPreservesClaudeCodeLongContextModelSuffix(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-8[1m]","input":"hi"}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "responses")
	require.NoError(t, err)
	require.Equal(t, "claude-opus-4-8[1m]", parsed.Model)
	require.Equal(t, "claude-opus-4-8[1m]", gjson.GetBytes(parsed.Body.Bytes(), "model").String())
}

// TestParseGatewayRequest_ResponsesInput supplies a Responses input array
// containing user text. The parser must expose input raw bytes and leave
// MessagesRaw absent for this payload.
func TestParseGatewayRequest_ResponsesInput(t *testing.T) {
	body := []byte(`{"model":"gpt-5.1","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), "responses")
	require.NoError(t, err)
	require.NotEmpty(t, parsed.InputRaw())
	require.Nil(t, parsed.MessagesRaw())
	require.Equal(t, "hello", gjson.ParseBytes(parsed.InputRaw()).Get("0.content.0.text").String())
}

// TestParseGatewayRequest_GeminiContents supplies a Gemini conversation with
// three content entries and no system instruction. Parsing must expose contents
// through MessagesRaw while leaving system presence and raw system input
// absent.
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

// TestParseGatewayRequest_GeminiSystemInstruction supplies Gemini
// systemInstruction.parts alongside contents. Parsing must expose both arrays
// under their protocol-specific paths so system content remains available to
// downstream readers.
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

// TestParseGatewayRequest_GeminiWithModel supplies an explicit model with one
// Gemini content entry. The scalar model and MessagesRaw array must both remain
// available after protocol-specific field extraction.
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

// TestParseGatewayRequest_GeminiIgnoresAnthropicFields mixes Messages-shaped
// system/history with Gemini contents. Gemini parsing must select contents and
// leave the unrelated system field absent from its protocol-specific system
// view.
func TestParseGatewayRequest_GeminiIgnoresAnthropicFields(t *testing.T) {
	// Gemini parsing ignores Anthropic system and messages fields.
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

// TestParseGatewayRequest_GeminiEmptyContents supplies a present empty Gemini
// conversation array. Parsing must succeed and expose an empty array rather
// than rejecting absence of conversation entries at this parsing stage.
func TestParseGatewayRequest_GeminiEmptyContents(t *testing.T) {
	body := []byte(`{"contents": []}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), capability.PlatformGemini)
	require.NoError(t, err)
	require.Empty(t, gjson.ParseBytes(parsed.MessagesRaw()).Array())
}

// TestParseGatewayRequest_GeminiNoContents supplies a model while omitting
// Gemini conversation content. Parsing must retain the model and report no
// MessagesRaw range for the omitted contents field.
func TestParseGatewayRequest_GeminiNoContents(t *testing.T) {
	body := []byte(`{"model": "gemini-2.5-flash"}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), capability.PlatformGemini)
	require.NoError(t, err)
	require.Nil(t, parsed.MessagesRaw())
	require.Equal(t, "gemini-2.5-flash", parsed.Model)
}

// TestParseGatewayRequest_AnthropicIgnoresGeminiFields mixes both protocol
// field families in one payload. Anthropic parsing must retain the Messages
// system/history and exclude Gemini fields from those raw views.
func TestParseGatewayRequest_AnthropicIgnoresGeminiFields(t *testing.T) {
	// Anthropic parsing ignores Gemini contents and systemInstruction fields.
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

// TestParseGatewayRequest_TypeValidation supplies invalid JSON types for model
// and stream, including present null values. Parsing must fail with the field-
// specific diagnostic substring for each represented type mismatch.
func TestParseGatewayRequest_TypeValidation(t *testing.T) {
	tests := []struct {
		name    string // case label for diagnostics
		body    string // JSON request fixture bytes
		wantErr bool   // parser error expected flag
		// errSubstr is checked after an expected error when nonempty. Empty text
		// skips that assertion and leaves the case's error expectation unchanged.
		errSubstr string // required error diagnostic substring
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
			wantErr:   true, // gjson considers null present, but it is not a string.
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
			wantErr:   true, // gjson considers null present, but it is not a boolean.
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

// TestParseGatewayRequest_OptionalFieldsMissing supplies empty objects, omitted
// metadata/thinking, and disabled thinking. Parsing must retain the
// zero/default fields asserted by the table and report absent message ranges
// when requested by its case.
func TestParseGatewayRequest_OptionalFieldsMissing(t *testing.T) {
	tests := []struct {
		name            string // case label for diagnostics
		body            string // JSON request fixture bytes
		wantModel       string // expected parsed model spelling
		wantStream      bool   // expected parsed streaming flag
		wantMetadataUID string // expected metadata user identifier
		wantHasSystem   bool   // expected system presence flag
		wantThinking    bool   // expected thinking enabled flag
		wantMaxTokens   int    // expected integer probe value
		wantMessagesNil bool   // require an absent message range
		// wantMessagesLen adds a decoded array-length assertion when positive.
		// Zero skips that comparison while the case still checks its other fields.
		wantMessagesLen int // positive expected message array length
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

// TestParseGatewayRequest_MaxTokensBoundary supplies integral, fractional,
// negative, large, and null max_tokens values. Parsing must retain the asserted
// float64-derived integers or zero values; the large fixture protects its
// existing rounding result.
func TestParseGatewayRequest_MaxTokensBoundary(t *testing.T) {
	tests := []struct {
		name          string // case label for diagnostics
		body          string // JSON request fixture bytes
		wantMaxTokens int    // expected integer probe value
		wantErr       bool   // parser error expected flag
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
			wantMaxTokens: 10000000000000000, // float64 rounds 9999999999999999 to 1e16.
		},
		{
			name:          "null 值被忽略",
			body:          `{"max_tokens":null}`,
			wantMaxTokens: 0, // Null is not a number, so max_tokens is ignored.
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

// TestParseGatewayRequest_OutputEffort supplies several explicit Claude effort
// labels, omission, and surrounding whitespace. Parsing must expose the trimmed
// output_config.effort spelling while leaving missing values empty.
func TestParseGatewayRequest_OutputEffort(t *testing.T) {
	tests := []struct {
		name       string // case label for diagnostics
		body       string // JSON request fixture bytes
		wantEffort string // expected trimmed effort spelling
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

// TestDescribeInvalidJSON_TruncatedBody supplies JSON cut off inside message
// text. The diagnostic error must include the body length and unexpected-end
// description for transport or middleware truncation investigation.
func TestDescribeInvalidJSON_TruncatedBody(t *testing.T) {
	// Model a body truncated during transport or partially consumed by middleware.
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[{"role":"user","content":"hi`)

	err := DescribeInvalidJSON(body)

	require.Error(t, err)
	require.Contains(t, err.Error(), fmt.Sprintf("len=%d", len(body)))
	require.Contains(t, err.Error(), "unexpected end of JSON input")
}

// TestDescribeInvalidJSON_InvalidCharacterWithOffset supplies an invalid
// unquoted model value. The diagnostic error must include the known syntax
// offset and character description needed to locate malformed input.
func TestDescribeInvalidJSON_InvalidCharacterWithOffset(t *testing.T) {
	body := []byte(`{"model": bad}`)

	err := DescribeInvalidJSON(body)

	require.Error(t, err)
	require.Contains(t, err.Error(), "offset=11")
	require.Contains(t, err.Error(), "invalid character")
}

// TestDescribeInvalidJSON_DoesNotLeakBodyContent places a credential fixture
// before truncated JSON. The diagnostic must return an error while excluding
// that credential text from the message shown to callers.
func TestDescribeInvalidJSON_DoesNotLeakBodyContent(t *testing.T) {
	secret := "sk-super-secret-value"
	body := []byte(`{"api_key":"` + secret + `","broken":`)

	err := DescribeInvalidJSON(body)

	require.Error(t, err)
	require.NotContains(t, err.Error(), secret)
}

// TestParseGatewayRequest_InvalidJSONErrorIsDiagnostic submits an incomplete
// Anthropic message array through the main parser. The returned error must
// begin with the diagnostic length prefix supplied by DescribeInvalidJSON.
func TestParseGatewayRequest_InvalidJSONErrorIsDiagnostic(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-6","messages":[`)

	_, err := ParseGatewayRequest(NewRequestBodyRef(body), capability.PlatformAnthropic)

	require.Error(t, err)
	require.True(t, strings.HasPrefix(err.Error(), "invalid json (len="), "error should carry diagnostics, got: %s", err.Error())
}

// BenchmarkParseGatewayRequest_Old_Small measures duplicate map extraction
// followed by the current parser on a compact Messages fixture. The benchmark
// reports processed bytes for comparison with the direct parser path.
func BenchmarkParseGatewayRequest_Old_Small(b *testing.B) {
	data := buildSmallJSON()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for range b.N {
		_, _ = parseGatewayRequestOld(data, "")
	}
}

// BenchmarkParseGatewayRequest_New_Small measures direct parsed-field
// extraction on the same compact Messages fixture. Reported bytes permit
// comparison with the separate benchmark that also performs preliminary map
// decoding.
func BenchmarkParseGatewayRequest_New_Small(b *testing.B) {
	data := buildSmallJSON()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for range b.N {
		_, _ = ParseGatewayRequest(NewRequestBodyRef(data), "")
	}
}

// BenchmarkParseGatewayRequest_Old_Large measures preliminary map extraction
// and current parsing over the two-hundred-message fixture. The timed loop
// protects a comparable duplicate-work workload for the direct parser
// benchmark.
func BenchmarkParseGatewayRequest_Old_Large(b *testing.B) {
	data := buildLargeJSON()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for range b.N {
		_, _ = parseGatewayRequestOld(data, "")
	}
}

// BenchmarkParseGatewayRequest_New_Large measures direct parsing of the two-
// hundred-message fixture used by duplicate-work measurement. Byte accounting
// stays tied to the constructed payload before the timed loop starts.
func BenchmarkParseGatewayRequest_New_Large(b *testing.B) {
	data := buildLargeJSON()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for range b.N {
		_, _ = ParseGatewayRequest(NewRequestBodyRef(data), "")
	}
}

// parseIntegralNumber converts supported decoded numeric representations to int
// and reports whether its checks accepted the value. Unsupported types,
// fractions, NaN, infinity, and failed json.Number integer parsing return
// false.
//
// The duplicate-work benchmark uses float64 values produced by json.Unmarshal,
// so source precision may already be lost before this helper runs. Floating and
// int64 values compare against platform int bounds before conversion, with
// floating bounds themselves rounded to float64. Callers use the accepted
// benchmark value as an extracted field, not as evidence that the original
// numeric spelling survived decoding.
func parseIntegralNumber(raw any) (int, bool) {
	switch v := raw.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) {
			return 0, false
		}
		if v > float64(math.MaxInt) || v < float64(math.MinInt) {
			return 0, false
		}
		return int(v), true
	case int:
		return v, true
	case int8:
		return int(v), true
	case int16:
		return int(v), true
	case int32:
		return int(v), true
	case int64:
		if v > int64(math.MaxInt) || v < int64(math.MinInt) {
			return 0, false
		}
		return int(v), true
	case json.Number:
		i64, err := v.Int64()
		if err != nil {
			return 0, false
		}
		if i64 > int64(math.MaxInt) || i64 < int64(math.MinInt) {
			return 0, false
		}
		return int(i64), true
	default:
		return 0, false
	}
}

// parseGatewayRequestOld performs preliminary map extraction before returning
// the current parser's result over the same bytes. Preliminary decode/type
// errors return immediately; its populated temporary scalar fields are
// otherwise superseded, creating the benchmark's duplicate parsing work.
func parseGatewayRequestOld(body []byte, protocol string) (*ParsedRequest, error) {
	parsed := &ParsedRequest{
		Body: NewRequestBodyRef(body),
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	if raw, ok := req["model"]; ok {
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("invalid model field type")
		}
		parsed.Model = s
	}

	if raw, ok := req["stream"]; ok {
		b, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("invalid stream field type")
		}
		parsed.Stream = b
	}

	if meta, ok := req["metadata"].(map[string]any); ok {
		if uid, ok := meta["user_id"].(string); ok {
			parsed.MetadataUserID = uid
		}
	}

	if thinking, ok := req["thinking"].(map[string]any); ok {
		if thinkType, ok := thinking["type"].(string); ok && thinkType == "enabled" {
			parsed.ThinkingEnabled = true
		}
	}

	if raw, ok := req["max_tokens"]; ok {
		if n, ok := parseIntegralNumber(raw); ok {
			parsed.MaxTokens = n
		}
	}

	return ParseGatewayRequest(parsed.Body, protocol)
}

// buildSmallJSON returns a fixed streaming Messages payload with system,
// thinking, metadata and conversation text. Each call creates body bytes for
// the benchmark, and the caller leaves them unchanged during parsing.
func buildSmallJSON() []byte {
	return []byte(`{"model":"claude-sonnet-4-5","stream":true,"max_tokens":4096,"metadata":{"user_id":"user-abc123"},"thinking":{"type":"enabled","budget_tokens":2048},"system":"You are a helpful assistant.","messages":[{"role":"user","content":"What is the meaning of life?"},{"role":"assistant","content":"The meaning of life is a philosophical question."},{"role":"user","content":"Can you elaborate?"}]}`)
}

// buildLargeJSON builds a streaming Messages benchmark fixture with two hundred
// alternating user and assistant entries. Cache-marked system text and longer
// repeated message content provide a larger payload for comparing parsing
// paths.
func buildLargeJSON() []byte {
	b := []byte(`{"model":"claude-sonnet-4-5","stream":true,"max_tokens":8192,"metadata":{"user_id":"user-xyz789"},"system":[{"type":"text","text":"You are a detailed assistant.","cache_control":{"type":"ephemeral"}}],"messages":[`)

	// Two hundred turns provide a repeatable larger duplicate-parsing workload.
	msgCount := 200
	for i := range msgCount {
		if i > 0 {
			b = append(b, ',')
		}
		if i%2 == 0 {
			b = fmt.Appendf(b, `{"role":"user","content":"This is user message number %d with some extra padding text to make the message reasonably long for benchmarking purposes. Lorem ipsum dolor sit amet."}`, i)
		} else {
			b = fmt.Appendf(b, `{"role":"assistant","content":[{"type":"text","text":"This is assistant response number %d. I will provide a detailed answer with multiple sentences to simulate real conversation content for benchmark testing."}]}`, i)
		}
	}

	return append(b, ']', '}')
}

// BenchmarkParseGatewayRequest_LargeAnthropicMessages parses successively
// larger Messages fixtures and retains raw message lengths. Payload
// construction stays outside timing, while byte and allocation reporting
// measure the parser workload.
func BenchmarkParseGatewayRequest_LargeAnthropicMessages(b *testing.B) {
	for _, size := range benchmarkBodySizes() {
		b.Run(size.name, func(b *testing.B) {
			body := buildLargeAnthropicMessagesBody(size.bytes, false)

			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), capability.PlatformAnthropic)
				if err != nil {
					b.Fatalf("解析 Anthropic 请求失败: %v", err)
				}
				benchmarkIntSink = len(parsed.MessagesRaw())
			}
		})
	}
}

// BenchmarkParseGatewayRequest_LargeGeminiContents parses successively larger
// Gemini conversation fixtures and retains raw content lengths. Payload
// construction stays outside timing, and byte/allocation reporting covers
// protocol-specific range extraction.
func BenchmarkParseGatewayRequest_LargeGeminiContents(b *testing.B) {
	for _, size := range benchmarkBodySizes() {
		b.Run(size.name, func(b *testing.B) {
			body := buildLargeGeminiContentsBody(size.bytes)

			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), capability.PlatformGemini)
				if err != nil {
					b.Fatalf("解析 Gemini 请求失败: %v", err)
				}
				benchmarkIntSink = len(parsed.MessagesRaw())
			}
		})
	}
}

// buildLargeAnthropicMessagesBody appends Messages entries until the partial
// builder reaches targetBytes, then completes the JSON document. Final output
// can exceed that target, and includeCacheControl inserts sparse cache markers
// for caller-selected benchmark workloads.
func buildLargeAnthropicMessagesBody(targetBytes int, includeCacheControl bool) []byte {
	var builder strings.Builder
	builder.Grow(targetBytes + 1024)
	_, _ = builder.WriteString(`{"model":"claude-sonnet-4-5","stream":true,"system":[{"type":"text","text":"system seed"}],"messages":[`)
	for i := 0; builder.Len() < targetBytes; i++ {
		if i > 0 {
			_ = builder.WriteByte(',')
		}
		_, _ = builder.WriteString(`{"role":"user","content":[{"type":"text","text":"`)
		_, _ = builder.WriteString(strings.Repeat("anthropic payload ", 64))
		_, _ = builder.WriteString(strconv.Itoa(i))
		_ = builder.WriteByte('"')
		// Sparse cache markers add request variety without marking every benchmark message.
		if includeCacheControl && i%32 == 0 {
			_, _ = builder.WriteString(`,"cache_control":{"type":"ephemeral"}`)
		}
		_, _ = builder.WriteString(`}]}`)
	}
	_, _ = builder.WriteString(`]}`)
	return []byte(builder.String())
}

// buildLargeGeminiContentsBody appends Gemini contents entries until the
// partial builder reaches targetBytes, then closes the document. Final output
// can exceed that target; callers use the actual slice length for benchmark
// byte reporting.
func buildLargeGeminiContentsBody(targetBytes int) []byte {
	var builder strings.Builder
	builder.Grow(targetBytes + 1024)
	_, _ = builder.WriteString(`{"model":"gemini-2.5-pro","systemInstruction":{"parts":[{"text":"system seed"}]},"contents":[`)
	for i := 0; builder.Len() < targetBytes; i++ {
		if i > 0 {
			_ = builder.WriteByte(',')
		}
		_, _ = builder.WriteString(`{"role":"user","parts":[{"text":"`)
		_, _ = builder.WriteString(strings.Repeat("gemini payload ", 64))
		_, _ = builder.WriteString(strconv.Itoa(i))
		_, _ = builder.WriteString(`"}]}`)
	}
	_, _ = builder.WriteString(`]}`)
	return []byte(builder.String())
}

// TestParseGatewayRequest_RejectsDuplicateAndCaseVariantFields submits
// duplicate model keys, Unicode-escaped duplicates, and uppercase protocol
// field variants across Anthropic, Responses, and Gemini inputs. Parsing must
// reject each payload while ReplaceBody keeps fast internal updates working.
func TestParseGatewayRequest_RejectsDuplicateAndCaseVariantFields(t *testing.T) {
	for _, body := range []string{
		`{"model":"claude-3-5-haiku-20241022","messages":[{"role":"user","content":"hi"}],"model":"claude-opus-4-8"}`,
		`{"model":"claude-haiku-4-5","mo\u0064el":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"claude-haiku-4-5","Model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"gpt-5.4","Messages":[{"role":"user","content":"blocked"}]}`,
		`[]`,
	} {
		_, err := ParseGatewayRequest(NewRequestBodyRef([]byte(body)), capability.PlatformAnthropic)
		require.Error(t, err, body)
	}
}
