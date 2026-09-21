package admission

import (
	"context"
	"errors"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/stretchr/testify/require"
)

type fundingCheckFixture func(context.Context, billing.CheckInput) error

func (f fundingCheckFixture) Check(ctx context.Context, input billing.CheckInput) error {
	return f(ctx, input)
}

type rpmCheckFixture func(context.Context, *scheduler.RPMUser, *scheduler.RPMGroup) error

func (f rpmCheckFixture) Check(ctx context.Context, user *scheduler.RPMUser, group *scheduler.RPMGroup) error {
	return f(ctx, user, group)
}

// 准入拒绝不得消耗RPM；等待后的资金复查也不能再次累计。
func TestFundingAdmissionPreservesOrderAndWaitBoundary(t *testing.T) {
	for _, mode := range []string{"rejected", "standard", "simple", "after_wait"} {
		t.Run(mode, func(t *testing.T) {
			var events []string
			denied := errors.New("funding denied")
			funds := fundingCheckFixture(func(_ context.Context, input billing.CheckInput) error {
				events = append(events, "funds")
				require.Equal(t, int64(11), input.Payer.ID)
				if mode == "rejected" {
					return denied
				}
				return nil
			})
			rpm := rpmCheckFixture(func(_ context.Context, user *scheduler.RPMUser, group *scheduler.RPMGroup) error {
				events = append(events, "rpm")
				require.Equal(t, int64(11), user.ID)
				require.Equal(t, int64(22), group.ID)
				return nil
			})
			checker := NewFundingAdmission(funds, rpm, func() bool { events = append(events, "mode"); return mode == "simple" })
			input := billing.CheckInput{Payer: &billing.UserSummary{ID: 11}}
			if mode == "after_wait" {
				require.NoError(t, checker.CheckFunding(context.Background(), input))
				require.Equal(t, []string{"funds"}, events)
				return
			}
			err := checker.Check(context.Background(), input, &scheduler.RPMUser{ID: 11}, &scheduler.RPMGroup{ID: 22})
			if mode == "rejected" {
				require.ErrorIs(t, err, denied)
				require.Equal(t, []string{"funds"}, events)
				return
			}
			require.NoError(t, err)
			if mode == "simple" {
				require.Equal(t, []string{"funds", "mode"}, events)
			} else {
				require.Equal(t, []string{"funds", "mode", "rpm"}, events)
			}
		})
	}
}
