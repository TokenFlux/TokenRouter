package admin

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// 最后一个 WS 离开后的三十秒空闲定时器不能拖延应用退出或在退出后重新安排。
func TestOpsWSShutdownCancelsIdleTimer(t *testing.T) {
	t.Cleanup(func() {
		qpsWSIdleStopMu.Lock()
		qpsWSRuntimeClosed = false
		qpsWSIdleStopMu.Unlock()
		qpsWSCache.mu.Lock()
		qpsWSCache.closed = false
		qpsWSCache.mu.Unlock()
	})
	scheduleQPSWSIdleStop()
	finished := make(chan struct{})
	go func() { StopOpsWSRuntime(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("关闭仍等待三十秒空闲定时器")
	}
	scheduleQPSWSIdleStop()
	qpsWSIdleStopMu.Lock()
	require.Nil(t, qpsWSIdleStopTimer)
	qpsWSIdleStopMu.Unlock()
	StopOpsWSRuntime()
}
