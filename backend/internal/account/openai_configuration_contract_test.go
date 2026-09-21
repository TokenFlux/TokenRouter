//go:build unit

package account_test

import (
	"time"

	"github.com/google/uuid"

	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/stretchr/testify/require"
)

// 通用账号导入复用 CreateAccount，因此创建构造器同时是导入持久化边界。
func TestBuildAccountForCreateNormalizesLegacyOpenAIConfigurationForCreateAndImport(t *testing.T) {
	input := &accountcore.CreateAccountInput{
		Name:     "legacy-openai",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "secret",
			"openai_capabilities": map[string]any{
				"chat_completions": true,
				"embeddings":       false,
			},
		},
	}
	extra := map[string]any{
		"openai_responses_mode":      "force_responses",
		"openai_responses_supported": false,
		"unrelated":                  map[string]any{"keep": true},
	}

	account, err := accountcore.BuildAccountForCreate(input, extra, accountcore.CreationOptions{Now: time.Now, LoadLocation: time.LoadLocation, NewSeed: uuid.NewString})

	require.NoError(t, err)
	require.Contains(t, account.UpstreamProtocols(), protocol.ProtocolOpenAIResponses)
	require.NotContains(t, account.UpstreamProtocols(), protocol.ProtocolOpenAIChatCompletions)
	require.NotContains(t, account.Extra, accountcore.ExtraKeyTextRouteMode)
	require.NotContains(t, account.Extra, "openai_responses_probe_status")
	require.Equal(t, false, account.Extra[accountcore.ExtraKeyResponsesContinuationSupported])
	require.Equal(t, map[string]any{"keep": true}, account.Extra["unrelated"])
	require.NotContains(t, account.Credentials, accountcore.LegacyOpenAICapabilitiesCredentialKey)
	require.NotContains(t, account.Extra, accountcore.LegacyOpenAIResponsesModeExtraKey)
	require.NotContains(t, account.Extra, "openai_responses_supported")
}

func TestNormalizeOpenAIAPIKeyConfigurationDefaultsAndExplicitEmpty(t *testing.T) {
	t.Run("缺失配置写入显式默认值", func(t *testing.T) {
		account := &accountcore.Record{Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}

		require.NoError(t, accountcore.NormalizeOpenAIAPIKeyConfiguration(account))
		require.Equal(t, []string{"text_generation", "embeddings"}, account.Credentials[accountcore.OpenAIWorkloadCapabilitiesCredentialKey])
		require.Equal(t, "preserve_client_protocol", account.Extra[accountcore.ExtraKeyTextRouteMode])
		require.NotContains(t, account.Extra, "openai_responses_probe_status")
		require.Equal(t, false, account.Extra[accountcore.ExtraKeyResponsesContinuationSupported])
	})

	t.Run("显式空能力集合保持为空", func(t *testing.T) {
		account := &accountcore.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.AccountTypeAPIKey,
			Credentials: map[string]any{
				accountcore.LegacyOpenAICapabilitiesCredentialKey: []any{},
			},
		}

		require.NoError(t, accountcore.NormalizeOpenAIAPIKeyConfiguration(account))
		require.Equal(t, []string{}, account.Credentials[accountcore.OpenAIWorkloadCapabilitiesCredentialKey])
		require.False(t, account.SupportsOpenAIEndpointCapability(accountcore.OpenAIEndpointCapabilityTextGeneration, nil))
		require.False(t, account.SupportsOpenAIEndpointCapability(accountcore.OpenAIEndpointCapabilityEmbeddings, nil))
	})
}

func TestNormalizeOpenAIAPIKeyConfigurationPatchSupportsLegacyBulkPayload(t *testing.T) {
	credentials := map[string]any{
		accountcore.LegacyOpenAICapabilitiesCredentialKey: []any{"chat_completions", "embeddings"},
	}
	extra := map[string]any{
		accountcore.LegacyOpenAIResponsesModeExtraKey: "auto",
		"openai_responses_supported":                  true,
	}

	require.NoError(t, accountcore.NormalizeOpenAIAPIKeyConfigurationPatch(credentials, extra))
	require.Equal(t, []string{"text_generation", "embeddings"}, credentials[accountcore.OpenAIWorkloadCapabilitiesCredentialKey])
	require.Equal(t, "preserve_client_protocol", extra[accountcore.ExtraKeyTextRouteMode])
	require.NotContains(t, extra, "openai_responses_probe_status")
	require.NotContains(t, credentials, accountcore.LegacyOpenAICapabilitiesCredentialKey)
	require.NotContains(t, extra, accountcore.LegacyOpenAIResponsesModeExtraKey)
	require.NotContains(t, extra, "openai_responses_supported")
}

func TestNormalizeOpenAIResponsesContinuationSupported(t *testing.T) {
	t.Run("explicit values are preserved", func(t *testing.T) {
		extra := map[string]any{
			accountcore.ExtraKeyResponsesContinuationSupported: true,
		}
		require.NoError(t, accountcore.NormalizeOpenAIAPIKeyConfigurationPatch(nil, extra))
		require.Equal(t, true, extra[accountcore.ExtraKeyResponsesContinuationSupported])

		extra[accountcore.ExtraKeyResponsesContinuationSupported] = false
		require.NoError(t, accountcore.NormalizeOpenAIAPIKeyConfigurationPatch(nil, extra))
		require.Equal(t, false, extra[accountcore.ExtraKeyResponsesContinuationSupported])
	})

	t.Run("null becomes false", func(t *testing.T) {
		extra := map[string]any{
			accountcore.ExtraKeyResponsesContinuationSupported: nil,
		}
		require.NoError(t, accountcore.NormalizeOpenAIAPIKeyConfigurationPatch(nil, extra))
		require.Equal(t, false, extra[accountcore.ExtraKeyResponsesContinuationSupported])
	})

	t.Run("invalid type is rejected", func(t *testing.T) {
		extra := map[string]any{
			accountcore.ExtraKeyResponsesContinuationSupported: "true",
		}
		err := accountcore.NormalizeOpenAIAPIKeyConfigurationPatch(nil, extra)
		require.Error(t, err)
		require.Contains(t, err.Error(), "OPENAI_RESPONSES_CONTINUATION_INVALID")
	})
}

func TestNormalizeOpenAITextRouteModeRejectsInvalidNewValue(t *testing.T) {
	extra := map[string]any{accountcore.ExtraKeyTextRouteMode: "auto"}

	err := accountcore.NormalizeOpenAIAPIKeyConfigurationPatch(nil, extra)

	require.Error(t, err)
}
