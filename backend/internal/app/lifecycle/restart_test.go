package lifecycle

import (
	"github.com/stretchr/testify/require"
	"sync/atomic"
	"testing"
	"time"
)

// 重启只发出一次关闭请求；关闭会取消尚未触发的延迟回调。
func TestRestarterPlatformDelayAndClose(t *testing.T) {
	for _, platform := range []string{"linux", "darwin", "windows"} {
		t.Run(platform, func(t *testing.T) {
			var calls atomic.Int32
			called := make(chan struct{}, 2)
			r := NewRestarter(platform, func() { calls.Add(1); called <- struct{}{} })
			defer r.Close()
			for range 8 {
				require.NoError(t, r.RequestRestart())
			}
			select {
			case <-called:
				t.Fatal("未保留响应发送窗口")
			case <-time.After(100 * time.Millisecond):
			}
			if platform == "linux" {
				select {
				case <-called:
				case <-time.After(time.Second):
					t.Fatal("没有请求关闭")
				}
			}
			r.Close()
			require.NoError(t, r.RequestRestart())
			expected := int32(0)
			if platform == "linux" {
				expected = 1
			}
			require.Equal(t, expected, calls.Load())
		})
	}
	r := NewRestarter("linux", func() { t.Error("关闭后仍发出重启") })
	require.NoError(t, r.RequestRestart())
	r.Close()
	time.Sleep(650 * time.Millisecond)
}

// 回调已经开始时，Close 必须等待它返回，避免在资源释放后继续调用入口。
func TestRestarterCloseWaitsForCallback(t *testing.T) {
	entered, release, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	r := NewRestarter("linux", func() { close(entered); <-release })
	require.NoError(t, r.RequestRestart())
	<-entered
	go func() { r.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("回调尚未完成")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-closed
}
