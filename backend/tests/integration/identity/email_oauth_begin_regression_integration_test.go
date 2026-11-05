//go:build integration

package identity_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity/rediscache"
	identitytestkit "github.com/TokenFlux/TokenRouter/internal/identity/testkit"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/testutil/rediscontainer"
)

// OAuth account creation can commit before its final identity-binding
// transaction begins. The PostgreSQL driver wrapper arms one transaction-begin
// failure after a successful user creation hook, and the handler scenario
// checks that compensation removes the created user while leaving the pending
// session unconsumed.
//
// An atomic compare-and-swap consumes the armed failure once and increments an
// observation counter before returning the injected begin error. Other
// transaction attempts delegate to the borrowed driver. Settings fixtures
// enable registration through selected read methods, and the test uses Redis
// refresh-token storage alongside the SQL wrapper so the HTTP completion
// follows the assembled identity flow.

// failNextTransaction consumes an armed transaction-begin failure while
// delegating other driver operations.
type failNextTransaction struct {
	// Driver borrows the cloned fixture's SQL pool and receives transaction calls
	// that do not claim the armed failure; database cleanup owns that pool's
	// lifetime.
	dialect.Driver
	// armed and failures record one-shot rejection: Tx clears armed when it wins
	// the claim and increments failures before returning the injected
	// transaction-begin error.
	armed    atomic.Bool  // next begin failure gate
	failures atomic.Int64 // observed injected begin failures
}

// oauthSettings supplies registration policy through selected reads for the
// OAuth compensation scenario.
type oauthSettings struct {
	// Repository exposes promoted methods beyond the two supplied reads. It
	// remains nil in this fixture, so the exercised flow uses GetValue and
	// GetMultiple.
	settingscore.Repository
}

// Tx rejects one transaction attempt when it atomically claims the armed
// failure and otherwise delegates to Driver.Tx. A claimed failure returns a nil
// transaction, while delegated calls retain the supplied context.
//
// Clearing armed before returning the error makes the failure consumable by one
// attempted begin, and failures counts those rejected calls. The user creation
// hook sets armed after its delegate succeeds so the tested later binding
// transaction encounters this error. Callers supply a usable borrowed driver;
// other transaction operations remain its responsibility.
func (d *failNextTransaction) Tx(ctx context.Context) (dialect.Tx, error) {
	// Claim and clear the armed rejection before returning failure so one begin
	// consumes it and later attempts can use the underlying PostgreSQL driver
	// again.
	if d.armed.CompareAndSwap(true, false) {
		d.failures.Add(1)
		return nil, errors.New("test injected final binding begin failure")
	}
	return d.Driver.Tx(ctx)
}

// GetValue returns true text for the registration-enabled setting and empty
// text for every other key. It reports no error, supplying the registration
// policy used by this compensation scenario.
func (oauthSettings) GetValue(_ context.Context, key string) (string, error) {
	if key == identity.SettingKeyRegistrationEnabled {
		return "true", nil
	}
	return "", nil
}

// GetMultiple returns an allocated map with one value for each requested key
// using this fixture's GetValue. That method reports no errors, and duplicate
// keys overwrite the same map entry.
func (s oauthSettings) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	// Keep every requested key in the result using the registration override or
	// empty default; the discarded GetValue error is always nil in this fixture.
	for _, key := range keys {
		out[key], _ = s.GetValue(ctx, key)
	}
	return out, nil
}

// TestEmailOAuthBeginFailureCompensatesUser arms a transaction-begin error
// after user creation in an OAuth completion request. One failure is observed,
// HTTP returns InternalServerError, the created user disappears, and pending
// consumption remains unset.
func TestEmailOAuthBeginFailureCompensatesUser(t *testing.T) {
	integrationDB, _ := identityDatabase(t)
	ctx := context.Background()
	driver := &failNextTransaction{Driver: entsql.OpenDB(dialect.Postgres, integrationDB)}
	client := dbent.NewClient(dbent.Driver(driver))
	// Arm the next begin after the user create mutation succeeds so the pending
	// finalization stage encounters a failure after the account's creation work
	// has proceeded.
	client.User.Use(func(next dbent.Mutator) dbent.Mutator {
		return dbent.MutateFunc(func(ctx context.Context, m dbent.Mutation) (dbent.Value, error) {
			v, e := next.Mutate(ctx, m)
			if e == nil && m.Op().Is(dbent.OpCreate) {
				driver.armed.Store(true)
			}
			return v, e
		})
	})
	users := identitypostgres.NewUserStore(client, integrationDB)
	cfg := &config.Config{}
	cfg.Auth.JWT.Secret = "test-local-fixture-only"
	cfg.Auth.JWT.AccessTokenTTLMinutes = 60
	cfg.Auth.JWT.RefreshTokenTTLDays = 1
	cfg.UserDefaults.Concurrency = 1
	settings := identitytestkit.Settings(oauthSettings{}, cfg)
	auth := identitytestkit.Auth(client, &identity.AuthDependencies{Users: users, RefreshTokens: rediscache.NewRefreshTokenCache(rediscontainer.New(t)), Options: identitytestkit.AuthOptions(cfg), Settings: settings})
	flow := &identity.PendingFlow{Store: identitypostgres.NewPendingRepository(client), Database: &identitypostgres.PendingFlowDatabase{Client: client, Auth: auth}, Auth: auth}
	sessionHTTP := identityhttp.NewSessionHandler(auth, nil, settings, nil, nil, flow, identityhttp.SessionHTTPOptions{})
	pendingHTTP := identityhttp.NewPendingHandler(sessionHTTP, flow, identityhttp.PendingHTTPOptions{})
	h := identityhttp.NewEmailOAuthHandler(pendingHTTP, nil, nil)
	email := "test-" + uuid.NewString() + "@example.invalid"
	session, err := client.PendingAuthSession.Create().SetSessionToken(uuid.NewString()).SetIntent("login").SetProviderType("github").SetProviderKey("github").SetProviderSubject(uuid.NewString()).SetResolvedEmail(email).SetBrowserSessionKey("test-browser").SetExpiresAt(time.Now().Add(time.Minute)).Save(ctx)
	require.NoError(t, err)
	// Remove the committed pending fixture after compensation assertions complete;
	// the cloned database owns shared pool closure after this later-registered
	// cleanup runs.
	t.Cleanup(func() { require.NoError(t, client.PendingAuthSession.DeleteOneID(session.ID).Exec(ctx)) })
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/github/complete-registration", strings.NewReader(`{"password":"fixture-password-only"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	// Encode the stored pending token and browser key with unpadded URL-safe
	// Base64 so the HTTP completion reads the same browser-bound session proof as
	// creation.
	c.Request.AddCookie(&http.Cookie{Name: "oauth_pending_session", Value: base64.RawURLEncoding.EncodeToString([]byte(session.SessionToken))})
	c.Request.AddCookie(&http.Cookie{Name: "oauth_pending_browser_session", Value: base64.RawURLEncoding.EncodeToString([]byte(session.BrowserSessionKey))})
	h.CompleteGitHubOAuthRegistration(c)
	require.Equal(t, int64(1), driver.failures.Load(), "必须执行到注册已提交后的第二段 Begin")
	require.Equal(t, http.StatusInternalServerError, recorder.Code, recorder.Body.String())
	_, err = users.GetByEmail(ctx, email)
	require.ErrorIs(t, err, identity.ErrUserNotFound, "绑定事务无法开始时必须补偿已创建用户")
	stored, err := client.PendingAuthSession.Get(ctx, session.ID)
	require.NoError(t, err)
	require.Nil(t, stored.ConsumedAt)
}
