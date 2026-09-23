package service

import (
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 恢复等待期间出现新阻断时，旧恢复不得清除新状态；显式清理仍使用原路径。
func TestManagedRecoveryFencePreservesNewRuntimeBlock(t *testing.T) {
	s := withSchedulerParametersForTest(&OpenAIGatewayService{})
	a := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 72, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}}
	s.BlockAccountScheduling(a, time.Now().Add(time.Minute), "first")
	fence := s.ManagedRecoveryFence(a.Record.ID)
	s.BlockAccountScheduling(a, time.Now().Add(2*time.Minute), "new")
	require.False(t, s.ClearAccountSchedulingBlockIfFence(a.Record.ID, fence))
	require.True(t, s.isOpenAIAccountRuntimeBlocked(a))
	require.True(t, s.ClearAccountSchedulingBlockIfFence(a.Record.ID, s.ManagedRecoveryFence(a.Record.ID)))
	require.False(t, s.isOpenAIAccountRuntimeBlocked(a))
	require.False(t, s.ClearAccountSchedulingBlockIfFence(a.Record.ID, fence))
}
