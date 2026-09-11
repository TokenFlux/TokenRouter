package provider

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// 日期回退需要第二次产生候选，但不能重新取得运行时平台默认值。
func TestCatalogQueryFreezesOneCandidateFactory(t *testing.T) {
	factories, lookups := 0, 0
	options := Options{ModelLookupCandidates: func() func(string) []string {
		factories++
		return func(model string) []string {
			lookups++
			if model == "gpt-9.0" {
				return []string{"priced-model"}
			}
			return []string{model}
		}
	}}
	service := NewPricingServiceFromSnapshot(options, nil, Snapshot{Data: map[string]*LiteLLMModelPricing{"priced-model": {InputCostPerToken: 0.001}}})
	price := service.GetModelPricing("gpt-9.0-20260101")
	require.NotNil(t, price)
	require.Equal(t, 0.001, price.InputCostPerToken)
	require.Equal(t, 1, factories)
	require.Equal(t, 2, lookups)
}

// 快照不暴露可写缓存别名，并保留 nil 与显式空切片。
func TestCatalogSnapshotIsIndependent(t *testing.T) {
	service := NewPricingServiceFromSnapshot(Options{}, nil, Snapshot{Data: map[string]*LiteLLMModelPricing{"model": {InputCostPerToken: 1, SupportedModalities: []string{"text"}, SupportedOutputModalities: []string{}}}})
	snapshot := service.Snapshot()
	require.NotNil(t, snapshot.Data["model"].SupportedOutputModalities)
	snapshot.Data["model"].InputCostPerToken = 2
	snapshot.Data["model"].SupportedModalities[0] = "image"
	delete(snapshot.Data, "model")
	stored := service.Snapshot().Data["model"]
	require.Equal(t, 1.0, stored.InputCostPerToken)
	require.Equal(t, []string{"text"}, stored.SupportedModalities)
	empty := NewPricingServiceFromSnapshot(Options{}, nil, Snapshot{})
	require.Nil(t, empty.Snapshot().Data)
}

type lifecyclePricingRemote struct{ calls atomic.Int64 }

func (r *lifecyclePricingRemote) FetchPricingJSON(context.Context, string) ([]byte, error) {
	r.calls.Add(1)
	return []byte(`{"model":{"input_cost_per_token":0.001,"output_cost_per_token":0.002}}`), nil
}
func (r *lifecyclePricingRemote) FetchHashText(context.Context, string) (string, error) {
	return "", nil
}

// 构造不加载数据；显式初始化后才启动唯一更新任务，重复停止等待同一任务退出。
func TestPricingConstructionAndLifecycle(t *testing.T) {
	remote := &lifecyclePricingRemote{}
	service := NewPricingService(Options{DataDir: t.TempDir(), RemoteURL: "https://pricing.invalid/catalog"}, remote)
	require.Zero(t, remote.calls.Load())
	require.NoError(t, service.Initialize())
	require.Equal(t, int64(1), remote.calls.Load())
	service.Start()
	service.Start()
	service.Stop()
	service.Stop()
	require.Equal(t, int64(1), remote.calls.Load())
	require.NotNil(t, service.GetModelPricing("model"))
}

// 更新线程整体替换目录时，并发读者只看见一份完整价格，停止后目录仍可读取。
func TestPricingConcurrentReadAndReload(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.json")
	second := filepath.Join(dir, "second.json")
	require.NoError(t, os.WriteFile(first, []byte(`{"model":{"input_cost_per_token":1,"output_cost_per_token":2}}`), 0600))
	require.NoError(t, os.WriteFile(second, []byte(`{"model":{"input_cost_per_token":3,"output_cost_per_token":4}}`), 0600))
	service := NewPricingService(Options{DataDir: dir}, nil)
	require.NoError(t, service.LoadPricingData(first))
	var readers sync.WaitGroup
	var inconsistent atomic.Bool
	for range 4 {
		readers.Go(func() {
			for range 200 {
				price := service.GetModelPricing("model")
				if price == nil || price.OutputCostPerToken-price.InputCostPerToken != 1 {
					inconsistent.Store(true)
				}
				_ = service.GetStatus()
				_ = service.Snapshot()
			}
		})
	}
	for i := range 20 {
		file := first
		if i%2 == 0 {
			file = second
		}
		require.NoError(t, service.LoadPricingData(file))
	}
	readers.Wait()
	require.False(t, inconsistent.Load())
	service.Stop()
	require.NotNil(t, service.GetModelPricing("model"))
}
