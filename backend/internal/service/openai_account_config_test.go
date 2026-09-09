//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
)

// 通用账号导入复用 CreateAccount，因此创建构造器同时是导入持久化边界。
func TestBuildAccountForCreateNormalizesLegacyOpenAIConfigurationForCreateAndImport(t *testing.T) {
	input := &CreateAccountInput{
		Name:     "legacy-openai",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
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

	account, err := buildAccountForCreate(input, extra)

	require.NoError(t, err)
	require.Contains(t, account.UpstreamProtocols(), GroupClientProtocolOpenAIResponses)
	require.NotContains(t, account.UpstreamProtocols(), GroupClientProtocolOpenAIChatCompletions)
	require.NotContains(t, account.Extra, openai_compat.ExtraKeyTextRouteMode)
	require.NotContains(t, account.Extra, "openai_responses_probe_status")
	require.Equal(t, false, account.Extra[openai_compat.ExtraKeyResponsesContinuationSupported])
	require.Equal(t, map[string]any{"keep": true}, account.Extra["unrelated"])
	require.NotContains(t, account.Credentials, legacyOpenAICapabilitiesCredentialKey)
	require.NotContains(t, account.Extra, legacyOpenAIResponsesModeExtraKey)
	require.NotContains(t, account.Extra, "openai_responses_supported")
}

func TestNormalizeOpenAIAPIKeyConfigurationDefaultsAndExplicitEmpty(t *testing.T) {
	t.Run("缺失配置写入显式默认值", func(t *testing.T) {
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

		require.NoError(t, normalizeOpenAIAPIKeyConfiguration(account))
		require.Equal(t, []string{"text_generation", "embeddings"}, account.Credentials[openAIWorkloadCapabilitiesCredentialKey])
		require.Equal(t, "preserve_client_protocol", account.Extra[openai_compat.ExtraKeyTextRouteMode])
		require.NotContains(t, account.Extra, "openai_responses_probe_status")
		require.Equal(t, false, account.Extra[openai_compat.ExtraKeyResponsesContinuationSupported])
	})

	t.Run("显式空能力集合保持为空", func(t *testing.T) {
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				legacyOpenAICapabilitiesCredentialKey: []any{},
			},
		}

		require.NoError(t, normalizeOpenAIAPIKeyConfiguration(account))
		require.Equal(t, []string{}, account.Credentials[openAIWorkloadCapabilitiesCredentialKey])
		require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityTextGeneration))
		require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityEmbeddings))
	})
}

func TestNormalizeOpenAIAPIKeyConfigurationPatchSupportsLegacyBulkPayload(t *testing.T) {
	credentials := map[string]any{
		legacyOpenAICapabilitiesCredentialKey: []any{"chat_completions", "embeddings"},
	}
	extra := map[string]any{
		legacyOpenAIResponsesModeExtraKey: "auto",
		"openai_responses_supported":      true,
	}

	require.NoError(t, normalizeOpenAIAPIKeyConfigurationPatch(credentials, extra))
	require.Equal(t, []string{"text_generation", "embeddings"}, credentials[openAIWorkloadCapabilitiesCredentialKey])
	require.Equal(t, "preserve_client_protocol", extra[openai_compat.ExtraKeyTextRouteMode])
	require.NotContains(t, extra, "openai_responses_probe_status")
	require.NotContains(t, credentials, legacyOpenAICapabilitiesCredentialKey)
	require.NotContains(t, extra, legacyOpenAIResponsesModeExtraKey)
	require.NotContains(t, extra, "openai_responses_supported")
}

func TestNormalizeOpenAIResponsesContinuationSupported(t *testing.T) {
	t.Run("explicit values are preserved", func(t *testing.T) {
		extra := map[string]any{
			openai_compat.ExtraKeyResponsesContinuationSupported: true,
		}
		require.NoError(t, normalizeOpenAIAPIKeyConfigurationPatch(nil, extra))
		require.Equal(t, true, extra[openai_compat.ExtraKeyResponsesContinuationSupported])

		extra[openai_compat.ExtraKeyResponsesContinuationSupported] = false
		require.NoError(t, normalizeOpenAIAPIKeyConfigurationPatch(nil, extra))
		require.Equal(t, false, extra[openai_compat.ExtraKeyResponsesContinuationSupported])
	})

	t.Run("null becomes false", func(t *testing.T) {
		extra := map[string]any{
			openai_compat.ExtraKeyResponsesContinuationSupported: nil,
		}
		require.NoError(t, normalizeOpenAIAPIKeyConfigurationPatch(nil, extra))
		require.Equal(t, false, extra[openai_compat.ExtraKeyResponsesContinuationSupported])
	})

	t.Run("invalid type is rejected", func(t *testing.T) {
		extra := map[string]any{
			openai_compat.ExtraKeyResponsesContinuationSupported: "true",
		}
		err := normalizeOpenAIAPIKeyConfigurationPatch(nil, extra)
		require.Error(t, err)
		require.Contains(t, err.Error(), "OPENAI_RESPONSES_CONTINUATION_INVALID")
	})
}

func TestNormalizeOpenAITextRouteModeRejectsInvalidNewValue(t *testing.T) {
	extra := map[string]any{openai_compat.ExtraKeyTextRouteMode: "auto"}

	err := normalizeOpenAIAPIKeyConfigurationPatch(nil, extra)

	require.Error(t, err)
}

func TestUpdateAccountLegacyPatchOverridesEchoedNewShape(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	account := &Account{
		Name:     "openai-account",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":                               "secret",
			openAIWorkloadCapabilitiesCredentialKey: []any{"embeddings"},
		},
		Extra: map[string]any{
			openai_compat.ExtraKeyTextRouteMode:                  "preserve_client_protocol",
			"openai_responses_probe_status":                      "supported",
			openai_compat.ExtraKeyResponsesContinuationSupported: true,
		},
	}
	require.NoError(t, repo.Create(ctx, account))
	svc := &adminServiceImpl{accountRepo: repo}

	updated, err := svc.UpdateAccount(ctx, account.ID, &UpdateAccountInput{
		Credentials: map[string]any{
			legacyOpenAICapabilitiesCredentialKey: []any{"chat_completions"},
		},
		Extra: map[string]any{
			openai_compat.ExtraKeyTextRouteMode: "force_responses",
			legacyOpenAIResponsesModeExtraKey:   "force_chat_completions",
			"openai_responses_probe_status":     "supported",
			"openai_responses_supported":        false,
		},
	})

	require.NoError(t, err)
	require.Contains(t, updated.UpstreamProtocols(), GroupClientProtocolOpenAIChatCompletions)
	require.NotContains(t, updated.UpstreamProtocols(), GroupClientProtocolOpenAIResponses)
	require.NotContains(t, updated.Credentials, legacyOpenAICapabilitiesCredentialKey)
	require.NotContains(t, updated.Extra, openai_compat.ExtraKeyTextRouteMode)
	require.NotContains(t, updated.Extra, "openai_responses_probe_status")
	require.Equal(t, true, updated.Extra[openai_compat.ExtraKeyResponsesContinuationSupported])
	require.NotContains(t, updated.Extra, legacyOpenAIResponsesModeExtraKey)
	require.NotContains(t, updated.Extra, "openai_responses_supported")
}
