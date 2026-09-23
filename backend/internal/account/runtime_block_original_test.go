//go:build unit

package account

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 原停调延长与清理断言迁至状态所有者，直接检查私有缓存。
func TestOpenAIRuntimeBlock_DoesNotShortenExistingBlock(t *testing.T) {
	svc := NewRuntimeBlockState(time.Now)
	account := &Record{ID: 46, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	longUntil := time.Now().Add(10 * time.Minute)

	svc.Block(account.ID, longUntil, "oauth_401")
	svc.Block(account.ID, time.Time{}, "upstream_disable")

	value, ok := svc.until.Load(account.ID)
	require.True(t, ok)
	actualUntil, ok := value.(time.Time)
	require.True(t, ok)
	require.WithinDuration(t, longUntil, actualUntil, time.Second)
}

func TestOpenAIRuntimeBlock_ClearAccountSchedulingBlock(t *testing.T) {
	svc := NewRuntimeBlockState(time.Now)
	account := &Record{ID: 47, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	svc.Block(account.ID, time.Now().Add(time.Minute), "429")
	require.True(t, svc.Blocked(account.ID, func() string { return RefreshCredentialIdentity(account) }))

	svc.ClearAccountSchedulingBlock(account.ID)
	require.False(t, svc.Blocked(account.ID, func() string { return RefreshCredentialIdentity(account) }))
}
