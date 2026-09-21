//go:build unit

package service

import (
	"sync"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func s06HeaderOverrideAccount() *Account {
	return &Account{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{
		"header_override_enabled": true, "header_overrides": map[string]any{"x-test-marker": "configured"}}}
}

// 临时修改覆写结果不能影响另一个请求的配置。
func TestS06HeaderOverrideResultIsolation(t *testing.T) {
	account := s06HeaderOverrideAccount()
	account.GetHeaderOverrides()["x-test-marker"] = "changed-by-caller"
	require.Equal(t, "configured", account.GetHeaderOverrides()["x-test-marker"])
}

// 并发解析不可变配置不得回写共享账号字段。
func TestS06HeaderOverrideConcurrentReads(t *testing.T) {
	account := s06HeaderOverrideAccount()
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			<-start
			for range 20 {
				if account.GetHeaderOverrides()["x-test-marker"] != "configured" {
					t.Error("请求头覆写读取结果发生变化")
				}
			}
		})
	}
	close(start)
	wg.Wait()
}
