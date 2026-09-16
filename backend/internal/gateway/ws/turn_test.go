package ws

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 终态写入尚未提交时不允许下一轮抢占同一完成快照。
func TestTurnCommitPrecedesNextAdmission(t *testing.T) {
	lifecycle := NewTurnLifecycle(true)
	lifecycle.BeginTerminalWrite()
	started := make(chan struct{})
	admitted := make(chan bool, 1)
	go func() { close(started); admitted <- lifecycle.BeginResponseCreate(nil) }()
	<-started
	select {
	case <-admitted:
		t.Fatal("next turn admitted before terminal commit")
	case <-time.After(10 * time.Millisecond):
	}
	lifecycle.FinishTerminalWrite(true, nil)
	require.True(t, <-admitted)
	require.False(t, lifecycle.BeginResponseCreate(nil))
}

// 每轮金额时刻和模型快照不能被后续复用的入站缓冲区改变。
func TestTurnQueueFreezesInputAndPreservesOrder(t *testing.T) {
	queue := NewTurnPayloadQueue()
	tier := "priority"
	body := []byte(`{"model":"first"}`)
	started := time.Now()
	queue.Push(TurnPayload{StartedAt: started, RequestBody: body, OriginalModel: "first", ServiceTier: &tier})
	body[0] = '!'
	tier = "flex"
	queue.Push(TurnPayload{OriginalModel: "second"})
	first := queue.Pop()
	require.Equal(t, started, first.StartedAt)
	require.Equal(t, []byte(`{"model":"first"}`), first.RequestBody)
	require.Equal(t, "priority", *first.ServiceTier)
	require.Equal(t, "second", queue.Pop().OriginalModel)
	require.Equal(t, "passthrough_missing", queue.Pop().Source)
}
