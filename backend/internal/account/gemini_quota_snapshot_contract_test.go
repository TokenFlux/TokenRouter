package account

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 策略读者不得通过返回对象改写服务下一次查询使用的缓存。
func TestGeminiQuotaPolicyReturnCannotMutateCachedPolicy(t *testing.T) {
	s := NewGeminiQuotaService(GeminiQuotaOptions{})
	policy := s.Policy(context.Background())
	limited := int64(1)
	policy.ApplyOverrides(map[string]GeminiTierQuotaOverride{"aistudio_free": {ProRPD: &limited}})
	next, ok := s.Policy(context.Background()).QuotaForTier("aistudio_free")
	require.True(t, ok)
	require.Equal(t, int64(50), next.ProRPD)
}
