// 验证授权状态的存取和有效期。
package account

import (
	"sync"
	"testing"
	"time"
)

func TestClaudeAuthorizationSessions_Stop_Idempotent(t *testing.T) {
	store := NewClaudeAuthorizationSessions()
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
func TestClaudeAuthorizationSessions_Stop_Concurrent(t *testing.T) {
	store := NewClaudeAuthorizationSessions()
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
