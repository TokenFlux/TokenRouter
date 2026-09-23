package account

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestOpenAIGatewayServiceGetAccessTokenSetupToken(t *testing.T) {
	svc := &OpenAIExecutionCredentials{OpenAI: func(context.Context, *Record) (string, error) {
		t.Fatal("setup-token 不应进入刷新源")
		return "", nil
	}}
	account := &Record{
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeSetupToken,
		Credentials: map[string]any{"access_token": "setup-token-value"},
	}

	token, tokenType, err := svc.Resolve(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "setup-token-value", token)
	require.Equal(t, "oauth", tokenType)

	delete(account.Credentials, "access_token")
	_, _, err = svc.Resolve(context.Background(), account)
	require.EqualError(t, err, "access_token not found in credentials")

	for _, platform := range []string{capability.PlatformAnthropic, capability.PlatformGrok} {
		foreign := &Record{
			Platform:    platform,
			Type:        capability.AccountTypeSetupToken,
			Credentials: map[string]any{"access_token": "foreign-token"},
		}
		_, _, err = svc.Resolve(context.Background(), foreign)
		require.EqualError(t, err, "unsupported account type: setup-token")
	}
}
