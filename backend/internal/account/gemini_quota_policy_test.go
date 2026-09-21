package account

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 静态参数、JSON 策略、动态设置依次覆盖；缓存到期前后保持原取值时机。
func TestGeminiQuotaPolicyPrecedenceTTLAndSnapshots(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	rpd := int64(10)
	raw := `{"tiers":{"aistudio_free":{"pro_rpd":30}}}`
	reads := 0
	core := NewGeminiQuotaService(GeminiQuotaOptions{Now: func() time.Time { return now }, StaticTiers: map[string]GeminiTierQuotaOverride{"aistudio_free": {ProRPD: &rpd}}, StaticPolicy: `{"quota_rules":{"aistudio_free":{"gemini_pro":{"rpd":20}}}}`, LoadPolicy: func(context.Context) (string, error) { reads++; return raw, nil }})
	staticOnly := NewGeminiQuotaService(GeminiQuotaOptions{StaticTiers: map[string]GeminiTierQuotaOverride{"aistudio_free": {ProRPD: &rpd}}})
	rpd = 99
	staticQuota, _ := staticOnly.Policy(context.Background()).QuotaForTier("aistudio_free")
	require.Equal(t, int64(10), staticQuota.ProRPD)
	first, ok := core.Policy(context.Background()).QuotaForTier("aistudio_free")
	require.True(t, ok)
	require.Equal(t, int64(30), first.ProRPD)
	require.Equal(t, 1, reads)
	raw = `{"quota_rules":{"aistudio_free":{"gemini_pro":{"rpd":40}}}}`
	next, _ := core.Policy(context.Background()).QuotaForTier("aistudio_free")
	require.Equal(t, int64(30), next.ProRPD)
	require.Equal(t, 1, reads)
	now = now.Add(time.Minute)
	next, _ = core.Policy(context.Background()).QuotaForTier("aistudio_free")
	require.Equal(t, int64(40), next.ProRPD)
	require.Equal(t, 2, reads)
	raw = "invalid-json"
	now = now.Add(time.Minute)
	next, _ = core.Policy(context.Background()).QuotaForTier("aistudio_free")
	require.Equal(t, int64(20), next.ProRPD, "设置损坏沿用原静态策略回退")
}

// 并发读者可修改各自返回对象，不能修改服务正在使用的策略缓存。
func TestGeminiQuotaPolicyConcurrentReaderIsolation(t *testing.T) {
	var reads atomic.Int64
	core := NewGeminiQuotaService(GeminiQuotaOptions{LoadPolicy: func(context.Context) (string, error) { reads.Add(1); return "", nil }})
	core.Policy(context.Background())
	var group sync.WaitGroup
	var corrupted atomic.Bool
	for i := 0; i < 20; i++ {
		group.Go(func() {
			for j := 0; j < 20; j++ {
				value := int64(j + 1)
				policy := core.Policy(context.Background())
				policy.ApplyOverrides(map[string]GeminiTierQuotaOverride{"aistudio_free": {ProRPD: &value}})
				next, _ := core.Policy(context.Background()).QuotaForTier("aistudio_free")
				if next.ProRPD != 50 {
					corrupted.Store(true)
				}
			}
		})
	}
	group.Wait()
	require.False(t, corrupted.Load(), "返回策略不能污染缓存")
	require.Equal(t, int64(1), reads.Load())
}
