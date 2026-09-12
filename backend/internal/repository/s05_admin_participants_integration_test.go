//go:build integration

package repository

import (
	"context"
	"errors"
	"testing"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	keycore "github.com/TokenFlux/TokenRouter/internal/apikey"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// s05AdminFailure 在参与写入已经执行后返回错误，检测独立提交与漏回滚。
type s05AdminFailure struct {
	identity.AdminKeyParticipant
	failure error
}

func (p s05AdminFailure) DeleteWithAudit(ctx context.Context, id int64) error {
	if err := p.AdminKeyParticipant.DeleteWithAudit(ctx, id); err != nil {
		return err
	}
	return p.failure
}
func (p s05AdminFailure) UpdateGroupIDByUserAndGroup(ctx context.Context, id, oldID, newID int64) (int64, error) {
	n, err := p.AdminKeyParticipant.UpdateGroupIDByUserAndGroup(ctx, id, oldID, newID)
	if err != nil {
		return n, err
	}
	return n, p.failure
}

type s05KeyUpdateFailure struct {
	keycore.APIKeyRepository
	failure error
}

func (p s05KeyUpdateFailure) Update(ctx context.Context, key *keycore.APIKey, fields keycore.APIKeyUpdateFields) error {
	if err := p.APIKeyRepository.Update(ctx, key, fields); err != nil {
		return err
	}
	return p.failure
}

func TestS05AdminDeleteRollsBackKeyParticipant(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	users := NewUserRepository(client, integrationDB)
	keys := keypostgres.NewKeyStore(client, integrationDB, nil)
	user := mustCreateUser(t, client, &service.User{})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "s05-delete-" + uuid.NewString()})
	failure := errors.New("s05 after tombstone write")
	mutations := &identitypostgres.AdminMutations{Client: client, Users: service.IdentityRepository(users), Keys: keys, KeysInTx: func(tx *dbent.Tx) identity.AdminKeyParticipant {
		return s05AdminFailure{keys.LifecycleInTx(tx), failure}
	}}
	before := s05OutboxCount(t, ctx, key.Key)
	err := mutations.DeleteUserAndKeys(ctx, user.ID, []identity.AdminKeySummary{{ID: key.ID, Key: key.Key}})
	require.ErrorIs(t, err, failure)
	_, err = users.GetByID(ctx, user.ID)
	require.NoError(t, err)
	stored, err := client.APIKey.Get(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, key.Key, stored.Key)
	require.Equal(t, before, s05OutboxCount(t, ctx, key.Key), "回滚不能留下成功失效事件")
	mutations.KeysInTx = func(tx *dbent.Tx) identity.AdminKeyParticipant { return keys.LifecycleInTx(tx) }
	require.NoError(t, mutations.DeleteUserAndKeys(ctx, user.ID, []identity.AdminKeySummary{{ID: key.ID, Key: key.Key}}))
	_, err = users.GetByID(ctx, user.ID)
	require.ErrorIs(t, err, identity.ErrUserNotFound)
}

func TestS05GroupReplacementRollsBackKeyParticipant(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	users := NewUserRepository(client, integrationDB)
	keys := keypostgres.NewKeyStore(client, integrationDB, nil)
	old := mustCreateGroup(t, client, &service.Group{Name: "s05-old-" + uuid.NewString(), IsExclusive: true})
	next := mustCreateGroup(t, client, &service.Group{Name: "s05-new-" + uuid.NewString(), IsExclusive: true})
	user := mustCreateUser(t, client, &service.User{AllowedGroups: []int64{old.ID}})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, GroupID: &old.ID, Key: "s05-replace-" + uuid.NewString()})
	failure := errors.New("s05 after group migration")
	mutations := &identitypostgres.AdminMutations{Client: client, Users: service.IdentityRepository(users), Keys: keys, KeysInTx: func(tx *dbent.Tx) identity.AdminKeyParticipant {
		return s05AdminFailure{keys.LifecycleInTx(tx), failure}
	}}
	before := s05OutboxCount(t, ctx, key.Key)
	_, err := mutations.ReplaceUserGroup(ctx, user.ID, old.ID, next.ID)
	require.ErrorIs(t, err, failure)
	stored, err := client.APIKey.Get(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, old.ID, *stored.GroupID)
	reloaded, err := users.GetByID(ctx, user.ID)
	require.NoError(t, err)
	require.Contains(t, reloaded.AllowedGroups, old.ID)
	require.NotContains(t, reloaded.AllowedGroups, next.ID)
	require.Equal(t, before, s05OutboxCount(t, ctx, key.Key))
	mutations.KeysInTx = func(tx *dbent.Tx) identity.AdminKeyParticipant { return keys.LifecycleInTx(tx) }
	n, err := mutations.ReplaceUserGroup(ctx, user.ID, old.ID, next.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	reloaded, err = users.GetByID(ctx, user.ID)
	require.NoError(t, err)
	require.NotContains(t, reloaded.AllowedGroups, old.ID)
	require.Contains(t, reloaded.AllowedGroups, next.ID)
}

func TestS05AdminKeyGrantRollsBackIdentityParticipant(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	users := NewUserRepository(client, integrationDB)
	keys := keypostgres.NewKeyStore(client, integrationDB, nil)
	group := mustCreateGroup(t, client, &service.Group{Name: "s05-grant-" + uuid.NewString(), IsExclusive: true})
	user := mustCreateUser(t, client, &service.User{})
	old := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "s05-grant-" + uuid.NewString()})
	key, err := keys.GetByID(ctx, old.ID)
	require.NoError(t, err)
	key.GroupID = &group.ID
	failure := errors.New("s05 after key group update")
	mutations := &keypostgres.AdminGroupMutations{Client: client, Users: users, Keys: s05KeyUpdateFailure{keys, failure}, UsersInTx: func(tx *dbent.Tx) keypostgres.GroupAccessWriter { return identitypostgres.GroupAccessInTx(tx) }}
	before := s05OutboxCount(t, ctx, old.Key)
	require.ErrorIs(t, mutations.GrantGroupAndUpdate(ctx, key, group.ID), failure)
	reloaded, err := users.GetByID(ctx, user.ID)
	require.NoError(t, err)
	require.NotContains(t, reloaded.AllowedGroups, group.ID)
	stored, err := client.APIKey.Get(ctx, old.ID)
	require.NoError(t, err)
	require.Nil(t, stored.GroupID)
	require.Equal(t, before, s05OutboxCount(t, ctx, old.Key))
	mutations.Keys = keys
	require.NoError(t, mutations.GrantGroupAndUpdate(ctx, key, group.ID))
	reloaded, err = users.GetByID(ctx, user.ID)
	require.NoError(t, err)
	require.Contains(t, reloaded.AllowedGroups, group.ID)
}

// s05OutboxCount 只观察当前测试凭据的事件，保留已有触发器入队机制。
func s05OutboxCount(t *testing.T, ctx context.Context, key string) int {
	t.Helper()
	var n int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM auth_cache_invalidation_outbox WHERE cache_key=$1", keycore.AuthCacheKey(key)).Scan(&n))
	return n
}
