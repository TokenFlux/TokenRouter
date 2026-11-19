package httpapi

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
)

// TestChatCompletionsCapturesFunctionToolUse verifies that admitted Chat bodies
// carry function-tool use into selection context. Plain text and custom-only
// requests leave that requirement unset for the native route.
func TestChatCompletionsCapturesFunctionToolUse(t *testing.T) {
	tests := []struct {
		name string // ingress fixture label
		body string // admitted Chat request
		want bool   // function-tool requirement
	}{
		{"text", `{"model":"gpt-6.2-astra","messages":[{"role":"user","content":"hello"}]}`, false},
		{"custom", `{"model":"gpt-6.2-astra","tools":[{"type":"custom","custom":{"name":"freeform"}}]}`, false},
		{"function", `{"model":"gpt-6.2-astra","tools":[{"type":"function","function":{"name":"lookup"}}]}`, true},
		{"legacy", `{"model":"gpt-6.2-sol","functions":[{"name":"lookup"}]}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe, handler, c, _, _ := newOpenAITextEntryProbe(t, tt.body)
			handler.ChatCompletions(c)
			require.NotNil(t, probe.call)
			require.Equal(t, tt.want, requeststate.ChatFunctionToolsRequired(c.Request.Context()))
		})
	}
}

// TestOpenAITextEntriesRejectDuplicateAndCaseVariantPayloads submits the
// security report's duplicate-model Messages PoC, Unicode-escaped duplicates,
// compact-trigger duplicates, and uppercase protocol variants across Messages,
// Chat, and Responses while preserving 403 protocol-gate precedence.
func TestOpenAITextEntriesRejectDuplicateAndCaseVariantPayloads(t *testing.T) {
	pocMessages := `{"model":"claude-3-5-haiku-20241022","max_tokens":64,"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"step"},{"type":"text","text":"ok"}]},{"role":"user","content":"hi"}],"model":"claude-opus-4-8"}`

	for _, payload := range []string{
		pocMessages,
		`{"model":"claude-haiku-4-5","mo\u0064el":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"claude-haiku-4-5","Model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}]}`,
		"{\"model\":\"claude-haiku-4-5\",\"max_tokens\":1,\"max_to\u212Aens\":999,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}",
		`{"model":"claude-haiku-4-5","Messages":[{"role":"user","content":"blocked"}]}`,
		"{\"model\":\"claude-haiku-4-5\",\"me\u017F\u017Fages\":[{\"role\":\"user\",\"content\":\"blocked\"}]}",
	} {
		probe, handler, c, w, _ := newOpenAITextEntryProbe(t, payload)
		c.Request.URL.Path = "/v1/messages"
		handler.Messages(c)
		require.Equal(t, http.StatusBadRequest, w.Code, payload)
		require.Contains(t, w.Body.String(), "JSON key", payload)
		require.Nil(t, probe.call)

		chatProbe, chatHandler, chatCtx, chatRec, _ := newOpenAITextEntryProbe(t, payload)
		chatCtx.Request.URL.Path = "/v1/chat/completions"
		chatHandler.ChatCompletions(chatCtx)
		require.Equal(t, http.StatusBadRequest, chatRec.Code, payload)
		require.Contains(t, chatRec.Body.String(), "JSON key", payload)
		require.Nil(t, chatProbe.call)
	}

	extProbe, extHandler, extCtx, extRec, _ := newOpenAITextEntryProbe(t, `{"model":"gpt-6.2-astra","clientinfo":"custom-sdk","messages":[{"role":"user","content":"hello"}]}`)
	extCtx.Request.URL.Path = "/v1/chat/completions"
	extHandler.ChatCompletions(extCtx)
	require.Equal(t, http.StatusOK, extRec.Code)
	require.NotNil(t, extProbe.call)

	for _, payload := range []string{
		`{"model":"gpt-5-mini","input":"hi","model":"gpt-5.4"}`,
		`{"model":"gpt-5-mini","input":[{"type":"compaction_trigger"}],"model":"gpt-5.4"}`,
		`{"model":"gpt-5-mini","Previous_Response_Id":"resp_foreign","input":"hi"}`,
	} {
		probe, handler, c, w, _ := newOpenAITextEntryProbe(t, payload)
		c.Request.URL.Path = "/v1/responses"
		handler.Responses(c)
		require.Equal(t, http.StatusBadRequest, w.Code, payload)
		require.Nil(t, probe.call)
	}

	deniedProbe, deniedHandler, deniedCtx, deniedRec, reader := newOpenAITextEntryProbe(t, pocMessages)
	deniedProbe.allowed = false
	deniedCtx.Request.URL.Path = "/v1/messages"
	deniedHandler.Messages(deniedCtx)
	require.Equal(t, http.StatusForbidden, deniedRec.Code)
	require.Zero(t, reader.reads)
	require.Nil(t, deniedProbe.call)
}
