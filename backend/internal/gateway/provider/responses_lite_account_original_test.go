//go:build unit

package provider

import (
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeOpenAIResponsesLitePayloadForAccount_APIKeyOnlyDisablesParallelToolCalls(t *testing.T) {
	account := &accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}
	body := []byte(`{
		"model":"gpt-5.6-terra",
		"parallel_tool_calls":true,
		"reasoning":{"context":"current_turn"},
		"tools":[{"type":"web_search"}],
		"input":[{"type":"message","nonce":9007199254740993}]
	}`)

	updated, changed, err := NormalizeResponsesLiteForAccount(account, body)

	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(updated, "parallel_tool_calls").Bool())
	require.Equal(t, "current_turn", gjson.GetBytes(updated, "reasoning.context").String())
	require.Equal(t, "web_search", gjson.GetBytes(updated, "tools.0.type").String())
	require.Equal(t, "9007199254740993", gjson.GetBytes(updated, "input.0.nonce").Raw)
}

func TestNormalizeOpenAIResponsesLitePayloadForAccount_IgnoresNonOpenAIAccount(t *testing.T) {
	account := &accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey}
	body := []byte(`{"parallel_tool_calls":true}`)

	updated, changed, err := NormalizeResponsesLiteForAccount(account, body)

	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, updated)
}

func TestNormalizeOpenAIResponsesLitePayloadForAccount_RejectsNullAPIKeyBody(t *testing.T) {
	account := &accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey}
	body := []byte(`null`)

	updated, changed, err := NormalizeResponsesLiteForAccount(account, body)

	require.ErrorContains(t, err, "request body must be a JSON object")
	require.False(t, changed)
	require.Equal(t, body, updated)
}
