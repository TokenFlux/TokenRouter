//go:build integration

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/ent"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	pluginpostgres "github.com/TokenFlux/TokenRouter/internal/plugin/postgres"
	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// plugin_wallet_scenario_integration_test.go covers app/plugin_capabilities.go,
// plugin/wallet.go, plugin/writes.go, plugin/postgres/ledger.go, and the existing
// billing balance and redemption adapters. The scenario checks one transaction
// boundary together with its post-commit cache and user-history effects on a real
// PostgreSQL database, using synthetic grants and counted cache operations.

// pluginCacheProbe counts both post-commit invalidation paths without contacting a shared cache.
type pluginCacheProbe struct {
	auth    atomic.Int64 // authentication invalidation call count
	balance atomic.Int64 // funding invalidation call count
	fail    atomic.Bool  // injected funding invalidation failure
}

// InvalidateAuthCacheByUserID records one completed adjustment's authentication invalidation request.
func (p *pluginCacheProbe) InvalidateAuthCacheByUserID(context.Context, int64) { p.auth.Add(1) }

// InvalidateUserBalance records one funding invalidation and optionally injects an auxiliary failure.
func (p *pluginCacheProbe) InvalidateUserBalance(context.Context, int64) error {
	p.balance.Add(1)
	if p.fail.Load() {
		return errors.New("injected cache failure")
	}
	return nil
}

// TestPluginWalletAtomicReplayQuotasAndHistory sends fifty concurrent identical credits through real billing and PostgreSQL transactions.
// It protects one balance effect, unchanged cumulative recharge, exact quota admission, caller-bound debits, and post-commit history/cache behavior.
func TestPluginWalletAtomicReplayQuotasAndHistory(t *testing.T) {
	db := postgrescontainer.New(t)
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	user, err := client.User.Create().SetEmail("plugin-wallet@example.invalid").SetPasswordHash("fixture").SetBalance(10).SetTotalRecharged(99).Save(t.Context())
	require.NoError(t, err)
	store := pluginpostgres.New(client)
	declaration, err := manifest.Parse([]byte(`{"schemaVersion":1,"id":"test.wallet","version":"1.0.0","apiVersion":1,"minHostVersion":"0.5.0","name":{"en":"Wallet test"},"description":{"en":"Wallet test"},"publisher":{"name":"Test"},"license":"MIT","permissions":["wallet.credit","wallet.debit"],"backend":{"executable":"bin/plugin"}}`))
	require.NoError(t, err)
	installation := &plugin.Installation{
		PluginID: "test.wallet",
		Enabled:  true,
		VersionSnapshot: plugin.VersionSnapshot{
			Version:            "1.0.0",
			Source:             plugin.SourceUpload,
			Manifest:           declaration,
			ManifestSHA256:     strings.Repeat("a", 64),
			GrantedPermissions: declaration.Permissions,
		},
	}
	require.NoError(t, store.Save(t.Context(), installation))
	observedAt := time.Now()
	accountingAt := observedAt
	grants, err := plugin.NewGrants("persistent-wallet-test-key", func() time.Time { return observedAt })
	require.NoError(t, err)
	cache := &pluginCacheProbe{}
	records := provideRedeemAdministration(billingpostgres.NewRedeemCodeRepository(client), client)
	effects := &pluginWalletEffects{balances: billingpostgres.NewBalanceStore(client), records: records, invalidator: cache, cache: cache}
	wallet := plugin.NewWalletService(plugin.NewWrites(store, store, func() time.Time { return accountingAt }), grants, effects, time.UTC, slog.New(slog.NewTextHandler(io.Discard, nil)))
	binding := plugin.Binding{PluginID: "test.wallet", Version: "1.0.0", Permissions: []string{"wallet.credit", "wallet.debit"}}
	input := plugin.WalletInput{UserID: user.ID, Amount: "1.00000000", Key: "same-credit", Reason: "Concurrent test credit"}
	results := make(chan *plugin.WalletChange, 50)
	failures := make(chan error, 50)
	var workers sync.WaitGroup
	for range 50 {
		workers.Go(func() {
			result, err := wallet.Credit(t.Context(), binding, input)
			if err != nil {
				failures <- err
				return
			}
			results <- result
		})
	}
	workers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	replays := 0
	for result := range results {
		require.Equal(t, "10.00000000", result.Before)
		require.Equal(t, "11.00000000", result.After)
		if result.Replayed {
			replays++
		}
	}
	require.Equal(t, 49, replays)
	require.Equal(t, int64(1), cache.auth.Load())
	require.Equal(t, int64(1), cache.balance.Load())
	var balance, recharged float64
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT balance,total_recharged FROM users WHERE id=$1", user.ID).Scan(&balance, &recharged))
	require.Equal(t, 11.0, balance)
	require.Equal(t, 99.0, recharged)
	var historyCount int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM redeem_codes WHERE type='plugin_balance' AND used_by=$1", user.ID).Scan(&historyCount))
	require.Equal(t, 1, historyCount)
	changed := input
	changed.Amount = "0.5"
	_, err = wallet.Credit(t.Context(), binding, changed)
	require.ErrorIs(t, err, plugin.ErrInvalidArgument)
	accepted, rejected := 0, 0
	for i := range 10 {
		next := input
		next.Key = fmt.Sprintf("daily-%d", i)
		_, err := wallet.Credit(t.Context(), binding, next)
		if err == nil {
			accepted++
		} else {
			require.ErrorIs(t, err, plugin.ErrLimitExceeded)
			rejected++
		}
	}
	require.Equal(t, 4, accepted)
	require.Equal(t, 6, rejected)
	debit := plugin.WalletInput{UserID: user.ID, Amount: "2", Key: "purchase", Reason: "Test purchase"}
	_, err = wallet.Debit(t.Context(), binding, debit)
	require.ErrorIs(t, err, plugin.ErrGrantInvalid)
	debit.Grant, err = grants.Issue("test.wallet", user.ID+1)
	require.NoError(t, err)
	_, err = wallet.Debit(t.Context(), binding, debit)
	require.ErrorIs(t, err, plugin.ErrGrantInvalid)
	debit.Grant, err = grants.Issue("test.other", user.ID)
	require.NoError(t, err)
	_, err = wallet.Debit(t.Context(), binding, debit)
	require.ErrorIs(t, err, plugin.ErrGrantInvalid)
	debit.Grant, err = grants.Issue("test.wallet", user.ID)
	require.NoError(t, err)
	conflict := debit
	conflict.Key = input.Key
	conflict.Amount = input.Amount
	conflict.Reason = input.Reason
	_, err = wallet.Debit(t.Context(), binding, conflict)
	require.ErrorIs(t, err, plugin.ErrInvalidArgument)
	debited, err := wallet.Debit(t.Context(), binding, debit)
	require.NoError(t, err)
	require.Equal(t, "13.00000000", debited.After)
	observedAt = observedAt.Add(301 * time.Second)
	_, err = wallet.Debit(t.Context(), binding, debit)
	require.ErrorIs(t, err, plugin.ErrGrantInvalid)
	debit.Grant, err = grants.Issue("test.wallet", user.ID)
	require.NoError(t, err)
	replayed, err := wallet.Debit(t.Context(), binding, debit)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, debited.After, replayed.After)
	insufficient := debit
	insufficient.Key = "insufficient"
	insufficient.Amount = "100"
	_, err = wallet.Debit(t.Context(), binding, insufficient)
	require.ErrorIs(t, err, plugin.ErrInsufficientBalance)
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT balance FROM users WHERE id=$1", user.ID).Scan(&balance))
	require.Equal(t, 13.0, balance)
	limits := plugin.DefaultLimits()
	limits.Wallet.MaxCreditPerOp = "0"
	installation.Limits, err = json.Marshal(limits)
	require.NoError(t, err)
	require.NoError(t, store.Save(t.Context(), installation))
	replayed, err = wallet.Credit(t.Context(), binding, input)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	cache.fail.Store(true)
	debit.Key = "cache-failure"
	debit.Amount = "1"
	_, err = wallet.Debit(t.Context(), binding, debit)
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT balance,total_recharged FROM users WHERE id=$1", user.ID).Scan(&balance, &recharged))
	require.Equal(t, 12.0, balance)
	require.Equal(t, 99.0, recharged)
	require.Equal(t, int64(7), cache.auth.Load())
	require.Equal(t, int64(7), cache.balance.Load())
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM redeem_codes WHERE type='plugin_balance' AND used_by=$1", user.ID).Scan(&historyCount))
	require.Equal(t, 7, historyCount)
	var ledgerCount int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM plugin_ledger WHERE plugin_id='test.wallet'").Scan(&ledgerCount))
	require.Equal(t, 7, ledgerCount)

	// Reject ledger persistence after the balance statement to prove that business effects and replay evidence roll back together on the same connection.
	_, err = db.ExecContext(t.Context(), `CREATE FUNCTION reject_plugin_ledger_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.idempotency_key='ledger-failure' THEN RAISE EXCEPTION 'injected ledger failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_plugin_ledger_fixture BEFORE INSERT ON plugin_ledger FOR EACH ROW EXECUTE FUNCTION reject_plugin_ledger_fixture();`)
	require.NoError(t, err)
	debit.Key = "ledger-failure"
	_, err = wallet.Debit(t.Context(), binding, debit)
	require.Error(t, err)
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT balance FROM users WHERE id=$1", user.ID).Scan(&balance))
	require.Equal(t, 12.0, balance)
	require.Equal(t, int64(7), cache.auth.Load())
	require.Equal(t, int64(7), cache.balance.Load())
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT count(*) FROM plugin_ledger WHERE plugin_id='test.wallet'").Scan(&ledgerCount))
	require.Equal(t, 7, ledgerCount)
}
