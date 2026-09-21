//go:build integration

package repository

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

	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"

	"entgo.io/ent/dialect"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/rediscache"
	identitytestkit "github.com/TokenFlux/TokenRouter/internal/identity/testkit"

	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"

	entsql "entgo.io/ent/dialect/sql"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// s05FailNextTransaction 在用户已创建后仅拒绝下一次 Begin，不影响真实 PostgreSQL 的补偿删除。
type s05FailNextTransaction struct {
	dialect.Driver
	armed    atomic.Bool
	failures atomic.Int64
}

func (d *s05FailNextTransaction) Tx(ctx context.Context) (dialect.Tx, error) {
	if d.armed.CompareAndSwap(true, false) {
		d.failures.Add(1)
		return nil, errors.New("s05 injected final binding begin failure")
	}
	return d.Driver.Tx(ctx)
}

type s05OAuthSettings struct{ settingscore.Repository }

func (s05OAuthSettings) GetValue(_ context.Context, key string) (string, error) {
	if key == identity.SettingKeyRegistrationEnabled {
		return "true", nil
	}
	return "", nil
}
func (s s05OAuthSettings) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, key := range keys {
		out[key], _ = s.GetValue(ctx, key)
	}
	return out, nil
}

// TestS05EmailOAuthBeginFailureCompensatesUser 覆盖真实创建提交后、第二段绑定事务无法开始的失败边界。
func TestS05EmailOAuthBeginFailureCompensatesUser(t *testing.T) {
	ctx := context.Background()
	testEntClient(t)
	driver := &s05FailNextTransaction{Driver: entsql.OpenDB(dialect.Postgres, integrationDB)}
	client := dbent.NewClient(dbent.Driver(driver))
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
	cfg.JWT.Secret = "s05-local-fixture-only"
	cfg.JWT.ExpireHour = 1
	cfg.JWT.RefreshTokenExpireDays = 1
	cfg.Default.UserConcurrency = 1
	settings := identitytestkit.Settings(s05OAuthSettings{}, cfg)
	auth := identitytestkit.Auth(client, &identity.AuthDependencies{Users: users, RefreshTokens: rediscache.NewRefreshTokenCache(testRedis(t)), Options: identitytestkit.AuthOptions(cfg), Settings: settings})
	flow := &identity.PendingFlow{Store: identitypostgres.NewPendingRepository(client), Database: &identitypostgres.PendingFlowDatabase{Client: client, Auth: auth}, Auth: auth}
	sessionHTTP := identityhttp.NewSessionHandler(auth, nil, settings, nil, nil, flow, identityhttp.SessionHTTPOptions{RunMode: cfg.RunMode})
	pendingHTTP := identityhttp.NewPendingHandler(sessionHTTP, flow, identityhttp.PendingHTTPOptions{})
	h := identityhttp.NewEmailOAuthHandler(pendingHTTP, nil, nil)
	email := "s05-" + uuid.NewString() + "@example.invalid"
	session, err := client.PendingAuthSession.Create().SetSessionToken(uuid.NewString()).SetIntent("login").SetProviderType("github").SetProviderKey("github").SetProviderSubject(uuid.NewString()).SetResolvedEmail(email).SetBrowserSessionKey("s05-browser").SetExpiresAt(time.Now().Add(time.Minute)).Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.PendingAuthSession.DeleteOneID(session.ID).Exec(ctx)) })
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/oauth/github/complete-registration", strings.NewReader(`{"password":"fixture-password-only"}`))
	c.Request.Header.Set("Content-Type", "application/json")
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
