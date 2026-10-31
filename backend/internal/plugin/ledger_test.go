package plugin

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAmountsNormalizeWithoutLosingPrecision checks the smallest amount, largest permitted amount, and equivalent decimal spellings.
// Invalid signs, exponents, zero, excess precision, and leading zeroes cannot become a financial mutation.
func TestAmountsNormalizeWithoutLosingPrecision(t *testing.T) {
	for input, want := range map[string]string{"0.00000001": "0.00000001", "9999999.99999999": "9999999.99999999", "1.00000000": "1", "0.10000000": "0.1"} {
		amount, err := ParseAmount(input)
		require.NoError(t, err, input)
		require.Equal(t, want, amount.String())
	}
	for _, input := range []string{"0", "0.00000000", "-1", "+1", "01", "1e2", "10000000", "0.000000001", " 1"} {
		_, err := ParseAmount(input)
		require.ErrorIs(t, err, ErrInvalidArgument, input)
	}
	amount, err := ParseAmount("0.00000001")
	require.NoError(t, err)
	require.False(t, exceedsAmount("0.99999999", amount, json.Number("1")))
	require.True(t, exceedsAmount("1", amount, json.Number("1")))
	require.True(t, exceedsAmount("0", amount, json.Number("0")))
}

// TestLedgerIntentAndCalendarBoundaries preserves reordered JSON intent and adjacent integer identities beyond float precision.
// Host calendar windows also follow daylight-saving days instead of adding an unconditional twenty-four-hour duration.
func TestLedgerIntentAndCalendarBoundaries(t *testing.T) {
	require.True(t, sameIntent([]byte(`{"user_id":9007199254740993,"amount":"1"}`), []byte(`{ "amount":"1", "user_id":9007199254740993 }`)))
	require.False(t, sameIntent([]byte(`{"user_id":9007199254740993}`), []byte(`{"user_id":9007199254740992}`)))
	location, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	since, until := dayWindow(time.Date(2026, 3, 8, 12, 0, 0, 0, location), location)
	require.Equal(t, 23*time.Hour, until.Sub(since))
	require.NoError(t, validateOperation("claim:one", ""))
	require.ErrorIs(t, validateOperation("bad key", "reason"), ErrInvalidArgument)
	require.ErrorIs(t, validateOperation("claim:one", "invalid\x00reason"), ErrInvalidArgument)
}
