//go:build integration

package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/ent"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/TokenFlux/TokenRouter/ent/usersubscription"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	pluginpostgres "github.com/TokenFlux/TokenRouter/internal/plugin/postgres"
	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
	"github.com/TokenFlux/TokenRouter/migrations"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// plugin_redeem_scenario_integration_test.go covers plugin/redeem.go, the shared
// ledger, app/plugin_redeem.go, native credential persistence, and billing redemption.
// PostgreSQL verifies one issued code under concurrency, quota enforcement, atomic
// rollback, frozen durations after plan edits, and unchanged legacy redemption behavior.

// redeemAffiliateProbe observes ordinary native redemption's post-commit rebate collaboration.
type redeemAffiliateProbe struct {
	calls  int     // observed rebate invocation count
	amount float64 // most recent qualifying balance grant
}

// pluginRedeemFixture binds issuance and redemption to one disposable migrated database.
type pluginRedeemFixture struct {
	db           *sql.DB                      // owned PostgreSQL fixture pool
	client       *ent.Client                  // transaction-aware fixture Ent client
	store        *pluginpostgres.Store        // installation and ledger persistence
	repo         *billingpostgres.RedeemStore // native credential persistence owner
	issuer       *plugin.RedeemCodes          // validated plugin issuance entry point
	redeem       *billing.RedeemService       // native recipient redemption entry point
	grants       *plugin.Grants               // deterministic credential derivation owner
	affiliate    *redeemAffiliateProbe        // observed native rebate collaboration
	installation *plugin.Installation         // retained fixture installation policy
	binding      plugin.Binding               // authenticated plugin capability identity
	now          time.Time                    // injectable issuance accounting instant
	planID       int64                        // selected live subscription template
}

// IsEnabled enables the fixture's observable native rebate collaboration.
func (*redeemAffiliateProbe) IsEnabled(context.Context) bool { return true }

// AccrueInviteRebate records the native positive-balance redemption without crediting another account.
func (p *redeemAffiliateProbe) AccrueInviteRebate(_ context.Context, _ int64, amount float64) (float64, error) {
	p.calls++
	p.amount = amount
	return 0, nil
}

// newPluginRedeemFixture assembles production issuance and redemption around isolated PostgreSQL storage.
// The bounded pool queues concurrent issuers instead of exceeding the fixture
// server's connection limit. All clocks and credentials remain local test inputs.
func newPluginRedeemFixture(t *testing.T) *pluginRedeemFixture {
	t.Helper()
	db := postgrescontainer.New(t)
	db.SetMaxOpenConns(16)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	fixture := &pluginRedeemFixture{db: db, client: client, now: time.Now().UTC().Truncate(time.Second), affiliate: &redeemAffiliateProbe{}}
	plan, err := client.SubscriptionPlan.Create().SetName("Plugin code plan").SetPrice(1).SetValidityDays(7).SetValidityUnit("day").Save(t.Context())
	require.NoError(t, err)
	fixture.planID = plan.ID
	subscriptions := billing.NewSubscriptionService(nil, billingpostgres.NewUserSubscriptionRepository(client), billingpostgres.NewSubscriptionMutations(client), billing.DateRuntime{Now: func() time.Time { return fixture.now }})
	fixture.repo = billingpostgres.NewRedeemCodeRepository(client)
	users := identitypostgres.NewUserStore(client, db)
	mutations := billingpostgres.NewRedeemMutations(client, billingpostgres.RedeemWriters{Balances: billingpostgres.NewBalanceStore(client), Concurrency: identitypostgres.NewConcurrencyStore(client)})
	fixture.redeem = billing.NewRedeemService(fixture.repo, billingIdentityUsers{Repository: users}, subscriptions, nil, nil, mutations, nil, fixture.affiliate, billing.RedeemRuntime{Now: func() time.Time { return fixture.now }})
	fixture.store = pluginpostgres.New(client)
	fixture.installation = &plugin.Installation{
		PluginID: "test.redeem",
		Enabled:  true,
		VersionSnapshot: plugin.VersionSnapshot{
			Version:        "1.0.0",
			Source:         plugin.SourceUpload,
			Manifest:       &manifest.Manifest{ID: "test.redeem", Version: "1.0.0"},
			ManifestSHA256: strings.Repeat("a", 64),
		},
	}
	require.NoError(t, fixture.store.Save(t.Context(), fixture.installation))
	fixture.grants, err = plugin.NewGrants("redeem-fixture-key", nil)
	require.NoError(t, err)
	effects := &pluginRedeemEffects{plans: billing.NewPlans(billingpostgres.NewPlanStore(client), nil, nil), redeem: fixture.redeem}
	fixture.issuer = plugin.NewRedeemCodes(plugin.NewWrites(fixture.store, fixture.store, func() time.Time { return fixture.now }), fixture.grants, effects, time.UTC)
	fixture.binding = plugin.Binding{PluginID: fixture.installation.PluginID, Version: "1.0.0", Permissions: []string{"redeem.issue"}}
	return fixture
}

// TestPluginRedeemConcurrentReplayAndNativeBalanceRules races fifty identical issuers and then redeems the single resulting credential normally.
// One code and ledger row commit, replay creates nothing, and native redemption still updates cumulative recharge and invokes its existing rebate collaborator.
func TestPluginRedeemConcurrentReplayAndNativeBalanceRules(t *testing.T) {
	f := newPluginRedeemFixture(t)
	input := plugin.RedeemInput{Type: "balance", Value: "1.00000000", Key: "one-code", Reason: "Reward", ExpiresUnix: f.now.Add(24 * time.Hour).Unix()}
	results := make(chan *plugin.RedeemResult, 50)
	failures := make(chan error, 50)
	var callers sync.WaitGroup
	for range 50 {
		callers.Go(func() {
			result, err := f.issuer.Issue(t.Context(), f.binding, input)
			results <- result
			failures <- err
		})
	}
	callers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	code := f.grants.RedeemCode(f.binding.PluginID, input.Key)
	replays := 0
	for result := range results {
		require.Equal(t, code, result.Code)
		if result.Replayed {
			replays++
		}
	}
	require.Equal(t, 49, replays)
	require.Len(t, code, 26)
	require.True(t, strings.HasPrefix(code, "PL"))
	stored, err := f.repo.GetByCode(t.Context(), code)
	require.NoError(t, err)
	require.Equal(t, 1, stored.MaxUses)
	require.False(t, stored.RequiresPayment)
	require.Equal(t, "[plugin:test.redeem] Reward", stored.Notes)
	require.Nil(t, stored.ValidityDays)
	require.Equal(t, input.ExpiresUnix, stored.ExpiresAt.Unix())
	var count int
	require.NoError(t, f.db.QueryRowContext(t.Context(), "SELECT count(*) FROM plugin_ledger").Scan(&count))
	require.Equal(t, 1, count)
	input.Value = "2"
	_, err = f.issuer.Issue(t.Context(), f.binding, input)
	require.ErrorIs(t, err, plugin.ErrInvalidArgument)
	input.Value = "1"
	f.installation.Limits = json.RawMessage(`{"redeem":{"max_codes_per_day":0,"max_balance_per_code":0}}`)
	require.NoError(t, f.store.Save(t.Context(), f.installation))
	issuedAt := f.now
	f.now = f.now.Add(2 * 24 * time.Hour)
	replayed, err := f.issuer.Issue(t.Context(), f.binding, input)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	f.now = issuedAt
	user, err := f.client.User.Create().SetEmail("code-recipient@example.invalid").SetPasswordHash("fixture").Save(t.Context())
	require.NoError(t, err)
	_, err = f.redeem.Redeem(t.Context(), user.ID, code)
	require.NoError(t, err)
	saved, err := f.client.User.Get(t.Context(), user.ID)
	require.NoError(t, err)
	require.Equal(t, float64(1), saved.Balance)
	require.Equal(t, float64(1), saved.TotalRecharged)
	require.Equal(t, 1, f.affiliate.calls)
	require.Equal(t, float64(1), f.affiliate.amount)
	_, err = f.redeem.Redeem(t.Context(), user.ID, code)
	require.Error(t, err)
	require.Equal(t, 1, f.affiliate.calls)
}

// TestPluginSubscriptionCodeFreezesDaysAndPreservesLegacyDefault issues a default-duration subscription code before extending the live plan.
// The plugin code retains seven days and replays without resolving new defaults, while a legacy NULL-duration code still receives the plan's new ninety days.
func TestPluginSubscriptionCodeFreezesDaysAndPreservesLegacyDefault(t *testing.T) {
	f := newPluginRedeemFixture(t)
	input := plugin.RedeemInput{Type: "subscription", PlanID: f.planID, Key: "fixed-default", Reason: "Trial"}
	_, err := f.issuer.Issue(t.Context(), f.binding, input)
	require.ErrorIs(t, err, plugin.ErrPlanNotAllowed)
	limits := plugin.DefaultLimits()
	limits.Redeem.AllowedPlanIDs = []int64{f.planID}
	f.installation.Limits, err = json.Marshal(limits)
	require.NoError(t, err)
	require.NoError(t, f.store.Save(t.Context(), f.installation))
	issued, err := f.issuer.Issue(t.Context(), f.binding, input)
	require.NoError(t, err)
	require.Equal(t, int32(7), issued.Days)
	stored, err := f.repo.GetByCode(t.Context(), issued.Code)
	require.NoError(t, err)
	require.NotNil(t, stored.ValidityDays)
	require.Equal(t, 7, *stored.ValidityDays)
	stored.Notes = "Operator note"
	require.NoError(t, f.repo.Update(t.Context(), stored))
	require.NoError(t, f.client.SubscriptionPlan.UpdateOneID(f.planID).SetValidityDays(90).Exec(t.Context()))
	replayed, err := f.issuer.Issue(t.Context(), f.binding, input)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, issued.Code, replayed.Code)
	require.Equal(t, int32(7), replayed.Days)
	input.Key = "new-default-too-long"
	_, err = f.issuer.Issue(t.Context(), f.binding, input)
	require.ErrorIs(t, err, plugin.ErrLimitExceeded)
	require.Equal(t, "redeem.max_subscription_days_per_code", apperror.FromError(err).Metadata["limit"])
	legacy := &billing.RedeemCode{Code: "LEGACY-SUB", Type: billing.RedeemTypeSubscription, PlanID: &f.planID, MaxUses: 1, Status: billing.StatusUnused}
	require.NoError(t, f.redeem.CreateCode(t.Context(), legacy))
	for _, tc := range []struct {
		code  string // redeemed subscription credential
		days  int    // expected assigned duration in days
		email string // unique fixture recipient mailbox
	}{
		{code: issued.Code, days: 7, email: "fixed-days@example.invalid"},
		{code: legacy.Code, days: 90, email: "legacy-days@example.invalid"},
	} {
		user, err := f.client.User.Create().SetEmail(tc.email).SetPasswordHash("fixture").Save(t.Context())
		require.NoError(t, err)
		_, err = f.redeem.Redeem(t.Context(), user.ID, tc.code)
		require.NoError(t, err)
		subscription, err := f.client.UserSubscription.Query().Where(usersubscription.UserIDEQ(user.ID)).Only(t.Context())
		require.NoError(t, err)
		require.Equal(t, subscription.StartsAt.AddDate(0, 0, tc.days), subscription.ExpiresAt)
	}
	legacy, err = f.repo.GetByCode(t.Context(), legacy.Code)
	require.NoError(t, err)
	require.Nil(t, legacy.ValidityDays)
}

// TestPluginRedeemQuotaExpiryAndLedgerFailureRollback exercises both daily limits and a rejected ledger insert after native code creation.
// Expiry validation and quota failures create no credential, concurrency keeps integer terms, and ledger failure rolls the deterministic code back with its transaction.
func TestPluginRedeemQuotaExpiryAndLedgerFailureRollback(t *testing.T) {
	f := newPluginRedeemFixture(t)
	f.installation.Limits = json.RawMessage(`{"redeem":{"max_balance_per_code":2,"max_balance_value_per_day":3,"max_codes_per_day":3,"max_concurrency_per_code":2}}`)
	require.NoError(t, f.store.Save(t.Context(), f.installation))
	input := plugin.RedeemInput{Type: "balance", Value: "2", Key: "first"}
	_, err := f.issuer.Issue(t.Context(), f.binding, input)
	require.NoError(t, err)
	input.Key = "daily-balance"
	_, err = f.issuer.Issue(t.Context(), f.binding, input)
	require.ErrorIs(t, err, plugin.ErrLimitExceeded)
	require.Equal(t, "redeem.max_balance_value_per_day", apperror.FromError(err).Metadata["limit"])
	input.Value = "3"
	_, err = f.issuer.Issue(t.Context(), f.binding, input)
	require.Equal(t, "redeem.max_balance_per_code", apperror.FromError(err).Metadata["limit"])
	input.Value = "1"
	for _, expires := range []int64{f.now.Unix(), f.now.Add(366 * 24 * time.Hour).Unix()} {
		input.ExpiresUnix = expires
		_, err = f.issuer.Issue(t.Context(), f.binding, input)
		require.ErrorIs(t, err, plugin.ErrInvalidArgument)
	}
	input.ExpiresUnix = 0
	input.Type = "concurrency"
	input.Value = "3"
	_, err = f.issuer.Issue(t.Context(), f.binding, input)
	require.Equal(t, "redeem.max_concurrency_per_code", apperror.FromError(err).Metadata["limit"])
	input.Value = "2"
	concurrency, err := f.issuer.Issue(t.Context(), f.binding, input)
	require.NoError(t, err)
	user, err := f.client.User.Create().SetEmail("concurrency-code@example.invalid").SetPasswordHash("fixture").SetConcurrency(3).Save(t.Context())
	require.NoError(t, err)
	_, err = f.redeem.Redeem(t.Context(), user.ID, concurrency.Code)
	require.NoError(t, err)
	user, err = f.client.User.Get(t.Context(), user.ID)
	require.NoError(t, err)
	require.Equal(t, 5, user.Concurrency)
	input.Type, input.Value, input.Key = "balance", "1", "rollback"
	// Reject the ledger after native code insertion; both writes must disappear before a retry could claim the same deterministic credential.
	_, err = f.db.ExecContext(t.Context(), `CREATE FUNCTION reject_plugin_code_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.idempotency_key='rollback' THEN RAISE EXCEPTION 'injected ledger failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER reject_plugin_code_fixture BEFORE INSERT ON plugin_ledger FOR EACH ROW EXECUTE FUNCTION reject_plugin_code_fixture();`)
	require.NoError(t, err)
	_, err = f.issuer.Issue(t.Context(), f.binding, input)
	require.Error(t, err)
	_, err = f.repo.GetByCode(t.Context(), f.grants.RedeemCode(f.binding.PluginID, input.Key))
	require.ErrorIs(t, err, billing.ErrRedeemCodeNotFound)
	input.Key = "third"
	_, err = f.issuer.Issue(t.Context(), f.binding, input)
	require.NoError(t, err)
	input.Type, input.Value, input.Key = "concurrency", "1", "count-limit"
	_, err = f.issuer.Issue(t.Context(), f.binding, input)
	require.Equal(t, "redeem.max_codes_per_day", apperror.FromError(err).Metadata["limit"])
}

// TestRedeemDurationMigrationKeepsExistingRowsNull recreates the pre-column schema around one existing subscription credential and reapplies migration 306.
// Legacy rows remain NULL, valid frozen durations survive repetition, and invalid day counts or non-subscription overrides are rejected by PostgreSQL itself.
func TestRedeemDurationMigrationKeepsExistingRowsNull(t *testing.T) {
	f := newPluginRedeemFixture(t)
	_, err := f.db.ExecContext(t.Context(), "ALTER TABLE redeem_codes DROP COLUMN validity_days")
	require.NoError(t, err)
	_, err = f.db.ExecContext(t.Context(), `INSERT INTO redeem_codes(code,type,value,status,max_uses,plan_id)
VALUES('LEGACY-BEFORE-MIGRATION','subscription',0,'unused',1,$1)`, f.planID)
	require.NoError(t, err)
	data, err := migrations.FS.ReadFile("306_redeem_code_validity_days.sql")
	require.NoError(t, err)
	_, err = f.db.ExecContext(t.Context(), string(data))
	require.NoError(t, err)
	var empty bool
	require.NoError(t, f.db.QueryRowContext(t.Context(), "SELECT validity_days IS NULL FROM redeem_codes").Scan(&empty))
	require.True(t, empty)
	_, err = f.db.ExecContext(t.Context(), "UPDATE redeem_codes SET validity_days=7")
	require.NoError(t, err)
	_, err = f.db.ExecContext(t.Context(), string(data))
	require.NoError(t, err)
	for _, statement := range []string{"UPDATE redeem_codes SET validity_days=0", "UPDATE redeem_codes SET validity_days=36501", "UPDATE redeem_codes SET type='balance'"} {
		_, err = f.db.ExecContext(t.Context(), statement)
		require.Error(t, err)
	}
}
