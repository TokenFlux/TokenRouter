//go:build unit

package grok_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
)

func TestCountGrokNativeSearchCallsFromJSON_MessagesStyleBody(t *testing.T) {
	// 验证 Anthropic 缓冲的 Grok /v1/messages 路径使用同一计数器。
	body := []byte(`{"id":"r1","output":[{"type":"web_search_call","id":"ws1"},{"type":"message","role":"assistant"}]}`)
	require.Equal(t, 1, grok.CountGrokNativeSearchCallsFromJSONBytes(body))
}
