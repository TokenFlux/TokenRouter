//go:build integration

package bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/ent/group"
	"github.com/TokenFlux/TokenRouter/internal/billing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestEnsureSimpleModeDefaultGroups_CreatesMissingDefaults(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()

	seedCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	require.NoError(t, EnsureSimpleModeDefaultGroups(seedCtx, client))

	assertGroupExists := func(name, platform string) {
		created, err := client.Group.Query().Where(group.NameEQ(name), group.DeletedAtIsNil()).Only(seedCtx)
		require.NoError(t, err)
		require.Equal(t, capability.DefaultGroupClientProtocols(platform), created.AllowedProtocols)
	}

	assertGroupExists(capability.PlatformAnthropic+"-default", capability.PlatformAnthropic)
	assertGroupExists(capability.PlatformOpenAI+"-default", capability.PlatformOpenAI)
	assertGroupExists(capability.PlatformGemini+"-default", capability.PlatformGemini)
	assertGroupExists(capability.PlatformAntigravity+"-default-1", capability.PlatformAntigravity)
	assertGroupExists(capability.PlatformAntigravity+"-default-2", capability.PlatformAntigravity)
	assertGroupExists(capability.PlatformGrok+"-default", capability.PlatformGrok)

	grokDefault, err := client.Group.Query().
		Where(group.NameEQ(capability.PlatformGrok+"-default"), group.DeletedAtIsNil()).
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
		SetName(capability.PlatformGrok + "-default").
		SetDescription("Auto-created default group").
		SetPlatform(capability.PlatformGrok).
		SetStatus(billing.StatusActive).
		SetRateMultiplier(1.0).
		SetIsExclusive(false).
		SetAllowImageGeneration(false).
		Save(seedCtx)
	require.NoError(t, err)

	operatorGroup, err := client.Group.Create().
		SetName("operator-grok-images-disabled-" + time.Now().Format(time.RFC3339Nano)).
		SetDescription("Operator-managed group").
		SetPlatform(capability.PlatformGrok).
		SetStatus(billing.StatusActive).
		SetRateMultiplier(1.0).
		SetIsExclusive(false).
		SetAllowImageGeneration(false).
		Save(seedCtx)
	require.NoError(t, err)

	require.NoError(t, EnsureSimpleModeDefaultGroups(seedCtx, client))

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
			status:      billing.StatusActive,
		},
		{
			name:        "disabled auto-created default",
			description: simpleModeDefaultGroupDescription,
			status:      billing.StatusDisabled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			client := testEntTx(t).Client()
			grokDefault, err := client.Group.Create().
				SetName(capability.PlatformGrok + "-default").
				SetDescription(tt.description).
				SetPlatform(capability.PlatformGrok).
				SetStatus(tt.status).
				SetRateMultiplier(1.0).
				SetIsExclusive(false).
				SetAllowImageGeneration(false).
				Save(ctx)
			require.NoError(t, err)

			require.NoError(t, EnsureSimpleModeDefaultGroups(ctx, client))

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
		SetName(capability.PlatformAnthropic + "-default").
		SetPlatform(capability.PlatformAnthropic).
		SetStatus(billing.StatusActive).
		SetRateMultiplier(1.0).
		SetIsExclusive(false).
		Save(seedCtx)
	require.NoError(t, err)

	_, err = client.Group.Delete().Where(group.IDEQ(g.ID)).Exec(seedCtx)
	require.NoError(t, err)

	require.NoError(t, EnsureSimpleModeDefaultGroups(seedCtx, client))

	// New active one should exist.
	count, err := client.Group.Query().Where(group.NameEQ(capability.PlatformAnthropic+"-default"), group.DeletedAtIsNil()).Count(seedCtx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestEnsureSimpleModeDefaultGroups_AntigravityNeedsTwoGroupsOnlyByCount(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()

	seedCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, err := client.Group.Create().SetName("ag-custom-1-" + time.Now().Format(time.RFC3339Nano)).SetPlatform(capability.PlatformAntigravity).Save(seedCtx)
	require.NoError(t, err)
	_, err = client.Group.Create().SetName("ag-custom-2-" + time.Now().Format(time.RFC3339Nano)).SetPlatform(capability.PlatformAntigravity).Save(seedCtx)
	require.NoError(t, err)

	require.NoError(t, EnsureSimpleModeDefaultGroups(seedCtx, client))

	count, err := client.Group.Query().Where(group.PlatformEQ(capability.PlatformAntigravity), group.DeletedAtIsNil()).Count(seedCtx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, count, 2)
}

// 持久化夹具保留既有自动创建标记，生产规则已归 routing 初始化。
const simpleModeDefaultGroupDescription = "Auto-created default group"
