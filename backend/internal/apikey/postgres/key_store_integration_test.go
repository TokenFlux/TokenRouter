//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"testing"
	"testing/fstest"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/migrations"
)

// key_store_integration_test.go verifies migration 309 against
// PostgreSQL and the production KeyStore. Existing credentials retain disabled
// protection, new credentials persist their ceiling, and edits invalidate
// authentication snapshots while enforcing database bounds and safe migration replay.

// tokenRateGroupStore reads changed group rates from the integration database.
type tokenRateGroupStore struct {
	apikey.GroupRepository         // unused group repository operations
	db                     *sql.DB // test-owned PostgreSQL connection pool
}

// GetByIDLite reads the current persisted multiplier for protected request admission.
// The caller owns the SQL pool and supplies an existing group; lookup errors
// prevent the price guard from admitting an unverifiable request.
func (s tokenRateGroupStore) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	group := &routing.Group{}
	err := s.db.QueryRowContext(ctx, `SELECT id, status, rate_multiplier FROM groups WHERE id=$1`, id).
		Scan(&group.ID, &group.Status, &group.RateMultiplier)
	if err != nil {
		return nil, err
	}
	return group, nil
}

// TestAPIKeyTokenRateLimitMigration upgrades an existing credential and exercises
// production storage reads and updates. It checks defaults, fractional values,
// omitted updates, explicit clearing, invalidation, and rejected database values.
// An administrator price increase rejects the previously saved 0.1-ceiling Key
// while preserving that ceiling and reporting both rates to its user.
func TestAPIKeyTokenRateLimitMigration(t *testing.T) {
	ctx := t.Context()
	pg, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23", tcpostgres.WithDatabase("key_token_rate"), tcpostgres.WithUsername("postgres"), tcpostgres.WithPassword("fixture"), tcpostgres.BasicWaitStrategies())
	// Startup can return a partially created container with an error. Register
	// independent cleanup before assertions so cancellation still removes that resource.
	if pg != nil {
		t.Cleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			require.NoError(t, pg.Terminate(cleanupCtx))
		})
	}
	require.NoError(t, err)
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	// Register SQL closure after container cleanup so test cleanup releases the
	// pool first, including when a later migration or persistence assertion fails.
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	legacy := fstest.MapFS{}
	entries, err := migrations.FS.ReadDir(".")
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.Name() == "309_api_key_token_rate_limit.sql" {
			continue
		}
		content, err := migrations.FS.ReadFile(entry.Name())
		require.NoError(t, err)
		legacy[entry.Name()] = &fstest.MapFile{Data: content}
	}
	require.NoError(t, postgres.ApplyMigrations(ctx, db, legacy))
	var userID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO users(email,password_hash) VALUES('token-limit@example.test','fixture') RETURNING id`).Scan(&userID))
	_, err = db.ExecContext(ctx, `INSERT INTO api_keys(user_id,key,name) VALUES($1,'sk-legacy-token-rate','legacy')`, userID)
	require.NoError(t, err)
	require.NoError(t, postgres.ApplyMigrations(ctx, db, migrations.FS))
	require.NoError(t, postgres.ApplyMigrations(ctx, db, migrations.FS))
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	store := NewKeyStore(client, db, nil)
	key, err := store.GetByKeyForAuth(ctx, "sk-legacy-token-rate")
	require.NoError(t, err)
	require.Zero(t, key.MaxTokenRateMultiplier)
	var groupID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO groups(name,rate_multiplier) VALUES('protected-group',0.1) RETURNING id`).Scan(&groupID))
	created := &apikey.APIKey{UserID: userID, GroupID: &groupID, Key: "sk-new-token-rate", Name: "limited", Status: apikey.StatusActive, BillingMode: apikey.APIKeyBillingModeBalance, FastModePolicy: apikey.APIKeyFastModePolicyFollowRequest, MaxTokenRateMultiplier: .1}
	require.NoError(t, store.Create(ctx, created))
	loaded, err := store.GetByKeyForAuth(ctx, created.Key)
	require.NoError(t, err)
	require.Equal(t, .1, loaded.MaxTokenRateMultiplier)
	keys := apikey.NewAPIKeyService(store, nil, tokenRateGroupStore{db: db}, nil, nil, nil, nil)
	t.Cleanup(keys.Stop)
	require.NoError(t, keys.CheckTokenRateLimit(ctx, loaded))
	_, err = db.ExecContext(ctx, `UPDATE groups SET rate_multiplier=0.2 WHERE id=$1`, groupID)
	require.NoError(t, err)
	failure := keys.CheckTokenRateLimit(ctx, loaded)
	require.ErrorIs(t, failure, apikey.ErrTokenRateLimitExceeded)
	require.Equal(t, "Token multiplier 0.2x exceeds API key limit 0.1x. Check group pricing or update the key limit.", apperror.Message(failure))
	require.Equal(t, .1, loaded.MaxTokenRateMultiplier)
	loaded.Name = "renamed"
	require.NoError(t, store.Update(ctx, loaded, apikey.APIKeyUpdateFields{Name: true}))
	loaded, err = store.GetByKeyForAuth(ctx, created.Key)
	require.NoError(t, err)
	require.Equal(t, .1, loaded.MaxTokenRateMultiplier)
	_, err = db.ExecContext(ctx, `TRUNCATE auth_cache_invalidation_outbox`)
	require.NoError(t, err)
	loaded.MaxTokenRateMultiplier = 0
	require.NoError(t, store.Update(ctx, loaded, apikey.APIKeyUpdateFields{MaxTokenRateMultiplier: true}))
	loaded, err = store.GetByKeyForAuth(ctx, created.Key)
	require.NoError(t, err)
	require.Zero(t, loaded.MaxTokenRateMultiplier)
	var invalidations int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM auth_cache_invalidation_outbox`).Scan(&invalidations))
	require.Positive(t, invalidations)
	for _, invalid := range []string{"-1", "NaN", "Infinity", "1000000000000"} {
		_, err := db.ExecContext(ctx, `UPDATE api_keys SET max_token_rate_multiplier=$1::double precision WHERE id=$2`, invalid, created.ID)
		require.Error(t, err, invalid)
	}
}
