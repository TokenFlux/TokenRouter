package app

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/stretchr/testify/require"
)

func TestSnapshotOpenAICompatibilityFallbackMetrics(t *testing.T) {
	before := gatewayCompatibilitySnapshot(nil)
	ctx := requeststate.WithThinkingEnabled(context.Background(), true)
	_, _ = requeststate.ThinkingEnabledFromContext(ctx)

	after := gatewayCompatibilitySnapshot(nil)
	// 请求已使用唯一原生快照，不再发生旧 key 回退；公开字段继续保留。
	require.Zero(t, after.MetadataTotal)
	require.Zero(t, before.MetadataTotal)
}
