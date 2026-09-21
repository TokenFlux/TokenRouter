package requeststate

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestMetadataWriteAndRead_NoBridge(t *testing.T) {
	ctx := WithIsMaxTokensOneHaikuRequest(context.Background(), true)
	ctx = WithThinkingEnabled(ctx, true)
	ctx = WithPrefetchedStickySession(ctx, 123, 456)
	ctx = WithSingleAccountRetry(ctx, true)
	ctx = WithAccountSwitchCount(ctx, 2)
	value, present := IsMaxTokensOneHaikuRequestFromContext(ctx)
	require.True(t, value)
	require.True(t, present)
	value, present = ThinkingEnabledFromContext(ctx)
	require.True(t, value)
	require.True(t, present)
	id, present := PrefetchedStickyAccountIDFromContext(ctx)
	require.Equal(t, int64(123), id)
	require.True(t, present)
	id, present = PrefetchedStickyGroupIDFromContext(ctx)
	require.Equal(t, int64(456), id)
	require.True(t, present)
	value, present = SingleAccountRetryFromContext(ctx)
	require.True(t, value)
	require.True(t, present)
	count, present := AccountSwitchCountFromContext(ctx)
	require.Equal(t, 2, count)
	require.True(t, present)
}

func TestExecutionHintsSnapshotIsolation(t *testing.T) {
	parent := WithThinkingEnabled(context.Background(), true)
	child := WithThinkingEnabled(parent, false)
	value, present := ThinkingEnabledFromContext(child)
	require.False(t, value)
	require.True(t, present)
	value, present = ThinkingEnabledFromContext(parent)
	require.True(t, value)
	require.True(t, present)
	// 修改取回的值不会污染已有 context，也不会影响随后创建的 attempt。
	snapshot := ExecutionHintsFromContext(parent)
	snapshot.ThinkingEnabled.Value = false
	require.True(t, ExecutionHintsFromContext(parent).ThinkingEnabled.Value)
	require.False(t, ExecutionHintsFromContext(WithExecutionHints(parent, snapshot)).ThinkingEnabled.Value)
}

func TestExecutionHintsMissingAndExplicitZero(t *testing.T) {
	value, present := ThinkingEnabledFromContext(context.Background())
	require.False(t, value)
	require.False(t, present)
	ctx := WithAccountSwitchCount(context.Background(), 0)
	count, present := AccountSwitchCountFromContext(ctx)
	require.Zero(t, count)
	require.True(t, present)
	ctx = WithPrefetchedStickySession(ctx, 0, 0)
	id, present := PrefetchedStickyGroupIDFromContext(ctx)
	require.Zero(t, id)
	require.True(t, present)
	// 未提供 context 的兼容边界仍返回空值，不创建隐式请求。
	var missing context.Context
	require.Zero(t, ExecutionHintsFromContext(missing))
	require.Nil(t, WithThinkingEnabled(missing, true))
}
