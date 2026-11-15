package usage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// TestLoadUsageExportLocationValidatesIANATimezones exercises LoadUsageExportLocation
// with valid padded IANA identifiers and invalid timezone strings. It verifies
// that canonical zone names resolve cleanly while blank, Local, and unknown
// names return structured 400 INVALID_TIMEZONE errors.
func TestLoadUsageExportLocationValidatesIANATimezones(t *testing.T) {
	loc, canonical, err := LoadUsageExportLocation("  Asia/Shanghai ")
	require.NoError(t, err)
	require.Equal(t, "Asia/Shanghai", canonical)
	require.NotNil(t, loc)

	for _, bad := range []string{"", "   ", "Local", "local", "Mars/Phobos"} {
		_, _, badErr := LoadUsageExportLocation(bad)
		require.Error(t, badErr)
		appErr := infraerrors.FromError(badErr)
		require.NotNil(t, appErr)
		require.Equal(t, "INVALID_TIMEZONE", appErr.Reason)
	}
}

// TestSanitizeAndValidateUsageExportFiltersEnforcesScopeIsolation submits mixed
// user and administrator filter payloads to SanitizeAndValidateUsageExportFilters.
// User scope pins UserID and IncludeOwnedTeam while stripping admin-only
// ProviderID and RequestSource fields, whereas admin scope preserves them and
// rejects unrecognized request sources.
func TestSanitizeAndValidateUsageExportFiltersEnforcesScopeIsolation(t *testing.T) {
	start := time.Date(2026, 4, 16, 1, 2, 3, 0, time.FixedZone("UTC+8", 8*3600))
	end := start.Add(2 * time.Hour)
	otherUser := int64(999)
	providerID := int64(42)
	streamFlag := true
	reqType := int16(RequestTypeWSV2)
	model := "  gpt-5.4  "
	mode := " TOKEN "
	teamID := int64(7)

	userFilters := UsageExportFilters{
		StartTime:         start,
		EndTime:           end,
		Timezone:          "Asia/Shanghai",
		Locale:            "en-US",
		UserID:            &otherUser,
		ProviderID:        &providerID,
		RequestSource:     SourceGroupProbe,
		TeamID:            &teamID,
		PersonalOnly:      true,
		Model:             &model,
		RequestType:       &reqType,
		Stream:            &streamFlag,
		BillingMode:       &mode,
		ModelFilterSource: " UPSTREAM ",
	}

	loc, err := SanitizeAndValidateUsageExportFilters(UsageExportScopeUser, 15, &userFilters)
	require.NoError(t, err)
	require.NotNil(t, loc)
	require.NotNil(t, userFilters.UserID)
	require.Equal(t, int64(15), *userFilters.UserID)
	require.True(t, userFilters.IncludeOwnedTeam)
	require.Nil(t, userFilters.ProviderID)
	require.Empty(t, userFilters.RequestSource)
	require.False(t, userFilters.PersonalOnly)
	require.Equal(t, UsageExportLocaleEN, userFilters.Locale)
	require.Equal(t, "USD", userFilters.BalanceUnitName)
	require.Equal(t, "gpt-5.4", *userFilters.Model)
	require.Equal(t, "upstream", userFilters.ModelFilterSource)
	require.Nil(t, userFilters.Stream)
	require.Equal(t, "token", *userFilters.BillingMode)
	require.Equal(t, time.UTC, userFilters.StartTime.Location())
	require.Equal(t, time.UTC, userFilters.EndTime.Location())

	adminFilters := UsageExportFilters{
		StartTime:     start,
		EndTime:       end,
		Timezone:      "UTC",
		ProviderID:    &providerID,
		RequestSource: "invalid_source",
	}
	_, err = SanitizeAndValidateUsageExportFilters(UsageExportScopeAdmin, 1, &adminFilters)
	require.Error(t, err)
	require.Equal(t, "INVALID_REQUEST_SOURCE", infraerrors.FromError(err).Reason)
}

// TestSanitizeAndValidateUsageExportFiltersRejectsInvalidBounds submits missing,
// equal, and inverted half-open [StartTime, EndTime) intervals to filter
// validation. Every malformed interval must fail with an INVALID_TIME_RANGE error.
func TestSanitizeAndValidateUsageExportFiltersRejectsInvalidBounds(t *testing.T) {
	instant := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)

	for _, tc := range []UsageExportFilters{
		{Timezone: "UTC", EndTime: instant},
		{Timezone: "UTC", StartTime: instant, EndTime: instant},
		{Timezone: "UTC", StartTime: instant, EndTime: instant.Add(-time.Second)},
	} {
		candidate := tc
		_, err := SanitizeAndValidateUsageExportFilters(UsageExportScopeUser, 7, &candidate)
		require.Error(t, err)
		require.Equal(t, "INVALID_TIME_RANGE", infraerrors.FromError(err).Reason)
	}
}

// TestUsageExportTicketSignerVerifiesSignatureAndExpiration reproduces browser
// download ticket signing, payload tampering, and clock advancement past TTL.
// Only unmodified tickets inspected before ExpiresAtUnix pass HMAC verification.
func TestUsageExportTicketSignerVerifiesSignatureAndExpiration(t *testing.T) {
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	signer := newUsageExportTicketSigner([]byte("0123456789abcdef0123456789abcdef"), func() time.Time {
		return now
	})

	claims := UsageExportDownloadTicketClaims{
		TaskID:        88,
		Scope:         UsageExportScopeUser,
		ActorUserID:   12,
		ExpiresAtUnix: now.Add(60 * time.Second).Unix(),
	}
	ticket, err := signer.Sign(claims)
	require.NoError(t, err)

	decoded, err := signer.Verify(ticket)
	require.NoError(t, err)
	require.Equal(t, claims, decoded)

	_, err = signer.Verify(ticket + "x")
	require.Error(t, err)
	require.Equal(t, "USAGE_EXPORT_INVALID_TICKET", infraerrors.FromError(err).Reason)

	now = now.Add(61 * time.Second)
	_, err = signer.Verify(ticket)
	require.Error(t, err)
	require.Equal(t, "USAGE_EXPORT_TICKET_EXPIRED", infraerrors.FromError(err).Reason)
}
