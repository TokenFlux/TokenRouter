package routing

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 原回退循环断言直接归属分组规则实现。
func TestGatewayService_ResolveGatewayGroup_DetectsFallbackCycle(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10)
	fallbackID := int64(11)

	group := &Group{
		ID:              groupID,
		Platform:        capability.PlatformAnthropic,
		Status:          StatusActive,
		ClaudeCodeOnly:  true,
		FallbackGroupID: &fallbackID,
	}
	fallbackGroup := &Group{
		ID:              fallbackID,
		Platform:        capability.PlatformAnthropic,
		Status:          StatusActive,
		ClaudeCodeOnly:  true,
		FallbackGroupID: &groupID,
	}

	groups := map[int64]*Group{groupID: group, fallbackID: fallbackGroup}
	gotGroup, gotID, err := ResolveClientGroup(ctx, &groupID, func(_ context.Context, id int64) (*Group, error) { return groups[id], nil }, func(context.Context) bool { return false }, ClientGroupPolicy{})
	require.Error(t, err)
	require.Nil(t, gotGroup)
	require.Nil(t, gotID)
	require.Contains(t, err.Error(), "fallback group cycle")
}
