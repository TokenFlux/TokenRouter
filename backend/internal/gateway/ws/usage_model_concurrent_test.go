package ws

import (
	"sync"
	"testing"
)

// 复用生产上下行调用的两个方法，以同一开始屏障固定并发读写。
func TestS09WSUsageModelConcurrentDirections(t *testing.T) {
	m := NewUsageMeta("first", nil, nil)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 10000; i++ {
			m.UpdateSessionRequestModel([]byte(`{"type":"session.update","session":{"model":"next"}}`))
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 10000; i++ {
			_ = m.RequestModelForFrame(nil)
		}
	}()
	close(start)
	wg.Wait()
}
