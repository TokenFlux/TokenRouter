package identity

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOIDCSyntheticEmailStableAndDistinct(t *testing.T) {
	k1 := OIDCIdentityKey("https://issuer.example.com", "subject-a")
	k2 := OIDCIdentityKey("https://issuer.example.com", "subject-b")

	e1 := OIDCSyntheticEmailFromIdentityKey(k1)
	e1Again := OIDCSyntheticEmailFromIdentityKey(k1)
	e2 := OIDCSyntheticEmailFromIdentityKey(k2)

	require.Equal(t, e1, e1Again)
	require.NotEqual(t, e1, e2)
	require.Contains(t, e1, "@oidc-connect.invalid")
}
