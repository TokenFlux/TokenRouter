//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/migrations"
)

// schema_integration_test.go applies embedded migration history before assertions inspect
// PostgreSQL catalogs. These checks compare selected current columns, named
// indexes, foreign-key actions, and definition fragments, while separate runner
// calls exercise re-entry and two concurrent callers on the already migrated
// shared pool. Historical data transformations are exercised by the dedicated
// migration fixtures rather than inferred from these catalog checks.

// TestMigrationsRunner_ConcurrentInstancesSerializeOnSessionLock calls the
// runner from two goroutines sharing the migrated fixture pool. It waits for
// both calls and requires each result to succeed within its own context budget.
func TestMigrationsRunner_ConcurrentInstancesSerializeOnSessionLock(t *testing.T) {
	// Two runner calls share one pool, reproducing concurrent entry without
	// adding pending SQL to the schema already initialized by TestMain.
	const instances = 2
	errorsByInstance := make([]error, instances)
	var wg sync.WaitGroup
	// Each worker writes a different result slot and signals completion
	// afterward. Wait before reading those slots so assertions observe
	// completed calls rather than racing their writes.
	for i := range instances {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			// Give each runner its own thirty-second context so a timeout in
			// one call does not cancel the other call's session-lock wait.
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			errorsByInstance[index] = postgresinfra.ApplyMigrations(ctx, integrationDB, migrations.FS)
		}(i)
	}
	wg.Wait()
	for i, err := range errorsByInstance {
		require.NoErrorf(t, err, "migration instance %d", i)
	}
}

// TestMigrationsRunner_IsIdempotent_AndSchemaIsUpToDate reapplies the embedded
// history to the initialized database. It checks a migration-count floor and
// selected current schema objects and retired columns through PostgreSQL
// catalogs.
func TestMigrationsRunner_IsIdempotent_AndSchemaIsUpToDate(t *testing.T) {
	tx := testTx(t)

	// ApplyMigrations uses its own reserved session rather than the
	// observation transaction; re-entry checks recorded files before these
	// catalog assertions inspect the shared database.
	require.NoError(t, postgresinfra.ApplyMigrations(context.Background(), integrationDB, migrations.FS))

	// The count floor catches an empty or severely incomplete migration
	// ledger. It deliberately does not compare the ledger with the complete
	// embedded filename set.
	var applied int
	require.NoError(t, tx.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM schema_migrations").Scan(&applied))
	require.GreaterOrEqual(t, applied, 7, "expected schema_migrations to contain applied migrations")

	// User stores read username and notes from these columns.
	requireColumn(t, tx, "users", "username", "character varying", 100, false)
	requireColumn(t, tx, "users", "notes", "text", 0, false)

	// Provider scheduling requires status, rate-limit timing, and
	// session-window fields.
	requireColumn(t, tx, "providers", "notes", "text", 0, true)
	requireColumn(t, tx, "providers", "schedulable", "boolean", 0, false)
	requireColumn(t, tx, "providers", "rate_limited_at", "timestamp with time zone", 0, true)
	requireColumn(t, tx, "providers", "rate_limit_reset_at", "timestamp with time zone", 0, true)
	requireColumn(t, tx, "providers", "overload_until", "timestamp with time zone", 0, true)
	requireColumn(t, tx, "providers", "session_window_status", "character varying", 20, true)
	requireIndex(t, tx, "providers", "idx_providers_autopause_expiry_due")

	// Live access belongs to groups; free Fast pricing belongs to
	// pricing_configs.
	requireColumn(t, tx, "groups", "allow_live", "boolean", 0, false)
	requireNoColumn(t, tx, "groups", "free_openai_fast")
	requireColumn(t, tx, "pricing_configs", "free_openai_fast", "boolean", 0, false)

	// API keys support up to 128 characters.
	requireColumn(t, tx, "api_keys", "key", "character varying", 128, false)

	// Subscription plans store daily, weekly, and monthly allowances.
	requireColumn(t, tx, "subscription_plans", "daily_limit_usd", "numeric", 0, true)
	requireColumn(t, tx, "subscription_plans", "weekly_limit_usd", "numeric", 0, true)
	requireColumn(t, tx, "subscription_plans", "monthly_limit_usd", "numeric", 0, true)
	requireNoColumn(t, tx, "subscription_plans", "group_id")

	// Subscriptions retain plan-specific allowances and source-order
	// attribution.
	requireColumn(t, tx, "user_subscriptions", "plan_id", "bigint", 0, false)
	requireColumn(t, tx, "user_subscriptions", "daily_limit_usd", "numeric", 0, true)
	requireColumn(t, tx, "user_subscriptions", "weekly_limit_usd", "numeric", 0, true)
	requireColumn(t, tx, "user_subscriptions", "monthly_limit_usd", "numeric", 0, true)
	requireColumn(t, tx, "user_subscriptions", "source_order_id", "bigint", 0, true)
	requireNoColumn(t, tx, "user_subscriptions", "group_id")
	requireIndex(t, tx, "user_subscriptions", "idx_user_subscriptions_plan_id")
	requireIndex(t, tx, "user_subscriptions", "idx_user_subscriptions_source_order_id")
	requireIndex(t, tx, "user_subscriptions", "idx_user_subscriptions_user_status_starts_expires")
	requireIndex(t, tx, "user_subscriptions", "idx_user_subscriptions_user_plan_starts")

	// Batch image jobs snapshot the internal model and separate balance and
	// subscription holds.
	requireColumn(t, tx, "batch_image_jobs", "internal_model", "character varying", 128, false)
	requireColumn(t, tx, "batch_image_jobs", "balance_hold_amount", "numeric", 0, false)
	requireColumn(t, tx, "batch_image_jobs", "subscription_hold_allocations", "jsonb", 0, false)
	requireColumn(t, tx, "batch_image_jobs", "subscription_rate_multiplier", "numeric", 0, false)
	requireColumn(t, tx, "batch_image_jobs", "balance_rate_multiplier", "numeric", 0, false)
	requireColumn(t, tx, "batch_image_jobs", "plan_group_rate_multiplier_enabled", "boolean", 0, false)

	// Subscription payment orders retain their plan snapshot.
	requireColumn(t, tx, "payment_orders", "plan_id", "bigint", 0, true)
	requireColumn(t, tx, "payment_orders", "plan_snapshot", "jsonb", 0, true)
	requireNoColumn(t, tx, "payment_orders", "subscription_group_id")
	requireNoColumn(t, tx, "payment_orders", "subscription_days")

	// Redeem codes retain nullable fixed subscription days for plugin issuance;
	// legacy NULL records continue using the plan duration at redemption.
	requireColumn(t, tx, "redeem_codes", "plan_id", "bigint", 0, true)
	requireNoColumn(t, tx, "redeem_codes", "group_id")
	requireColumn(t, tx, "redeem_codes", "validity_days", "integer", 0, true)
	requireIndex(t, tx, "redeem_codes", "idx_redeem_codes_plan_id")

	// Usage records retain billing type for filtering and aggregation.
	requireColumn(t, tx, "usage_logs", "billing_type", "smallint", 0, false)
	requireColumn(t, tx, "usage_logs", "request_type", "smallint", 0, false)
	requireColumn(t, tx, "usage_logs", "openai_ws_mode", "boolean", 0, false)
	requireColumn(t, tx, "usage_logs", "subscription_amount_usd", "numeric", 0, false)
	requireColumn(t, tx, "usage_logs", "balance_amount_usd", "numeric", 0, false)
	requireColumn(t, tx, "usage_logs", "billing_allocations", "jsonb", 0, true)
	requireColumn(t, tx, "usage_logs", "image_input_size", "character varying", 32, true)
	requireColumn(t, tx, "usage_logs", "image_output_size", "character varying", 32, true)
	requireColumn(t, tx, "usage_logs", "image_size_source", "character varying", 16, true)
	requireColumn(t, tx, "usage_logs", "image_size_breakdown", "jsonb", 0, true)
	requireColumn(t, tx, "usage_logs", "video_count", "integer", 0, false)
	requireColumn(t, tx, "usage_logs", "video_resolution", "character varying", 50, true)
	requireColumn(t, tx, "usage_logs", "video_duration_seconds", "integer", 0, true)
	// System logs support filtering by API key and creation time.
	requireColumn(t, tx, "ops_system_logs", "api_key_id", "bigint", 0, true)
	requireIndex(t, tx, "ops_system_logs", "idx_ops_system_logs_api_key_id_created_at")
	// Retired data-sharing tables and fields must be absent.
	for _, table := range []string{"data_share_sessions", "data_share_export_artifacts"} {
		var regclass sql.NullString
		require.NoError(t, tx.QueryRowContext(context.Background(), "SELECT to_regclass('public.' || $1)", table).Scan(&regclass))
		require.Falsef(t, regclass.Valid, "expected %s table to be absent", table)
	}
	requireNoColumn(t, tx, "groups", "data_sharing_enabled")
	requireNoColumn(t, tx, "api_keys", "data_sharing_notice_version")
	requireNoColumn(t, tx, "api_keys", "data_sharing_confirmed_group_id")
	requireNoColumn(t, tx, "api_keys", "data_sharing_confirmed_at")
	requireNoColumn(t, tx, "api_key_composite_groups", "data_sharing_notice_version")
	requireNoColumn(t, tx, "api_key_composite_groups", "data_sharing_confirmed_at")
	requireColumn(t, tx, "ops_metrics_hourly", "ttft_sample_count", "bigint", 0, false)
	requireColumn(t, tx, "ops_metrics_daily", "ttft_sample_count", "bigint", 0, false)
	requireConstraintDefinitionContains(
		t,
		tx,
		"usage_logs",
		"usage_logs_image_size_source_check",
		"image_size_source",
		"'output'",
		"'input'",
		"'default'",
		"'legacy'",
	)
	requireConstraintDefinitionContains(
		t,
		tx,
		"usage_logs",
		"usage_logs_image_billing_size_check",
		"image_count",
		"billing_mode",
		"'video'",
		"video_count",
		"image_size IS NOT NULL",
		"'1K'",
		"'2K'",
		"'4K'",
		"'mixed'",
	)

	// Subscription settings belong to plans after migration.
	requireNoColumn(t, tx, "groups", "subscription_type")
	requireNoColumn(t, tx, "groups", "daily_limit_usd")
	requireNoColumn(t, tx, "groups", "weekly_limit_usd")
	requireNoColumn(t, tx, "groups", "monthly_limit_usd")
	requireNoColumn(t, tx, "groups", "default_validity_days")

	// Pending scheduler events have a deduplication key and index.
	requireColumn(t, tx, "scheduler_outbox", "dedup_key", "text", 0, true)
	requireIndex(t, tx, "scheduler_outbox", "idx_scheduler_outbox_pending_dedup_key")

	// Billing deduplication records retain the request fingerprint and
	// lookup indexes.
	var usageBillingDedupRegclass sql.NullString
	require.NoError(t, tx.QueryRowContext(context.Background(), "SELECT to_regclass('public.usage_billing_dedup')").Scan(&usageBillingDedupRegclass))
	require.True(t, usageBillingDedupRegclass.Valid, "expected usage_billing_dedup table to exist")
	requireColumn(t, tx, "usage_billing_dedup", "request_fingerprint", "character varying", 64, false)
	requireIndex(t, tx, "usage_billing_dedup", "idx_usage_billing_dedup_request_api_key")
	requireIndex(t, tx, "usage_billing_dedup", "idx_usage_billing_dedup_created_at_brin")

	var usageBillingDedupArchiveRegclass sql.NullString
	require.NoError(t, tx.QueryRowContext(context.Background(), "SELECT to_regclass('public.usage_billing_dedup_archive')").Scan(&usageBillingDedupArchiveRegclass))
	require.True(t, usageBillingDedupArchiveRegclass.Valid, "expected usage_billing_dedup_archive table to exist")
	requireColumn(t, tx, "usage_billing_dedup_archive", "request_fingerprint", "character varying", 64, false)
	requireIndex(t, tx, "usage_billing_dedup_archive", "usage_billing_dedup_archive_pkey")

	// Runtime settings are persisted in settings.
	var settingsRegclass sql.NullString
	require.NoError(t, tx.QueryRowContext(context.Background(), "SELECT to_regclass('public.settings')").Scan(&settingsRegclass))
	require.True(t, settingsRegclass.Valid, "expected settings table to exist")

	// Server-side secrets are persisted in security_secrets.
	var securitySecretsRegclass sql.NullString
	require.NoError(t, tx.QueryRowContext(context.Background(), "SELECT to_regclass('public.security_secrets')").Scan(&securitySecretsRegclass))
	require.True(t, securitySecretsRegclass.Valid, "expected security_secrets table to exist")

	// Ingress rejection counts are indexed by time bucket and client IP.
	requireColumn(t, tx, "ops_ingress_reject_aggregates", "bucket_start", "timestamp with time zone", 0, false)
	requireColumn(t, tx, "ops_ingress_reject_aggregates", "client_ip", "inet", 0, false)
	requireColumn(t, tx, "ops_ingress_reject_aggregates", "request_count", "bigint", 0, false)
	requireIndex(t, tx, "ops_ingress_reject_aggregates", "idx_ops_ingress_reject_aggregates_bucket")
	requireIndex(t, tx, "ops_ingress_reject_aggregates", "idx_ops_ingress_reject_aggregates_ip_bucket")
	// User group grants are persisted in user_allowed_groups.
	var uagRegclass sql.NullString
	require.NoError(t, tx.QueryRowContext(context.Background(), "SELECT to_regclass('public.user_allowed_groups')").Scan(&uagRegclass))
	require.True(t, uagRegclass.Valid, "expected user_allowed_groups table to exist")

	// Public groups disabled by a user are persisted in
	// user_disabled_public_groups.
	var udpgRegclass sql.NullString
	require.NoError(t, tx.QueryRowContext(context.Background(), "SELECT to_regclass('public.user_disabled_public_groups')").Scan(&udpgRegclass))
	require.True(t, udpgRegclass.Valid, "expected user_disabled_public_groups table to exist")

	// Subscriptions retain a nullable soft-deletion timestamp.
	requireColumn(t, tx, "user_subscriptions", "deleted_at", "timestamp with time zone", 0, true)

	// Orphaned group grants have an audit table.
	var orphanAuditRegclass sql.NullString
	require.NoError(t, tx.QueryRowContext(context.Background(), "SELECT to_regclass('public.orphan_allowed_groups_audit')").Scan(&orphanAuditRegclass))
	require.True(t, orphanAuditRegclass.Valid, "expected orphan_allowed_groups_audit table to exist")

	// Provider-group creation timestamps include a timezone.
	requireColumn(t, tx, "provider_groups", "created_at", "timestamp with time zone", 0, false)

	// User group preference timestamps include a timezone.
	requireColumn(t, tx, "user_allowed_groups", "created_at", "timestamp with time zone", 0, false)
	requireColumn(t, tx, "user_disabled_public_groups", "created_at", "timestamp with time zone", 0, false)
	requireIndex(t, tx, "user_disabled_public_groups", "idx_user_disabled_public_groups_group_id")
}

// TestMigrationsRunner_AuthIdentityAndPaymentSchemaStayAligned inspects the
// migrated identity and payment schema. It checks report width, signup
// metadata, foreign-key deletion actions, payment index uniqueness and
// fragments, and absence of the retired index.
func TestMigrationsRunner_AuthIdentityAndPaymentSchemaStayAligned(t *testing.T) {
	tx := testTx(t)

	requireColumn(t, tx, "auth_identity_migration_reports", "report_type", "character varying", 80, false)
	requireColumn(t, tx, "users", "signup_source", "character varying", 20, false)
	requireColumnDefaultContains(t, tx, "users", "signup_source", "email")
	requireConstraintDefinitionContains(
		t,
		tx,
		"users",
		"users_signup_source_check",
		"signup_source",
		"'email'",
		"'linuxdo'",
		"'wechat'",
		"'oidc'",
		"'github'",
		"'google'",
	)

	requireForeignKeyOnDelete(t, tx, "auth_identities", "user_id", "users", "CASCADE")
	requireForeignKeyOnDelete(t, tx, "auth_identity_channels", "identity_id", "auth_identities", "CASCADE")
	requireForeignKeyOnDelete(t, tx, "pending_auth_sessions", "target_user_id", "users", "SET NULL")
	requireForeignKeyOnDelete(t, tx, "identity_adoption_decisions", "pending_auth_session_id", "pending_auth_sessions", "CASCADE")
	requireForeignKeyOnDelete(t, tx, "identity_adoption_decisions", "identity_id", "auth_identities", "SET NULL")

	requireIndex(t, tx, "payment_orders", "paymentorder_out_trade_no")
	requirePartialUniqueIndexDefinition(t, tx, "payment_orders", "paymentorder_out_trade_no", "out_trade_no", "WHERE")
	requireIndexAbsent(t, tx, "payment_orders", "paymentorder_out_trade_no_unique")
}

// requireIndex requires a named index on table in the public schema. It queries
// pg_indexes through the supplied transaction and fails the calling test when
// the catalog lookup or presence assertion fails.
func requireIndex(t *testing.T, tx *sql.Tx, table, index string) {
	t.Helper()

	var exists bool
	err := tx.QueryRowContext(context.Background(), `
SELECT EXISTS (
	SELECT 1
	FROM pg_indexes
	WHERE schemaname = 'public'
	  AND tablename = $1
	  AND indexname = $2
)
`, table, index).Scan(&exists)
	require.NoError(t, err, "query pg_indexes for %s.%s", table, index)
	require.True(t, exists, "expected index %s on %s", index, table)
}

// requireIndexAbsent requires a named index to be absent from table in public.
// The supplied transaction observes the fixture schema, and a lookup error
// fails the test before the absence assertion.
func requireIndexAbsent(t *testing.T, tx *sql.Tx, table, index string) {
	t.Helper()

	var exists bool
	err := tx.QueryRowContext(context.Background(), `
SELECT EXISTS (
	SELECT 1
	FROM pg_indexes
	WHERE schemaname = 'public'
	  AND tablename = $1
	  AND indexname = $2
)
`, table, index).Scan(&exists)
	require.NoError(t, err, "query pg_indexes for %s.%s", table, index)
	require.False(t, exists, "expected index %s on %s to be absent", index, table)
}

// requirePartialUniqueIndexDefinition requires uniqueness and each supplied
// text fragment in a public table's index definition. Callers include the
// predicate fragments they intend to check; the helper does not execute
// duplicate writes or independently inspect a partial-index predicate.
func requirePartialUniqueIndexDefinition(t *testing.T, tx *sql.Tx, table, index string, fragments ...string) {
	t.Helper()

	var (
		unique bool
		def    string
	)

	err := tx.QueryRowContext(context.Background(), `
SELECT
	i.indisunique,
	pg_get_indexdef(i.indexrelid)
FROM pg_class idx
JOIN pg_index i ON i.indexrelid = idx.oid
JOIN pg_class tbl ON tbl.oid = i.indrelid
JOIN pg_namespace ns ON ns.oid = tbl.relnamespace
WHERE ns.nspname = 'public'
  AND tbl.relname = $1
  AND idx.relname = $2
`, table, index).Scan(&unique, &def)
	require.NoError(t, err, "query index definition for %s.%s", table, index)
	require.True(t, unique, "expected index %s on %s to be unique", index, table)

	for _, fragment := range fragments {
		require.Contains(t, def, fragment, "expected index definition for %s.%s to contain %q", table, index, fragment)
	}
}

// requireForeignKeyOnDelete compares the deletion action of a matching public
// foreign key with expected. It selects one matching constraint for table,
// column, and refTable, so callers use a fixture with the intended unambiguous
// relationship.
func requireForeignKeyOnDelete(t *testing.T, tx *sql.Tx, table, column, refTable, expected string) {
	t.Helper()

	var actual string
	err := tx.QueryRowContext(context.Background(), `
SELECT CASE c.confdeltype
	WHEN 'a' THEN 'NO ACTION'
	WHEN 'r' THEN 'RESTRICT'
	WHEN 'c' THEN 'CASCADE'
	WHEN 'n' THEN 'SET NULL'
	WHEN 'd' THEN 'SET DEFAULT'
END
FROM pg_constraint c
JOIN pg_class tbl ON tbl.oid = c.conrelid
JOIN pg_namespace ns ON ns.oid = tbl.relnamespace
JOIN pg_class ref_tbl ON ref_tbl.oid = c.confrelid
JOIN pg_attribute attr ON attr.attrelid = tbl.oid AND attr.attnum = ANY(c.conkey)
WHERE ns.nspname = 'public'
  AND c.contype = 'f'
  AND tbl.relname = $1
  AND attr.attname = $2
  AND ref_tbl.relname = $3
LIMIT 1
`, table, column, refTable).Scan(&actual)
	require.NoError(t, err, "query foreign key action for %s.%s -> %s", table, column, refTable)
	require.Equal(t, expected, actual, "unexpected ON DELETE action for %s.%s -> %s", table, column, refTable)
}

// requireConstraintDefinitionContains reads a named constraint definition on a
// public table and requires each supplied fragment. Fragments are checked as
// case-sensitive substrings, giving callers structural evidence for the
// selected definition.
func requireConstraintDefinitionContains(t *testing.T, tx *sql.Tx, table, constraint string, fragments ...string) {
	t.Helper()

	var def string
	err := tx.QueryRowContext(context.Background(), `
SELECT pg_get_constraintdef(c.oid)
FROM pg_constraint c
JOIN pg_class tbl ON tbl.oid = c.conrelid
JOIN pg_namespace ns ON ns.oid = tbl.relnamespace
WHERE ns.nspname = 'public'
  AND tbl.relname = $1
  AND c.conname = $2
`, table, constraint).Scan(&def)
	require.NoError(t, err, "query constraint definition for %s.%s", table, constraint)

	for _, fragment := range fragments {
		require.Contains(t, def, fragment, "expected constraint definition for %s.%s to contain %q", table, constraint, fragment)
	}
}

// requireColumnDefaultContains requires a nonnull stored default expression for
// a public column and checks each supplied substring. It inspects the catalog's
// expression text rather than inserting a row to evaluate the default.
func requireColumnDefaultContains(t *testing.T, tx *sql.Tx, table, column string, fragments ...string) {
	t.Helper()

	var columnDefault sql.NullString
	err := tx.QueryRowContext(context.Background(), `
SELECT column_default
FROM information_schema.columns
WHERE table_schema = 'public'
  AND table_name = $1
  AND column_name = $2
`, table, column).Scan(&columnDefault)
	require.NoError(t, err, "query column_default for %s.%s", table, column)
	require.True(t, columnDefault.Valid, "expected column_default for %s.%s", table, column)

	for _, fragment := range fragments {
		require.Contains(t, columnDefault.String, fragment, "expected default for %s.%s to contain %q", table, column, fragment)
	}
}

// requireColumn checks a public column's PostgreSQL data type and nullability.
// A positive maxLen also requires that character length; zero or negative
// maxLen skips the length comparison, allowing numeric and timestamp columns to
// use the same helper.
func requireColumn(t *testing.T, tx *sql.Tx, table, column, dataType string, maxLen int, nullable bool) {
	t.Helper()

	var row struct {
		DataType string        // PostgreSQL catalog type name
		MaxLen   sql.NullInt64 // optional character length limit
		Nullable string        // YES or NO nullability
	}

	err := tx.QueryRowContext(context.Background(), `
SELECT
  data_type,
  character_maximum_length,
  is_nullable
FROM information_schema.columns
WHERE table_schema = 'public'
  AND table_name = $1
  AND column_name = $2
`, table, column).Scan(&row.DataType, &row.MaxLen, &row.Nullable)
	require.NoError(t, err, "query information_schema.columns for %s.%s", table, column)
	require.Equal(t, dataType, row.DataType, "data_type mismatch for %s.%s", table, column)

	if maxLen > 0 {
		require.True(t, row.MaxLen.Valid, "expected maxLen for %s.%s", table, column)
		require.Equal(t, int64(maxLen), row.MaxLen.Int64, "maxLen mismatch for %s.%s", table, column)
	}

	if nullable {
		require.Equal(t, "YES", row.Nullable, "nullable mismatch for %s.%s", table, column)
	} else {
		require.Equal(t, "NO", row.Nullable, "nullable mismatch for %s.%s", table, column)
	}
}

// requireNoColumn requires a column name to be absent from a public table. It
// checks information_schema through the supplied transaction and fails the test
// on either a lookup error or an existing column.
func requireNoColumn(t *testing.T, tx *sql.Tx, table, column string) {
	t.Helper()

	var exists bool
	err := tx.QueryRowContext(context.Background(), `
SELECT EXISTS (
	SELECT 1
	FROM information_schema.columns
	WHERE table_schema = 'public'
	  AND table_name = $1
	  AND column_name = $2
)
`, table, column).Scan(&exists)
	require.NoError(t, err, "query information_schema.columns for %s.%s", table, column)
	require.False(t, exists, "expected %s.%s to be absent", table, column)
}
