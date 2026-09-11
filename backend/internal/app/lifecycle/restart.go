package lifecycle

import (
	"sync"
	"time"
)

// Restarter 只请求主循环关闭，进程退出与 supervisor 重启仍由入口负责。
type Restarter struct {
	mu       sync.Mutex
	platform string
	request  func()
	timer    *time.Timer
	closed   bool
	wg       sync.WaitGroup
}

func NewRestarter(platform string, request func()) *Restarter {
	return &Restarter{platform: platform, request: request}
}

// RequestRestart 保留原 500ms + 100ms 响应发送窗口，Linux 以外保持无操作。
func (r *Restarter) RequestRestart() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.platform != "linux" || r.closed || r.timer != nil {
		return nil
	}
	r.wg.Add(1)
	r.timer = time.AfterFunc(600*time.Millisecond, func() {
		defer r.wg.Done()
		r.request()
	})
	return nil
}

// Close 取消尚未发出的重启请求，并等待已经触发的回调返回。
func (r *Restarter) Close() {
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		if r.timer != nil && r.timer.Stop() {
			r.wg.Done()
		}
	}
	r.mu.Unlock()
	r.wg.Wait()
}
