package app

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/stretchr/testify/require"
)

// 原构造器身份断言直接验证组合根，并确认运行时读取同一会话缓存。
func TestNewQoderGatewayServiceUsesInjectedRefreshAPI(t *testing.T) {
	tokens := provideQoderTokens(nil, nil)
	t.Cleanup(func() { require.NoError(t, tokens.StopContext(context.Background())) })
	coordinator := &account.OAuthRefreshAPI{}
	refresh := provideQoderRequestRefresh(nil, tokens, coordinator, nil, nil)
	runtime := provideQoderRuntime(tokens, nil, nil, nil)
	require.Same(t, coordinator, refresh.Coordinator)
	require.Same(t, tokens, refresh.Tokens)
	value := &account.Record{ID: 1, Platform: account.PlatformQoder, Type: account.AccountTypeCosy, Credentials: map[string]any{}}
	session := &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "fixture"}}
	tokens.Core.Sessions[value.ID] = account.QoderSessionCacheEntry[*qoder.SessionContext]{CredentialsHash: account.QoderCredentialsHash(value.Credentials), Session: session}
	got, err := runtime.Target(qoder.RequestMetadata{}, value).Session(context.Background())
	require.NoError(t, err)
	require.Same(t, session, got)
}
