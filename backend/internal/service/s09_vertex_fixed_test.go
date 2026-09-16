package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 控制锁返回时刻，在缓存返回之前确定地取消调用。
type s09VertexCancelCache struct {
	GeminiTokenCache
	cancel context.CancelFunc
	reads  int
}

func (c *s09VertexCancelCache) GetAccessToken(context.Context, string) (string, error) {
	c.reads++
	if c.reads > 1 {
		return "peer-token", nil
	}
	return "", nil
}
func (c *s09VertexCancelCache) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	c.cancel()
	return false, nil
}
func TestS09VertexLockWaitCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cache := &s09VertexCancelCache{cancel: cancel}
	a := &Account{ID: 99, Type: AccountTypeServiceAccount, Platform: PlatformGemini, Credentials: map[string]any{"service_account_json": `{"type":"service_account","project_id":"fixture","private_key_id":"fixture","private_key":"unused-key","client_email":"fixture@example.invalid"}`}}
	started := time.Now()
	token, err := getVertexServiceAccountAccessToken(ctx, cache, a)
	if !errors.Is(err, context.Canceled) || token != "" {
		t.Fatalf("canceled lock wait continued %v and returned token=%q err=%v; cache reads=%d", time.Since(started), token, err, cache.reads)
	}
}
