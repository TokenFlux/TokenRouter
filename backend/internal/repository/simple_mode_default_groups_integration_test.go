//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/ent/group"
	"github.com/BrandonVee/TokenRouter/internal/domain"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestEnsureSimpleModeDefaultGroups_CreatesMissingDefaults(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()

	seedCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	require.NoError(t, ensureSimpleModeDefaultGroups(seedCtx, client))

	assertGroupExists := func(name, platform string) {
		created, err := client.Group.Query().Where(group.NameEQ(name), group.DeletedAtIsNil()).Only(seedCtx)
		require.NoError(t, err)
		require.Equal(t, domain.DefaultGroupClientProtocols(platform), created.AllowedClientProtocols)
	}

	assertGroupExists(service.PlatformAnthropic+"-default", service.PlatformAnthropic)
	assertGroupExists(service.PlatformOpenAI+"-default", service.PlatformOpenAI)
	assertGroupExists(service.PlatformGemini+"-default", service.PlatformGemini)
	assertGroupExists(service.PlatformAntigravity+"-default-1", service.PlatformAntigravity)
	assertGroupExists(service.PlatformAntigravity+"-default-2", service.PlatformAntigravity)
	assertGroupExists(service.PlatformGrok+"-default", service.PlatformGrok)

	grokDefault, err := client.Group.Query().
		Where(group.NameEQ(service.PlatformGrok+"-default"), group.DeletedAtIsNil()).
		Only(seedCtx)
	require.NoError(t, err)
	require.True(t, grokDefault.AllowImageGeneration)
}

func TestEnsureSimpleModeDefaultGroups_BackfillsOnlyAutoCreatedGrokDefault(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()

	seedCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	autoDefault, err := client.Group.Create().
		SetName(service.PlatformGrok + "-default").
		SetDescription("Auto-created default group").
		SetPlatform(service.PlatformGrok).
		SetStatus(service.StatusActive).
		SetRateMultiplier(1.0).
		SetIsExclusive(false).
		SetAllowImageGeneration(false).
		Save(seedCtx)
	require.NoError(t, err)

	operatorGroup, err := client.Group.Create().
		SetName("operator-grok-images-disabled-" + time.Now().Format(time.RFC3339Nano)).
		SetDescription("Operator-managed group").
		SetPlatform(service.PlatformGrok).
		SetStatus(service.StatusActive).
		SetRateMultiplier(1.0).
		SetIsExclusive(false).
		SetAllowImageGeneration(false).
		Save(seedCtx)
	require.NoError(t, err)

	require.NoError(t, ensureSimpleModeDefaultGroups(seedCtx, client))

	autoDefault, err = client.Group.Get(seedCtx, autoDefault.ID)
	require.NoError(t, err)
	require.True(t, autoDefault.AllowImageGeneration)

	operatorGroup, err = client.Group.Get(seedCtx, operatorGroup.ID)
	require.NoError(t, err)
	require.False(t, operatorGroup.AllowImageGeneration, "operator-managed false must be preserved")
}

func TestEnsureSimpleModeDefaultGroups_PreservesExplicitFalse(t *testing.T) {
	tests := []struct {
		name        string
		description string
		status      string
	}{
		{
			name:        "operator managed default",
			description: "Operator-managed group",
			status:      service.StatusActive,
		},
		{
			name:        "disabled auto-created default",
			description: simpleModeDefaultGroupDescription,
			status:      service.StatusDisabled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			client := testEntTx(t).Client()
			grokDefault, err := client.Group.Create().
				SetName(service.PlatformGrok + "-default").
				SetDescription(tt.description).
				SetPlatform(service.PlatformGrok).
				SetStatus(tt.status).
				SetRateMultiplier(1.0).
				SetIsExclusive(false).
				SetAllowImageGeneration(false).
				Save(ctx)
			require.NoError(t, err)

			require.NoError(t, ensureSimpleModeDefaultGroups(ctx, client))

			grokDefault, err = client.Group.Get(ctx, grokDefault.ID)
			require.NoError(t, err)
			require.False(t, grokDefault.AllowImageGeneration)
		})
	}
}

func TestEnsureSimpleModeDefaultGroups_IgnoresSoftDeletedGroups(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()

	seedCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Create and then soft-delete an anthropic default group.
	g, err := client.Group.Create().
		SetName(service.PlatformAnthropic + "-default").
		SetPlatform(service.PlatformAnthropic).
		SetStatus(service.StatusActive).
		SetRateMultiplier(1.0).
		SetIsExclusive(false).
		Save(seedCtx)
	require.NoError(t, err)

	_, err = client.Group.Delete().Where(group.IDEQ(g.ID)).Exec(seedCtx)
	require.NoError(t, err)

	require.NoError(t, ensureSimpleModeDefaultGroups(seedCtx, client))

	// New active one should exist.
	count, err := client.Group.Query().Where(group.NameEQ(service.PlatformAnthropic+"-default"), group.DeletedAtIsNil()).Count(seedCtx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestEnsureSimpleModeDefaultGroups_AntigravityNeedsTwoGroupsOnlyByCount(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()

	seedCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	mustCreateGroup(t, client, &service.Group{Name: "ag-custom-1-" + time.Now().Format(time.RFC3339Nano), Platform: service.PlatformAntigravity})
	mustCreateGroup(t, client, &service.Group{Name: "ag-custom-2-" + time.Now().Format(time.RFC3339Nano), Platform: service.PlatformAntigravity})

	require.NoError(t, ensureSimpleModeDefaultGroups(seedCtx, client))

	count, err := client.Group.Query().Where(group.PlatformEQ(service.PlatformAntigravity), group.DeletedAtIsNil()).Count(seedCtx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, 2)
}
