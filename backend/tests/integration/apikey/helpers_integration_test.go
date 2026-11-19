//go:build integration

package apikey_test

import (
	"context"
	"database/sql"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/suite"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	apikeypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
)

// API Key storage suites reuse a migrated PostgreSQL database but isolate each
// suite case in a rollback transaction. Standalone concurrency cases request
// separate databases so their repository writes can commit and become visible
// across connections.

// APIKeyRepoSuite binds each repository case to a rollback transaction in its
// suite database.
type APIKeyRepoSuite struct {
	// root owns the suite database connection and stays usable until the suite
	// cleanup closes it.
	root        *dbent.Client
	suite.Suite // assertion and suite lifecycle support
	// ctx, client, repo, and usage are replaced together before each case.
	// client, repo, and usage share that case transaction, which its test cleanup
	// rolls back after assertions.
	ctx    context.Context                // case execution context
	client *dbent.Client                  // case transaction client
	repo   *apikeypostgres.KeyStore       // transaction-bound key storage fixture
	usage  *billingpostgres.KeyUsageStore // transaction-bound key usage fixture
}

// SetupSuite starts the suite database through keyDatabase and retains its Ent
// client. Suite-level test cleanup closes that client after the transaction
// cleanup registered by individual cases.
func (s *APIKeyRepoSuite) SetupSuite() {
	_, s.root = keyDatabase(s.T())
}

// SetupTest opens a fresh transaction and binds key storage and usage totals to
// its executor. Each case leaves transaction completion to cleanup, which rolls
// back its writes.
func (s *APIKeyRepoSuite) SetupTest() {
	s.ctx = context.Background()
	tx, err := s.root.Tx(s.ctx)
	s.Require().NoError(err)
	// Roll back after each case so later suite cases start with no rows from it;
	// cleanup deliberately discards errors from an already completed transaction.
	s.T().Cleanup(func() { _ = tx.Rollback() })
	s.client = tx.Client()
	s.repo = newKeyStoreFixture(s.client, tx)
	s.usage = billingpostgres.NewKeyUsageStore(s.client, tx)
}

// mustCreateUser inserts an active ordinary user with a fixture password hash
// and returns its identity view. Creation failures fail the current test before
// its repository operation begins.
func (s *APIKeyRepoSuite) mustCreateUser(email string) *identity.User {
	s.T().Helper()

	u, err := s.client.User.Create().
		SetEmail(email).
		SetPasswordHash("test-password-hash").
		SetStatus(billing.StatusActive).
		SetRole(identity.RoleUser).
		Save(s.ctx)
	s.Require().NoError(err, "create user")
	return identitypostgres.UserFromEntity(u)
}

// mustCreateGroup inserts an active routing group and returns its domain view
// for key assignments. The group belongs to the current case transaction and
// disappears during rollback cleanup.
func (s *APIKeyRepoSuite) mustCreateGroup(name string) *routing.Group {
	s.T().Helper()

	g, err := s.client.Group.Create().
		SetName(name).
		SetStatus(billing.StatusActive).
		Save(s.ctx)
	s.Require().NoError(err, "create group")
	return routingpostgres.GroupFromEnt(g)
}

// mustCreateApiKey persists an active key for userID with the supplied optional
// ordinary group. The returned object contains storage-assigned identity and
// timestamps inside the current transaction.
func (s *APIKeyRepoSuite) mustCreateApiKey(userID int64, key, name string, groupID *int64) *apikey.APIKey {
	s.T().Helper()

	k := &apikey.APIKey{
		UserID:  userID,
		Key:     key,
		Name:    name,
		GroupID: groupID,
		Status:  billing.StatusActive,
	}
	s.Require().NoError(s.repo.Create(s.ctx, k), "create api key")
	return k
}

// keyDatabase returns SQL and Ent access to a dedicated migrated PostgreSQL
// container. Test cleanup closes the Ent client before the pool and container
// cleanups registered by postgrescontainer.New.
func keyDatabase(t *testing.T) (*sql.DB, *dbent.Client) {
	t.Helper()
	db := postgrescontainer.New(t)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	// Ent shares the SQL pool created by postgrescontainer.New. Its later cleanup
	// runs first, and repeated pool closure errors are discarded by this callback.
	t.Cleanup(func() { _ = client.Close() })
	return db, client
}
