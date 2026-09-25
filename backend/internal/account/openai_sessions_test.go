// 验证授权会话的创建、消费和清理。
package account

import (
	"sync"
	"testing"
	"time"
)

func TestOpenAISessionStore_Stop_Idempotent(t *testing.T) {
	store := NewOpenAISessionStore()
	store.Start()

	store.Stop()
	store.Stop()

	select {
	case <-store.stopCh:
		// ok
	case <-time.After(time.Second):
		t.Fatal("stopCh 未关闭")
	}
}
func TestOpenAISessionStore_Stop_Concurrent(t *testing.T) {
	store := NewOpenAISessionStore()
	store.Start()

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store.Stop()
		}()
	}

	wg.Wait()

	select {
	case <-store.stopCh:
		// ok
	case <-time.After(time.Second):
		t.Fatal("stopCh 未关闭")
	}
}
