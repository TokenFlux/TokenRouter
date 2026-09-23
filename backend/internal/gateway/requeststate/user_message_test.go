package requeststate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 用户消息与工具回包必须保留不同的串行等待资格。
func TestIsRealUserMessage(t *testing.T) {
	require.False(t, IsRealUserMessage(nil))
	for _, tc := range []struct {
		name, messages string
		want           bool
	}{
		{"empty", `[]`, false},
		{"object", `{}`, false},
		{"non_object_last", `["user"]`, false},
		{"assistant", `[{"role":"user"},{"role":"assistant"}]`, false},
		{"missing_content", `[{"role":"user"}]`, true},
		{"text", `[{"role":"user","content":"hello"}]`, true},
		{"empty_content", `[{"role":"user","content":[]}]`, true},
		{"text_block", `[{"role":"user","content":[{"type":"text","text":"hello"}]}]`, true},
		{"tool_result", `[{"role":"user","content":[{"type":"text"},{"type":"tool_result"}]}]`, false},
		{"tool_use_result", `[{"role":"user","content":[{"type":"tool_use_result"}]}]`, false},
		{"earlier_tool", `[{"role":"user","content":[{"type":"tool_result"}]},{"role":"user","content":"next"}]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"test","messages":`+tc.messages+`}`)), "anthropic")
			require.NoError(t, err)
			require.Equal(t, tc.want, IsRealUserMessage(parsed))
		})
	}
}
