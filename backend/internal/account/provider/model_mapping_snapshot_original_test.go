//go:build unit

package provider

import (
	"sync"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 返回模型规则不能暴露账号内部派生状态，调用方的临时修改不得污染后续请求。
func TestS06ModelMappingResultIsolation(t *testing.T) {
	account := &accountcore.Record{Platform: capability.PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"alias": "target"}}}
	first := accountcore.ResolveModelMapping(account, ModelDefaults())
	first["alias"] = "changed-by-caller"
	require.Equal(t, "target", accountcore.ResolveModelMapping(account, ModelDefaults())["alias"])
}

// 调度与展示可以并发读取同一配置，纯模型规则的读取不能写入共享账号字段。
func TestS06ModelMappingConcurrentReads(t *testing.T) {
	account := &accountcore.Record{Platform: capability.PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"alias": "target"}}}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			<-start
			for range 20 {
				if accountcore.ResolveModelMapping(account, ModelDefaults())["alias"] != "target" {
					t.Error("模型映射读取结果发生变化")
				}
			}
		})
	}
	close(start)
	wg.Wait()
}
