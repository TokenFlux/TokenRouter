//go:build integration

package postgres

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
	"github.com/TokenFlux/TokenRouter/migrations"
)

// pricing_config_models_integration_test.go checks reference storage on PostgreSQL.
// An isolated database verifies the additive migration against an existing card,
// then exercises reference writes, reads, updates, and configuration replacement
// through the production SQL store.

// TestPriceReferenceMigrationAndRoundTrip upgrades an existing price row and saves references.
// Old rows retain their amounts, and reference targets survive each SQL read path.
func TestPriceReferenceMigrationAndRoundTrip(t *testing.T) {
	db := postgrescontainer.New(t)
	ctx := t.Context()
	_, err := db.ExecContext(ctx, `ALTER TABLE pricing_config_model_pricing DROP COLUMN reference_model`)
	require.NoError(t, err)
	var configID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO pricing_configs(name) VALUES ('reference-test') RETURNING id`).Scan(&configID))
	_, err = db.ExecContext(ctx, `INSERT INTO pricing_config_model_pricing(pricing_config_id,models,billing_mode,input_price) VALUES ($1,'["existing"]','token',0)`, configID)
	require.NoError(t, err)
	migration, err := migrations.FS.ReadFile("303_model_price_references.sql")
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	_, err = tx.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	store := NewPricingConfigStore(db)
	old, err := store.ListModelPricing(ctx, configID)
	require.NoError(t, err)
	require.Empty(t, old[0].ReferenceModel)
	require.NotNil(t, old[0].InputPrice)
	require.Zero(t, *old[0].InputPrice)
	entry := routing.ModelPricingEntry{PricingConfigID: configID, Models: []string{"custom-*"}, ReferenceModel: "target"}
	require.NoError(t, store.CreateModelPricing(ctx, &entry))
	loaded, err := store.GetByID(ctx, configID)
	require.NoError(t, err)
	require.Equal(t, "target", loaded.ModelPricing[1].ReferenceModel)
	entry.ReferenceModel = "replacement"
	require.NoError(t, store.UpdateModelPricing(ctx, &entry))
	all, err := store.ListAll(ctx)
	require.NoError(t, err)
	require.Equal(t, "replacement", all[0].ModelPricing[1].ReferenceModel)
	require.NoError(t, store.ReplaceModelPricing(ctx, configID, []routing.ModelPricingEntry{entry}))
	rows, err := store.ListModelPricing(ctx, configID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "replacement", rows[0].ReferenceModel)
}

// TestGPTCacheWriteMigrationAndRoundTrip upgrades a preexisting uniform price
// and saves optional lifetime amounts through the real PostgreSQL reader and
// writer. It verifies that a new zero remains present and old prices retain NULLs.
func TestGPTCacheWriteMigrationAndRoundTrip(t *testing.T) {
	db := postgrescontainer.New(t)
	ctx := t.Context()
	tables := []string{
		"pricing_config_model_pricing",
		"pricing_config_pricing_intervals",
		"pricing_config_provider_stats_model_pricing",
		"pricing_config_provider_stats_pricing_intervals",
	}
	for _, table := range tables {
		_, err := db.ExecContext(ctx, "ALTER TABLE "+table+" DROP COLUMN cache_write_30m_price, DROP COLUMN cache_write_12h_price")
		require.NoError(t, err)
	}
	_, err := db.ExecContext(ctx, "ALTER TABLE usage_logs DROP COLUMN cache_creation_30m_tokens, DROP COLUMN cache_creation_12h_tokens")
	require.NoError(t, err)
	var configID int64
	require.NoError(t, db.QueryRowContext(ctx, "INSERT INTO pricing_configs(name) VALUES ('cache-ttl-test') RETURNING id").Scan(&configID))
	_, err = db.ExecContext(ctx, `INSERT INTO pricing_config_model_pricing(pricing_config_id,models,billing_mode,cache_write_price) VALUES ($1,'["legacy"]','token',0.000006)`, configID)
	require.NoError(t, err)

	migration, err := migrations.FS.ReadFile("314_gpt_cache_write_lifetimes.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migration))
	require.NoError(t, err)

	store := NewPricingConfigStore(db)
	rows, err := store.ListModelPricing(ctx, configID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, 6e-6, *rows[0].CacheWritePrice)
	require.Nil(t, rows[0].CacheWrite30mPrice)
	require.Nil(t, rows[0].CacheWrite12hPrice)
	entry := rows[0]
	entry.CacheWrite30mPrice = new(2.5e-6)
	entry.CacheWrite12hPrice = new(0.0)
	require.NoError(t, store.ReplaceModelPricing(ctx, configID, []routing.ModelPricingEntry{entry}))
	rows, err = store.ListModelPricing(ctx, configID)
	require.NoError(t, err)
	require.Equal(t, 2.5e-6, *rows[0].CacheWrite30mPrice)
	require.NotNil(t, rows[0].CacheWrite12hPrice)
	require.Zero(t, *rows[0].CacheWrite12hPrice)
}
