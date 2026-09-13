package service

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestRefreshRuntimePublicationHonorsClearAndOtherVersions(t *testing.T) {
	gateway := &OpenAIGatewayService{}
	value := func(token string) *Account {
		return &Account{ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": token}}
	}
	a, b, c := value("a"), value("b"), value("c")
	notice := func(v *Account) account.RefreshFailureNotice {
		n := account.FailureNotice(AccountRecordView(v))
		n.Until = time.Now().Add(time.Minute)
		return n
	}
	publishA, publishB := gateway.PrepareRefreshFailure(1), gateway.PrepareRefreshFailure(1)
	publishB(notice(b))
	publishA(notice(a))
	require.True(t, gateway.isOpenAIAccountRuntimeBlocked(a))
	require.True(t, gateway.isOpenAIAccountRuntimeBlocked(b))
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(c))
	// 原账号级容量/额度阻断继续覆盖全部身份，不被凭据作用域削弱。
	gateway.BlockAccountScheduling(c, time.Now().Add(time.Hour), "429")
	require.True(t, gateway.isOpenAIAccountRuntimeBlocked(c))
	gateway.ClearAccountSchedulingBlock(1)
	publishA(notice(a))
	publishB(notice(b))
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(a))
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(b))
	gateway.PrepareRefreshFailure(1)(notice(c))
	require.True(t, gateway.isOpenAIAccountRuntimeBlocked(c))
	// 原 Grok 临时阻断的回滚不得抹掉独立的刷新身份阻断。
	publishAfterProbe := gateway.PrepareRefreshFailure(1)
	release := gateway.blockGrokCredentialRuntime(&Account{ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth}, time.Now().Add(time.Minute), "credential_probe")
	release()
	publishAfterProbe(notice(value("d")))
	require.True(t, gateway.isOpenAIAccountRuntimeBlocked(value("d")))
	require.True(t, gateway.isOpenAIAccountRuntimeBlocked(c))
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(a))
}

// 仅为原 HEAD 对照测试保留旧调用形状，生产用例显式在写入前准备发布。
func (s *TokenRefreshService) notifyAccountSchedulingBlocked(value *Account, until time.Time, reason string) {
	s.prepareRefreshFailure(value)(until, reason)
}
