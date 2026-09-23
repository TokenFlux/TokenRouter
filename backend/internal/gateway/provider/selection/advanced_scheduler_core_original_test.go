package selection

import (
	"context"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/stretchr/testify/require"
)

func TestAdvancedSchedulerCoreSelectsNonOpenAIGroupAndMarksResult(t *testing.T) {
	groupID := int64(42)
	group := &routing.Group{ID: groupID, Platform: capability.PlatformGemini, SchedulerType: routing.GroupSchedulerTypeAdvanced}
	ctx := requeststate.WithGroup(context.Background(), group)
	service := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{}}, nil)

	selection, selected, err := service.tryAcquireByAdvancedScheduler(ctx, &groupID, "session", []accountWithLoad{
		{
			account:  &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 101, Platform: capability.PlatformGemini, Priority: 1, Schedulable: true, Status: billing.StatusActive}},
			loadInfo: &scheduler.AccountLoadInfo{AccountID: 101, LoadRate: 0},
		},
	})

	require.NoError(t, err)
	require.True(t, selected)
	require.NotNil(t, selection)
	require.Equal(t, int64(101), selection.Account.Record.ID)
	require.True(t, selection.AdvancedScheduler)

	basicCtx := requeststate.WithGroup(context.Background(), &routing.Group{ID: 43, Platform: capability.PlatformGemini, SchedulerType: routing.GroupSchedulerTypeBasic})
	basicSelection, err := service.newSelectionResult(basicCtx, &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 102}}, true, func() {}, nil)
	require.NoError(t, err)
	require.False(t, basicSelection.AdvancedScheduler)
}
