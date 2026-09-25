//go:build unit

package failover

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShouldStopOpenAIOAuth429Failover_AfterBoundedFullWindows(t *testing.T) {

	account := OAuth429Account{OpenAI: true}
	apiKeyAccount := OAuth429Account{}
	var state OAuth429State

	require.False(t, StopOAuth429(account, 429, 1, &state))

	require.False(t, StopOAuth429(account, 429, 1, &state))
	require.False(t, StopOAuth429(account, 429, 2, &state))
	require.True(t, StopOAuth429(account, 429, 3, &state))
	require.False(t, StopOAuth429(apiKeyAccount, 429, 1, &state))
	require.False(t, StopOAuth429(account, 500, 1, &state))
	require.False(t, StopOAuth429(account, 429, 0, &state))
}

func TestShouldStopOpenAIOAuth429Failover_TracksOneGrokFollowupAttempt(t *testing.T) {

	account := OAuth429Account{Grok: true}
	apiKeyAccount := OAuth429Account{}

	t.Run("429 then 500 stops after one followup", func(t *testing.T) {
		var state OAuth429State
		require.False(t, StopOAuth429(account, 429, 1, &state))
		require.True(t, StopOAuth429(account, 500, 2, &state))
	})

	t.Run("500 then 429 still allows one followup", func(t *testing.T) {
		var state OAuth429State
		require.False(t, StopOAuth429(account, 500, 1, &state))
		require.False(t, StopOAuth429(account, 429, 2, &state))
		require.True(t, StopOAuth429(account, 502, 3, &state))
	})

	t.Run("OAuth 429 then API-key failure consumes the same followup", func(t *testing.T) {
		var state OAuth429State
		require.False(t, StopOAuth429(account, 429, 1, &state))
		require.True(t, StopOAuth429(apiKeyAccount, 500, 2, &state))
	})

	var state OAuth429State
	require.False(t, StopOAuth429(account, 429, 0, &state))
	require.False(t, StopOAuth429(apiKeyAccount, 429, 2, &state))
}
