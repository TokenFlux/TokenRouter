package account

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRefreshRuntimePublicationHonorsClearAndOtherVersions(t *testing.T) {
	gateway := NewRuntimeBlockState(time.Now)
	blocked := func(v *Record) bool {
		return gateway.Blocked(v.ID, func() string { return RefreshCredentialIdentity(v) })
	}
	value := func(token string) *Record {
		return &Record{LoadLocation: time.LoadLocation, ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": token}}
	}
	a, b, c := value("a"), value("b"), value("c")
	notice := func(v *Record) RefreshFailureNotice {
		n := FailureNotice(v)
		n.Until = time.Now().Add(time.Minute)
		return n
	}
	publishA, publishB := gateway.PrepareRefreshFailure(1), gateway.PrepareRefreshFailure(1)
	publishB(notice(b))
	publishA(notice(a))
	require.True(t, blocked(a))
	require.True(t, blocked(b))
	require.False(t, blocked(c))
	// 原账号级容量/额度阻断继续覆盖全部身份，不被凭据作用域削弱。
	gateway.BlockAccountScheduling(c, time.Now().Add(time.Hour), "429")
	require.True(t, blocked(c))
	gateway.ClearAccountSchedulingBlock(1)
	publishA(notice(a))
	publishB(notice(b))
	require.False(t, blocked(a))
	require.False(t, blocked(b))
	gateway.PrepareRefreshFailure(1)(notice(c))
	require.True(t, blocked(c))
	// 原 Grok 临时阻断的回滚不得抹掉独立的刷新身份阻断。
	publishAfterProbe := gateway.PrepareRefreshFailure(1)
	release := gateway.BlockRollback(1, time.Now().Add(time.Minute), "credential_probe")
	release()
	publishAfterProbe(notice(value("d")))
	require.True(t, blocked(value("d")))
	require.True(t, blocked(c))
	require.False(t, blocked(a))
}
