package bridge

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRetypedResponsesToolCallItemID(t *testing.T) {
	for _, tc := range []struct {
		name     string
		id       string
		itemType string
		want     string
	}{
		{name: "function to custom", id: "fc_abc", itemType: "custom_tool_call", want: "ctc_abc"},
		{name: "function to search", id: "fc_abc", itemType: "tool_search_call", want: "tsc_abc"},
		{name: "correct prefix unchanged", id: "ctc_abc", itemType: "custom_tool_call", want: "ctc_abc"},
		{name: "custom to function", id: "ctc_abc", itemType: "function_call", want: "fc_abc"},
		{name: "unknown prefix unchanged", id: "item_abc", itemType: "custom_tool_call", want: "item_abc"},
		{name: "unprefixed unchanged", id: "abc", itemType: "custom_tool_call", want: "abc"},
		{name: "empty unchanged", id: "", itemType: "custom_tool_call", want: ""},
		{name: "unconstrained type unchanged", id: "fc_abc", itemType: "message", want: "fc_abc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, retypedResponsesToolCallItemID(tc.id, tc.itemType))
		})
	}
}
