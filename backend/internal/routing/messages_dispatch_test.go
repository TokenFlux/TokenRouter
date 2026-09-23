package routing

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// 不适用的模型和平台不得提前读取 Grok 动态映射。
func TestMessagesDispatchReadsDynamicModelOnlyForMatchedFamily(t *testing.T) {
	reads := 0
	options := MessagesDispatchOptions{
		NormalizeModel: strings.TrimSpace,
		CrossClientModel: func() string {
			reads++
			return "dynamic-model"
		},
	}
	require.Empty(t, ResolveMessagesDispatchModel(nil, "claude-sonnet", options))
	require.Empty(t, ResolveMessagesDispatchModel(&Group{Platform: "grok"}, "", options))
	require.Empty(t, ResolveMessagesDispatchModel(&Group{Platform: "grok"}, "gpt-5.5", options))
	require.Empty(t, ResolveMessagesDispatchModel(&Group{Platform: "openai"}, "claude-sonnet", options))
	require.Zero(t, reads)
	require.Equal(t, "dynamic-model", ResolveMessagesDispatchModel(&Group{Platform: "grok"}, "claude-sonnet", options))
	require.Equal(t, 1, reads)
}
