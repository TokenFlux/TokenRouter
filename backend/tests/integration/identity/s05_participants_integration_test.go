//go:build integration

package identity_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/redeemcodeusage"

	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"

	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"

	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"

	teampostgres "github.com/TokenFlux/TokenRouter/internal/team/postgres"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// 在 Key 已写入后注入错误，验证成员和 Key 实际使用同一事务连接。
type s05FailingMemberKeys struct {
	*keypostgres.TeamKeys
	err error
}

func (p s05FailingMemberKeys) DisableMemberInTx(ctx context.Context, tx *sql.Tx, teamID, userID int64, now time.Time) error {
	if err := p.TeamKeys.DisableMemberInTx(ctx, tx, teamID, userID, now); err != nil {
		return err
	}
	return p.err
}

func TestS05MemberRemovalRollsBackKeyParticipant(t *testing.T) {
	integrationDB, integrationEntClient := identityDatabase(t)
	ctx := context.Background()
	owner := mustCreateUser(t, integrationEntClient, &identity.User{Email: "s05-owner-" + uuid.NewString() + "@example.com"})
	member := mustCreateUser(t, integrationEntClient, &identity.User{Email: "s05-member-" + uuid.NewString() + "@example.com"})
	repo := teampostgres.NewTeamRepository(integrationDB, keypostgres.NewTeamKeys(integrationDB), billingpostgres.NewMemberUsageStore(integrationDB, nil))
	current, err := repo.Create(ctx, "事务参与验证", owner.ID, 10)
	require.NoError(t, err)
	token := uuid.NewString()
	_, err = repo.CreateInvitation(ctx, current.Team.ID, owner.ID, member.Email, token, time.Now().Add(time.Hour))
	require.NoError(t, err)
	_, err = repo.ResolveInvitation(ctx, token, member.ID, member.Email, "accepted", time.Now())
	require.NoError(t, err)
	key, err := integrationEntClient.APIKey.Create().SetUserID(member.ID).SetTeamID(current.Team.ID).
		SetKey("s05-" + uuid.NewString()).SetName("参与者验证").SetStatus("active").Save(ctx)
	require.NoError(t, err)
	failure := errors.New("s05 key participant failed after write")
	broken := teampostgres.NewTeamRepository(integrationDB,
		s05FailingMemberKeys{TeamKeys: keypostgres.NewTeamKeys(integrationDB), err: failure},
		billingpostgres.NewMemberUsageStore(integrationDB, nil))
	require.ErrorIs(t, broken.RemoveMember(ctx, current.Team.ID, member.ID, time.Now()), failure)
	var leftAt sql.NullTime
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT left_at FROM team_memberships WHERE team_id = $1 AND user_id = $2", current.Team.ID, member.ID).Scan(&leftAt))
	require.False(t, leftAt.Valid, "Key 参与者失败必须回滚成员离开状态")
	stored, err := integrationEntClient.APIKey.Get(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, "active", stored.Status, "Key 禁用不得在另一个连接独立提交")
	require.NoError(t, repo.RemoveMember(ctx, current.Team.ID, member.ID, time.Now()))
	stored, err = integrationEntClient.APIKey.Get(ctx, key.ID)
	require.NoError(t, err)
	require.Equal(t, "disabled", stored.Status)
}

func TestS05RedeemConcurrencyUsesOuterEntTransaction(t *testing.T) {
	ctx := context.Background()
	_, client := identityDatabase(t)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	user, err := tx.Client().User.Create().SetEmail("s05-concurrency-" + uuid.NewString() + "@example.com").
		SetPasswordHash("hash").SetConcurrency(3).SetBalance(10).Save(ctx)
	require.NoError(t, err)
	participant := billingpostgres.RedeemInTx(tx, identitypostgres.ConcurrencyInTx(tx))
	require.NoError(t, participant.ApplyConcurrency(ctx, user.ID, 7))
	inside, err := tx.Client().User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 10, inside.Concurrency)
	require.Equal(t, 10.0, inside.Balance)
	require.NoError(t, participant.ApplyConcurrency(ctx, user.ID, -12))
	inside, err = tx.Client().User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Zero(t, inside.Concurrency, "负数兑换保持下限为零")
	_, err = client.User.Get(ctx, user.ID)
	require.True(t, dbent.IsNotFound(err), "参与者不得提交尚未提交的用户")
	require.NoError(t, tx.Rollback())
	_, err = client.User.Get(ctx, user.ID)
	require.True(t, dbent.IsNotFound(err))
}

func TestS05InitialFundsDoNotCountAsRecharge(t *testing.T) {
	ctx := context.Background()
	integrationDB, client := identityDatabase(t)
	user := &identity.User{Email: "s05-initial-" + uuid.NewString() + "@example.com", PasswordHash: "hash",
		Role: identity.RoleUser, Status: billing.StatusActive, Balance: 12.5, Concurrency: 2}
	require.NoError(t, identitypostgres.NewUserStore(client, integrationDB).Create(ctx, user))
	stored, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 12.5, stored.Balance)
	require.Zero(t, stored.TotalRecharged, "注册初始资金不能套用累计充值语义")
}

// TestS05RegistrationInvitationUsesOuterTransaction 验证注册邀请码沿用同一事务，不混入普通多次兑换的 usage 语义。
func TestS05RegistrationInvitationUsesOuterTransaction(t *testing.T) {
	ctx := context.Background()
	_, client := identityDatabase(t)
	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	user, err := tx.Client().User.Create().SetEmail("s05-invite-" + uuid.NewString() + "@example.invalid").SetPasswordHash("hash").Save(ctx)
	require.NoError(t, err)
	invitation, err := tx.Client().RedeemCode.Create().SetCode("s05-" + uuid.NewString()[:24]).SetType("invitation").SetStatus("unused").SetValue(0).SetMaxUses(1).Save(ctx)
	require.NoError(t, err)
	participant := billingpostgres.RegistrationInvitationsInTx(tx)
	require.NoError(t, participant.Consume(ctx, invitation.ID, user.ID))
	usages, err := tx.Client().RedeemCodeUsage.Query().Where(redeemcodeusage.RedeemCodeIDEQ(invitation.ID)).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, usages, "注册邀请码继续只更新原使用状态，不新增普通兑换 usage")
	stored, err := participant.Load(ctx, invitation.Code)
	require.NoError(t, err)
	require.Equal(t, "used", stored.Status)
	require.Equal(t, 1, stored.UsedCount)
	require.Equal(t, user.ID, *stored.UsedBy)
	require.ErrorIs(t, participant.Consume(ctx, invitation.ID, user.ID), billing.ErrRedeemCodeUsed)
	_, err = client.RedeemCode.Get(ctx, invitation.ID)
	require.True(t, dbent.IsNotFound(err), "参与方法不得提前提交调用方的创建")
	stored.Status = "unused"
	stored.UsedCount = 0
	stored.UsedBy = nil
	stored.UsedAt = nil
	require.NoError(t, participant.RestoreSnapshot(ctx, stored))
	restored, err := tx.Client().RedeemCode.Get(ctx, invitation.ID)
	require.NoError(t, err)
	require.Equal(t, "unused", restored.Status)
	require.Nil(t, restored.UsedBy)
	require.NoError(t, tx.Rollback())
	_, err = client.RedeemCode.Get(ctx, invitation.ID)
	require.True(t, dbent.IsNotFound(err))
	_, err = client.User.Get(ctx, user.ID)
	require.True(t, dbent.IsNotFound(err))
}
