package requeststate

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
)

func TestRoutingStateIsolatesRequestAndAttempt(t *testing.T) {
	group := &routing.Group{ID: 7, Hydrated: true, ModelRouting: map[string][]int64{"model": {1, 2}}}
	ctx := WithGroup(context.Background(), group)
	group.ModelRouting["model"][0] = 99
	first, ok := GroupFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, []int64{1, 2}, first.ModelRouting["model"])
	first.ModelRouting["model"][1] = 100
	second, ok := GroupFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, []int64{1, 2}, second.ModelRouting["model"])

	ctx = WithClientProtocol(ctx, protocol.ProtocolAnthropicMessages)
	ctx = WithRoutePlan(ctx, routing.Plan(routing.PlanInput{Group: second, ClientProtocol: protocol.ProtocolAnthropicMessages}))
	child := WithGroup(ctx, &routing.Group{ID: 8})
	parentGroup, _ := GroupFromContext(ctx)
	childGroup, _ := GroupFromContext(child)
	require.Equal(t, int64(7), parentGroup.ID)
	require.Equal(t, int64(8), childGroup.ID)
	// 分组改变后仍保留原计划供执行层检查 ID，不悄悄替换原协议或重新授权。
	plan, ok := RoutePlanFromContext(child)
	require.True(t, ok)
	require.Equal(t, int64(7), plan.GroupID())
	source, ok := ClientProtocolFromContext(child)
	require.True(t, ok)
	require.Equal(t, protocol.ProtocolAnthropicMessages, source)
}
