package httpapi

import (
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
