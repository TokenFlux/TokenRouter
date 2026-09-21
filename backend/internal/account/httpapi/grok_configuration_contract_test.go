//go:build unit

package httpapi_test

import (
	"net/http"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	"github.com/stretchr/testify/require"
)

func TestNormalizeGrokMediaEligibilityExtra(t *testing.T) {
	t.Run("boolean override is accepted", func(t *testing.T) {
		extra, err := accountcore.NormalizeGrokMediaEligibilityExtra(capability.PlatformGrok, map[string]any{accountcore.GrokMediaEligibleExtraKey: false})

		require.NoError(t, err)
		require.Equal(t, false, extra[accountcore.GrokMediaEligibleExtraKey])
	})

	t.Run("null clears override", func(t *testing.T) {
		extra, err := accountcore.NormalizeGrokMediaEligibilityExtra(capability.PlatformGrok, map[string]any{accountcore.GrokMediaEligibleExtraKey: nil})

		require.NoError(t, err)
		require.NotContains(t, extra, accountcore.GrokMediaEligibleExtraKey)
	})

	t.Run("malformed override is rejected", func(t *testing.T) {
		_, err := accountcore.NormalizeGrokMediaEligibilityExtra(capability.PlatformGrok, map[string]any{accountcore.GrokMediaEligibleExtraKey: "false"})

		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))
	})

	t.Run("other platforms ignore provider owned value", func(t *testing.T) {
		extra := map[string]any{accountcore.GrokMediaEligibleExtraKey: "provider-owned"}
		normalized, err := accountcore.NormalizeGrokMediaEligibilityExtra(capability.PlatformOpenAI, extra)

		require.NoError(t, err)
		require.Equal(t, extra, normalized)
	})
}

func TestNormalizeGrokMediaEligibilityUpdateExtra(t *testing.T) {
	account := &accountcore.Record{Platform: capability.PlatformGrok, Extra: map[string]any{accountcore.GrokMediaEligibleExtraKey: false}}

	t.Run("omitted override preserves current value", func(t *testing.T) {
		input := &accountcore.UpdateAccountInput{Extra: map[string]any{"quota_used": float64(1)}}
		normalized, err := accountcore.NormalizeGrokMediaEligibilityUpdateExtra(account, input, map[string]any{"quota_used": float64(1)})

		require.NoError(t, err)
		require.Equal(t, false, normalized[accountcore.GrokMediaEligibleExtraKey])
	})

	t.Run("null removes current override", func(t *testing.T) {
		input := &accountcore.UpdateAccountInput{Extra: map[string]any{accountcore.GrokMediaEligibleExtraKey: nil}}
		normalized, err := accountcore.NormalizeGrokMediaEligibilityUpdateExtra(account, input, map[string]any{accountcore.GrokMediaEligibleExtraKey: nil})

		require.NoError(t, err)
		require.NotContains(t, normalized, accountcore.GrokMediaEligibleExtraKey)
		require.Contains(t, input.Extra, accountcore.GrokMediaEligibleExtraKey)
	})

	t.Run("provided boolean replaces current override", func(t *testing.T) {
		input := &accountcore.UpdateAccountInput{Extra: map[string]any{accountcore.GrokMediaEligibleExtraKey: true}}
		normalized, err := accountcore.NormalizeGrokMediaEligibilityUpdateExtra(account, input, map[string]any{accountcore.GrokMediaEligibleExtraKey: true})

		require.NoError(t, err)
		require.Equal(t, true, normalized[accountcore.GrokMediaEligibleExtraKey])
	})

	t.Run("malformed override is rejected on update", func(t *testing.T) {
		input := &accountcore.UpdateAccountInput{Extra: map[string]any{accountcore.GrokMediaEligibleExtraKey: "false"}}
		_, err := accountcore.NormalizeGrokMediaEligibilityUpdateExtra(account, input, nil)

		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))
	})

	t.Run("non grok update is unchanged", func(t *testing.T) {
		input := &accountcore.UpdateAccountInput{Extra: map[string]any{accountcore.GrokMediaEligibleExtraKey: "provider-owned"}}
		normalized := map[string]any{accountcore.GrokMediaEligibleExtraKey: "provider-owned"}
		got, err := accountcore.NormalizeGrokMediaEligibilityUpdateExtra(&accountcore.Record{Platform: capability.PlatformOpenAI}, input, normalized)

		require.NoError(t, err)
		require.Equal(t, normalized, got)
	})
}
