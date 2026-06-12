package service

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	"github.com/stretchr/testify/require"
)

func TestValidateQoderCosyCredentialsAcceptsDirectToken(t *testing.T) {
	account := &Account{
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "dt-token",
			"machine_id":           "machine-1",
		},
	}

	require.NoError(t, ValidateQoderCosyCredentials(context.Background(), account))
}

func TestValidateQoderCosyCredentialsExchangesPAT(t *testing.T) {
	old := qoderValidatePAT
	defer func() { qoderValidatePAT = old }()
	calls := 0
	qoderValidatePAT = func(ctx context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		calls++
		require.Equal(t, "pat-123", pat)
		return &qoder.AuthIdentity{UID: "uid", SecurityOauthToken: "dt-token"}, nil
	}

	account := &Account{
		Platform:    PlatformQoder,
		Type:        AccountTypeCosy,
		Credentials: map[string]any{"pat": "pat-123"},
	}

	require.NoError(t, ValidateQoderCosyCredentials(context.Background(), account))
	require.Equal(t, 1, calls)
}

func TestValidateQoderCosyCredentialsRejectsBadPAT(t *testing.T) {
	old := qoderValidatePAT
	defer func() { qoderValidatePAT = old }()
	qoderValidatePAT = func(ctx context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		return nil, errors.New("bad pat")
	}

	account := &Account{
		Platform:    PlatformQoder,
		Type:        AccountTypeCosy,
		Credentials: map[string]any{"pat": "pat-123"},
	}

	require.ErrorContains(t, ValidateQoderCosyCredentials(context.Background(), account), "bad pat")
}
