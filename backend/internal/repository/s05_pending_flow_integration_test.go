//go:build integration

package repository

import (
	"context"
	"errors"
	"testing"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/authidentity"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	identitytestkit "github.com/TokenFlux/TokenRouter/internal/identity/testkit"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// TestS05PendingFinalizeRollbackAndCompensation 验证原两段注册事务在真实 PostgreSQL 上的回滚与补偿。
func TestS05PendingFinalizeRollbackAndCompensation(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	users := identitypostgres.NewUserStore(client, integrationDB)
	auth := identitytestkit.Auth(client, &identity.AuthDependencies{Users: users, Options: identitytestkit.AuthOptions(&config.Config{})})
	pending := identitypostgres.NewPendingRepository(client)
	flow := &identity.PendingFlow{Auth: auth, Store: pending, Database: &identitypostgres.PendingFlowDatabase{Client: client, Auth: auth}}
	for _, fail := range []bool{true, false} {
		label := "commit"
		if fail {
			label = "rollback"
		}
		t.Run(label, func(t *testing.T) {
			user := mustCreateUser(t, client, &identity.User{})
			session, err := pending.CreatePendingSession(ctx, identity.CreatePendingAuthSessionInput{Intent: "login", Identity: identity.PendingAuthIdentityKey{ProviderType: "oidc", ProviderKey: "https://s05.example.invalid", ProviderSubject: uuid.NewString()}, BrowserSessionKey: uuid.NewString(), ResolvedEmail: user.Email})
			require.NoError(t, err)
			failure := errors.New("s05 after identity binding and pending consume")
			called := false
			request := identity.PendingAccountFinalization{Session: session, User: identity.CopyUser(user), BeforeCommit: func(txCtx context.Context, _ *identity.PendingAuthSession) error {
				called = true
				tx := dbent.TxFromContext(txCtx)
				require.NotNil(t, tx, "必须沿用原 Ent context")
				n, err := tx.Client().AuthIdentity.Query().Where(authidentity.UserIDEQ(user.ID)).Count(txCtx)
				require.NoError(t, err)
				require.Equal(t, 1, n)
				inside, err := tx.Client().PendingAuthSession.Get(txCtx, session.ID)
				require.NoError(t, err)
				require.NotNil(t, inside.ConsumedAt, "绑定与 pending 消费必须在同一事务可见")
				outside, err := client.PendingAuthSession.Get(ctx, session.ID)
				require.NoError(t, err)
				require.Nil(t, outside.ConsumedAt, "提交前其他连接不能看到成功消费")
				if fail {
					return failure
				}
				return nil
			}}
			err = flow.FinalizeCreatedAccount(ctx, request)
			require.True(t, called)
			stored, loadErr := client.PendingAuthSession.Get(ctx, session.ID)
			require.NoError(t, loadErr)
			n, loadErr := client.AuthIdentity.Query().Where(authidentity.UserIDEQ(user.ID)).Count(ctx)
			require.NoError(t, loadErr)
			if fail {
				require.ErrorIs(t, err, failure)
				require.Nil(t, stored.ConsumedAt)
				require.Zero(t, n)
				_, err = users.GetByID(ctx, user.ID)
				require.ErrorIs(t, err, identity.ErrUserNotFound, "绑定失败后应补偿已经创建的用户")
			} else {
				require.NoError(t, err)
				require.NotNil(t, stored.ConsumedAt)
				require.Equal(t, 1, n)
				_, err = users.GetByID(ctx, user.ID)
				require.NoError(t, err)
			}
		})
	}
}
