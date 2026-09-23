package account

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 原 Messages 分支与 OpenAI 专有入口并不等价，以下矩阵保留各自类型与错误边界。
func TestMessageCredentialsPreserveStoredTypes(t *testing.T) {
	tests := []struct {
		name, platform, kind, token, auth, errorText string
		credentials                                  map[string]any
	}{
		{name: "api key preserves whitespace", platform: capability.PlatformAnthropic, kind: capability.AccountTypeAPIKey, credentials: map[string]any{"api_key": " key "}, token: " key ", auth: "apikey"},
		{name: "missing api key", platform: capability.PlatformAnthropic, kind: capability.AccountTypeAPIKey, errorText: "api_key not found in credentials"},
		{name: "oauth fallback", platform: capability.PlatformAnthropic, kind: capability.AccountTypeOAuth, credentials: map[string]any{"access_token": "stored"}, token: "stored", auth: "oauth"},
		{name: "setup does not refresh", platform: capability.PlatformAnthropic, kind: capability.AccountTypeSetupToken, credentials: map[string]any{"access_token": "setup"}, token: "setup", auth: "oauth"},
		{name: "grok stored", platform: capability.PlatformGrok, kind: capability.AccountTypeOAuth, credentials: map[string]any{"access_token": "grok"}, token: "grok", auth: "oauth"},
		{name: "grok missing", platform: capability.PlatformGrok, kind: capability.AccountTypeOAuth, errorText: "grok access_token not found in credentials"},
		{name: "bedrock separate signing", platform: capability.PlatformAnthropic, kind: capability.AccountTypeBedrock, auth: "bedrock"},
		{name: "vertex source missing", platform: capability.PlatformAnthropic, kind: capability.AccountTypeServiceAccount, errorText: "claude token provider not configured"},
		{name: "foreign service account", platform: capability.PlatformGemini, kind: capability.AccountTypeServiceAccount, errorText: "unsupported service account platform: gemini"},
		{name: "unknown type", platform: capability.PlatformAnthropic, kind: "unknown", errorText: "unsupported account type: unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var source *MessageCredentialSource
			token, auth, err := source.Resolve(context.Background(), &Record{Platform: tt.platform, Type: tt.kind, Credentials: tt.credentials})
			if tt.errorText != "" {
				require.EqualError(t, err, tt.errorText)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.token, token)
			require.Equal(t, tt.auth, auth)
		})
	}
}
func TestMessageCredentialsReuseConfiguredSourceOnlyForOriginalBranches(t *testing.T) {
	for _, kind := range []string{capability.AccountTypeOAuth, capability.AccountTypeServiceAccount, capability.AccountTypeSetupToken} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			value := &Record{ID: 8, Platform: capability.PlatformAnthropic, Type: kind, Credentials: map[string]any{"access_token": "stored"}}
			source := &MessageCredentialSource{Claude: func(ctx context.Context, read *Record) (string, error) {
				calls++
				require.Same(t, value, read)
				require.NoError(t, ctx.Err())
				return "loaded", nil
			}}
			token, auth, err := source.Resolve(context.Background(), value)
			require.NoError(t, err)
			if kind == capability.AccountTypeSetupToken {
				require.Zero(t, calls)
				require.Equal(t, "stored", token)
			} else {
				require.Equal(t, 1, calls)
				require.Equal(t, "loaded", token)
			}
			want := "oauth"
			if kind == capability.AccountTypeServiceAccount {
				want = "service_account"
			}
			require.Equal(t, want, auth)
		})
	}
	sentinel := errors.New("source failed")
	source := &MessageCredentialSource{Claude: func(ctx context.Context, _ *Record) (string, error) { return "", sentinel }}
	token, auth, err := source.Resolve(context.Background(), &Record{Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth})
	require.ErrorIs(t, err, sentinel)
	require.Empty(t, token)
	require.Empty(t, auth)
}
