//go:build unit

package selection

import (
	"context"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	"github.com/stretchr/testify/require"
)

func TestSelectAccountForModelWithExclusions_UsesFallbackGroupForChannelRestriction(t *testing.T) {
	t.Parallel()

	groupID := int64(10)
	fallbackID := int64(11)
	ch := routing.Channel{
		ID:             1,
		Status:         billing.StatusActive,
		GroupIDs:       []int64{fallbackID},
		RestrictModels: true,
		ModelPricing: []routing.ChannelModelPricing{
			{Platform: capability.PlatformAnthropic, Models: []string{"claude-sonnet-4-6"}},
		},
	}
	channelSvc := routingtestkit.ChannelWithRepository(routingtestkit.StandardChannelRepository(ch, map[int64]string{
		fallbackID: capability.PlatformAnthropic,
	}))
	accountRepo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range accountRepo.accounts {
		accountRepo.accountsByID[accountRepo.accounts[i].Record.ID] = &accountRepo.accounts[i]
	}
	groupRepo := &mockGroupRepoForGateway{
		groups: map[int64]*routing.Group{
			groupID: {
				ID:              groupID,
				Platform:        capability.PlatformAnthropic,
				Status:          billing.StatusActive,
				ClaudeCodeOnly:  true,
				FallbackGroupID: &fallbackID,
				Hydrated:        true,
			},
			fallbackID: {
				ID:       fallbackID,
				Platform: capability.PlatformAnthropic,
				Status:   billing.StatusActive,
				Hydrated: true,
			},
		},
	}

	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{Accounts: accountRepo,

			Groups: groupRepo},
		Shared: Shared{Channels: channelSvc},
	}, testConfig())

	ctx := requeststate.WithGroup(context.Background(), groupRepo.groups[groupID])
	account, err := svc.SelectAccountForModelWithExclusions(ctx, &groupID, "", "claude-sonnet-4-6", nil)
	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, int64(1), account.Record.ID)
}

func TestSelectAccountWithLoadAwareness_UsesFallbackGroupForChannelRestriction(t *testing.T) {
	t.Parallel()

	groupID := int64(10)
	fallbackID := int64(11)
	ch := routing.Channel{
		ID:             1,
		Status:         billing.StatusActive,
		GroupIDs:       []int64{fallbackID},
		RestrictModels: true,
		ModelPricing: []routing.ChannelModelPricing{
			{Platform: capability.PlatformAnthropic, Models: []string{"claude-sonnet-4-6"}},
		},
	}
	channelSvc := routingtestkit.ChannelWithRepository(routingtestkit.StandardChannelRepository(ch, map[int64]string{
		fallbackID: capability.PlatformAnthropic,
	}))
	accountRepo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range accountRepo.accounts {
		accountRepo.accountsByID[accountRepo.accounts[i].Record.ID] = &accountRepo.accounts[i]
	}
	groupRepo := &mockGroupRepoForGateway{
		groups: map[int64]*routing.Group{
			groupID: {
				ID:              groupID,
				Platform:        capability.PlatformAnthropic,
				Status:          billing.StatusActive,
				ClaudeCodeOnly:  true,
				FallbackGroupID: &fallbackID,
				Hydrated:        true,
			},
			fallbackID: {
				ID:       fallbackID,
				Platform: capability.PlatformAnthropic,
				Status:   billing.StatusActive,
				Hydrated: true,
			},
		},
	}

	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{Accounts: accountRepo,

			Groups: groupRepo},
		Shared: Shared{Channels: channelSvc},
	}, testConfig())

	ctx := requeststate.WithGroup(context.Background(), groupRepo.groups[groupID])
	result, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "claude-sonnet-4-6", nil, "", 0)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Account)
	require.Equal(t, int64(1), result.Account.Record.ID)
}
