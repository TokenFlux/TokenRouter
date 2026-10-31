//go:build integration

package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/ent"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	pluginpostgres "github.com/TokenFlux/TokenRouter/internal/plugin/postgres"
	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// plugin_subscriptions_scenario_integration_test.go covers plugin/subscriptions.go,
// plugin/writes.go, the PostgreSQL ledger, app/plugin_capabilities.go, and existing
// billing subscription issuance. It verifies live templates, queued entitlements,
// authorization limits, atomic rollback, and post-commit invalidation using a real
// database and an observable authentication-cache collaborator.

// subscriptionInvalidationCounter observes only the post-commit authentication collaboration.
type subscriptionInvalidationCounter struct {
	calls atomic.Int64 // completed authentication invalidation attempts
}

// InvalidateAuthCacheByUserID records one successful entitlement's cache invalidation attempt.
func (p *subscriptionInvalidationCounter) InvalidateAuthCacheByUserID(context.Context, int64) {
	p.calls.Add(1)
}

// TestPluginSubscriptionGrantsReuseTemplatesAndRollback atomically queues a plugin grant behind an existing subscription with live plan quotas.
// Plan allowlisting, count limits, replay, deleted recipients, and injected ledger failure cannot create unauthorized or duplicate entitlements.
func TestPluginSubscriptionGrantsReuseTemplatesAndRollback(t *testing.T) {
	db := postgrescontainer.New(t)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	recipient, err := client.User.Create().SetEmail("plugin-subscription@example.invalid").SetPasswordHash("fixture").Save(t.Context())
	require.NoError(t, err)
	other, err := client.User.Create().SetEmail("plugin-subscription-other@example.invalid").SetPasswordHash("fixture").Save(t.Context())
	require.NoError(t, err)
	plan, err := client.SubscriptionPlan.Create().SetName("Plugin grant plan").SetPrice(1).SetValidityDays(30).SetValidityUnit("days").SetDailyLimitUsd(1.5).Save(t.Context())
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Second)
	records := billingpostgres.NewUserSubscriptionRepository(client)
	domain := billing.NewSubscriptionService(nil, records, billingpostgres.NewSubscriptionMutations(client), billing.DateRuntime{Now: func() time.Time { return now }})
	original, _, err := domain.AssignOrExtendSubscription(t.Context(), &billing.AssignSubscriptionInput{UserID: recipient.ID, PlanID: plan.ID, ValidityDays: 3})
	require.NoError(t, err)
	store := pluginpostgres.New(client)
	installation := &plugin.Installation{PluginID: "test.subscriptions", Enabled: true, VersionSnapshot: plugin.VersionSnapshot{
		Version: "1.0.0", Source: plugin.SourceUpload, Manifest: &manifest.Manifest{ID: "test.subscriptions", Version: "1.0.0"}, ManifestSHA256: strings.Repeat("a", 64),
	}}
	require.NoError(t, store.Save(t.Context(), installation))
	invalidation := &subscriptionInvalidationCounter{}
	effects := &pluginSubscriptionEffects{service: domain, invalidator: invalidation}
	service := plugin.NewSubscriptions(plugin.NewWrites(store, store, func() time.Time { return now }), effects, time.UTC)
	binding := plugin.Binding{PluginID: installation.PluginID, Version: installation.Version, Permissions: []string{"subscription.grant"}}
	input := plugin.SubscriptionInput{UserID: recipient.ID, PlanID: plan.ID, Days: 2, Key: "grant-one", Reason: "Feature reward"}
	_, err = service.Grant(t.Context(), binding, input)
	require.ErrorIs(t, err, plugin.ErrPlanNotAllowed)
	limits := plugin.DefaultLimits()
	limits.Subscription.AllowedPlanIDs = []int64{plan.ID}
	limits.Subscription.MaxGrantsPerDay = 1
	installation.Limits, err = json.Marshal(limits)
	require.NoError(t, err)
	require.NoError(t, store.Save(t.Context(), installation))
	tooLong := input
	tooLong.Days = 32
	_, err = service.Grant(t.Context(), binding, tooLong)
	require.ErrorIs(t, err, plugin.ErrLimitExceeded)
	require.Equal(t, "subscription.max_days_per_grant", apperror.FromError(err).Metadata["limit"])
	granted, err := service.Grant(t.Context(), binding, input)
	require.NoError(t, err)
	require.True(t, granted.Queued)
	require.Equal(t, original.ExpiresAt.AddDate(0, 0, 2).Unix(), granted.ExpiresUnix)
	saved, err := records.GetByID(t.Context(), granted.ID)
	require.NoError(t, err)
	require.NotNil(t, saved.DailyLimitUSD)
	require.Equal(t, 1.5, *saved.DailyLimitUSD)
	require.Nil(t, saved.SourceOrderID)
	require.Nil(t, saved.AssignedBy)
	require.Equal(t, "[plugin:test.subscriptions] Feature reward", saved.Notes)
	replayed, err := service.Grant(t.Context(), binding, input)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, granted.ID, replayed.ID)
	require.Equal(t, int64(1), invalidation.calls.Load())
	next := input
	next.Key = "user-limit"
	_, err = service.Grant(t.Context(), binding, next)
	require.ErrorIs(t, err, plugin.ErrLimitExceeded)
	require.Equal(t, "subscription.max_grants_per_user_per_day", apperror.FromError(err).Metadata["limit"])
	next.UserID = other.ID
	next.Key = "plugin-limit"
	_, err = service.Grant(t.Context(), binding, next)
	require.ErrorIs(t, err, plugin.ErrLimitExceeded)
	require.Equal(t, "subscription.max_grants_per_day", apperror.FromError(err).Metadata["limit"])
	limits.Subscription.AllowedPlanIDs = []int64{}
	installation.Limits, err = json.Marshal(limits)
	require.NoError(t, err)
	require.NoError(t, store.Save(t.Context(), installation))
	replayed, err = service.Grant(t.Context(), binding, input)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	limits.Subscription.AllowedPlanIDs = []int64{plan.ID}
	limits.Subscription.MaxGrantsPerDay = 10
	limits.Subscription.MaxGrantsPerUserPerDay = 10
	installation.Limits, err = json.Marshal(limits)
	require.NoError(t, err)
	require.NoError(t, store.Save(t.Context(), installation))
	require.NoError(t, client.User.UpdateOneID(other.ID).SetDeletedAt(now).Exec(t.Context()))
	next.Key = "deleted-recipient"
	_, err = service.Grant(t.Context(), binding, next)
	require.ErrorIs(t, err, plugin.ErrHostUserNotFound)
	// Reject the ledger row after subscription creation so the outer transaction must remove the entitlement and suppress post-commit invalidation together.
	_, err = db.ExecContext(t.Context(), `CREATE FUNCTION reject_plugin_grant_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.idempotency_key='grant-rollback' THEN RAISE EXCEPTION 'injected grant ledger failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_plugin_grant_fixture BEFORE INSERT ON plugin_ledger FOR EACH ROW EXECUTE FUNCTION reject_plugin_grant_fixture();`)
	require.NoError(t, err)
	input.Key = "grant-rollback"
	_, err = service.Grant(t.Context(), binding, input)
	require.Error(t, err)
	count, err := client.UserSubscription.Query().Count(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.Equal(t, int64(1), invalidation.calls.Load())
	var ledgerCount int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM plugin_ledger WHERE plugin_id='test.subscriptions'").Scan(&ledgerCount))
	require.Equal(t, 1, ledgerCount)
}
