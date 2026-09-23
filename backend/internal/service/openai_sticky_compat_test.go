package service

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/stretchr/testify/require"
)

func TestSnapshotOpenAICompatibilityFallbackMetrics(t *testing.T) {
	ctx := requeststate.WithThinkingEnabled(context.Background(), true)
	_, _ = requeststate.ThinkingEnabledFromContext(ctx)

	after := (withSchedulerParametersForTest(&OpenAIGatewayService{})).SnapshotOpenAICompatibilityFallbackMetrics()
	// 请求已使用唯一原生快照，不再发生旧 key 回退；公开字段继续保留。
	require.Zero(t, after.MetadataLegacyFallbackTotal)
	require.Zero(t, after.MetadataLegacyFallbackThinkingEnabledTotal)
}
