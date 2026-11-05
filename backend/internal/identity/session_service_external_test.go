package identity_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
)

// TestAuthService_ValidateToken_ExpiredReturnsClaimsWithError validates a
// generated access token, then a separately generated expired token. It
// requires account claims on both paths and ErrTokenExpired alongside the
// expired claims.
func TestAuthService_ValidateToken_ExpiredReturnsClaimsWithError(t *testing.T) {
	repo := &userRepoStub{}
	service := newAuthService(repo, nil, nil)

	// Create a user and issue an access token.
	user := &identity.User{
		ID:           1,
		Email:        "test@test.com",
		Role:         identity.RoleUser,
		Status:       billing.StatusActive,
		TokenVersion: 1,
	}
	token, err := service.GenerateToken(context.Background(), user)
	require.NoError(t, err)

	// Validate the unexpired token.
	claims, err := service.ValidateToken(token)
	require.NoError(t, err)
	require.NotNil(t, claims)
	require.Equal(t, int64(1), claims.UserID)

	// Sign using a past clock so the ordinary positive lifetime has already
	// elapsed when validation runs, without supplying an invalid process configuration.
	service.Options.JWT.Now = func() time.Time { return time.Now().Add(-2 * time.Hour) }
	rebuildSessionForTest(service)
	expiredToken, err := service.GenerateToken(context.Background(), user)
	require.NoError(t, err)
	service.Options.JWT.Now = time.Now // Restore current time before validating expiration.
	rebuildSessionForTest(service)

	// An expired token returns its claims alongside ErrTokenExpired.
	claims, err = service.ValidateToken(expiredToken)
	require.ErrorIs(t, err, identity.ErrTokenExpired)
	require.NotNil(t, claims, "claims should not be nil when token is expired")
	require.Equal(t, int64(1), claims.UserID)
	require.Equal(t, "test@test.com", claims.Email)
}

// TestAuthService_RefreshToken_ExpiredTokenNoPanic issues an expired access
// token for a current active account. It requires the legacy renewal path to
// return a nonempty replacement successfully while accessing expiration-path
// claims.
func TestAuthService_RefreshToken_ExpiredTokenNoPanic(t *testing.T) {
	user := &identity.User{
		ID:           1,
		Email:        "test@test.com",
		Role:         identity.RoleUser,
		Status:       billing.StatusActive,
		TokenVersion: 1,
	}
	repo := &userRepoStub{user: user}
	service := newAuthService(repo, nil, nil)

	// Sign against an earlier clock, then restore current time so renewal reads
	// claims from an actually expired token with the normal positive lifetime.
	service.Options.JWT.Now = func() time.Time { return time.Now().Add(-2 * time.Hour) }
	rebuildSessionForTest(service)
	expiredToken, err := service.GenerateToken(context.Background(), user)
	require.NoError(t, err)
	service.Options.JWT.Now = time.Now
	rebuildSessionForTest(service)

	// Renewal must handle expired-token claims without panicking.
	require.NotPanics(t, func() {
		newToken, err := service.RefreshToken(context.Background(), expiredToken)
		require.NoError(t, err)
		require.NotEmpty(t, newToken)
	})
}

// TestAuthService_GetAccessTokenExpiresIn_FallbackToExpireHour disables the
// minute lifetime and sets twenty-four legacy hours. It requires the client
// lifetime accessor to report the matching number of seconds after rebuilding
// session options.
func TestAuthService_GetAccessTokenExpiresIn_FallbackToExpireHour(t *testing.T) {
	service := newAuthService(&userRepoStub{}, nil, nil)
	service.Options.JWT.ExpireHour = 24
	rebuildSessionForTest(service)
	service.Options.JWT.AccessTokenExpireMinutes = 0
	rebuildSessionForTest(service)

	require.Equal(t, 24*3600, service.GetAccessTokenExpiresIn())
}

// TestAuthService_GetAccessTokenExpiresIn_MinutesHasPriority provides both a
// legacy hour value and ninety minute lifetime. It requires the minute
// configuration to determine the client-facing seconds after the session
// fixture copies those options.
func TestAuthService_GetAccessTokenExpiresIn_MinutesHasPriority(t *testing.T) {
	service := newAuthService(&userRepoStub{}, nil, nil)
	service.Options.JWT.ExpireHour = 24
	rebuildSessionForTest(service)
	service.Options.JWT.AccessTokenExpireMinutes = 90
	rebuildSessionForTest(service)

	require.Equal(t, 90*60, service.GetAccessTokenExpiresIn())
}

// TestAuthService_GenerateToken_UsesExpireHourWhenMinutesZero signs with zero
// minutes and a twenty-four-hour legacy setting. It checks the parsed expiry
// against issued time using the test tolerance for the selected hour lifetime.
func TestAuthService_GenerateToken_UsesExpireHourWhenMinutesZero(t *testing.T) {
	service := newAuthService(&userRepoStub{}, nil, nil)
	service.Options.JWT.ExpireHour = 24
	rebuildSessionForTest(service)
	service.Options.JWT.AccessTokenExpireMinutes = 0
	rebuildSessionForTest(service)

	user := &identity.User{
		ID:           1,
		Email:        "test@test.com",
		Role:         identity.RoleUser,
		Status:       billing.StatusActive,
		TokenVersion: 1,
	}

	token, err := service.GenerateToken(context.Background(), user)
	require.NoError(t, err)

	claims, err := service.ValidateToken(token)
	require.NoError(t, err)
	require.NotNil(t, claims)
	require.NotNil(t, claims.IssuedAt)
	require.NotNil(t, claims.ExpiresAt)

	// Allow two seconds around parsed JWT dates for this wall-clock lifetime
	// assertion.
	require.WithinDuration(t, claims.IssuedAt.Add(24*time.Hour), claims.ExpiresAt.Time, 2*time.Second)
}

// TestAuthService_GenerateToken_UsesMinutesWhenConfigured signs with ninety
// minutes and a separate legacy hour setting. It checks the parsed expiry
// against issued time using the test tolerance for the selected minute
// lifetime.
func TestAuthService_GenerateToken_UsesMinutesWhenConfigured(t *testing.T) {
	service := newAuthService(&userRepoStub{}, nil, nil)
	service.Options.JWT.ExpireHour = 24
	rebuildSessionForTest(service)
	service.Options.JWT.AccessTokenExpireMinutes = 90
	rebuildSessionForTest(service)

	user := &identity.User{
		ID:           2,
		Email:        "test2@test.com",
		Role:         identity.RoleUser,
		Status:       billing.StatusActive,
		TokenVersion: 1,
	}

	token, err := service.GenerateToken(context.Background(), user)
	require.NoError(t, err)

	claims, err := service.ValidateToken(token)
	require.NoError(t, err)
	require.NotNil(t, claims)
	require.NotNil(t, claims.IssuedAt)
	require.NotNil(t, claims.ExpiresAt)

	// Allow two seconds around parsed JWT dates for this wall-clock lifetime
	// assertion.
	require.WithinDuration(t, claims.IssuedAt.Add(90*time.Minute), claims.ExpiresAt.Time, 2*time.Second)
}
