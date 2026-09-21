//go:build unit

package account_test

import (
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

func TestAccountGrokNeedsReauth(t *testing.T) {
	require.False(t, accountcore.GrokNeedsReauth(nil))
	require.True(t, accountcore.GrokNeedsReauth(&accountcore.Record{
		Extra: map[string]any{"grok_needs_reauth": true},
	}))
	require.True(t, accountcore.GrokNeedsReauth(&accountcore.Record{
		Status:       accountcore.StatusError,
		ErrorMessage: "Grok spending limit reached; reauthorize or wait for billing reset",
	}))
}
