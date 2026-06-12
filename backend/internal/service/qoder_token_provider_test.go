package service

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	"github.com/stretchr/testify/require"
)

func TestQoderTokenProviderBuildsAndCachesDirectSession(t *testing.T) {
	provider := NewQoderTokenProvider()
	account := &Account{
		ID:       101,
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "dt-token",
			"machine_id":           "machine-1",
		},
	}

	session1, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.NotNil(t, session1)
	require.Equal(t, "dt-token", session1.Identity.SecurityOauthToken)
	require.Equal(t, "machine-1", session1.Machine.MachineID)

	session2, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.Same(t, session1, session2, "session should be cached per account credentials")
}

func TestQoderTokenProviderRejectsUnsupportedCredentialShape(t *testing.T) {
	provider := NewQoderTokenProvider()
	account := &Account{
		ID:          102,
		Platform:    PlatformQoder,
		Type:        AccountTypeCosy,
		Credentials: map[string]any{"security_oauth_token": "dt-token"},
	}

	_, err := provider.GetSession(context.Background(), account)
	require.ErrorContains(t, err, "machine_id")
}

func TestQoderTokenProviderSupportsInjectedPATExchange(t *testing.T) {
	calls := 0
	provider := NewQoderTokenProvider()
	provider.exchangePAT = func(_ context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		calls++
		require.Equal(t, "pat-123", pat)
		require.NotEmpty(t, machine.MachineID)
		return &qoder.AuthIdentity{
			Name:               "PAT User",
			UID:                "uid-1",
			AID:                "uid-1",
			UserType:           "personal_standard",
			SecurityOauthToken: "dt-from-pat",
			RefreshToken:       "rt-from-pat",
		}, nil
	}

	account := &Account{
		ID:          103,
		Platform:    PlatformQoder,
		Type:        AccountTypeCosy,
		Credentials: map[string]any{"pat": "pat-123"},
	}

	session1, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "dt-from-pat", session1.Identity.SecurityOauthToken)

	session2, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.Same(t, session1, session2)
	require.Equal(t, 1, calls, "PAT exchange should not run after cache hit")
}
