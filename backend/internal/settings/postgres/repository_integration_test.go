//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
)

// SettingRepoSuite exercises raw setting operations through transaction-scoped PostgreSQL fixtures.
type SettingRepoSuite struct {
	suite.Suite // assertions and case lifecycle
	// client stays bound to the suite database while ctx and repo are replaced
	// for each case. SetupTest binds repo to a fresh transaction, and fixture
	// cleanup owns the shared database connection after the suite finishes.
	client *dbent.Client       // suite database Ent client
	ctx    context.Context     // current case operation context
	repo   *settingscore.Store // Store bound to case transaction
}

// TestSettingsAtomicFailure installs a trigger that rejects one settings write
// in a combined update. The earlier setting retains its saved value, the rejected
// key remains absent, and the post-commit callback is uncalled.
func TestSettingsAtomicFailure(t *testing.T) {
	integrationEntClient, integrationDB := settingsDatabase(t)
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, `CREATE FUNCTION test_reject_setting() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test rejected settings write'; END $$;
 CREATE TRIGGER test_reject_setting BEFORE INSERT OR UPDATE ON settings FOR EACH ROW WHEN (NEW.key='test_atomic_failure') EXECUTE FUNCTION test_reject_setting();`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := integrationDB.ExecContext(context.Background(), `DROP TRIGGER test_reject_setting ON settings; DROP FUNCTION test_reject_setting(); DELETE FROM settings WHERE key IN ('test_atomic_site','test_atomic_failure');`)
		require.NoError(t, cleanupErr)
	})
	repo := NewSettingRepository(integrationEntClient)
	require.NoError(t, repo.Set(ctx, "test_atomic_site", "before"))
	update, err := settingscore.New(repo).Updates().Begin(ctx)
	require.NoError(t, err)
	defer update.Close()
	applied := false
	err = update.Commit(settingscore.PreparedChange{Module: "site", Values: map[string]string{"test_atomic_site": "after"}, Apply: func(context.Context) error { applied = true; return nil }}, settingscore.PreparedChange{Module: "payment", Values: map[string]string{"test_atomic_failure": "true"}})
	require.Error(t, err)
	require.False(t, applied)
	actual, err := repo.GetValue(ctx, "test_atomic_site")
	require.NoError(t, err)
	require.Equal(t, "before", actual)
	_, err = repo.GetValue(ctx, "test_atomic_failure")
	require.ErrorIs(t, err, settingscore.ErrSettingNotFound)
}

// settingsDatabase creates an Ent client over the test's isolated PostgreSQL
// connection. The container fixture owns connection cleanup; callers can use
// transactions or independent Store coordinators against that same database.
func settingsDatabase(t *testing.T) (*dbent.Client, *sql.DB) {
	t.Helper()
	db := postgrescontainer.New(t)
	return dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db))), db
}

// TestLocalizedSettingsConcurrentWrite starts two Store instances with the same
// expected localized value and different replacement batches. One commit succeeds,
// one reports locale.ErrConflict, and the sibling setting matches the winning
// localized value after both attempts finish.
func TestLocalizedSettingsConcurrentWrite(t *testing.T) {
	client, _ := settingsDatabase(t)
	ctx := context.Background()
	repo := NewSettingRepository(client)
	const key = "test_localization_cas"
	const sibling = "test_localization_sibling"
	before := `{"revision":1}`
	require.NoError(t, repo.Set(ctx, key, before))
	// Cleanup removes both fixture keys after concurrent commits finish. Delete
	// errors are discarded here because outcome assertions have already read the winning batch.
	t.Cleanup(func() { _ = repo.Delete(ctx, key); _ = repo.Delete(ctx, sibling) })
	results := make(chan error, 2)
	var ready sync.WaitGroup
	// Admit two independent Store sessions before either compares its expected
	// value, so their competition is resolved by database locks rather than one instance gate.
	ready.Add(2)
	start := make(chan struct{})
	for _, value := range []string{"first", "second"} {
		go func(value string) {
			store := settingscore.New(repo)
			session, err := store.Updates().Begin(ctx)
			if err != nil {
				ready.Done()
				results <- err
				return
			}
			defer session.Close()
			ready.Done()
			<-start
			results <- session.Commit(settingscore.PreparedChange{Module: "site", Values: map[string]string{key: value, sibling: value}, Expected: map[string]*string{key: &before}})
		}(value)
	}
	ready.Wait()
	// Release both sessions after readiness so each attempts a conditional batch
	// against the same old value and the test observes the database conflict outcome.
	close(start)
	first, second := <-results, <-results
	if first == nil {
		require.ErrorIs(t, second, locale.ErrConflict)
	} else {
		require.ErrorIs(t, first, locale.ErrConflict)
		require.NoError(t, second)
	}
	actual, err := repo.GetValue(ctx, key)
	require.NoError(t, err)
	other, err := repo.GetValue(ctx, sibling)
	require.NoError(t, err)
	require.Equal(t, actual, other)
}

// SetupTest starts a transaction on the suite database and binds a fresh Store
// to its client. Test cleanup rolls that transaction back, isolating records
// created or replaced by each suite case.
func (s *SettingRepoSuite) SetupTest() {
	s.ctx = context.Background()
	tx, err := s.client.Tx(s.ctx)
	s.Require().NoError(err)
	// Roll back each case's transaction after its assertions; the fixture discards
	// that cleanup error and the next case starts a separate transaction on the suite client.
	s.T().Cleanup(func() { _ = tx.Rollback() })
	s.repo = settingscore.New(NewSettingRepository(tx.Client()))
}

// TestSettingRepoSuite runs the PostgreSQL setting CRUD and bulk-upsert cases
// through transaction-scoped setup. Their assertions protect returned values,
// missing-record classification, clearing, and absent-key deletion behavior.
func TestSettingRepoSuite(t *testing.T) {
	suite.Run(t, new(SettingRepoSuite))
}

// TestSetAndGetValue saves a raw setting string through the bound PostgreSQL
// repository. Reading the same key returns that value, protecting the basic
// storage-to-core representation used by module readers.
func (s *SettingRepoSuite) TestSetAndGetValue() {
	s.Require().NoError(s.repo.Set(s.ctx, "k1", "v1"), "Set")
	got, err := s.repo.GetValue(s.ctx, "k1")
	s.Require().NoError(err, "GetValue")
	s.Require().Equal("v1", got, "GetValue mismatch")
}

// TestSet_Upsert writes two values under one key inside the test transaction.
// The subsequent read returns the later string, protecting replacement by the
// setting-key conflict target.
func (s *SettingRepoSuite) TestSet_Upsert() {
	s.Require().NoError(s.repo.Set(s.ctx, "k1", "v1"), "Set")
	s.Require().NoError(s.repo.Set(s.ctx, "k1", "v2"), "Set upsert")
	got, err := s.repo.GetValue(s.ctx, "k1")
	s.Require().NoError(err, "GetValue after upsert")
	s.Require().Equal("v2", got, "upsert mismatch")
}

// TestGetValue_Missing reads a key absent from the transaction's settings table.
// The returned error matches ErrSettingNotFound, retaining the absence classification
// needed when a module selects a default.
func (s *SettingRepoSuite) TestGetValue_Missing() {
	_, err := s.repo.GetValue(s.ctx, "nonexistent")
	s.Require().Error(err, "expected error for missing key")
	s.Require().ErrorIs(err, settingscore.ErrSettingNotFound)
}

// TestSetMultiple_AndGetMultiple saves two settings in one bulk call before
// reading their keys together. The resulting map retains both values, protecting
// batch upsert and batch read mapping.
func (s *SettingRepoSuite) TestSetMultiple_AndGetMultiple() {
	s.Require().NoError(s.repo.SetMultiple(s.ctx, map[string]string{"k2": "v2", "k3": "v3"}), "SetMultiple")
	m, err := s.repo.GetMultiple(s.ctx, []string{"k2", "k3"})
	s.Require().NoError(err, "GetMultiple")
	s.Require().Equal("v2", m["k2"])
	s.Require().Equal("v3", m["k3"])
}

// TestGetMultiple_EmptyKeys passes an empty key list to the repository. The
// operation succeeds with an empty map, protecting callers that have no selected
// settings to include in a batch read.
func (s *SettingRepoSuite) TestGetMultiple_EmptyKeys() {
	m, err := s.repo.GetMultiple(s.ctx, []string{})
	s.Require().NoError(err, "GetMultiple with empty keys")
	s.Require().Empty(m, "expected empty map")
}

// TestGetMultiple_Subset saves three values and requests two with an absent
// key. The returned map includes the two requested saved values and omits
// that absent key from the result.
func (s *SettingRepoSuite) TestGetMultiple_Subset() {
	s.Require().NoError(s.repo.SetMultiple(s.ctx, map[string]string{"a": "1", "b": "2", "c": "3"}))
	m, err := s.repo.GetMultiple(s.ctx, []string{"a", "c", "nonexistent"})
	s.Require().NoError(err, "GetMultiple subset")
	s.Require().Equal("1", m["a"])
	s.Require().Equal("3", m["c"])
	_, exists := m["nonexistent"]
	s.Require().False(exists, "nonexistent key should not be in map")
}

// TestGetAll saves two setting records before reading the full table map.
// Both saved keys retain their values, while the assertion permits other records
// that can already exist in the fixture.
func (s *SettingRepoSuite) TestGetAll() {
	s.Require().NoError(s.repo.SetMultiple(s.ctx, map[string]string{"x": "1", "y": "2"}))
	all, err := s.repo.GetAll(s.ctx)
	s.Require().NoError(err, "GetAll")
	s.Require().GreaterOrEqual(len(all), 2, "expected at least 2 settings")
	s.Require().Equal("1", all["x"])
	s.Require().Equal("2", all["y"])
}

// TestGetConfigurationValues excludes operational notification delivery, locale,
// and preference prefixes while keeping administrator configuration and template
// keys available, and leaves GetAll returning the complete stored key set.
func (s *SettingRepoSuite) TestGetConfigurationValues() {
	s.Require().NoError(s.repo.SetMultiple(s.ctx, map[string]string{
		"site_name":                               "TokenRouter",
		"notification_email_template:welcome":     "{}",
		"notification_email_delivery:v2:123":      "2026-11-19T00:00:00Z",
		"notification_email_locale:email:456":     "en",
		"notification_email_preference:user:1:ev": "unsubscribed",
	}))
	cfg, err := s.repo.GetConfigurationValues(s.ctx)
	s.Require().NoError(err)
	s.Require().Equal("TokenRouter", cfg["site_name"])
	s.Require().Equal("{}", cfg["notification_email_template:welcome"])
	s.Require().NotContains(cfg, "notification_email_delivery:v2:123")
	s.Require().NotContains(cfg, "notification_email_locale:email:456")
	s.Require().NotContains(cfg, "notification_email_preference:user:1:ev")

	all, err := s.repo.GetAll(s.ctx)
	s.Require().NoError(err)
	s.Require().Contains(all, "notification_email_delivery:v2:123")
}

// TestDelete saves a setting and removes its key inside the transaction.
// A later read returns the not-found classification, protecting cleanup behavior
// used when restoring a module's missing-setting default.
func (s *SettingRepoSuite) TestDelete() {
	s.Require().NoError(s.repo.Set(s.ctx, "todelete", "val"))
	s.Require().NoError(s.repo.Delete(s.ctx, "todelete"), "Delete")
	_, err := s.repo.GetValue(s.ctx, "todelete")
	s.Require().Error(err, "expected missing key error after Delete")
	s.Require().ErrorIs(err, settingscore.ErrSettingNotFound)
}

// TestDelete_Idempotent removes a key absent from the table without a prior
// write. The repository returns success, protecting callers that repeat cleanup
// after a setting has already disappeared.
func (s *SettingRepoSuite) TestDelete_Idempotent() {
	s.Require().NoError(s.repo.Delete(s.ctx, "nonexistent_delete"), "Delete nonexistent should be idempotent")
}

// TestSetMultiple_Upsert combines replacement of an existing setting with insertion
// of a new key. Reads retain both requested results, protecting the bulk operation
// when its conflict targets mix saved and absent records.
func (s *SettingRepoSuite) TestSetMultiple_Upsert() {
	s.Require().NoError(s.repo.Set(s.ctx, "upsert_key", "old_value"))
	s.Require().NoError(s.repo.SetMultiple(s.ctx, map[string]string{"upsert_key": "new_value", "new_key": "new_val"}))

	got, err := s.repo.GetValue(s.ctx, "upsert_key")
	s.Require().NoError(err)
	s.Require().Equal("new_value", got, "SetMultiple should upsert existing key")

	got2, err := s.repo.GetValue(s.ctx, "new_key")
	s.Require().NoError(err)
	s.Require().Equal("new_val", got2)
}

// TestSet_EmptyValue saves an empty string for a present setting key. Its
// read succeeds with empty text, distinguishing an intentionally cleared value
// from the absent-record result.
func (s *SettingRepoSuite) TestSet_EmptyValue() {
	s.Require().NoError(s.repo.Set(s.ctx, "empty_key", ""), "Set with empty value should succeed")

	got, err := s.repo.GetValue(s.ctx, "empty_key")
	s.Require().NoError(err, "GetValue for empty value")
	s.Require().Equal("", got, "empty value should be preserved")
}

// TestSetMultiple_WithEmptyValues saves branding text alongside empty optional
// site fields in one batch. Each requested value is returned, including empty
// logo, URL, and contact entries that remain saved records.
func (s *SettingRepoSuite) TestSetMultiple_WithEmptyValues() {
	settings := map[string]string{
		"site_name":     "Sub2api",
		"site_subtitle": "Subscription to API",
		"site_logo":     "", // no uploaded logo
		"api_base_url":  "", // no API base URL
		"contact_info":  "", // no contact information
		"doc_url":       "", // no documentation URL
	}

	s.Require().NoError(s.repo.SetMultiple(s.ctx, settings), "SetMultiple with empty values should succeed")

	result, err := s.repo.GetMultiple(s.ctx, []string{"site_name", "site_subtitle", "site_logo", "api_base_url", "contact_info", "doc_url"})
	s.Require().NoError(err, "GetMultiple after SetMultiple with empty values")

	s.Require().Equal("Sub2api", result["site_name"])
	s.Require().Equal("Subscription to API", result["site_subtitle"])
	s.Require().Equal("", result["site_logo"], "empty site_logo should be preserved")
	s.Require().Equal("", result["api_base_url"], "empty api_base_url should be preserved")
	s.Require().Equal("", result["contact_info"], "empty contact_info should be preserved")
	s.Require().Equal("", result["doc_url"], "empty doc_url should be preserved")
}

// TestSetMultiple_UpdateToEmpty replaces a populated setting with empty text
// through the batch writer. The record remains readable with an empty value,
// protecting clearing of an existing optional setting.
func (s *SettingRepoSuite) TestSetMultiple_UpdateToEmpty() {
	s.Require().NoError(s.repo.Set(s.ctx, "clearable_key", "initial_value"))

	got, err := s.repo.GetValue(s.ctx, "clearable_key")
	s.Require().NoError(err)
	s.Require().Equal("initial_value", got)

	s.Require().NoError(s.repo.SetMultiple(s.ctx, map[string]string{"clearable_key": ""}), "Update to empty should succeed")

	got, err = s.repo.GetValue(s.ctx, "clearable_key")
	s.Require().NoError(err)
	s.Require().Equal("", got, "value should be updated to empty string")
}

// SetupSuite obtains the suite's Ent client from the isolated database fixture.
// Individual cases start their own transactions on that client, while fixture
// cleanup closes the underlying connection after the suite finishes.
func (s *SettingRepoSuite) SetupSuite() { s.client, _ = settingsDatabase(s.T()) }
