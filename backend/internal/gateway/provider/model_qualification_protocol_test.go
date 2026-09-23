package provider

import (
	"net/http"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/stretchr/testify/require"
)

// 目录、保存与实际路线共享同一矩阵，显式空集合和平台拒绝均不能被默认值覆盖。
func TestProtocolNativeMatrixAndSave(t *testing.T) {
	for _, tc := range []struct {
		platform, kind, auth string
		count                int
	}{
		{capability.PlatformAnthropic, capability.AccountTypeAPIKey, "", 1}, {capability.PlatformAnthropic, capability.AccountTypeBedrock, "", 1},
		{capability.PlatformOpenAI, capability.AccountTypeAPIKey, "", 8}, {capability.PlatformOpenAI, capability.AccountTypeOAuth, "", 5},
		{capability.PlatformOpenAI, capability.AccountTypeOAuth, accountcore.OpenAIAuthModePersonalAccessToken, 3}, {capability.PlatformOpenAI, capability.AccountTypeOAuth, accountcore.OpenAIAuthModeAgentIdentity, 4},
		{capability.PlatformDeepseek, capability.AccountTypeAPIKey, "", 3}, {capability.PlatformKimi, capability.AccountTypeAPIKey, "", 3}, {capability.PlatformZhipu, capability.AccountTypeAPIKey, "", 2},
		{capability.PlatformGemini, capability.AccountTypeAPIKey, "", 2}, {capability.PlatformGemini, capability.AccountTypeServiceAccount, "", 2}, {capability.PlatformGemini, capability.AccountTypeOAuth, "", 1},
		{capability.PlatformAntigravity, capability.AccountTypeOAuth, "", 1}, {capability.PlatformAntigravity, capability.AccountTypeAPIKey, "", 0},
		{capability.PlatformGrok, capability.AccountTypeAPIKey, "", 11}, {capability.PlatformGrok, capability.AccountTypeOAuth, "", 11}, {capability.PlatformQoder, capability.AccountTypeCosy, "", 1},
	} {
		t.Run(tc.platform+"/"+tc.kind+"/"+tc.auth, func(t *testing.T) {
			account := &accountcore.Record{Platform: tc.platform, Type: tc.kind, Credentials: map[string]any{"auth_mode": tc.auth}}
			options := account.NativeProtocolOptions()
			require.Len(t, options, tc.count)
			for _, protocol := range options {
				account.Credentials[accountcore.UpstreamProtocolsKey] = []string{string(protocol)}
				require.NoError(t, accountcore.NormalizeAccountProtocols(account))
				require.Equal(t, []protocolcore.ProtocolID{protocol}, account.UpstreamProtocols())
				target, ok := (ModelPolicy{Record: account}).ProtocolRoute(nil, protocol)
				require.True(t, ok)
				require.Equal(t, protocol, target)
			}
			account.Credentials[accountcore.UpstreamProtocolsKey] = []string{}
			require.NoError(t, accountcore.NormalizeAccountProtocols(account))
			require.Empty(t, account.UpstreamProtocols())
			account.Credentials[accountcore.UpstreamProtocolsKey] = []string{"unknown"}
			require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(accountcore.NormalizeAccountProtocols(account)))
		})
	}
}
