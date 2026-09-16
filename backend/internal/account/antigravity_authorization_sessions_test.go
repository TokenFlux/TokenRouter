// 原会话白盒测试随唯一实现迁移，保留所有断言。
package account

import (
	"testing"
	"time"
)

func TestNewAntigravitySessionStore(t *testing.T) {
	store := NewAntigravityAuthorizationSessions()
	store.Start()
	defer store.Stop()

	if store == nil {
		t.Fatal("NewAntigravityAuthorizationSessions 返回 nil")
	}
	if store.sessions == nil {
		t.Error("sessions map 不应为 nil")
	}
}
func TestAntigravitySessionStore_SetAndGet(t *testing.T) {
	store := NewAntigravityAuthorizationSessions()
	store.Start()
	defer store.Stop()

	session := &AntigravityAuthorizationSession{
		State:        "test-state",
		CodeVerifier: "test-verifier",
		ProxyURL:     "http://proxy.example.com",
		CreatedAt:    time.Now(),
	}

	store.Set("session-1", session)

	got, ok := store.Get("session-1")
	if !ok {
		t.Fatal("Get 应返回 true")
	}
	if got.State != "test-state" {
		t.Errorf("State 不匹配: got %s", got.State)
	}
	if got.CodeVerifier != "test-verifier" {
		t.Errorf("CodeVerifier 不匹配: got %s", got.CodeVerifier)
	}
	if got.ProxyURL != "http://proxy.example.com" {
		t.Errorf("ProxyURL 不匹配: got %s", got.ProxyURL)
	}
}
func TestAntigravitySessionStore_Get_不存在(t *testing.T) {
	store := NewAntigravityAuthorizationSessions()
	store.Start()
	defer store.Stop()

	_, ok := store.Get("nonexistent")
	if ok {
		t.Error("不存在的 session 应返回 false")
	}
}
func TestAntigravitySessionStore_Get_过期(t *testing.T) {
	store := NewAntigravityAuthorizationSessions()
	store.Start()
	defer store.Stop()

	session := &AntigravityAuthorizationSession{
		State:     "expired-state",
		CreatedAt: time.Now().Add(-AntigravitySessionTTL - time.Minute), // 已过期
	}

	store.Set("expired-session", session)

	_, ok := store.Get("expired-session")
	if ok {
		t.Error("过期的 session 应返回 false")
	}
}
func TestAntigravitySessionStore_Delete(t *testing.T) {
	store := NewAntigravityAuthorizationSessions()
	store.Start()
	defer store.Stop()

	session := &AntigravityAuthorizationSession{
		State:     "to-delete",
		CreatedAt: time.Now(),
	}

	store.Set("del-session", session)
	store.Delete("del-session")

	_, ok := store.Get("del-session")
	if ok {
		t.Error("删除后 Get 应返回 false")
	}
}
func TestAntigravitySessionStore_Delete_不存在(t *testing.T) {
	store := NewAntigravityAuthorizationSessions()
	store.Start()
	defer store.Stop()

	// 删除不存在的 session 不应 panic
	store.Delete("nonexistent")
}
func TestAntigravitySessionStore_Stop(t *testing.T) {
	store := NewAntigravityAuthorizationSessions()
	store.Start()
	store.Stop()

	// 多次 Stop 不应 panic
	store.Stop()
}
func TestAntigravitySessionStore_多个Session(t *testing.T) {
	store := NewAntigravityAuthorizationSessions()
	store.Start()
	defer store.Stop()

	for i := 0; i < 10; i++ {
		session := &AntigravityAuthorizationSession{
			State:     "state-" + string(rune('0'+i)),
			CreatedAt: time.Now(),
		}
		store.Set("session-"+string(rune('0'+i)), session)
	}

	// 验证都能取到
	for i := 0; i < 10; i++ {
		_, ok := store.Get("session-" + string(rune('0'+i)))
		if !ok {
			t.Errorf("session-%d 应存在", i)
		}
	}
}
