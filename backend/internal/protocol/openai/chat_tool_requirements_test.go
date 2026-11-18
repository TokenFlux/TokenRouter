package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestChatUsesFunctionTools exercises selectors and history alongside tool
// declarations. Only ordinary function tools, including the legacy wire forms,
// should constrain selection of the native Chat protocol.
func TestChatUsesFunctionTools(t *testing.T) {
	tests := []struct {
		name string // tool fixture label
		body string // submitted Chat payload
		want bool   // function-tool requirement
	}{
		{"named function", `{"tool_choice":{"type":"function","function":{"name":"lookup"}}}`, true},
		{"allowed custom", `{"tool_choice":{"type":"custom","custom":{"name":"freeform"}}}`, false},
		{"legacy name", `{"function_call":{"name":"lookup"}}`, true},
		{"legacy result", `{"messages":[{"role":"function","name":"lookup","content":"result"}]}`, true},
		{"modern history", `{"messages":[{"role":"assistant","tool_calls":[{"type":"function"}]}]}`, true},
		{"custom history", `{"messages":[{"role":"assistant","tool_calls":[{"type":"custom"}]}]}`, false},
		{"mixed declarations", `{"tools":[{"type":"custom"},{"type":"function"}]}`, true},
		{"empty and none", `{"functions":[],"tools":[],"function_call":"none"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ChatUsesFunctionTools([]byte(tt.body)))
		})
	}
}
