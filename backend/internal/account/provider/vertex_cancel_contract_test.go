package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// 控制锁返回时刻，在缓存返回之前确定地取消调用。
type s09VertexCancelCache struct {
	account.AccessTokenCache
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
	a := &account.Record{ID: 99, Type: capability.AccountTypeServiceAccount, Platform: capability.PlatformGemini, Credentials: map[string]any{"service_account_json": `{"type":"service_account","project_id":"fixture","private_key_id":"fixture","private_key":"unused-key","client_email":"fixture@example.invalid"}`}}
	started := time.Now()
	token, err := VertexServiceAccountAccessToken(ctx, cache, a)
	if !errors.Is(err, context.Canceled) || token != "" {
		t.Fatalf("canceled lock wait continued %v and returned token=%q err=%v; cache reads=%d", time.Since(started), token, err, cache.reads)
	}
}
