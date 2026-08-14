//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestTeamInvitationConcurrentAcceptanceEnforcesMemberLimit(t *testing.T) {
	ctx := context.Background()
	repo := NewTeamRepository(integrationDB)
	owner := mustCreateUser(t, integrationEntClient, &service.User{Email: uniqueTeamTestEmail("owner"), Balance: 10})
	first := mustCreateUser(t, integrationEntClient, &service.User{Email: uniqueTeamTestEmail("first")})
	second := mustCreateUser(t, integrationEntClient, &service.User{Email: uniqueTeamTestEmail("second")})
	teamCtx, err := repo.Create(ctx, "并发邀请团队", owner.ID, 1)
	require.NoError(t, err)

	firstToken := uuid.NewString()
	secondToken := uuid.NewString()
	_, err = repo.CreateInvitation(ctx, teamCtx.Team.ID, owner.ID, first.Email, firstToken, time.Now().Add(time.Hour))
	require.NoError(t, err)
	_, err = repo.CreateInvitation(ctx, teamCtx.Team.ID, owner.ID, second.Email, secondToken, time.Now().Add(time.Hour))
	require.NoError(t, err)

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	accept := func(token string, user *service.User) {
		defer wg.Done()
		<-start
		_, acceptErr := repo.ResolveInvitation(ctx, token, user.ID, user.Email, "accepted", time.Now())
		results <- acceptErr
	}
	wg.Add(2)
	go accept(firstToken, first)
	go accept(secondToken, second)
	close(start)
	wg.Wait()
	close(results)

	var accepted, limited int
	for result := range results {
		switch {
		case result == nil:
			accepted++
		case errors.Is(result, service.ErrTeamMemberLimitReached):
			limited++
		default:
			require.NoError(t, result)
		}
	}
	require.Equal(t, 1, accepted)
	require.Equal(t, 1, limited)

	var memberCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_memberships WHERE team_id = $1 AND left_at IS NULL AND role = 'member'`, teamCtx.Team.ID).Scan(&memberCount))
	require.Equal(t, 1, memberCount)
}

func TestTeamInvitationMemberLimitZeroRejectsMembers(t *testing.T) {
	ctx := context.Background()
	repo := NewTeamRepository(integrationDB)
	owner := mustCreateUser(t, integrationEntClient, &service.User{Email: uniqueTeamTestEmail("zero-owner")})
	member := mustCreateUser(t, integrationEntClient, &service.User{Email: uniqueTeamTestEmail("zero-member")})
	teamCtx, err := repo.Create(ctx, "零成员容量团队", owner.ID, 0)
	require.NoError(t, err)
	token := uuid.NewString()
	_, err = repo.CreateInvitation(ctx, teamCtx.Team.ID, owner.ID, member.Email, token, time.Now().Add(time.Hour))
	require.NoError(t, err)

	_, err = repo.ResolveInvitation(ctx, token, member.ID, member.Email, "accepted", time.Now())
	require.ErrorIs(t, err, service.ErrTeamMemberLimitReached)
}

func TestTeamMembershipAndBillingAreIdempotent(t *testing.T) {
	ctx := context.Background()
	teamRepo := NewTeamRepository(integrationDB)
	owner := mustCreateUser(t, integrationEntClient, &service.User{Email: uniqueTeamTestEmail("billing-owner"), Balance: 10})
	member := mustCreateUser(t, integrationEntClient, &service.User{Email: uniqueTeamTestEmail("billing-member")})
	teamCtx, err := teamRepo.Create(ctx, "计费团队", owner.ID, 10)
	require.NoError(t, err)
	token := uuid.NewString()
	_, err = teamRepo.CreateInvitation(ctx, teamCtx.Team.ID, owner.ID, member.Email, token, time.Now().Add(time.Hour))
	require.NoError(t, err)
	_, err = teamRepo.ResolveInvitation(ctx, token, member.ID, member.Email, "accepted", time.Now())
	require.NoError(t, err)
	oldWindow := time.Now().AddDate(0, -2, 0)
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE team_memberships SET daily_usage_usd = 9, weekly_usage_usd = 9, monthly_usage_usd = 9,
			daily_window_start = $3, weekly_window_start = $3, monthly_window_start = $3
		WHERE team_id = $1 AND user_id = $2`, teamCtx.Team.ID, member.ID, oldWindow)
	require.NoError(t, err)

	otherOwner := mustCreateUser(t, integrationEntClient, &service.User{Email: uniqueTeamTestEmail("other-owner")})
	otherTeam, err := teamRepo.Create(ctx, "其他团队", otherOwner.ID, 10)
	require.NoError(t, err)
	otherToken := uuid.NewString()
	_, err = teamRepo.CreateInvitation(ctx, otherTeam.Team.ID, otherOwner.ID, member.Email, otherToken, time.Now().Add(time.Hour))
	require.NoError(t, err)
	_, err = teamRepo.ResolveInvitation(ctx, otherToken, member.ID, member.Email, "accepted", time.Now())
	require.ErrorIs(t, err, service.ErrTeamAlreadyJoined)

	teamID := teamCtx.Team.ID
	apiKey := mustCreateApiKey(t, integrationEntClient, &service.APIKey{UserID: member.ID, TeamID: &teamID, Key: "sk-team-" + uuid.NewString(), Name: "team"})
	account := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "team-account-" + uuid.NewString(), Type: service.AccountTypeAPIKey})
	billingRepo := NewUsageBillingRepository(integrationEntClient, integrationDB)
	command := &service.UsageBillingCommand{
		RequestID:         uuid.NewString(),
		APIKeyID:          apiKey.ID,
		UserID:            owner.ID,
		ActorUserID:       member.ID,
		TeamID:            &teamID,
		AccountID:         account.ID,
		AccountType:       service.AccountTypeAPIKey,
		BillableAmountUSD: 1.25,
	}
	firstResult, err := billingRepo.Apply(ctx, command)
	require.NoError(t, err)
	require.True(t, firstResult.Applied)
	secondResult, err := billingRepo.Apply(ctx, command)
	require.NoError(t, err)
	require.False(t, secondResult.Applied)

	var daily, weekly, monthly float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT daily_usage_usd, weekly_usage_usd, monthly_usage_usd
		FROM team_memberships WHERE team_id = $1 AND user_id = $2 AND left_at IS NULL`, teamID, member.ID).Scan(&daily, &weekly, &monthly))
	require.InDelta(t, 1.25, daily, 0.000001)
	require.InDelta(t, 1.25, weekly, 0.000001)
	require.InDelta(t, 1.25, monthly, 0.000001)

	// 请求开始后发生所有权转让，结算仍使用命令中保存的旧 Owner 付款快照。
	transferToken := uuid.NewString()
	_, err = teamRepo.CreateOwnershipTransfer(ctx, teamID, owner.ID, member.ID, transferToken, time.Now().Add(time.Hour))
	require.NoError(t, err)
	_, err = teamRepo.ResolveOwnershipTransfer(ctx, transferToken, member.ID, "accepted", time.Now())
	require.NoError(t, err)
	secondCommand := *command
	secondCommand.RequestID = uuid.NewString()
	secondCommand.BillableAmountUSD = 0.75
	secondCommand.RequestFingerprint = ""
	transferResult, err := billingRepo.Apply(ctx, &secondCommand)
	require.NoError(t, err)
	require.True(t, transferResult.Applied)

	var oldOwnerBalance, newOwnerBalance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance FROM users WHERE id = $1`, owner.ID).Scan(&oldOwnerBalance))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance FROM users WHERE id = $1`, member.ID).Scan(&newOwnerBalance))
	require.InDelta(t, 8, oldOwnerBalance, 0.000001)
	require.InDelta(t, 0, newOwnerBalance, 0.000001)

	var oldRole, newRole string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT role FROM team_memberships WHERE team_id = $1 AND user_id = $2`, teamID, owner.ID).Scan(&oldRole))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT role FROM team_memberships WHERE team_id = $1 AND user_id = $2`, teamID, member.ID).Scan(&newRole))
	require.Equal(t, service.TeamRoleMember, oldRole)
	require.Equal(t, service.TeamRoleOwner, newRole)
}

func TestActiveTeamOwnerCannotBeDeleted(t *testing.T) {
	ctx := context.Background()
	repo := NewTeamRepository(integrationDB)
	userRepo := NewUserRepository(integrationEntClient, integrationDB)
	owner := mustCreateUser(t, integrationEntClient, &service.User{Email: uniqueTeamTestEmail("protected-owner")})
	teamCtx, err := repo.Create(ctx, "删除保护团队", owner.ID, 10)
	require.NoError(t, err)

	err = userRepo.Delete(ctx, owner.ID)
	require.ErrorIs(t, err, service.ErrTeamOwnerTransferRequired)

	_, err = integrationDB.ExecContext(ctx, `UPDATE users SET deleted_at = NOW() WHERE id = $1`, owner.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "TEAM_OWNER_TRANSFER_REQUIRED")

	require.NoError(t, repo.Dissolve(ctx, teamCtx.Team.ID, time.Now()))
	_, err = integrationDB.ExecContext(ctx, `UPDATE users SET deleted_at = NOW() WHERE id = $1`, owner.ID)
	require.NoError(t, err)
}

func TestSoftDeletedTeamMemberIsRemovedAndKeysDisabled(t *testing.T) {
	ctx := context.Background()
	repo := NewTeamRepository(integrationDB)
	owner := mustCreateUser(t, integrationEntClient, &service.User{Email: uniqueTeamTestEmail("delete-owner")})
	member := mustCreateUser(t, integrationEntClient, &service.User{Email: uniqueTeamTestEmail("delete-member")})
	teamCtx, err := repo.Create(ctx, "成员删除清理团队", owner.ID, 5)
	require.NoError(t, err)
	token := uuid.NewString()
	_, err = repo.CreateInvitation(ctx, teamCtx.Team.ID, owner.ID, member.Email, token, time.Now().Add(time.Hour))
	require.NoError(t, err)
	_, err = repo.ResolveInvitation(ctx, token, member.ID, member.Email, "accepted", time.Now())
	require.NoError(t, err)
	teamID := teamCtx.Team.ID
	apiKey := mustCreateApiKey(t, integrationEntClient, &service.APIKey{
		UserID: member.ID,
		TeamID: &teamID,
		Key:    "sk-team-delete-" + uuid.NewString(),
		Name:   "delete-member-key",
	})

	_, err = integrationDB.ExecContext(ctx, `UPDATE users SET deleted_at = NOW() WHERE id = $1`, member.ID)
	require.NoError(t, err)

	var leftAt time.Time
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT left_at FROM team_memberships WHERE team_id = $1 AND user_id = $2`, teamID, member.ID).Scan(&leftAt))
	require.False(t, leftAt.IsZero())
	var status string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status FROM api_keys WHERE id = $1`, apiKey.ID).Scan(&status))
	require.Equal(t, service.StatusAPIKeyDisabled, status)
}

func TestTeamOwnerKeyLockRequiresExplicitOwnerEnable(t *testing.T) {
	ctx := context.Background()
	repo := NewTeamRepository(integrationDB)
	owner := mustCreateUser(t, integrationEntClient, &service.User{Email: uniqueTeamTestEmail("key-owner")})
	member := mustCreateUser(t, integrationEntClient, &service.User{Email: uniqueTeamTestEmail("key-member")})
	teamCtx, err := repo.Create(ctx, "团队密钥锁定测试", owner.ID, 5)
	require.NoError(t, err)
	token := uuid.NewString()
	_, err = repo.CreateInvitation(ctx, teamCtx.Team.ID, owner.ID, member.Email, token, time.Now().Add(time.Hour))
	require.NoError(t, err)
	_, err = repo.ResolveInvitation(ctx, token, member.ID, member.Email, "accepted", time.Now())
	require.NoError(t, err)
	teamID := teamCtx.Team.ID
	apiKey := mustCreateApiKey(t, integrationEntClient, &service.APIKey{
		UserID: member.ID,
		TeamID: &teamID,
		Key:    "sk-team-owner-lock-" + uuid.NewString(),
		Name:   "owner-lock",
	})

	_, err = repo.DisableTeamKey(ctx, teamID, apiKey.ID, nil)
	require.NoError(t, err)
	var status string
	var ownerDisabled bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status, team_owner_disabled FROM api_keys WHERE id = $1`, apiKey.ID).Scan(&status, &ownerDisabled))
	require.Equal(t, service.StatusAPIKeyDisabled, status)
	require.True(t, ownerDisabled)

	// 普通更新只能改状态，无法清除 Owner 的独立锁定标记。
	_, err = integrationDB.ExecContext(ctx, `UPDATE api_keys SET status = 'active' WHERE id = $1`, apiKey.ID)
	require.NoError(t, err)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status, team_owner_disabled FROM api_keys WHERE id = $1`, apiKey.ID).Scan(&status, &ownerDisabled))
	require.Equal(t, service.StatusAPIKeyActive, status)
	require.True(t, ownerDisabled)

	_, err = repo.EnableTeamKey(ctx, teamID, apiKey.ID, nil)
	require.NoError(t, err)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status, team_owner_disabled FROM api_keys WHERE id = $1`, apiKey.ID).Scan(&status, &ownerDisabled))
	require.Equal(t, service.StatusAPIKeyActive, status)
	require.False(t, ownerDisabled)
}

func TestTeamInvitationCopiesCurrentDefaultMemberLimits(t *testing.T) {
	ctx := context.Background()
	repo := NewTeamRepository(integrationDB)
	owner := mustCreateUser(t, integrationEntClient, &service.User{Email: uniqueTeamTestEmail("default-limit-owner")})
	member := mustCreateUser(t, integrationEntClient, &service.User{Email: uniqueTeamTestEmail("default-limit-member")})
	teamCtx, err := repo.Create(ctx, "默认限额团队", owner.ID, 10)
	require.NoError(t, err)
	require.NoError(t, repo.SetDefaultMemberLimits(ctx, teamCtx.Team.ID, 1.5, 8, 30))

	token := uuid.NewString()
	_, err = repo.CreateInvitation(ctx, teamCtx.Team.ID, owner.ID, member.Email, token, time.Now().Add(time.Hour))
	require.NoError(t, err)
	memberCtx, err := repo.ResolveInvitation(ctx, token, member.ID, member.Email, "accepted", time.Now())
	require.NoError(t, err)
	require.InDelta(t, 1.5, memberCtx.Membership.DailyLimitUSD, 0.000001)
	require.InDelta(t, 8, memberCtx.Membership.WeeklyLimitUSD, 0.000001)
	require.InDelta(t, 30, memberCtx.Membership.MonthlyLimitUSD, 0.000001)

	// 后续修改默认值只影响新成员，不追溯覆盖已经加入的成员。
	require.NoError(t, repo.SetDefaultMemberLimits(ctx, teamCtx.Team.ID, 2, 10, 40))
	memberCtx, err = repo.GetContextByUserID(ctx, member.ID)
	require.NoError(t, err)
	require.InDelta(t, 1.5, memberCtx.Membership.DailyLimitUSD, 0.000001)
}

func TestTeamMemberUsageSeriesKeepsDepartedMemberHistory(t *testing.T) {
	ctx := context.Background()
	teamRepo := NewTeamRepository(integrationDB)
	usageRepo := newUsageLogRepositoryWithSQL(integrationEntClient, integrationDB)
	owner := mustCreateUser(t, integrationEntClient, &service.User{Email: uniqueTeamTestEmail("usage-owner")})
	member := mustCreateUser(t, integrationEntClient, &service.User{Email: uniqueTeamTestEmail("usage-member")})
	teamCtx, err := teamRepo.Create(ctx, "历史成员用量团队", owner.ID, 5)
	require.NoError(t, err)
	token := uuid.NewString()
	_, err = teamRepo.CreateInvitation(ctx, teamCtx.Team.ID, owner.ID, member.Email, token, time.Now().Add(time.Hour))
	require.NoError(t, err)
	_, err = teamRepo.ResolveInvitation(ctx, token, member.ID, member.Email, "accepted", time.Now())
	require.NoError(t, err)
	teamID := teamCtx.Team.ID
	ownerKey := mustCreateApiKey(t, integrationEntClient, &service.APIKey{UserID: owner.ID, TeamID: &teamID, Key: "sk-team-usage-owner-" + uuid.NewString()})
	memberKey := mustCreateApiKey(t, integrationEntClient, &service.APIKey{UserID: member.ID, TeamID: &teamID, Key: "sk-team-usage-member-" + uuid.NewString()})
	account := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "team-usage-" + uuid.NewString(), Type: service.AccountTypeAPIKey})
	createdAt := time.Now().UTC()
	for _, item := range []struct {
		userID   int64
		apiKeyID int64
		cost     float64
	}{
		{userID: owner.ID, apiKeyID: ownerKey.ID, cost: 1.2},
		{userID: member.ID, apiKeyID: memberKey.ID, cost: 0.8},
	} {
		_, err = usageRepo.Create(ctx, &service.UsageLog{
			UserID:        item.userID,
			BillingUserID: owner.ID,
			TeamID:        &teamID,
			APIKeyID:      item.apiKeyID,
			AccountID:     account.ID,
			RequestID:     uuid.NewString(),
			Model:         "team-usage-test",
			InputTokens:   10,
			OutputTokens:  5,
			TotalCost:     item.cost,
			ActualCost:    item.cost,
			CreatedAt:     createdAt,
		})
		require.NoError(t, err)
	}
	require.NoError(t, teamRepo.RemoveMember(ctx, teamID, member.ID, time.Now()))

	query := service.TeamUsageQuery{From: createdAt.Add(-time.Hour), To: createdAt.Add(time.Hour)}
	total, err := teamRepo.GetUsageSummary(ctx, teamID, query)
	require.NoError(t, err)
	series, err := teamRepo.ListMemberUsageSeries(ctx, teamID, query)
	require.NoError(t, err)

	var seriesTotal float64
	statuses := make(map[int64]string, len(series))
	for _, item := range series {
		seriesTotal += item.Summary.ActualCost
		statuses[item.ActorUserID] = item.Status
	}
	require.InDelta(t, total.ActualCost, seriesTotal, 0.000001)
	require.InDelta(t, 2, seriesTotal, 0.000001)
	require.Equal(t, "active", statuses[owner.ID])
	require.Equal(t, "left", statuses[member.ID])
}

func uniqueTeamTestEmail(prefix string) string {
	return fmt.Sprintf("team-%s-%s@example.com", prefix, uuid.NewString())
}
