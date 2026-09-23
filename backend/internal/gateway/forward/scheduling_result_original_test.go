package forward_test

import (
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIForwardSucceededForScheduling(t *testing.T) {
	require.True(t, schedulingSucceeded(nil))
	require.True(t, schedulingSucceeded(&forwardcore.OpenAIResult{}))
	require.True(t, schedulingSucceeded(&forwardcore.OpenAIResult{
		OpenAIWSMode:          true,
		UpstreamTerminalEvent: "response.completed",
	}))
	require.False(t, schedulingSucceeded(&forwardcore.OpenAIResult{
		OpenAIWSMode:          true,
		UpstreamTerminalEvent: "response.failed",
	}))
}

// 原 nil 与 WS 终态断言直接验证结果值的方法。
func schedulingSucceeded(result *forwardcore.OpenAIResult) bool {
	return result.SucceededForScheduling()
}
