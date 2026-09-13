package service

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"
	"github.com/stretchr/testify/require"
	"testing"
)

// 共享 flight 的每次返回必须隔离额度指针、Header 和本地统计。
func TestGrokProbeResultDoesNotExposeSharedValues(t *testing.T) {
	limit, retry := int64(20), 9
	source := &GrokQuotaProbeResult{Snapshot: &xai.QuotaSnapshot{
		Tokens: &xai.QuotaWindow{Limit: &limit}, RetryAfterSeconds: &retry, Headers: map[string]string{"limit": "20"},
	}, LocalUsage24h: &WindowStats{Tokens: 3}}
	svc := &GrokQuotaService{}
	got, err := svc.runProbeFlight(context.Background(), "copy", func(context.Context) (*GrokQuotaProbeResult, error) { return source, nil })
	require.NoError(t, err)
	*got.Snapshot.Tokens.Limit = 40
	*got.Snapshot.RetryAfterSeconds = 100
	got.Snapshot.Headers["limit"] = "40"
	got.LocalUsage24h.Tokens = 50
	require.Equal(t, int64(20), *source.Snapshot.Tokens.Limit)
	require.Equal(t, 9, *source.Snapshot.RetryAfterSeconds)
	require.Equal(t, "20", source.Snapshot.Headers["limit"])
	require.Equal(t, int64(3), source.LocalUsage24h.Tokens)
}
