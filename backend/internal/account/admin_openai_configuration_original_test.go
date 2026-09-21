//go:build unit

package account_test

import (
	"context"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestUpdateAccountLegacyPatchOverridesEchoedNewShape(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	account := &accountcore.Record{
		Name:     "openai-account",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "secret",
			accountcore.OpenAIWorkloadCapabilitiesCredentialKey: []any{"embeddings"},
		},
		Extra: map[string]any{
			accountcore.ExtraKeyTextRouteMode:                  "preserve_client_protocol",
			"openai_responses_probe_status":                    "supported",
			accountcore.ExtraKeyResponsesContinuationSupported: true,
		},
	}
	require.NoError(t, repo.Create(ctx, account))
	svc := newOriginalAccountEditor(repo)

	updated, err := svc.UpdateAccount(ctx, account.ID, &accountcore.UpdateAccountInput{
		Credentials: map[string]any{
			accountcore.LegacyOpenAICapabilitiesCredentialKey: []any{"chat_completions"},
		},
		Extra: map[string]any{
			accountcore.ExtraKeyTextRouteMode:             "force_responses",
			accountcore.LegacyOpenAIResponsesModeExtraKey: "force_chat_completions",
			"openai_responses_probe_status":               "supported",
			"openai_responses_supported":                  false,
		},
	})

	require.NoError(t, err)
	require.Contains(t, updated.UpstreamProtocols(), protocol.ProtocolOpenAIChatCompletions)
	require.NotContains(t, updated.UpstreamProtocols(), protocol.ProtocolOpenAIResponses)
	require.NotContains(t, updated.Credentials, accountcore.LegacyOpenAICapabilitiesCredentialKey)
	require.NotContains(t, updated.Extra, accountcore.ExtraKeyTextRouteMode)
	require.NotContains(t, updated.Extra, "openai_responses_probe_status")
	require.Equal(t, true, updated.Extra[accountcore.ExtraKeyResponsesContinuationSupported])
	require.NotContains(t, updated.Extra, accountcore.LegacyOpenAIResponsesModeExtraKey)
	require.NotContains(t, updated.Extra, "openai_responses_supported")
}
