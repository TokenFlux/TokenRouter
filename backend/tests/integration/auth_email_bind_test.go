//go:build unit

package integration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/authidentity"
	"github.com/TokenFlux/TokenRouter/ent/enttest"

	"entgo.io/ent/dialect"
	dbuser "github.com/TokenFlux/TokenRouter/ent/user"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"

	entsql "entgo.io/ent/dialect/sql"

	_ "modernc.org/sqlite"
)

type emailBindDefaultSubAssignerStub struct {
	calls []*billing.AssignSubscriptionInput
}

func (s *emailBindDefaultSubAssignerStub) AssignOrExtendSubscription(
	_ context.Context,
	input *billing.AssignSubscriptionInput,
) (*billing.UserSubscription, bool, error) {
	cloned := *input
	s.calls = append(s.calls, &cloned)
	return &billing.UserSubscription{UserID: input.UserID, PlanID: input.PlanID}, false, nil
}

type flakyEmailBindDefaultSubAssignerStub struct {
	err   error
	calls []*billing.AssignSubscriptionInput
}

func (s *flakyEmailBindDefaultSubAssignerStub) AssignOrExtendSubscription(
	_ context.Context,
	input *billing.AssignSubscriptionInput,
) (*billing.UserSubscription, bool, error) {
	cloned := *input
	s.calls = append(s.calls, &cloned)
	return nil, false, s.err
}

func newAuthServiceForEmailBind(
	t *testing.T,
	settings map[string]string,
	emailCache identity.EmailCache,
	defaultSubAssigner identity.DefaultSubscriptionAssigner,
) (*identity.AuthService, identity.UserRepository, *dbent.Client) {
	return newAuthServiceForEmailBindWithRefreshCache(t, settings, emailCache, defaultSubAssigner, nil)
}

func newAuthServiceForEmailBindWithRefreshCache(
	t *testing.T,
	settings map[string]string,
	emailCache identity.EmailCache,
	defaultSubAssigner identity.DefaultSubscriptionAssigner,
	refreshTokenCache identity.RefreshTokenCache,
) (*identity.AuthService, identity.UserRepository, *dbent.Client) {
	t.Helper()

	dbName := fmt.Sprintf("file:auth_service_email_bind_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := sql.Open("sqlite", dbName)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS user_provider_default_grants (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id INTEGER NOT NULL,
	provider_type TEXT NOT NULL,
	grant_reason TEXT NOT NULL DEFAULT 'first_bind',
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE(user_id, provider_type, grant_reason)
)`)
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	repo := identitypostgres.NewUserStore(client, db)
	options := &identity.AuthOptions{
		JWT: identity.SessionOptions{
			Secret:     "test-bind-email-secret",
			ExpireHour: 1,
		},
	}
	options.Default.UserBalance = 3.5
	options.Default.UserConcurrency = 2

	settingRepo := &emailBindSettingRepoStub{values: settings}
	svc := newIdentityAuthForTest(client, repo, options, settingRepo, emailCache, refreshTokenCache, defaultSubAssigner)
	return svc, repo, client
}

func TestAuthServiceBindEmailIdentity_UpdatesEmailAndAppliesFirstBindDefaults(t *testing.T) {
	assigner := &emailBindDefaultSubAssignerStub{}
	cache := &emailBindCacheStub{
		data: &identity.VerificationCodeData{
			Code:      "123456",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
	}
	svc, _, client := newAuthServiceForEmailBind(t, map[string]string{
		identity.SettingKeyAuthSourceDefaultEmailBalance:          "8.5",
		identity.SettingKeyAuthSourceDefaultEmailConcurrency:      "4",
		identity.SettingKeyAuthSourceDefaultEmailSubscriptions:    `[{"plan_id":11}]`,
		identity.SettingKeyAuthSourceDefaultEmailGrantOnFirstBind: "true",
	}, cache, assigner)

	ctx := context.Background()
	user, err := client.User.Create().
		SetEmail("legacy-user" + identity.LinuxDoConnectSyntheticEmailDomain).
		SetUsername("legacy-user").
		SetPasswordHash("old-hash").
		SetBalance(2.5).
		SetConcurrency(1).
		SetRole(identity.RoleUser).
		SetStatus(identity.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	updatedUser, err := svc.BindEmailIdentity(ctx, user.ID, "  NewEmail@Example.com  ", "123456", "new-password")
	require.NoError(t, err)
	require.NotNil(t, updatedUser)
	require.Equal(t, "newemail@example.com", updatedUser.Email)

	storedUser, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, "newemail@example.com", storedUser.Email)
	require.Equal(t, 11.0, storedUser.Balance)
	require.Equal(t, 5, storedUser.Concurrency)
	require.True(t, svc.CheckPassword("new-password", storedUser.PasswordHash))

	identityCount, err := client.AuthIdentity.Query().
		Where(
			authidentity.UserIDEQ(user.ID),
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ("newemail@example.com"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, identityCount)

	require.Len(t, assigner.calls, 1)
	require.Equal(t, user.ID, assigner.calls[0].UserID)
	require.Equal(t, int64(11), assigner.calls[0].PlanID)
	require.Equal(t, 1, countProviderGrantRecords(t, client, user.ID, "email", "first_bind"))
}

func TestAuthServiceBindEmailIdentity_RejectsExistingEmailOnAnotherUser(t *testing.T) {
	cache := &emailBindCacheStub{
		data: &identity.VerificationCodeData{
			Code:      "123456",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
	}
	svc, _, client := newAuthServiceForEmailBind(t, nil, cache, nil)

	ctx := context.Background()
	sourceUser, err := client.User.Create().
		SetEmail("source-user" + identity.OIDCConnectSyntheticEmailDomain).
		SetUsername("source-user").
		SetPasswordHash("old-hash").
		SetBalance(1).
		SetConcurrency(1).
		SetRole(identity.RoleUser).
		SetStatus(identity.StatusActive).
		Save(ctx)
	require.NoError(t, err)
	_, err = client.User.Create().
		SetEmail("taken@example.com").
		SetUsername("taken-user").
		SetPasswordHash("hash").
		SetBalance(1).
		SetConcurrency(1).
		SetRole(identity.RoleUser).
		SetStatus(identity.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	updatedUser, err := svc.BindEmailIdentity(ctx, sourceUser.ID, "taken@example.com", "123456", "new-password")
	require.ErrorIs(t, err, identity.ErrEmailExists)
	require.Nil(t, updatedUser)

	storedUser, err := client.User.Get(ctx, sourceUser.ID)
	require.NoError(t, err)
	require.Equal(t, "source-user"+identity.OIDCConnectSyntheticEmailDomain, storedUser.Email)
	require.Equal(t, 0, countProviderGrantRecords(t, client, sourceUser.ID, "email", "first_bind"))
}

func TestAuthServiceBindEmailIdentity_RejectsBoundEmailChangeWhenDisabled(t *testing.T) {
	cache := &emailBindCacheStub{
		data: &identity.VerificationCodeData{
			Code:      "123456",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
	}
	svc, _, client := newAuthServiceForEmailBind(t, nil, cache, nil)

	ctx := context.Background()
	hashedPassword, err := svc.HashPassword("current-password")
	require.NoError(t, err)
	user := createEmailBindTestUser(t, client, "current@example.com", "bound-user", hashedPassword)

	err = svc.SendEmailIdentityBindCode(ctx, user.ID, "new@example.com")
	require.ErrorIs(t, err, identity.ErrEmailChangeDisabled)
	require.Empty(t, cache.setEmails)

	updatedUser, err := svc.BindEmailIdentity(ctx, user.ID, "new@example.com", "123456", "current-password")
	require.ErrorIs(t, err, identity.ErrEmailChangeDisabled)
	require.Nil(t, updatedUser)

	storedUser, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, "current@example.com", storedUser.Email)
}

func TestAuthServiceBindEmailIdentity_RejectsAliasOfExistingEmailOnAnotherUser(t *testing.T) {
	cache := &emailBindCacheStub{
		data: &identity.VerificationCodeData{
			Code:      "123456",
			CreatedAt: time.Now().UTC().Add(-10 * time.Minute),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
	}
	svc, _, client := newAuthServiceForEmailBind(t, nil, cache, nil)

	ctx := context.Background()
	sourceUser := createEmailBindTestUser(
		t,
		client,
		"source-user"+identity.OIDCConnectSyntheticEmailDomain,
		"source-user",
		"old-hash",
	)
	createEmailBindTestUser(t, client, "zck.ioio123@gmail.com", "inbox-owner", "hash")

	err := svc.SendEmailIdentityBindCode(ctx, sourceUser.ID, "zckioio123+new@gmail.com")
	require.ErrorIs(t, err, identity.ErrEmailExists)
	require.Empty(t, cache.setEmails)

	updatedUser, err := svc.BindEmailIdentity(
		ctx,
		sourceUser.ID,
		"zckioio123+new@gmail.com",
		"123456",
		"new-password",
	)
	require.ErrorIs(t, err, identity.ErrEmailExists)
	require.Nil(t, updatedUser)

	storedUser, err := client.User.Get(ctx, sourceUser.ID)
	require.NoError(t, err)
	require.Equal(t, "source-user"+identity.OIDCConnectSyntheticEmailDomain, storedUser.Email)
	require.Equal(t, "old-hash", storedUser.PasswordHash)
}

func TestAuthServiceBindEmailIdentity_AllowsOnlyOneConcurrentAliasVariant(t *testing.T) {
	cache := &emailBindCacheStub{
		data: &identity.VerificationCodeData{
			Code:      "123456",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
	}
	svc, _, client := newAuthServiceForEmailBind(t, nil, cache, nil)

	ctx := context.Background()
	unique := fmt.Sprintf("%d", time.Now().UnixNano())
	first := createEmailBindTestUser(
		t,
		client,
		"first-"+unique+identity.OIDCConnectSyntheticEmailDomain,
		"first-"+unique,
		"old-hash",
	)
	second := createEmailBindTestUser(
		t,
		client,
		"second-"+unique+identity.OIDCConnectSyntheticEmailDomain,
		"second-"+unique,
		"old-hash",
	)

	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := svc.BindEmailIdentity(ctx, first.ID, "inbox-"+unique+"+one@gmail.com", "123456", "new-password")
		results <- err
	}()
	go func() {
		<-start
		_, err := svc.BindEmailIdentity(ctx, second.ID, "inbox-"+unique+"+two@gmail.com", "123456", "new-password")
		results <- err
	}()
	close(start)

	var successes, conflicts int
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, identity.ErrEmailExists):
			conflicts++
		default:
			t.Fatalf("unexpected bind error: %v", err)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)

	boundCount, err := client.User.Query().
		Where(dbuser.EmailIn(
			"inbox-"+unique+"+one@gmail.com",
			"inbox-"+unique+"+two@gmail.com",
		)).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, boundCount)
}

func TestAuthServiceBindEmailIdentity_RejectsNewAliasWhenAnotherUserSharesCurrentUserInbox(t *testing.T) {
	cache := &emailBindCacheStub{
		data: &identity.VerificationCodeData{
			Code:      "123456",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
	}
	svc, _, client := newAuthServiceForEmailBind(t, map[string]string{
		identity.SettingKeyUserEmailChangeEnabled: "true",
	}, cache, nil)

	ctx := context.Background()
	hashedPassword, err := svc.HashPassword("current-password")
	require.NoError(t, err)
	currentUser := createEmailBindTestUser(t, client, "inbox+own@gmail.com", "current", hashedPassword)
	createEmailBindTestUser(t, client, "inbox+legacy@gmail.com", "legacy", "hash")

	updatedUser, err := svc.BindEmailIdentity(
		ctx,
		currentUser.ID,
		"inbox+new@gmail.com",
		"123456",
		"current-password",
	)
	require.ErrorIs(t, err, identity.ErrEmailExists)
	require.Nil(t, updatedUser)

	storedUser, err := client.User.Get(ctx, currentUser.ID)
	require.NoError(t, err)
	require.Equal(t, "inbox+own@gmail.com", storedUser.Email)
}

func TestAuthServiceBindEmailIdentity_RollsBackWhenFirstBindDefaultsFail(t *testing.T) {
	assigner := &flakyEmailBindDefaultSubAssignerStub{err: errors.New("temporary assign failure")}
	cache := &emailBindCacheStub{
		data: &identity.VerificationCodeData{
			Code:      "123456",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
	}
	svc, _, client := newAuthServiceForEmailBind(t, map[string]string{
		identity.SettingKeyAuthSourceDefaultEmailBalance:          "8.5",
		identity.SettingKeyAuthSourceDefaultEmailConcurrency:      "4",
		identity.SettingKeyAuthSourceDefaultEmailSubscriptions:    `[{"plan_id":11}]`,
		identity.SettingKeyAuthSourceDefaultEmailGrantOnFirstBind: "true",
	}, cache, assigner)

	ctx := context.Background()
	originalEmail := "legacy-rollback" + identity.LinuxDoConnectSyntheticEmailDomain
	user, err := client.User.Create().
		SetEmail(originalEmail).
		SetUsername("legacy-rollback").
		SetPasswordHash("old-hash").
		SetBalance(2.5).
		SetConcurrency(1).
		SetRole(identity.RoleUser).
		SetStatus(identity.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	updatedUser, err := svc.BindEmailIdentity(ctx, user.ID, "rollback@example.com", "123456", "new-password")
	require.ErrorContains(t, err, "apply email first bind defaults")
	require.ErrorContains(t, err, "temporary assign failure")
	require.Nil(t, updatedUser)

	storedUser, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, originalEmail, storedUser.Email)
	require.Equal(t, "old-hash", storedUser.PasswordHash)
	require.Equal(t, 2.5, storedUser.Balance)
	require.Equal(t, 1, storedUser.Concurrency)

	identityCount, err := client.AuthIdentity.Query().
		Where(
			authidentity.UserIDEQ(user.ID),
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ("rollback@example.com"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, identityCount)

	require.Len(t, assigner.calls, 1)
	require.Equal(t, 0, countProviderGrantRecords(t, client, user.ID, "email", "first_bind"))
}

func TestAuthServiceBindEmailIdentity_RejectsReservedEmail(t *testing.T) {
	cache := &emailBindCacheStub{
		data: &identity.VerificationCodeData{
			Code:      "123456",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
	}
	svc, _, client := newAuthServiceForEmailBind(t, nil, cache, nil)

	ctx := context.Background()
	user, err := client.User.Create().
		SetEmail("source-user@example.com").
		SetUsername("source-user").
		SetPasswordHash("old-hash").
		SetBalance(1).
		SetConcurrency(1).
		SetRole(identity.RoleUser).
		SetStatus(identity.StatusActive).
		Save(ctx)
	require.NoError(t, err)

	updatedUser, err := svc.BindEmailIdentity(ctx, user.ID, "reserved"+identity.LinuxDoConnectSyntheticEmailDomain, "123456", "new-password")
	require.ErrorIs(t, err, identity.ErrEmailReserved)
	require.Nil(t, updatedUser)
}

func TestAuthServiceBindEmailIdentity_ReplacesBoundEmailAndSkipsFirstBindDefaults(t *testing.T) {
	assigner := &emailBindDefaultSubAssignerStub{}
	cache := &emailBindCacheStub{
		data: &identity.VerificationCodeData{
			Code:      "123456",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
	}
	svc, _, client := newAuthServiceForEmailBind(t, map[string]string{
		identity.SettingKeyAuthSourceDefaultEmailBalance:          "8.5",
		identity.SettingKeyAuthSourceDefaultEmailConcurrency:      "4",
		identity.SettingKeyAuthSourceDefaultEmailSubscriptions:    `[{"plan_id":11}]`,
		identity.SettingKeyAuthSourceDefaultEmailGrantOnFirstBind: "true",
		identity.SettingKeyUserEmailChangeEnabled:                 "true",
	}, cache, assigner)

	ctx := context.Background()
	hashedPassword, err := svc.HashPassword("current-password")
	require.NoError(t, err)

	user, err := client.User.Create().
		SetEmail("current@example.com").
		SetUsername("bound-user").
		SetPasswordHash(hashedPassword).
		SetBalance(7.5).
		SetConcurrency(3).
		SetRole(identity.RoleUser).
		SetStatus(identity.StatusActive).
		Save(ctx)
	require.NoError(t, err)
	require.NoError(t, client.AuthIdentity.Create().
		SetUserID(user.ID).
		SetProviderType("email").
		SetProviderKey("email").
		SetProviderSubject("current@example.com").
		SetVerifiedAt(time.Now().UTC()).
		SetMetadata(map[string]any{"source": "test"}).
		Exec(ctx))

	updatedUser, err := svc.BindEmailIdentity(ctx, user.ID, "new@example.com", "123456", "current-password")
	require.NoError(t, err)
	require.NotNil(t, updatedUser)
	require.Equal(t, "new@example.com", updatedUser.Email)

	storedUser, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, "new@example.com", storedUser.Email)
	require.Equal(t, 7.5, storedUser.Balance)
	require.Equal(t, 3, storedUser.Concurrency)
	require.True(t, svc.CheckPassword("current-password", storedUser.PasswordHash))

	newIdentityCount, err := client.AuthIdentity.Query().
		Where(
			authidentity.UserIDEQ(user.ID),
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ("new@example.com"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, newIdentityCount)

	oldIdentityCount, err := client.AuthIdentity.Query().
		Where(
			authidentity.UserIDEQ(user.ID),
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ("current@example.com"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, oldIdentityCount)

	require.Empty(t, assigner.calls)
	require.Equal(t, 0, countProviderGrantRecords(t, client, user.ID, "email", "first_bind"))
}

func TestAuthServiceBindEmailIdentity_RejectsWrongCurrentPasswordForBoundEmail(t *testing.T) {
	cache := &emailBindCacheStub{
		data: &identity.VerificationCodeData{
			Code:      "123456",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
	}
	svc, _, client := newAuthServiceForEmailBind(t, map[string]string{
		identity.SettingKeyUserEmailChangeEnabled: "true",
	}, cache, nil)

	ctx := context.Background()
	hashedPassword, err := svc.HashPassword("current-password")
	require.NoError(t, err)

	user, err := client.User.Create().
		SetEmail("current@example.com").
		SetUsername("bound-user").
		SetPasswordHash(hashedPassword).
		SetBalance(1).
		SetConcurrency(1).
		SetRole(identity.RoleUser).
		SetStatus(identity.StatusActive).
		Save(ctx)
	require.NoError(t, err)
	require.NoError(t, client.AuthIdentity.Create().
		SetUserID(user.ID).
		SetProviderType("email").
		SetProviderKey("email").
		SetProviderSubject("current@example.com").
		SetVerifiedAt(time.Now().UTC()).
		SetMetadata(map[string]any{"source": "test"}).
		Exec(ctx))

	updatedUser, err := svc.BindEmailIdentity(ctx, user.ID, "new@example.com", "123456", "wrong-password")
	require.ErrorIs(t, err, identity.ErrPasswordIncorrect)
	require.Nil(t, updatedUser)

	storedUser, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, "current@example.com", storedUser.Email)
	require.True(t, svc.CheckPassword("current-password", storedUser.PasswordHash))

	oldIdentityCount, err := client.AuthIdentity.Query().
		Where(
			authidentity.UserIDEQ(user.ID),
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ("current@example.com"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, oldIdentityCount)

	newIdentityCount, err := client.AuthIdentity.Query().
		Where(
			authidentity.UserIDEQ(user.ID),
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ("new@example.com"),
		).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, newIdentityCount)
}

func TestAuthServiceBindEmailIdentity_RevokesExistingAccessAndRefreshTokens(t *testing.T) {
	ctx := context.Background()
	cache := &emailBindCacheStub{
		data: &identity.VerificationCodeData{
			Code:      "123456",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
	}
	refreshTokenCache := newEmailBindRefreshTokenCacheStub()
	userRepo := newEmailBindUserRepoStub(&identity.User{
		ID:           41,
		Email:        "legacy-user" + identity.OIDCConnectSyntheticEmailDomain,
		Username:     "legacy-user",
		PasswordHash: "old-hash",
		Role:         identity.RoleUser,
		Status:       identity.StatusActive,
		TokenVersion: 4,
	})
	options := &identity.AuthOptions{
		JWT: identity.SessionOptions{
			Secret:                   "test-bind-email-secret",
			ExpireHour:               1,
			AccessTokenExpireMinutes: 60,
			RefreshTokenExpireDays:   7,
		},
	}
	svc := newIdentityAuthForTest(nil, userRepo, options, nil, cache, refreshTokenCache, nil)

	oldTokenPair, err := svc.GenerateTokenPair(ctx, &identity.User{
		ID:           41,
		Email:        "legacy-user" + identity.OIDCConnectSyntheticEmailDomain,
		Role:         identity.RoleUser,
		Status:       identity.StatusActive,
		TokenVersion: 4,
	}, "")
	require.NoError(t, err)

	updatedUser, err := svc.BindEmailIdentity(ctx, 41, "new@example.com", "123456", "new-password")
	require.NoError(t, err)
	require.NotNil(t, updatedUser)

	storedUser, err := userRepo.GetByID(ctx, 41)
	require.NoError(t, err)
	require.Equal(t, "new@example.com", storedUser.Email)
	require.True(t, svc.CheckPassword("new-password", storedUser.PasswordHash))

	_, err = svc.RefreshToken(ctx, oldTokenPair.AccessToken)
	require.ErrorIs(t, err, identity.ErrTokenRevoked)

	_, err = svc.RefreshTokenPair(ctx, oldTokenPair.RefreshToken)
	require.True(t, errors.Is(err, identity.ErrTokenRevoked) || errors.Is(err, identity.ErrRefreshTokenInvalid))
}

func TestAuthServiceEmailIdentityBinding_RejectsEmailOutsideRegistrationSuffixWhitelist(t *testing.T) {
	ctx := context.Background()
	cache := &emailBindCacheStub{
		data: &identity.VerificationCodeData{
			Code:      "123456",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
	}
	svc, _, client := newAuthServiceForEmailBind(t, map[string]string{
		identity.SettingKeyRegistrationEmailSuffixWhitelist: `["@qq.com"]`,
	}, cache, nil)

	user := createEmailBindTestUser(t, client, "legacy-user"+identity.OIDCConnectSyntheticEmailDomain, "legacy-user", "old-hash")

	err := svc.SendEmailIdentityBindCode(ctx, user.ID, "intruder@gmail.com")
	require.ErrorIs(t, err, identity.ErrEmailSuffixNotAllowed)
	require.Empty(t, cache.setEmails)

	updatedUser, err := svc.BindEmailIdentity(ctx, user.ID, "intruder@gmail.com", "123456", "new-password")
	require.ErrorIs(t, err, identity.ErrEmailSuffixNotAllowed)
	require.Nil(t, updatedUser)

	storedUser, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, "legacy-user"+identity.OIDCConnectSyntheticEmailDomain, storedUser.Email)
}

func TestAuthServiceBindEmailIdentity_AllowsEmailInsideRegistrationSuffixWhitelist(t *testing.T) {
	ctx := context.Background()
	cache := &emailBindCacheStub{
		data: &identity.VerificationCodeData{
			Code:      "123456",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
	}
	svc, _, client := newAuthServiceForEmailBind(t, map[string]string{
		identity.SettingKeyRegistrationEmailSuffixWhitelist: `["@qq.com"]`,
	}, cache, nil)

	user := createEmailBindTestUser(t, client, "legacy-qq"+identity.LinuxDoConnectSyntheticEmailDomain, "legacy-qq", "old-hash")

	updatedUser, err := svc.BindEmailIdentity(ctx, user.ID, " Member@QQ.com ", "123456", "new-password")
	require.NoError(t, err)
	require.NotNil(t, updatedUser)
	require.Equal(t, "member@qq.com", updatedUser.Email)

	storedUser, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, "member@qq.com", storedUser.Email)
}

func TestAuthServiceBindEmailIdentity_RegistrationSuffixWhitelistWildcard(t *testing.T) {
	ctx := context.Background()

	t.Run("allows wildcard suffix", func(t *testing.T) {
		cache := &emailBindCacheStub{
			data: &identity.VerificationCodeData{
				Code:      "123456",
				CreatedAt: time.Now().UTC(),
				ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
			},
		}
		svc, _, client := newAuthServiceForEmailBind(t, map[string]string{
			identity.SettingKeyRegistrationEmailSuffixWhitelist: `["*.edu.cn"]`,
		}, cache, nil)
		user := createEmailBindTestUser(t, client, "legacy-student"+identity.OIDCConnectSyntheticEmailDomain, "legacy-student", "old-hash")

		updatedUser, err := svc.BindEmailIdentity(ctx, user.ID, "student@cs.edu.cn", "123456", "new-password")
		require.NoError(t, err)
		require.NotNil(t, updatedUser)
		require.Equal(t, "student@cs.edu.cn", updatedUser.Email)
	})

	t.Run("rejects outside wildcard suffix", func(t *testing.T) {
		cache := &emailBindCacheStub{
			data: &identity.VerificationCodeData{
				Code:      "123456",
				CreatedAt: time.Now().UTC(),
				ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
			},
		}
		svc, _, client := newAuthServiceForEmailBind(t, map[string]string{
			identity.SettingKeyRegistrationEmailSuffixWhitelist: `["*.edu.cn"]`,
		}, cache, nil)
		user := createEmailBindTestUser(t, client, "legacy-wildcard"+identity.OIDCConnectSyntheticEmailDomain, "legacy-wildcard", "old-hash")

		updatedUser, err := svc.BindEmailIdentity(ctx, user.ID, "foo@gmail.com", "123456", "new-password")
		require.ErrorIs(t, err, identity.ErrEmailSuffixNotAllowed)
		require.Nil(t, updatedUser)

		storedUser, err := client.User.Get(ctx, user.ID)
		require.NoError(t, err)
		require.Equal(t, "legacy-wildcard"+identity.OIDCConnectSyntheticEmailDomain, storedUser.Email)
	})
}

func TestAuthServiceBindEmailIdentity_AllowsAnyEmailWhenRegistrationSuffixWhitelistEmpty(t *testing.T) {
	ctx := context.Background()
	cache := &emailBindCacheStub{
		data: &identity.VerificationCodeData{
			Code:      "123456",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
	}
	svc, _, client := newAuthServiceForEmailBind(t, map[string]string{
		identity.SettingKeyRegistrationEmailSuffixWhitelist: "[]",
	}, cache, nil)

	user := createEmailBindTestUser(t, client, "legacy-empty"+identity.LinuxDoConnectSyntheticEmailDomain, "legacy-empty", "old-hash")

	updatedUser, err := svc.BindEmailIdentity(ctx, user.ID, "anyone@gmail.com", "123456", "new-password")
	require.NoError(t, err)
	require.NotNil(t, updatedUser)
	require.Equal(t, "anyone@gmail.com", updatedUser.Email)
}

func createEmailBindTestUser(t *testing.T, client *dbent.Client, email, username, passwordHash string) *dbent.User {
	t.Helper()

	user, err := client.User.Create().
		SetEmail(email).
		SetUsername(username).
		SetPasswordHash(passwordHash).
		SetBalance(1).
		SetConcurrency(1).
		SetRole(identity.RoleUser).
		SetStatus(identity.StatusActive).
		Save(context.Background())
	require.NoError(t, err)
	return user
}

type emailBindSettingRepoStub struct {
	values map[string]string
}

func (s *emailBindSettingRepoStub) Get(context.Context, string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

func (s *emailBindSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", settingscore.ErrSettingNotFound
}

func (s *emailBindSettingRepoStub) Set(context.Context, string, string) error {
	panic("unexpected Set call")
}

func (s *emailBindSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if v, ok := s.values[key]; ok {
			out[key] = v
		}
	}
	return out, nil
}

func (s *emailBindSettingRepoStub) SetMultiple(context.Context, map[string]string) error {
	panic("unexpected SetMultiple call")
}

func (s *emailBindSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *emailBindSettingRepoStub) Delete(context.Context, string) error {
	panic("unexpected Delete call")
}

type emailBindCacheStub struct {
	data      *identity.VerificationCodeData
	err       error
	setEmails []string
}

func (s *emailBindCacheStub) GetVerificationCode(context.Context, string) (*identity.VerificationCodeData, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.data, nil
}

func (s *emailBindCacheStub) SetVerificationCode(_ context.Context, email string, _ *identity.VerificationCodeData, _ time.Duration) error {
	s.setEmails = append(s.setEmails, email)
	return nil
}

func (s *emailBindCacheStub) DeleteVerificationCode(context.Context, string) error {
	return nil
}

func (s *emailBindCacheStub) GetNotifyVerifyCode(context.Context, string) (*identity.VerificationCodeData, error) {
	return nil, nil
}

func (s *emailBindCacheStub) SetNotifyVerifyCode(context.Context, string, *identity.VerificationCodeData, time.Duration) error {
	return nil
}

func (s *emailBindCacheStub) DeleteNotifyVerifyCode(context.Context, string) error {
	return nil
}

func (s *emailBindCacheStub) GetPasswordResetToken(context.Context, string) (*identity.PasswordResetTokenData, error) {
	return nil, nil
}

func (s *emailBindCacheStub) SetPasswordResetToken(context.Context, string, *identity.PasswordResetTokenData, time.Duration) error {
	return nil
}

func (s *emailBindCacheStub) DeletePasswordResetToken(context.Context, string) error {
	return nil
}

func (s *emailBindCacheStub) IsPasswordResetEmailInCooldown(context.Context, string) bool {
	return false
}

func (s *emailBindCacheStub) SetPasswordResetEmailCooldown(context.Context, string, time.Duration) error {
	return nil
}

func (s *emailBindCacheStub) GetNotifyCodeUserRate(context.Context, int64) (int64, error) {
	return 0, nil
}

func (s *emailBindCacheStub) IncrNotifyCodeUserRate(context.Context, int64, time.Duration) (int64, error) {
	return 0, nil
}

type emailBindRefreshTokenCacheStub struct {
	mu       sync.Mutex
	tokens   map[string]*identity.RefreshTokenData
	userSets map[int64]map[string]struct{}
	families map[string]map[string]struct{}
}

func newEmailBindRefreshTokenCacheStub() *emailBindRefreshTokenCacheStub {
	return &emailBindRefreshTokenCacheStub{
		tokens:   make(map[string]*identity.RefreshTokenData),
		userSets: make(map[int64]map[string]struct{}),
		families: make(map[string]map[string]struct{}),
	}
}

func (s *emailBindRefreshTokenCacheStub) StoreRefreshToken(_ context.Context, tokenHash string, data *identity.RefreshTokenData, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cloned := *data
	s.tokens[tokenHash] = &cloned
	return nil
}

func (s *emailBindRefreshTokenCacheStub) GetRefreshToken(_ context.Context, tokenHash string) (*identity.RefreshTokenData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.tokens[tokenHash]
	if !ok {
		return nil, identity.ErrRefreshTokenNotFound
	}
	cloned := *data
	return &cloned, nil
}

func (s *emailBindRefreshTokenCacheStub) DeleteRefreshToken(_ context.Context, tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, tokenHash)
	for _, tokenSet := range s.userSets {
		delete(tokenSet, tokenHash)
	}
	for _, tokenSet := range s.families {
		delete(tokenSet, tokenHash)
	}
	return nil
}

func (s *emailBindRefreshTokenCacheStub) DeleteUserRefreshTokens(_ context.Context, userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tokenHash := range s.userSets[userID] {
		delete(s.tokens, tokenHash)
		for _, tokenSet := range s.families {
			delete(tokenSet, tokenHash)
		}
	}
	delete(s.userSets, userID)
	return nil
}

func (s *emailBindRefreshTokenCacheStub) DeleteTokenFamily(_ context.Context, familyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for tokenHash := range s.families[familyID] {
		delete(s.tokens, tokenHash)
		for _, tokenSet := range s.userSets {
			delete(tokenSet, tokenHash)
		}
	}
	delete(s.families, familyID)
	return nil
}

func (s *emailBindRefreshTokenCacheStub) AddToUserTokenSet(_ context.Context, userID int64, tokenHash string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.userSets[userID] == nil {
		s.userSets[userID] = make(map[string]struct{})
	}
	s.userSets[userID][tokenHash] = struct{}{}
	return nil
}

func (s *emailBindRefreshTokenCacheStub) AddToFamilyTokenSet(_ context.Context, familyID string, tokenHash string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.families[familyID] == nil {
		s.families[familyID] = make(map[string]struct{})
	}
	s.families[familyID][tokenHash] = struct{}{}
	return nil
}

func (s *emailBindRefreshTokenCacheStub) GetUserTokenHashes(_ context.Context, userID int64) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tokenSet := s.userSets[userID]
	out := make([]string, 0, len(tokenSet))
	for tokenHash := range tokenSet {
		out = append(out, tokenHash)
	}
	return out, nil
}

func (s *emailBindRefreshTokenCacheStub) GetFamilyTokenHashes(_ context.Context, familyID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tokenSet := s.families[familyID]
	out := make([]string, 0, len(tokenSet))
	for tokenHash := range tokenSet {
		out = append(out, tokenHash)
	}
	return out, nil
}

func (s *emailBindRefreshTokenCacheStub) IsTokenInFamily(_ context.Context, familyID string, tokenHash string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.families[familyID][tokenHash]
	return ok, nil
}

type emailBindUserRepoStub struct {
	mu           sync.Mutex
	usersByID    map[int64]*identity.User
	usersByEmail map[string]*identity.User
}

func newEmailBindUserRepoStub(user *identity.User) *emailBindUserRepoStub {
	cloned := cloneEmailBindUser(user)
	return &emailBindUserRepoStub{
		usersByID: map[int64]*identity.User{
			cloned.ID: cloned,
		},
		usersByEmail: map[string]*identity.User{
			cloned.Email: cloned,
		},
	}
}

func (s *emailBindUserRepoStub) Create(context.Context, *identity.User) error { return nil }

func (s *emailBindUserRepoStub) CreateWithNormalizedEmailGuard(ctx context.Context, user *identity.User, _ string) error {
	return s.Create(ctx, user)
}

func (s *emailBindUserRepoStub) GetByID(_ context.Context, id int64) (*identity.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.usersByID[id]
	if !ok {
		return nil, identity.ErrUserNotFound
	}
	return cloneEmailBindUser(user), nil
}

func (s *emailBindUserRepoStub) GetByEmail(_ context.Context, email string) (*identity.User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.usersByEmail[email]
	if !ok {
		return nil, identity.ErrUserNotFound
	}
	return cloneEmailBindUser(user), nil
}

func (s *emailBindUserRepoStub) GetFirstAdmin(context.Context) (*identity.User, error) {
	panic("unexpected GetFirstAdmin call")
}

func (s *emailBindUserRepoStub) Update(_ context.Context, user *identity.User, _ identity.UserUpdateFields) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.usersByID[user.ID]
	if !ok {
		return identity.ErrUserNotFound
	}
	delete(s.usersByEmail, existing.Email)
	cloned := cloneEmailBindUser(user)
	s.usersByID[user.ID] = cloned
	s.usersByEmail[cloned.Email] = cloned
	return nil
}

func (s *emailBindUserRepoStub) UpdateWithNormalizedEmailGuard(ctx context.Context, user *identity.User, _ string, fields identity.UserUpdateFields) error {
	return s.Update(ctx, user, fields)
}

func (s *emailBindUserRepoStub) Delete(context.Context, int64) error { return nil }

func (s *emailBindUserRepoStub) GetUserAvatar(context.Context, int64) (*identity.UserAvatar, error) {
	return nil, nil
}

func (s *emailBindUserRepoStub) UpsertUserAvatar(context.Context, int64, identity.UpsertUserAvatarInput) (*identity.UserAvatar, error) {
	panic("unexpected UpsertUserAvatar call")
}

func (s *emailBindUserRepoStub) DeleteUserAvatar(context.Context, int64) error {
	panic("unexpected DeleteUserAvatar call")
}

func (s *emailBindUserRepoStub) List(context.Context, pagination.PaginationParams) ([]identity.User, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *emailBindUserRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, identity.UserListFilters) ([]identity.User, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *emailBindUserRepoStub) GetLatestUsedAtByUserIDs(context.Context, []int64) (map[int64]*time.Time, error) {
	return map[int64]*time.Time{}, nil
}

func (s *emailBindUserRepoStub) GetLatestUsedAtByUserID(context.Context, int64) (*time.Time, error) {
	return nil, nil
}

func (s *emailBindUserRepoStub) UpdateUserLastActiveAt(context.Context, int64, time.Time) error {
	return nil
}

func (s *emailBindUserRepoStub) AddBalance(_ context.Context, id int64, amount float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	user, ok := s.usersByID[id]
	if !ok {
		return identity.ErrUserNotFound
	}
	user.Balance += amount
	return nil
}

func (s *emailBindUserRepoStub) UpdateBalance(ctx context.Context, id int64, amount float64) error {
	return s.AddBalance(ctx, id, amount)
}

func (s *emailBindUserRepoStub) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	if err := s.AddBalance(ctx, id, -amount); err != nil {
		return 0, err
	}
	return amount, nil
}
func (s *emailBindUserRepoStub) UpdateConcurrency(context.Context, int64, int) error { return nil }
func (s *emailBindUserRepoStub) BatchSetConcurrency(context.Context, []int64, int) (int, error) {
	return 0, nil
}
func (s *emailBindUserRepoStub) BatchAddConcurrency(context.Context, []int64, int) (int, error) {
	return 0, nil
}

func (s *emailBindUserRepoStub) ExistsByEmail(_ context.Context, email string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.usersByEmail[email]
	return ok, nil
}

func (s *emailBindUserRepoStub) ExistsByNormalizedEmail(_ context.Context, normalizedEmail string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for email := range s.usersByEmail {
		if identity.NormalizeRegistrationEmailAddress(email) == normalizedEmail {
			return true, nil
		}
	}
	return false, nil
}

func (s *emailBindUserRepoStub) AdjustBalance(ctx context.Context, id int64, delta float64) (identity.BalanceChange, error) {
	panic("unexpected AdjustBalance call")
}

func (s *emailBindUserRepoStub) SetBalance(ctx context.Context, id int64, value float64) (identity.BalanceChange, error) {
	panic("unexpected SetBalance call")
}

func (s *emailBindUserRepoStub) LockRegistrationEmail(context.Context, string) error {
	return nil
}
func (s *emailBindUserRepoStub) BatchUpdateLimits(context.Context, []int64, *int, *int) (int, error) {
	return 0, nil
}

func (s *emailBindUserRepoStub) RemoveGroupFromAllowedGroups(context.Context, int64) (int64, error) {
	return 0, nil
}

func (s *emailBindUserRepoStub) AddGroupToAllowedGroups(context.Context, int64, int64) error {
	return nil
}

func (s *emailBindUserRepoStub) RemoveGroupFromUserAllowedGroups(context.Context, int64, int64) error {
	return nil
}

func (s *emailBindUserRepoStub) ListUserAuthIdentities(context.Context, int64) ([]identity.UserAuthIdentityRecord, error) {
	return nil, nil
}

func (s *emailBindUserRepoStub) UnbindUserAuthProvider(context.Context, int64, string) error {
	return nil
}

func (s *emailBindUserRepoStub) UpdateTotpSecret(context.Context, int64, *string) error { return nil }
func (s *emailBindUserRepoStub) EnableTotp(context.Context, int64) error                { return nil }
func (s *emailBindUserRepoStub) DisableTotp(context.Context, int64) error               { return nil }
func (s *emailBindUserRepoStub) GetByIDIncludeDeleted(ctx context.Context, id int64) (*identity.User, error) {
	return s.GetByID(ctx, id)
}

func cloneEmailBindUser(user *identity.User) *identity.User {
	if user == nil {
		return nil
	}
	cloned := *user
	return &cloned
}

// ConsumeRefreshToken 在同一互斥内完成存在性判断与删除，保持测试中的一次性轮换。
func (s *emailBindRefreshTokenCacheStub) ConsumeRefreshToken(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tokens[key]; !ok {
		return false, nil
	}
	delete(s.tokens, key)
	for _, set := range s.userSets {
		delete(set, key)
	}
	for _, set := range s.families {
		delete(set, key)
	}
	return true, nil
}
