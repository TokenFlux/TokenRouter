//go:build unit

package account

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestNormalizeAccountConcurrencyDefaultsInvalidGrokOAuthToOne(t *testing.T) {
	require.Equal(t, 1, NormalizeAccountConcurrency(capability.PlatformGrok, capability.AccountTypeOAuth, 0))
	require.Equal(t, 1, NormalizeAccountConcurrency(capability.PlatformGrok, capability.AccountTypeOAuth, -5))
}

func TestNormalizeAccountConcurrencyPreservesExplicitValues(t *testing.T) {
	require.Equal(t, 50, NormalizeAccountConcurrency(capability.PlatformGrok, capability.AccountTypeOAuth, 50))
	require.Equal(t, 2, NormalizeAccountConcurrency(capability.PlatformOpenAI, capability.AccountTypeOAuth, 2))
	require.Equal(t, 2, NormalizeAccountConcurrency(capability.PlatformGrok, capability.AccountTypeAPIKey, 2))
}
