package account_test

import (
	"testing"
	"time"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 恢复等待期间出现新阻断时，旧恢复不得清除新状态；显式清理仍使用原路径。
func TestManagedRecoveryFencePreservesNewRuntimeBlock(t *testing.T) {
	s := acctcore.NewRuntimeBlockState(time.Now)
	a := &acctcore.Record{LoadLocation: time.LoadLocation, ID: 72, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth}
	s.BlockAccountScheduling(a, time.Now().Add(time.Minute), "first")
	fence := s.ManagedRecoveryFence(a.ID)
	s.BlockAccountScheduling(a, time.Now().Add(2*time.Minute), "new")
	require.False(t, s.ClearAccountSchedulingBlockIfFence(a.ID, fence))
	require.True(t, s.Blocked(a.ID, func() string { return acctcore.RefreshCredentialIdentity(a) }))
	require.True(t, s.ClearAccountSchedulingBlockIfFence(a.ID, s.ManagedRecoveryFence(a.ID)))
	require.False(t, s.Blocked(a.ID, func() string { return acctcore.RefreshCredentialIdentity(a) }))
	require.False(t, s.ClearAccountSchedulingBlockIfFence(a.ID, fence))
}
