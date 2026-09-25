// 验证授权会话的并发访问和过期行为。
package account

import (
	"sync"
	"testing"
	"time"
)

func TestSessionStore_SetAndGet(t *testing.T) {
	store := NewGeminiAuthorizationSessions()
	store.Start()
	defer store.Stop()

	session := &GeminiOAuthSession{
		State:     "test-state",
		OAuthType: "code_assist",
		CreatedAt: time.Now(),
	}
	store.Set("sid-1", session)

	got, ok := store.Get("sid-1")
	if !ok {
		t.Fatal("期望 Get 返回 ok=true，实际返回 false")
	}
	if got.State != "test-state" {
		t.Errorf("期望 State=%q，实际=%q", "test-state", got.State)
	}
}
func TestSessionStore_GetNotFound(t *testing.T) {
	store := NewGeminiAuthorizationSessions()
	store.Start()
	defer store.Stop()

	_, ok := store.Get("不存在的ID")
	if ok {
		t.Error("期望不存在的 sessionID 返回 ok=false")
	}
}
func TestSessionStore_GetExpired(t *testing.T) {
	store := NewGeminiAuthorizationSessions()
	store.Start()
	defer store.Stop()

	// 创建一个已过期的 session（CreatedAt 设置为 GeminiAuthorizationSessionTTL+1 分钟之前）
	session := &GeminiOAuthSession{
		State:     "expired-state",
		OAuthType: "code_assist",
		CreatedAt: time.Now().Add(-(GeminiAuthorizationSessionTTL + 1*time.Minute)),
	}
	store.Set("expired-sid", session)

	_, ok := store.Get("expired-sid")
	if ok {
		t.Error("期望过期的 session 返回 ok=false")
	}
}
func TestSessionStore_Delete(t *testing.T) {
	store := NewGeminiAuthorizationSessions()
	store.Start()
	defer store.Stop()

	session := &GeminiOAuthSession{
		State:     "to-delete",
		OAuthType: "code_assist",
		CreatedAt: time.Now(),
	}
	store.Set("del-sid", session)

	// 先确认存在
	if _, ok := store.Get("del-sid"); !ok {
		t.Fatal("删除前 session 应该存在")
	}

	store.Delete("del-sid")

	if _, ok := store.Get("del-sid"); ok {
		t.Error("删除后 session 不应该存在")
	}
}
func TestSessionStore_Stop_Idempotent(t *testing.T) {
	store := NewGeminiAuthorizationSessions()
	store.Start()

	// 多次调用 Stop 不应 panic
	store.Stop()
	store.Stop()
	store.Stop()
}
func TestSessionStore_ConcurrentAccess(t *testing.T) {
	store := NewGeminiAuthorizationSessions()
	store.Start()
	defer store.Stop()

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	// 并发写入
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			sid := "concurrent-" + string(rune('A'+idx%26))
			store.Set(sid, &GeminiOAuthSession{
				State:     sid,
				OAuthType: "code_assist",
				CreatedAt: time.Now(),
			})
		}(i)
	}

	// 并发读取
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			sid := "concurrent-" + string(rune('A'+idx%26))
			store.Get(sid) // 可能找到也可能没找到，关键是不 panic
		}(i)
	}

	// 并发删除
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			sid := "concurrent-" + string(rune('A'+idx%26))
			store.Delete(sid)
		}(i)
	}

	wg.Wait()
}
