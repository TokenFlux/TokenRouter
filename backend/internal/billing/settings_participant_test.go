package billing

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// TestSettingsParticipantValidatesPlansOnlyWhenSubmitted exercises partial
// billing updates with and without default_subscriptions. It checks that plan
// validation runs only when default_subscriptions is submitted and preserves
// explicit zero writes while omitting absent billing keys.
func TestSettingsParticipantValidatesPlansOnlyWhenSubmitted(t *testing.T) {
	calls := 0
	wantErr := errors.New("plan lookup failed")
	participant := SettingsParticipant(func(_ context.Context, items []DefaultSubscriptionSetting) error {
		calls++
		if len(items) == 1 && items[0].PlanID == 99 {
			return wantErr
		}
		return nil
	})

	change, err := participant.Prepare(context.Background(), settings.Fields{
		"default_balance": json.RawMessage("0"),
	}, nil)
	require.NoError(t, err)
	require.Zero(t, calls)
	require.Equal(t, map[string]string{SettingKeyDefaultBalance: "0.00000000"}, change.Values)

	_, err = participant.Prepare(context.Background(), settings.Fields{
		"default_subscriptions": json.RawMessage(`[{"plan_id":99}]`),
	}, nil)
	require.ErrorIs(t, err, wantErr)
	require.Equal(t, 1, calls)

	change, err = participant.Prepare(context.Background(), settings.Fields{
		"default_subscriptions": json.RawMessage(`[{"plan_id":7}]`),
	}, nil)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
	require.Equal(t, map[string]string{SettingKeyDefaultSubscriptions: `[{"plan_id":7}]`}, change.Values)
}
