package plugin

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCallerGrantBindsPluginUserAndExpiry verifies that caller tickets retain their scope and expire at the exact boundary.
// Reconstructed signers accept unexpired tickets only when derived from the same persistent secret.
func TestCallerGrantBindsPluginUserAndExpiry(t *testing.T) {
	now := time.Unix(1800000000, 0)
	signer, err := NewGrants("persistent-test-secret", func() time.Time { return now })
	require.NoError(t, err)
	token, err := signer.Issue("test.plugin", 42)
	require.NoError(t, err)
	require.NoError(t, signer.Verify("test.plugin", 42, token))
	require.ErrorIs(t, signer.Verify("test.other", 42, token), ErrGrantInvalid)
	require.ErrorIs(t, signer.Verify("test.plugin", 43, token), ErrGrantInvalid)
	reconstructed, err := NewGrants("persistent-test-secret", func() time.Time { return now })
	require.NoError(t, err)
	require.NoError(t, reconstructed.Verify("test.plugin", 42, token))
	different, err := NewGrants("different-secret", func() time.Time { return now })
	require.NoError(t, err)
	require.ErrorIs(t, different.Verify("test.plugin", 42, token), ErrGrantInvalid)
	now = now.Add(299 * time.Second)
	require.NoError(t, signer.Verify("test.plugin", 42, token))
	now = now.Add(time.Second)
	require.ErrorIs(t, signer.Verify("test.plugin", 42, token), ErrGrantInvalid)
}

// TestCallerGrantRejectsTamperingAndRedeemCodesRemainStable checks malformed tokens and plugin-separated code derivation.
// Stable operation identity produces the same bounded code, while another plugin or replay key produces a different code.
func TestCallerGrantRejectsTamperingAndRedeemCodesRemainStable(t *testing.T) {
	signer, err := NewGrants("persistent-test-secret", nil)
	require.NoError(t, err)
	token, err := signer.Issue("test.plugin", 42)
	require.NoError(t, err)
	parts := strings.Split(token, ".")
	for _, invalid := range []string{"", strings.Repeat("x", 513), token + ".extra", "v2." + parts[1] + "." + parts[2], "v1.e30." + parts[2], "v1." + parts[1] + ".invalid"} {
		require.ErrorIs(t, signer.Verify("test.plugin", 42, invalid), ErrGrantInvalid)
	}
	code := signer.RedeemCode("test.plugin", "claim:one")
	require.Len(t, code, 26)
	require.Regexp(t, `^PL[A-Z2-7]{24}$`, code)
	require.Equal(t, code, signer.RedeemCode("test.plugin", "claim:one"))
	require.NotEqual(t, code, signer.RedeemCode("test.other", "claim:one"))
	require.NotEqual(t, code, signer.RedeemCode("test.plugin", "claim:two"))
}
