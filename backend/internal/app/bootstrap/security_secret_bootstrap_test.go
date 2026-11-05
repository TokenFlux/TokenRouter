package bootstrap

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/enttest"
	"github.com/TokenFlux/TokenRouter/ent/securitysecret"
	"github.com/TokenFlux/TokenRouter/internal/config"
)

// Persisted JWT values must survive configured mismatches and simultaneous
// bootstrap callers. SQLite fixtures exercise candidate insertion and
// authoritative readback, while injected random-reader failures and
// missing-row queries make error handling, retry limits, and cancellation
// observable independently of the normal bootstrap path.

// newSecuritySecretTestClient creates a named in-memory SQLite database and
// Ent test client for this test. Test cleanup attempts both client and pool
// closure, discarding their close errors; constructor and schema setup
// failures stop the test.
func newSecuritySecretTestClient(t *testing.T) *dbent.Client {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "_")
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_fk=1", name)

	db, err := sql.Open("sqlite", dsn)
	require.NoError(t, err)
	// The fixture owns its SQLite pool. Attempt closure at test cleanup and
	// discard close errors after the assertions have observed bootstrap behavior.
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	// Ent uses the same pool through its driver. This later cleanup runs first,
	// and the earlier pool cleanup can repeat closure with its result discarded.
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestEnsureBootstrapSecretsNilInputs passes a nil Ent client and then a nil
// configuration. Bootstrap must return the matching diagnostic error for each
// absent dependency, identifying which required bootstrap input is missing.
func TestEnsureBootstrapSecretsNilInputs(t *testing.T) {
	err := ensureBootstrapSecrets(context.Background(), nil, &config.Config{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil ent client")

	client := newSecuritySecretTestClient(t)
	err = ensureBootstrapSecrets(context.Background(), client, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil config")
}

// TestEnsureBootstrapSecretsGenerateAndPersistJWTSecret starts with no
// configured secret. Bootstrap must populate a nonempty value meeting the byte
// minimum and persist exactly that value in the JWT secret row.
func TestEnsureBootstrapSecretsGenerateAndPersistJWTSecret(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	cfg := &config.Config{}

	err := ensureBootstrapSecrets(context.Background(), client, cfg)
	require.NoError(t, err)
	require.NotEmpty(t, cfg.Auth.JWT.Secret)
	require.GreaterOrEqual(t, len([]byte(cfg.Auth.JWT.Secret)), 32)

	stored, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(securitySecretKeyJWT)).Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, cfg.Auth.JWT.Secret, stored.Value)
}

// TestEnsureBootstrapSecretsLoadExistingJWTSecret seeds one valid persisted
// JWT value and leaves configuration empty. Successful bootstrap must copy
// that existing value into cfg, establishing which signing value this instance
// receives from the existing-row path.
func TestEnsureBootstrapSecretsLoadExistingJWTSecret(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	_, err := client.SecuritySecret.Create().SetKey(securitySecretKeyJWT).SetValue("existing-jwt-secret-32bytes-long!!!!").Save(context.Background())
	require.NoError(t, err)

	cfg := &config.Config{}
	err = ensureBootstrapSecrets(context.Background(), client, cfg)
	require.NoError(t, err)
	require.Equal(t, "existing-jwt-secret-32bytes-long!!!!", cfg.Auth.JWT.Secret)
}

// TestEnsureBootstrapSecretsRejectInvalidStoredSecret seeds a short persisted
// value. Bootstrap must return an error containing the minimum byte
// requirement when resolving that row into otherwise empty configuration.
func TestEnsureBootstrapSecretsRejectInvalidStoredSecret(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	_, err := client.SecuritySecret.Create().SetKey(securitySecretKeyJWT).SetValue("too-short").Save(context.Background())
	require.NoError(t, err)

	cfg := &config.Config{}
	err = ensureBootstrapSecrets(context.Background(), client, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 32 bytes")
}

// TestEnsureBootstrapSecretsPersistConfiguredJWTSecret supplies a valid
// configured candidate with no existing row. Bootstrap must succeed and the
// subsequent database query must contain that configured value.
func TestEnsureBootstrapSecretsPersistConfiguredJWTSecret(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	cfg := &config.Config{Auth: config.AuthConfig{JWT: config.JWTConfig{Secret: "configured-jwt-secret-32bytes-long!!"}}}

	err := ensureBootstrapSecrets(context.Background(), client, cfg)
	require.NoError(t, err)

	stored, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(securitySecretKeyJWT)).Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, "configured-jwt-secret-32bytes-long!!", stored.Value)
}

// TestEnsureBootstrapSecretsConfiguredSecretTooShort supplies a configured
// candidate below the stored byte minimum. Bootstrap must return an error
// naming the required minimum before it can resolve a usable secret.
func TestEnsureBootstrapSecretsConfiguredSecretTooShort(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	cfg := &config.Config{Auth: config.AuthConfig{JWT: config.JWTConfig{Secret: "short"}}}

	err := ensureBootstrapSecrets(context.Background(), client, cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 32 bytes")
}

// TestEnsureBootstrapSecretsConfiguredSecretDuplicateIgnored seeds a valid row
// and supplies a different configured candidate. Bootstrap must keep the
// stored value and replace cfg with that same authoritative database value.
func TestEnsureBootstrapSecretsConfiguredSecretDuplicateIgnored(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	_, err := client.SecuritySecret.Create().
		SetKey(securitySecretKeyJWT).
		SetValue("existing-jwt-secret-32bytes-long!!!!").
		Save(context.Background())
	require.NoError(t, err)

	cfg := &config.Config{Auth: config.AuthConfig{JWT: config.JWTConfig{Secret: "another-configured-jwt-secret-32!!!!"}}}
	err = ensureBootstrapSecrets(context.Background(), client, cfg)
	require.NoError(t, err)

	stored, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(securitySecretKeyJWT)).Only(context.Background())
	require.NoError(t, err)
	require.Equal(t, "existing-jwt-secret-32bytes-long!!!!", stored.Value)
	require.Equal(t, "existing-jwt-secret-32bytes-long!!!!", cfg.Auth.JWT.Secret)
}

// TestGetOrCreateGeneratedSecuritySecretTrimmedExistingValue seeds a secret
// with surrounding spaces. Resolution must return its trimmed value and false
// for the generated-candidate flag, retaining the existing-row path.
func TestGetOrCreateGeneratedSecuritySecretTrimmedExistingValue(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	_, err := client.SecuritySecret.Create().
		SetKey("trimmed_key").
		SetValue("  existing-trimmed-secret-32bytes-long!!  ").
		Save(context.Background())
	require.NoError(t, err)

	value, created, err := getOrCreateGeneratedSecuritySecret(context.Background(), client, "trimmed_key", 32)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "existing-trimmed-secret-32bytes-long!!", value)
}

// TestGetOrCreateGeneratedSecuritySecretQueryError closes the fixture client
// before lookup. Resolution must return a query error rather than treating the
// closed database as an absent secret eligible for generation.
func TestGetOrCreateGeneratedSecuritySecretQueryError(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	require.NoError(t, client.Close())

	_, _, err := getOrCreateGeneratedSecuritySecret(context.Background(), client, "closed_client_key", 32)
	require.Error(t, err)
}

// TestGetOrCreateGeneratedSecuritySecretCreateValidationError supplies an
// oversized secret key with no matching row. Resolution must return an error
// for this candidate whose key exceeds the schema's configured length limit.
func TestGetOrCreateGeneratedSecuritySecretCreateValidationError(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	// Exceed the schema's one-hundred-character key limit with an ASCII fixture.
	tooLongKey := strings.Repeat("k", 101)

	_, _, err := getOrCreateGeneratedSecuritySecret(context.Background(), client, tooLongKey, 32)
	require.Error(t, err)
}

// TestGetOrCreateGeneratedSecuritySecretConcurrentCreation runs eight
// resolutions for one absent key. Every call must succeed with the same
// nonempty value, this fixture must observe one matching generated-candidate
// flag, and storage must contain one row.
func TestGetOrCreateGeneratedSecuritySecretConcurrentCreation(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	// Multiple callers exercise unique-key insertion and subsequent common readback.
	const goroutines = 8
	key := "concurrent_bootstrap_key"

	values := make([]string, goroutines)
	createdFlags := make([]bool, goroutines)
	errs := make([]error, goroutines)

	// Each worker owns one result index. Wait for all writes before comparing
	// the stored values and flags, so the fixture does not read active slots.
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			values[idx], createdFlags[idx], errs[idx] = getOrCreateGeneratedSecuritySecret(context.Background(), client, key, 32)
		}(i)
	}
	wg.Wait()

	for i := range errs {
		require.NoError(t, errs[i])
		require.NotEmpty(t, values[i])
	}
	for i := 1; i < len(values); i++ {
		require.Equal(t, values[0], values[i])
	}

	createdCount := 0
	for _, created := range createdFlags {
		if created {
			createdCount++
		}
	}
	require.GreaterOrEqual(t, createdCount, 1)
	require.LessOrEqual(t, createdCount, 1)

	count, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ(key)).Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

// TestGetOrCreateGeneratedSecuritySecretGenerateError replaces the random
// reader with a failing function for an absent key. Resolution must return an
// error containing that injected cause, exposing random-source failure to
// the bootstrap caller resolving this absent key.
func TestGetOrCreateGeneratedSecuritySecretGenerateError(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	// Inject a failure only in this serial test and restore the shared reader
	// during cleanup so subsequent generation cases use the normal random source.
	originalRead := readRandomBytes
	readRandomBytes = func([]byte) (int, error) {
		return 0, errors.New("boom")
	}
	t.Cleanup(func() {
		readRandomBytes = originalRead
	})

	_, _, err := getOrCreateGeneratedSecuritySecret(context.Background(), client, "gen_error_key", 32)
	require.Error(t, err)
	require.Contains(t, err.Error(), "boom")
}

// TestCreateSecuritySecretIfAbsent submits short, valid, and conflicting
// configured candidates. The short value must fail, later valid reads must
// return the first accepted value, and the key must have exactly one row.
func TestCreateSecuritySecretIfAbsent(t *testing.T) {
	client := newSecuritySecretTestClient(t)

	_, err := createSecuritySecretIfAbsent(context.Background(), client, "abc", "short")
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 32 bytes")

	stored, err := createSecuritySecretIfAbsent(context.Background(), client, "abc", "valid-jwt-secret-value-32bytes-long")
	require.NoError(t, err)
	require.Equal(t, "valid-jwt-secret-value-32bytes-long", stored)

	stored, err = createSecuritySecretIfAbsent(context.Background(), client, "abc", "another-valid-secret-value-32bytes")
	require.NoError(t, err)
	require.Equal(t, "valid-jwt-secret-value-32bytes-long", stored)

	count, err := client.SecuritySecret.Query().Where(securitysecret.KeyEQ("abc")).Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

// TestCreateSecuritySecretIfAbsentValidationError supplies an oversized key
// with a valid-length value. The adapter must return an error for this
// candidate, exercising the oversized-key path independently of value length.
func TestCreateSecuritySecretIfAbsentValidationError(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	_, err := createSecuritySecretIfAbsent(
		context.Background(),
		client,
		strings.Repeat("k", 101),
		"valid-jwt-secret-value-32bytes-long",
	)
	require.Error(t, err)
}

// TestCreateSecuritySecretIfAbsentExecError closes the client before inserting
// a valid candidate. The adapter must return an error for the unavailable
// database, making connection failure observable to its bootstrap caller.
func TestCreateSecuritySecretIfAbsentExecError(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	require.NoError(t, client.Close())

	_, err := createSecuritySecretIfAbsent(context.Background(), client, "closed-client-key", "valid-jwt-secret-value-32bytes-long")
	require.Error(t, err)
}

// TestQuerySecuritySecretWithRetrySuccess seeds an existing row before lookup.
// The retry helper must return that row with its exact identifier and stored
// value, preserving successful keyed lookup.
func TestQuerySecuritySecretWithRetrySuccess(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	created, err := client.SecuritySecret.Create().
		SetKey("retry_success_key").
		SetValue("retry-success-jwt-secret-value-32!!").
		Save(context.Background())
	require.NoError(t, err)

	got, err := querySecuritySecretWithRetry(context.Background(), client, "retry_success_key")
	require.NoError(t, err)
	require.Equal(t, created.ID, got.ID)
	require.Equal(t, "retry-success-jwt-secret-value-32!!", got.Value)
}

// TestQuerySecuritySecretWithRetryExhausted queries a key that remains absent.
// The final result must be an error recognized as missing-row, exercising
// exhaustion of the bounded retry path.
func TestQuerySecuritySecretWithRetryExhausted(t *testing.T) {
	client := newSecuritySecretTestClient(t)

	_, err := querySecuritySecretWithRetry(context.Background(), client, "retry_missing_key")
	require.Error(t, err)
	require.True(t, isSecretNotFoundError(err))
}

// TestQuerySecuritySecretWithRetryContextCanceled queries an absent key with a
// deadline shorter than one retry delay. The helper must propagate
// DeadlineExceeded whether the budget expires during the query or its retry
// wait.
func TestQuerySecuritySecretWithRetryContextCanceled(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	// Use half a retry delay so the budget cannot cover a complete scheduled wait.
	// The assertion accepts DeadlineExceeded from query execution or delay cancellation.
	ctx, cancel := context.WithTimeout(context.Background(), securitySecretReadRetryWait/2)
	defer cancel()

	_, err := querySecuritySecretWithRetry(ctx, client, "retry_ctx_cancel_key")
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestQuerySecuritySecretWithRetryNonNotFoundError closes the client before
// querying. The returned failure must remain outside the missing-row
// classification so unavailable storage is not treated as a retryable absent
// record.
func TestQuerySecuritySecretWithRetryNonNotFoundError(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	require.NoError(t, client.Close())

	_, err := querySecuritySecretWithRetry(context.Background(), client, "retry_closed_client_key")
	require.Error(t, err)
	require.False(t, isSecretNotFoundError(err))
}

// TestSecretNotFoundHelpers supplies nil, sentinel, wrapped, textual, and
// unrelated errors. SQL no-row compatibility and secret missing-row checks
// must recognize their expected inputs while rejecting nil and unrelated
// failures.
func TestSecretNotFoundHelpers(t *testing.T) {
	require.False(t, isSecretNotFoundError(nil))
	require.False(t, isSQLNoRowsError(nil))

	require.True(t, isSQLNoRowsError(sql.ErrNoRows))
	require.True(t, isSQLNoRowsError(fmt.Errorf("wrapped: %w", sql.ErrNoRows)))
	require.True(t, isSQLNoRowsError(errors.New("sql: no rows in result set")))

	require.True(t, isSecretNotFoundError(sql.ErrNoRows))
	require.True(t, isSecretNotFoundError(errors.New("sql: no rows in result set")))
	require.False(t, isSecretNotFoundError(errors.New("some other error")))
}

// TestGenerateHexSecretReadError injects a random-reader failure. Secret
// generation must return an error containing the injected cause, exposing
// the random-source failure through the returned diagnostic to its caller.
func TestGenerateHexSecretReadError(t *testing.T) {
	// Inject a failure only in this serial test and restore the shared reader
	// during cleanup so subsequent generation cases use the normal random source.
	originalRead := readRandomBytes
	readRandomBytes = func([]byte) (int, error) {
		return 0, errors.New("read random failed")
	}
	t.Cleanup(func() {
		readRandomBytes = originalRead
	})

	_, err := generateHexSecret(32)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read random failed")
}

// TestGenerateHexSecretLengths compares default raw-byte sizing with an
// explicit sixteen-byte request. Both results must decode as hex with the
// expected encoded lengths, and the two differently sized results must differ.
func TestGenerateHexSecretLengths(t *testing.T) {
	v1, err := generateHexSecret(0)
	require.NoError(t, err)
	require.Len(t, v1, 64)
	_, err = hex.DecodeString(v1)
	require.NoError(t, err)

	v2, err := generateHexSecret(16)
	require.NoError(t, err)
	require.Len(t, v2, 32)
	_, err = hex.DecodeString(v2)
	require.NoError(t, err)

	require.NotEqual(t, v1, v2)
}

// TestEnsureBootstrapSecretsOAuthPersistsIndependentKey starts before any frontend URL exists.
// The generated OAuth key must differ from the JWT secret and remain unchanged
// when another bootstrap follows a backend restart.
func TestEnsureBootstrapSecretsOAuthPersistsIndependentKey(t *testing.T) {
	client := newSecuritySecretTestClient(t)
	first := &config.Config{}
	require.NoError(t, ensureBootstrapSecrets(t.Context(), client, first))
	require.Len(t, first.MCPOAuthSigningSecret, 64)
	require.NotEqual(t, first.Auth.JWT.Secret, first.MCPOAuthSigningSecret)
	second := &config.Config{}
	require.NoError(t, ensureBootstrapSecrets(t.Context(), client, second))
	require.Equal(t, first.MCPOAuthSigningSecret, second.MCPOAuthSigningSecret)
}
