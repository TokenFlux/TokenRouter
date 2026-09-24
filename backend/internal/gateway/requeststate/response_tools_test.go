package requeststate

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// 会话下一轮的名称发布与当前 turn 输出并行，当前映射和调用方输入保持独立。
func TestResponseToolsSessionUpdateKeepsActiveTurn(t *testing.T) {
	state := &ResponseTools{}
	input := map[string]string{"alias": "active"}
	state.SetCodexNames(false, input)
	input["alias"] = "caller changed"
	var tasks sync.WaitGroup
	for range 32 {
		tasks.Go(func() {
			state.SetCodexNames(true, map[string]string{"alias": "next"})
			require.Equal(t, "active", state.CodexNames(false)["alias"])
		})
	}
	tasks.Wait()
	require.Equal(t, "active", state.CodexNames(false)["alias"])
	require.Equal(t, "next", state.CodexNames(true)["alias"])
	other := &ResponseTools{}
	require.Nil(t, other.CodexNames(false))
}
