//go:build unit

package httpapi

import (
	"testing"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/stretchr/testify/require"
)

func TestGrokChatResponsesBridgeEligibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		body   string
		want   bool
		reason string
	}{
		{
			name: "plain text chat",
			body: `{"model":"grok","messages":[{"role":"system","content":"concise"},{"role":"user","content":"hi"}],"stream":false}`,
			want: true,
		},
		{
			name: "safe generation options",
			body: `{"model":"grok","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true},"max_completion_tokens":256,"temperature":0.2,"top_p":0.9,"prompt_cache_key":"session","tools":[],"functions":null,"tool_choice":"none"}`,
			want: true,
		},
		{
			name: "Trae compatible fields",
			body: `{"model":"grok","messages":[{"role":"user","content":"return json"}],"instructions":"Be concise","response_format":{"type":"json_object"},"service_tier":"fast","stop":null,"reasoning_effort":null}`,
			want: true,
		},
		{
			name: "Trae nullable SDK fields",
			body: `{"model":"grok","messages":[{"role":"user","content":"hi"}],"instructions":null,"response_format":null,"service_tier":null,"stop":null,"reasoning_effort":null}`,
			want: true,
		},
		{
			name:   "invalid instructions fall back",
			body:   `{"model":"grok","messages":[{"role":"user","content":"hi"}],"instructions":{"text":"invalid"}}`,
			reason: "invalid_instructions",
		},
		{
			name:   "invalid response format falls back",
			body:   `{"model":"grok","messages":[{"role":"user","content":"hi"}],"response_format":"json_object"}`,
			reason: "invalid_response_format",
		},
		{
			name:   "invalid service tier falls back",
			body:   `{"model":"grok","messages":[{"role":"user","content":"hi"}],"service_tier":1}`,
			reason: "invalid_service_tier",
		},
		{
			name:   "stop falls back",
			body:   `{"model":"grok","messages":[{"role":"user","content":"hi"}],"stop":"done"}`,
			reason: "unsupported_stop",
		},
		{
			name:   "developer role falls back",
			body:   `{"model":"grok","messages":[{"role":"developer","content":"rules"},{"role":"user","content":"hi"}]}`,
			reason: "unsupported_message_role_developer",
		},
		{
			name: "image content is bridgeable",
			body: `{"model":"grok","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,QQ=="}}]}]}`,
			want: true,
		},
		{
			name: "text and image parts are bridgeable",
			body: `{"model":"grok","messages":[{"role":"user","content":[{"type":"text","text":"what is this"},{"type":"image_url","image_url":{"url":"data:image/png;base64,QQ=="}}]}]}`,
			want: true,
		},
		{
			name: "text only parts are bridgeable",
			body: `{"model":"grok","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`,
			want: true,
		},
		{
			name:   "unknown content part falls back",
			body:   `{"model":"grok","messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"AA=="}}]}]}`,
			reason: "unsupported_content_part_input_audio",
		},
		{
			name:   "empty content array falls back",
			body:   `{"model":"grok","messages":[{"role":"user","content":[]}]}`,
			reason: "empty_message_content",
		},
		{
			name: "function tools bridge",
			body: `{"model":"grok","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"},"strict":false}}]}`,
			want: true,
		},
		{
			name:   "legacy functions fall back",
			body:   `{"model":"grok","messages":[{"role":"user","content":"hi"}],"functions":[{"name":"lookup","parameters":{"type":"object"}}]}`,
			reason: "unsupported_functions",
		},
		{
			name: "automatic tool choice bridges",
			body: `{"model":"grok","messages":[{"role":"user","content":"hi"}],"tools":[],"tool_choice":"auto"}`,
			want: true,
		},
		{
			name:   "required tool choice without tools falls back",
			body:   `{"model":"grok","messages":[{"role":"user","content":"hi"}],"tools":[],"tool_choice":"required"}`,
			reason: "required_tool_choice_without_tools",
		},
		{
			name: "tool history bridges",
			body: `{"model":"grok","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_lookup","type":"function","function":{"name":"lookup","arguments":"{\"key\":\"alpha\"}"}}]},{"role":"tool","tool_call_id":"call_lookup","content":"{\"value\":\"ok\"}"},{"role":"user","content":"summarize"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"tool_choice":"auto","parallel_tool_calls":true}`,
			want: true,
		},
		{
			name: "Trae reasoning and indexed tool history bridges",
			body: `{"model":"grok","messages":[{"role":"assistant","content":null,"reasoning_content":"I should call lookup","tool_calls":[{"index":0,"id":"call_lookup","type":"function","function":{"name":"lookup","arguments":"{\"key\":\"alpha\"}"}}]},{"role":"tool","tool_call_id":"call_lookup","content":"{\"value\":\"ok\"}"},{"role":"user","content":"summarize"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`,
			want: true,
		},
		{
			name: "reasoning only assistant history bridges",
			body: `{"model":"grok","messages":[{"role":"assistant","reasoning_content":"Prior reasoning"},{"role":"user","content":"continue"}]}`,
			want: true,
		},
		{
			name:   "invalid reasoning content falls back",
			body:   `{"model":"grok","messages":[{"role":"assistant","reasoning_content":{"text":"invalid"}},{"role":"user","content":"continue"}]}`,
			reason: "invalid_reasoning_content",
		},
		{
			name:   "negative tool call index falls back",
			body:   `{"model":"grok","messages":[{"role":"assistant","content":null,"tool_calls":[{"index":-1,"id":"call_lookup","type":"function","function":{"name":"lookup","arguments":"{}"}}]}]}`,
			reason: "invalid_tool_call_index",
		},
		{
			name:   "unknown tool type falls back",
			body:   `{"model":"grok","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"web_search","function":{"name":"lookup","parameters":{"type":"object"}}}]}`,
			reason: "unsupported_tool_type",
		},
		{
			name:   "missing tool schema falls back",
			body:   `{"model":"grok","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup"}}]}`,
			reason: "invalid_tool_function_parameters",
		},
		{
			name:   "named tool choice falls back",
			body:   `{"model":"grok","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"tool_choice":{"type":"function","function":{"name":"lookup"}}}`,
			reason: "unsupported_tool_choice",
		},
		{
			name:   "invalid tool call arguments fall back",
			body:   `{"model":"grok","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_lookup","type":"function","function":{"name":"lookup","arguments":"{"}}]}]}`,
			reason: "invalid_tool_call_arguments",
		},
		{
			name:   "tool result without call id falls back",
			body:   `{"model":"grok","messages":[{"role":"tool","content":"ok"}]}`,
			reason: "invalid_tool_call_id",
		},
		{
			name:   "non boolean parallel tool calls falls back",
			body:   `{"model":"grok","messages":[{"role":"user","content":"hi"}],"parallel_tool_calls":"true"}`,
			reason: "invalid_parallel_tool_calls",
		},
		{
			name:   "reasoning effort falls back because conversion adds summary",
			body:   `{"model":"grok","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"high"}`,
			reason: "unsupported_reasoning_effort",
		},
		{
			name:   "both token limits fall back",
			body:   `{"model":"grok","messages":[{"role":"user","content":"hi"}],"max_tokens":256,"max_completion_tokens":256}`,
			reason: "conflicting_max_tokens",
		},
		{
			name:   "empty message falls back",
			body:   `{"model":"grok","messages":[{"role":"assistant","content":""},{"role":"user","content":"hi"}]}`,
			reason: "empty_message_content",
		},
		{
			name:   "empty tool history falls back",
			body:   `{"model":"grok","messages":[{"role":"assistant","content":"","tool_calls":[]}]}`,
			reason: "empty_message_content",
		},
		{
			name:   "unknown field falls back",
			body:   `{"model":"grok","messages":[{"role":"user","content":"hi"}],"seed":7}`,
			reason: "unknown_field_seed",
		},
		{
			name:   "small max tokens falls back because conversion clamps it",
			body:   `{"model":"grok","messages":[{"role":"user","content":"hi"}],"max_tokens":32}`,
			reason: "unsafe_max_tokens",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := gatewayprovider.GrokBodyCodec().GrokChatResponsesBridgeEligibility([]byte(tt.body))
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.reason, reason)
		})
	}
}
func TestGrokChatResponsesRuntimeEligibility(t *testing.T) {
	t.Parallel()
	require.True(t, gatewayprovider.GrokBodyCodec().GrokChatResponsesRuntimeEligible("grok-4.5", "isolated-id"))
	require.True(t, gatewayprovider.GrokBodyCodec().GrokChatResponsesRuntimeEligible("grok-4.6", "isolated-id"))
	require.True(t, gatewayprovider.GrokBodyCodec().GrokChatResponsesRuntimeEligible("grok-4.6-latest", "isolated-id"))
	require.False(t, gatewayprovider.GrokBodyCodec().GrokChatResponsesRuntimeEligible("grok-4.3", "isolated-id"))
	require.False(t, gatewayprovider.GrokBodyCodec().GrokChatResponsesRuntimeEligible("grok-4.5-build-free", "isolated-id"))
	require.False(t, gatewayprovider.GrokBodyCodec().GrokChatResponsesRuntimeEligible("grok-4.5", ""))
	require.False(t, gatewayprovider.GrokBodyCodec().GrokChatResponsesRuntimeEligible("grok-4.6", ""))
}
