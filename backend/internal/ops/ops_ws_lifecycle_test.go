package ops

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 最后一个 WS 离开后的三十秒空闲定时器不能拖延应用退出或在退出后重新安排。
func TestOpsWSShutdownCancelsIdleTimer(t *testing.T) {
	rt := NewRealtimeRuntime()

	rt.scheduleQPSWSIdleStop()
	finished := make(chan struct{})
	go func() { rt.Stop(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("关闭仍等待三十秒空闲定时器")
	}
	rt.scheduleQPSWSIdleStop()
	rt.qpsWSIdleStopMu.Lock()
	require.Nil(t, rt.qpsWSIdleStopTimer)
	rt.qpsWSIdleStopMu.Unlock()
	rt.Stop()
}

// 跨请求的序列化快照仍是独立副本，不允许下游改写共享缓存。
func TestS08RealtimePayloadRequestIsolation(t *testing.T) {
	r := NewRealtimeRuntime()
	r.cache.payload.Store([]byte(`{"type":"qps_update"}`))
	first := r.Payload()
	first[0] = 'x'
	require.Equal(t, byte('{'), r.Payload()[0])
	r.Stop()
}
