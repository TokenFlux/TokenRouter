package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/timezone"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/lib/pq"
)

type teamRepository struct {
	db *sql.DB
}

// NewTeamRepository 创建使用事务保证成员和所有权约束的团队仓储。
func NewTeamRepository(db *sql.DB) service.TeamRepository {
	return &teamRepository{db: db}
}

func (r *teamRepository) Create(ctx context.Context, name string, ownerUserID int64, memberLimit int) (*service.TeamContext, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, ownerUserID); err != nil {
		return nil, err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM team_memberships WHERE user_id = $1 AND left_at IS NULL)`, ownerUserID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, service.ErrTeamAlreadyJoined
	}
	var teamID int64
	if err = tx.QueryRowContext(ctx, `
		INSERT INTO teams (name, status, member_limit, created_at, updated_at)
		VALUES ($1, 'active', $2, NOW(), NOW()) RETURNING id`, name, memberLimit).Scan(&teamID); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO team_memberships (team_id, user_id, role, joined_at, created_at, updated_at)
		VALUES ($1, $2, 'owner', NOW(), NOW(), NOW())`, teamID, ownerUserID); err != nil {
		return nil, mapTeamConstraintError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, mapTeamConstraintError(err)
	}
	return r.GetContextByTeamID(ctx, teamID)
}

func (r *teamRepository) GetContextByUserID(ctx context.Context, userID int64) (*service.TeamContext, error) {
	return r.scanContext(ctx, `WHERE current_membership.user_id = $1 AND current_membership.left_at IS NULL`, userID)
}

func (r *teamRepository) GetContextByTeamID(ctx context.Context, teamID int64) (*service.TeamContext, error) {
	return r.scanContext(ctx, `WHERE t.id = $1 AND current_membership.role = 'owner'`, teamID)
}

func (r *teamRepository) scanContext(ctx context.Context, where string, arg int64) (*service.TeamContext, error) {
	query := `
		SELECT t.id, t.name, t.status, t.member_limit,
		       t.default_daily_limit_usd, t.default_weekly_limit_usd, t.default_monthly_limit_usd,
		       t.created_at, t.updated_at,
		       (SELECT COUNT(*) FROM team_memberships cm WHERE cm.team_id = t.id AND cm.left_at IS NULL AND cm.role = 'member') AS member_count,
		       current_membership.id, current_membership.team_id, current_membership.user_id,
		       actor_user.email, actor_user.username, current_membership.role,
		       current_membership.daily_limit_usd, current_membership.weekly_limit_usd, current_membership.monthly_limit_usd,
		       current_membership.daily_usage_usd, current_membership.weekly_usage_usd, current_membership.monthly_usage_usd,
		       current_membership.daily_window_start, current_membership.weekly_window_start, current_membership.monthly_window_start,
		       current_membership.joined_at, actor_user.last_active_at,
		       owner_membership.id, owner_membership.team_id, owner_membership.user_id,
		       owner_user.email, owner_user.username, owner_membership.role,
		       owner_membership.daily_limit_usd, owner_membership.weekly_limit_usd, owner_membership.monthly_limit_usd,
		       owner_membership.daily_usage_usd, owner_membership.weekly_usage_usd, owner_membership.monthly_usage_usd,
		       owner_membership.daily_window_start, owner_membership.weekly_window_start, owner_membership.monthly_window_start,
		       owner_membership.joined_at, owner_user.last_active_at
		FROM teams t
		JOIN team_memberships current_membership ON current_membership.team_id = t.id AND current_membership.left_at IS NULL
		JOIN users actor_user ON actor_user.id = current_membership.user_id AND actor_user.deleted_at IS NULL
		JOIN team_memberships owner_membership ON owner_membership.team_id = t.id AND owner_membership.left_at IS NULL AND owner_membership.role = 'owner'
		JOIN users owner_user ON owner_user.id = owner_membership.user_id AND owner_user.deleted_at IS NULL
		` + where + ` AND t.deleted_at IS NULL
		LIMIT 1`
	row := r.db.QueryRowContext(ctx, query, arg)
	teamCtx := &service.TeamContext{Team: &service.Team{}, Membership: &service.TeamMembership{}, Owner: &service.TeamMembership{}}
	err := row.Scan(
		&teamCtx.Team.ID, &teamCtx.Team.Name, &teamCtx.Team.Status, &teamCtx.Team.MemberLimit,
		&teamCtx.Team.DefaultDailyLimitUSD, &teamCtx.Team.DefaultWeeklyLimitUSD, &teamCtx.Team.DefaultMonthlyLimitUSD,
		&teamCtx.Team.CreatedAt, &teamCtx.Team.UpdatedAt, &teamCtx.Team.MemberCount,
		&teamCtx.Membership.ID, &teamCtx.Membership.TeamID, &teamCtx.Membership.UserID, &teamCtx.Membership.Email, &teamCtx.Membership.Username, &teamCtx.Membership.Role,
		&teamCtx.Membership.DailyLimitUSD, &teamCtx.Membership.WeeklyLimitUSD, &teamCtx.Membership.MonthlyLimitUSD,
		&teamCtx.Membership.DailyUsageUSD, &teamCtx.Membership.WeeklyUsageUSD, &teamCtx.Membership.MonthlyUsageUSD,
		&teamCtx.Membership.DailyWindowStart, &teamCtx.Membership.WeeklyWindowStart, &teamCtx.Membership.MonthlyWindowStart,
		&teamCtx.Membership.JoinedAt, &teamCtx.Membership.LastActiveAt,
		&teamCtx.Owner.ID, &teamCtx.Owner.TeamID, &teamCtx.Owner.UserID, &teamCtx.Owner.Email, &teamCtx.Owner.Username, &teamCtx.Owner.Role,
		&teamCtx.Owner.DailyLimitUSD, &teamCtx.Owner.WeeklyLimitUSD, &teamCtx.Owner.MonthlyLimitUSD,
		&teamCtx.Owner.DailyUsageUSD, &teamCtx.Owner.WeeklyUsageUSD, &teamCtx.Owner.MonthlyUsageUSD,
		&teamCtx.Owner.DailyWindowStart, &teamCtx.Owner.WeeklyWindowStart, &teamCtx.Owner.MonthlyWindowStart,
		&teamCtx.Owner.JoinedAt, &teamCtx.Owner.LastActiveAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrTeamNotFound
	}
	if err == nil {
		normalizeTeamMembershipWindows(teamCtx.Membership, time.Now())
		normalizeTeamMembershipWindows(teamCtx.Owner, time.Now())
	}
	return teamCtx, err
}

func (r *teamRepository) UpdateName(ctx context.Context, teamID int64, name string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE teams SET name = $2, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, teamID, name)
	return requireTeamAffected(result, err)
}

func (r *teamRepository) SetDefaultMemberLimits(ctx context.Context, teamID int64, daily, weekly, monthly float64) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE teams SET default_daily_limit_usd = $2, default_weekly_limit_usd = $3,
			default_monthly_limit_usd = $4, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL`, teamID, daily, weekly, monthly)
	return requireTeamAffected(result, err)
}

func (r *teamRepository) ListMembers(ctx context.Context, teamID int64) ([]service.TeamMembership, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT m.id, m.team_id, m.user_id, u.email, u.username, m.role,
		       m.daily_limit_usd, m.weekly_limit_usd, m.monthly_limit_usd,
		       m.daily_usage_usd, m.weekly_usage_usd, m.monthly_usage_usd,
		       m.daily_window_start, m.weekly_window_start, m.monthly_window_start,
		       m.joined_at, u.last_active_at
		FROM team_memberships m
		JOIN users u ON u.id = m.user_id AND u.deleted_at IS NULL
		WHERE m.team_id = $1 AND m.left_at IS NULL
		ORDER BY CASE WHEN m.role = 'owner' THEN 0 ELSE 1 END, m.joined_at ASC`, teamID)
	if err != nil {
		return nil, err
	}
	// 查询结束时关闭结果集，读取阶段的错误统一通过 rows.Err 返回。
	defer func() { _ = rows.Close() }()
	members := make([]service.TeamMembership, 0)
	for rows.Next() {
		var member service.TeamMembership
		if err := scanTeamMembership(rows, &member); err != nil {
			return nil, err
		}
		normalizeTeamMembershipWindows(&member, time.Now())
		members = append(members, member)
	}
	return members, rows.Err()
}

func (r *teamRepository) CreateInvitation(ctx context.Context, teamID, inviterUserID int64, email, tokenHash string, expiresAt time.Time) (*service.TeamInvitation, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `UPDATE team_invitations SET status = 'revoked', updated_at = NOW() WHERE team_id = $1 AND email = $2 AND status = 'pending'`, teamID, email); err != nil {
		return nil, err
	}
	invitation := &service.TeamInvitation{}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO team_invitations (team_id, inviter_user_id, email, token_hash, status, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'pending', $5, NOW(), NOW())
		RETURNING id, team_id, inviter_user_id, email, status, expires_at, accepted_at, created_at`,
		teamID, inviterUserID, email, tokenHash, expiresAt,
	).Scan(&invitation.ID, &invitation.TeamID, &invitation.InviterUserID, &invitation.Email, &invitation.Status, &invitation.ExpiresAt, &invitation.AcceptedAt, &invitation.CreatedAt)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return invitation, nil
}

func (r *teamRepository) ListInvitations(ctx context.Context, teamID int64) ([]service.TeamInvitation, error) {
	_, _ = r.db.ExecContext(ctx, `UPDATE team_invitations SET status = 'expired', updated_at = NOW() WHERE team_id = $1 AND status = 'pending' AND expires_at <= NOW()`, teamID)
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, team_id, inviter_user_id, email, status, expires_at, accepted_at, created_at
		FROM team_invitations WHERE team_id = $1 ORDER BY created_at DESC`, teamID)
	if err != nil {
		return nil, err
	}
	// 查询结束时关闭结果集，读取阶段的错误统一通过 rows.Err 返回。
	defer func() { _ = rows.Close() }()
	items := make([]service.TeamInvitation, 0)
	for rows.Next() {
		var item service.TeamInvitation
		if err := rows.Scan(&item.ID, &item.TeamID, &item.InviterUserID, &item.Email, &item.Status, &item.ExpiresAt, &item.AcceptedAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *teamRepository) GetInvitationByID(ctx context.Context, teamID, invitationID int64) (*service.TeamInvitation, error) {
	item := &service.TeamInvitation{}
	err := r.db.QueryRowContext(ctx, `
		SELECT id, team_id, inviter_user_id, email, status, expires_at, accepted_at, created_at
		FROM team_invitations WHERE team_id = $1 AND id = $2`, teamID, invitationID).
		Scan(&item.ID, &item.TeamID, &item.InviterUserID, &item.Email, &item.Status, &item.ExpiresAt, &item.AcceptedAt, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrTeamInvitationInvalid
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (r *teamRepository) ReissueInvitation(ctx context.Context, teamID, invitationID int64, tokenHash string, expiresAt time.Time) (*service.TeamInvitation, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var email, status string
	err = tx.QueryRowContext(ctx, `SELECT email, status FROM team_invitations WHERE id = $2 AND team_id = $1 FOR UPDATE`, teamID, invitationID).Scan(&email, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrTeamInvitationInvalid
	}
	if err != nil {
		return nil, err
	}
	if status != "pending" && status != "expired" {
		return nil, service.ErrTeamInvitationInvalid
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE team_invitations SET status = 'revoked', updated_at = NOW()
		WHERE team_id = $1 AND email = $2 AND id <> $3 AND status = 'pending'`, teamID, email, invitationID); err != nil {
		return nil, err
	}
	item := &service.TeamInvitation{}
	err = tx.QueryRowContext(ctx, `
		UPDATE team_invitations SET token_hash = $3, status = 'pending', expires_at = $4, accepted_by_user_id = NULL, accepted_at = NULL, updated_at = NOW()
		WHERE id = $2 AND team_id = $1 AND status IN ('pending', 'expired')
		RETURNING id, team_id, inviter_user_id, email, status, expires_at, accepted_at, created_at`,
		teamID, invitationID, tokenHash, expiresAt,
	).Scan(&item.ID, &item.TeamID, &item.InviterUserID, &item.Email, &item.Status, &item.ExpiresAt, &item.AcceptedAt, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrTeamInvitationInvalid
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return item, nil
}

func (r *teamRepository) RevokeInvitation(ctx context.Context, teamID, invitationID int64) error {
	result, err := r.db.ExecContext(ctx, `UPDATE team_invitations SET status = 'revoked', updated_at = NOW() WHERE id = $2 AND team_id = $1 AND status = 'pending'`, teamID, invitationID)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return service.ErrTeamInvitationInvalid
	}
	return nil
}

// PreviewInvitation 仅在令牌、状态和受邀邮箱均匹配时返回邀请摘要。
func (r *teamRepository) PreviewInvitation(ctx context.Context, tokenHash, normalizedEmail string, now time.Time) (*service.TeamInvitationPreview, error) {
	var invitationID int64
	var invitationEmail, status string
	preview := &service.TeamInvitationPreview{}
	err := r.db.QueryRowContext(ctx, `
		SELECT ti.id, ti.email, ti.status, ti.expires_at, t.name,
		       COALESCE(NULLIF(BTRIM(inviter.username), ''), inviter.email), inviter.email
		FROM team_invitations ti
		JOIN teams t ON t.id = ti.team_id AND t.deleted_at IS NULL
		JOIN users inviter ON inviter.id = ti.inviter_user_id AND inviter.deleted_at IS NULL
		WHERE ti.token_hash = $1`, tokenHash).
		Scan(
			&invitationID,
			&invitationEmail,
			&status,
			&preview.ExpiresAt,
			&preview.TeamName,
			&preview.InviterName,
			&preview.InviterEmail,
		)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrTeamInvitationInvalid
	}
	if err != nil {
		return nil, err
	}
	if status != "pending" {
		return nil, service.ErrTeamInvitationInvalid
	}
	if !preview.ExpiresAt.After(now) {
		_, _ = r.db.ExecContext(ctx, `UPDATE team_invitations SET status = 'expired', updated_at = $2 WHERE id = $1 AND status = 'pending'`, invitationID, now)
		return nil, service.ErrTeamInvitationExpired
	}
	if !strings.EqualFold(invitationEmail, normalizedEmail) {
		return nil, service.ErrTeamInvitationEmail
	}
	return preview, nil
}

func (r *teamRepository) ResolveInvitation(ctx context.Context, tokenHash string, userID int64, normalizedEmail, resolution string, now time.Time) (*service.TeamContext, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// 同一用户接受不同邀请时先串行化，再获取邀请和团队行锁，避免形成反向等待。
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, userID); err != nil {
		return nil, err
	}
	var invitationID, teamID int64
	var email, status string
	var expiresAt time.Time
	err = tx.QueryRowContext(ctx, `SELECT id, team_id, email, status, expires_at FROM team_invitations WHERE token_hash = $1 FOR UPDATE`, tokenHash).
		Scan(&invitationID, &teamID, &email, &status, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrTeamInvitationInvalid
	}
	if err != nil {
		return nil, err
	}
	if status != "pending" {
		return nil, service.ErrTeamInvitationInvalid
	}
	if !expiresAt.After(now) {
		_, _ = tx.ExecContext(ctx, `UPDATE team_invitations SET status = 'expired', updated_at = $2 WHERE id = $1`, invitationID, now)
		_ = tx.Commit()
		return nil, service.ErrTeamInvitationExpired
	}
	if !strings.EqualFold(email, normalizedEmail) {
		return nil, service.ErrTeamInvitationEmail
	}
	if resolution == "declined" {
		if _, err = tx.ExecContext(ctx, `UPDATE team_invitations SET status = 'declined', updated_at = $2 WHERE id = $1`, invitationID, now); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	var teamStatus string
	var defaultDaily, defaultWeekly, defaultMonthly float64
	var memberLimit, memberCount int
	err = tx.QueryRowContext(ctx, `
		SELECT t.status, t.member_limit, t.default_daily_limit_usd,
		       t.default_weekly_limit_usd, t.default_monthly_limit_usd
		FROM teams t WHERE t.id = $1 AND t.deleted_at IS NULL FOR UPDATE`, teamID).
		Scan(&teamStatus, &memberLimit, &defaultDaily, &defaultWeekly, &defaultMonthly)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrTeamNotFound
	}
	if err != nil {
		return nil, err
	}
	if teamStatus != service.TeamStatusActive {
		return nil, service.ErrTeamSuspended
	}
	// 团队行锁获取后另起一条语句统计，确保能看到前一个接受事务刚提交的成员。
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_memberships WHERE team_id = $1 AND left_at IS NULL AND role = 'member'`, teamID).Scan(&memberCount); err != nil {
		return nil, err
	}
	if memberCount >= memberLimit {
		return nil, service.ErrTeamMemberLimitReached
	}
	var alreadyJoined bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM team_memberships WHERE user_id = $1 AND left_at IS NULL)`, userID).Scan(&alreadyJoined); err != nil {
		return nil, err
	}
	if alreadyJoined {
		return nil, service.ErrTeamAlreadyJoined
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO team_memberships (
			team_id, user_id, role, daily_limit_usd, weekly_limit_usd, monthly_limit_usd,
			joined_at, created_at, updated_at
		)
		VALUES ($1, $2, 'member', $3, $4, $5, $6, $6, $6)`,
		teamID, userID, defaultDaily, defaultWeekly, defaultMonthly, now); err != nil {
		return nil, mapTeamConstraintError(err)
	}
	// 再次加入时旧 Membership 生命周期内的 Key 不得自动恢复可用。
	if _, err = tx.ExecContext(ctx, `
		UPDATE api_keys
		SET status = 'disabled', updated_at = $3
		WHERE team_id = $1 AND user_id = $2 AND created_at < $3 AND deleted_at IS NULL`, teamID, userID, now); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE team_invitations SET status = 'accepted', accepted_by_user_id = $2, accepted_at = $3, updated_at = $3 WHERE id = $1`, invitationID, userID, now); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE team_invitations SET status = 'revoked', updated_at = $2
		WHERE email = $1 AND status = 'pending' AND id <> $3`, email, now, invitationID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, mapTeamConstraintError(err)
	}
	return r.GetContextByUserID(ctx, userID)
}

func (r *teamRepository) RemoveMember(ctx context.Context, teamID, userID int64, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `
		UPDATE team_memberships SET left_at = $3, updated_at = $3
		WHERE team_id = $1 AND user_id = $2 AND left_at IS NULL AND role = 'member'`, teamID, userID, now)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return service.ErrTeamMembershipRequired
	}
	if _, err = tx.ExecContext(ctx, `UPDATE api_keys SET status = 'disabled', updated_at = $3 WHERE team_id = $1 AND user_id = $2 AND deleted_at IS NULL`, teamID, userID, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *teamRepository) UpdateMemberLimits(ctx context.Context, teamID, userID int64, daily, weekly, monthly float64) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE team_memberships SET daily_limit_usd = $3, weekly_limit_usd = $4, monthly_limit_usd = $5, updated_at = NOW()
		WHERE team_id = $1 AND user_id = $2 AND left_at IS NULL AND role = 'member'`, teamID, userID, daily, weekly, monthly)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return service.ErrTeamMembershipRequired
	}
	return nil
}

func (r *teamRepository) ResetMemberUsage(ctx context.Context, teamID, userID int64, resetDaily, resetWeekly, resetMonthly bool, now time.Time) error {
	dailyStart := timezone.StartOfDay(now)
	weeklyStart := timezone.StartOfWeek(now)
	monthlyStart := timezone.StartOfMonth(now)
	result, err := r.db.ExecContext(ctx, `
		UPDATE team_memberships SET
			daily_usage_usd = CASE WHEN $3 THEN 0 ELSE daily_usage_usd END,
			weekly_usage_usd = CASE WHEN $4 THEN 0 ELSE weekly_usage_usd END,
			monthly_usage_usd = CASE WHEN $5 THEN 0 ELSE monthly_usage_usd END,
			daily_window_start = CASE WHEN $3 THEN $6 ELSE daily_window_start END,
			weekly_window_start = CASE WHEN $4 THEN $7 ELSE weekly_window_start END,
			monthly_window_start = CASE WHEN $5 THEN $8 ELSE monthly_window_start END,
			updated_at = NOW()
		WHERE team_id = $1 AND user_id = $2 AND left_at IS NULL AND role = 'member'`,
		teamID, userID, resetDaily, resetWeekly, resetMonthly, dailyStart, weeklyStart, monthlyStart)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return service.ErrTeamMembershipRequired
	}
	return nil
}

func (r *teamRepository) CreateOwnershipTransfer(ctx context.Context, teamID, fromUserID, toUserID int64, tokenHash string, expiresAt time.Time) (*service.TeamOwnershipTransfer, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var targetMember bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM team_memberships WHERE team_id = $1 AND user_id = $2 AND left_at IS NULL AND role = 'member')`, teamID, toUserID).Scan(&targetMember); err != nil {
		return nil, err
	}
	if !targetMember {
		return nil, service.ErrTeamTransferInvalid
	}
	_, _ = tx.ExecContext(ctx, `UPDATE team_ownership_transfers SET status = 'cancelled', resolved_at = NOW(), updated_at = NOW() WHERE team_id = $1 AND status = 'pending'`, teamID)
	item := &service.TeamOwnershipTransfer{}
	err = tx.QueryRowContext(ctx, `
		INSERT INTO team_ownership_transfers (team_id, from_user_id, to_user_id, token_hash, status, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'pending', $5, NOW(), NOW())
		RETURNING id, team_id, from_user_id, to_user_id, status, expires_at, resolved_at, created_at`,
		teamID, fromUserID, toUserID, tokenHash, expiresAt,
	).Scan(&item.ID, &item.TeamID, &item.FromUserID, &item.ToUserID, &item.Status, &item.ExpiresAt, &item.ResolvedAt, &item.CreatedAt)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return item, nil
}

func (r *teamRepository) ResolveOwnershipTransfer(ctx context.Context, tokenHash string, actorUserID int64, resolution string, now time.Time) (*service.TeamContext, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var id, teamID, fromUserID, toUserID int64
	var status string
	var expiresAt time.Time
	err = tx.QueryRowContext(ctx, `SELECT id, team_id, from_user_id, to_user_id, status, expires_at FROM team_ownership_transfers WHERE token_hash = $1 FOR UPDATE`, tokenHash).
		Scan(&id, &teamID, &fromUserID, &toUserID, &status, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrTeamTransferInvalid
	}
	if err != nil {
		return nil, err
	}
	if status != "pending" || actorUserID != toUserID {
		return nil, service.ErrTeamTransferInvalid
	}
	if !expiresAt.After(now) {
		_, _ = tx.ExecContext(ctx, `UPDATE team_ownership_transfers SET status = 'expired', resolved_at = $2, updated_at = $2 WHERE id = $1`, id, now)
		_ = tx.Commit()
		return nil, service.ErrTeamTransferExpired
	}
	if resolution == "declined" {
		if _, err = tx.ExecContext(ctx, `UPDATE team_ownership_transfers SET status = 'declined', resolved_at = $2, updated_at = $2 WHERE id = $1`, id, now); err != nil {
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return r.GetContextByUserID(ctx, actorUserID)
	}
	if err = transferTeamOwnership(ctx, tx, teamID, fromUserID, toUserID, now); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE team_ownership_transfers SET status = 'accepted', resolved_at = $2, updated_at = $2 WHERE id = $1`, id, now); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetContextByUserID(ctx, actorUserID)
}

func (r *teamRepository) CancelOwnershipTransfer(ctx context.Context, teamID, actorUserID int64, now time.Time) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE team_ownership_transfers SET status = 'cancelled', resolved_at = $3, updated_at = $3
		WHERE team_id = $1 AND from_user_id = $2 AND status = 'pending'`, teamID, actorUserID, now)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return service.ErrTeamTransferInvalid
	}
	return nil
}

func (r *teamRepository) Dissolve(ctx context.Context, teamID int64, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if result, execErr := tx.ExecContext(ctx, `UPDATE teams SET status = 'suspended', deleted_at = $2, updated_at = $2 WHERE id = $1 AND deleted_at IS NULL`, teamID, now); execErr != nil {
		return execErr
	} else if affected, _ := result.RowsAffected(); affected == 0 {
		return service.ErrTeamNotFound
	}
	if _, err = tx.ExecContext(ctx, `UPDATE team_memberships SET left_at = $2, updated_at = $2 WHERE team_id = $1 AND left_at IS NULL`, teamID, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE team_invitations SET status = 'revoked', updated_at = $2 WHERE team_id = $1 AND status = 'pending'`, teamID, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE team_ownership_transfers SET status = 'cancelled', resolved_at = $2, updated_at = $2 WHERE team_id = $1 AND status = 'pending'`, teamID, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE api_keys SET status = 'disabled', updated_at = $2 WHERE team_id = $1 AND deleted_at IS NULL`, teamID, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *teamRepository) SetStatus(ctx context.Context, teamID int64, status string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE teams SET status = $2, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, teamID, status)
	return requireTeamAffected(result, err)
}

func (r *teamRepository) SetMemberLimit(ctx context.Context, teamID int64, limit int) error {
	result, err := r.db.ExecContext(ctx, `UPDATE teams SET member_limit = $2, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, teamID, limit)
	return requireTeamAffected(result, err)
}

func (r *teamRepository) UpdateAdmin(ctx context.Context, teamID int64, update service.TeamAdminUpdate) error {
	setClauses := []string{"updated_at = NOW()"}
	args := []any{teamID}
	if update.Name != nil {
		args = append(args, *update.Name)
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", len(args)))
	}
	if update.Status != nil {
		args = append(args, *update.Status)
		setClauses = append(setClauses, fmt.Sprintf("status = $%d", len(args)))
	}
	if update.MemberLimit != nil {
		args = append(args, *update.MemberLimit)
		setClauses = append(setClauses, fmt.Sprintf("member_limit = $%d", len(args)))
	}
	result, err := r.db.ExecContext(ctx, `UPDATE teams SET `+strings.Join(setClauses, ", ")+` WHERE id = $1 AND deleted_at IS NULL`, args...)
	return requireTeamAffected(result, err)
}

func (r *teamRepository) ForceTransfer(ctx context.Context, teamID, toUserID int64, now time.Time) (*service.TeamContext, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var fromUserID int64
	if err = tx.QueryRowContext(ctx, `SELECT user_id FROM team_memberships WHERE team_id = $1 AND left_at IS NULL AND role = 'owner' FOR UPDATE`, teamID).Scan(&fromUserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrTeamNotFound
		}
		return nil, err
	}
	if fromUserID == toUserID {
		return nil, service.ErrTeamTransferInvalid
	}
	var targetAvailable bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL)`, toUserID).Scan(&targetAvailable); err != nil {
		return nil, err
	}
	if !targetAvailable {
		return nil, service.ErrTeamTransferInvalid
	}
	if err = transferTeamOwnership(ctx, tx, teamID, fromUserID, toUserID, now); err != nil {
		return nil, err
	}
	_, _ = tx.ExecContext(ctx, `UPDATE team_ownership_transfers SET status = 'cancelled', resolved_at = $2, updated_at = $2 WHERE team_id = $1 AND status = 'pending'`, teamID, now)
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return r.GetContextByTeamID(ctx, teamID)
}

// transferTeamOwnership 分两步交换角色，避免部分唯一索引在单条 UPDATE 中看到两个 Owner。
func transferTeamOwnership(ctx context.Context, tx *sql.Tx, teamID, fromUserID, toUserID int64, now time.Time) error {
	demoted, err := tx.ExecContext(ctx, `
		UPDATE team_memberships SET role = 'member', daily_limit_usd = 0, weekly_limit_usd = 0, monthly_limit_usd = 0, updated_at = $4
		WHERE team_id = $1 AND user_id = $2 AND user_id <> $3 AND left_at IS NULL AND role = 'owner'`, teamID, fromUserID, toUserID, now)
	if err != nil {
		return err
	}
	if affected, _ := demoted.RowsAffected(); affected != 1 {
		return service.ErrTeamTransferInvalid
	}
	promoted, err := tx.ExecContext(ctx, `
		UPDATE team_memberships SET role = 'owner', daily_limit_usd = 0, weekly_limit_usd = 0, monthly_limit_usd = 0, updated_at = $4
		WHERE team_id = $1 AND user_id = $3 AND user_id <> $2 AND left_at IS NULL AND role = 'member'`, teamID, fromUserID, toUserID, now)
	if err != nil {
		return err
	}
	if affected, _ := promoted.RowsAffected(); affected != 1 {
		return service.ErrTeamTransferInvalid
	}
	return nil
}

func (r *teamRepository) ListAdmin(ctx context.Context) ([]service.TeamAdminListItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT t.id, t.name, t.status, t.member_limit,
		       t.default_daily_limit_usd, t.default_weekly_limit_usd, t.default_monthly_limit_usd,
		       t.created_at, t.updated_at,
		       (SELECT COUNT(*) FROM team_memberships cm WHERE cm.team_id = t.id AND cm.left_at IS NULL AND cm.role = 'member'),
		       owner.user_id, u.email
		FROM teams t
		JOIN team_memberships owner ON owner.team_id = t.id AND owner.left_at IS NULL AND owner.role = 'owner'
		JOIN users u ON u.id = owner.user_id
		WHERE t.deleted_at IS NULL ORDER BY t.created_at DESC`)
	if err != nil {
		return nil, err
	}
	// 查询结束时关闭结果集，读取阶段的错误统一通过 rows.Err 返回。
	defer func() { _ = rows.Close() }()
	items := make([]service.TeamAdminListItem, 0)
	for rows.Next() {
		var item service.TeamAdminListItem
		if err := rows.Scan(
			&item.ID, &item.Name, &item.Status, &item.MemberLimit,
			&item.DefaultDailyLimitUSD, &item.DefaultWeeklyLimitUSD, &item.DefaultMonthlyLimitUSD,
			&item.CreatedAt, &item.UpdatedAt, &item.MemberCount, &item.OwnerUserID, &item.OwnerEmail,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *teamRepository) ListTeamKeyStrings(ctx context.Context, teamID int64) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT key FROM api_keys WHERE team_id = $1 AND deleted_at IS NULL`, teamID)
	if err != nil {
		return nil, err
	}
	// 查询结束时关闭结果集，读取阶段的错误统一通过 rows.Err 返回。
	defer func() { _ = rows.Close() }()
	keys := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func teamUsageWhere(teamID int64, query service.TeamUsageQuery) (string, []any) {
	conditions := []string{"ul.team_id = $1", "ul.created_at >= $2", "ul.created_at < $3"}
	args := []any{teamID, query.From, query.To}
	if query.ActorUserID != nil && *query.ActorUserID > 0 {
		args = append(args, *query.ActorUserID)
		conditions = append(conditions, fmt.Sprintf("ul.user_id = $%d", len(args)))
	}
	if query.APIKeyID != nil && *query.APIKeyID > 0 {
		args = append(args, *query.APIKeyID)
		conditions = append(conditions, fmt.Sprintf("ul.api_key_id = $%d", len(args)))
	}
	return strings.Join(conditions, " AND "), args
}

func (r *teamRepository) GetUsageSummary(ctx context.Context, teamID int64, query service.TeamUsageQuery) (*service.TeamUsageSummary, error) {
	where, args := teamUsageWhere(teamID, query)
	summary := &service.TeamUsageSummary{Daily: make([]service.TeamUsageDaily, 0)}
	err := r.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(ul.actual_cost), 0), COUNT(*), COALESCE(SUM(ul.input_tokens), 0), COALESCE(SUM(ul.output_tokens), 0) FROM usage_logs ul WHERE `+where, args...).
		Scan(&summary.ActualCost, &summary.RequestCount, &summary.InputTokens, &summary.OutputTokens)
	if err != nil {
		return nil, err
	}
	args = append(args, timezone.Name())
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT TO_CHAR(ul.created_at AT TIME ZONE $%d, 'YYYY-MM-DD'), COALESCE(SUM(ul.actual_cost), 0), COUNT(*)
		FROM usage_logs ul WHERE %s
		GROUP BY 1 ORDER BY 1`, len(args), where), args...)
	if err != nil {
		return nil, err
	}
	// 查询结束时关闭结果集，读取阶段的错误统一通过 rows.Err 返回。
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var item service.TeamUsageDaily
		if err := rows.Scan(&item.Date, &item.ActualCost, &item.RequestCount); err != nil {
			return nil, err
		}
		summary.Daily = append(summary.Daily, item)
	}
	return summary, rows.Err()
}

func (r *teamRepository) ListMemberUsageSeries(ctx context.Context, teamID int64, query service.TeamUsageQuery) ([]service.TeamMemberUsageSeries, error) {
	args := []any{teamID, query.From, query.To, timezone.Name()}
	membershipActorFilter := ""
	usageActorFilter := ""
	if query.ActorUserID != nil && *query.ActorUserID > 0 {
		args = append(args, *query.ActorUserID)
		position := len(args)
		membershipActorFilter = fmt.Sprintf(" AND m.user_id = $%d", position)
		usageActorFilter = fmt.Sprintf(" AND ul.user_id = $%d", position)
	}
	rows, err := r.db.QueryContext(ctx, `
		WITH actors AS (
			SELECT m.user_id
			FROM team_memberships m
			WHERE m.team_id = $1 AND m.left_at IS NULL`+membershipActorFilter+`
			UNION
			SELECT ul.user_id
			FROM usage_logs ul
			WHERE ul.team_id = $1 AND ul.created_at >= $2 AND ul.created_at < $3`+usageActorFilter+`
		), daily AS (
			SELECT ul.user_id,
			       TO_CHAR(ul.created_at AT TIME ZONE $4, 'YYYY-MM-DD') AS usage_date,
			       COALESCE(SUM(ul.actual_cost), 0) AS actual_cost,
			       COUNT(*) AS request_count,
			       COALESCE(SUM(ul.input_tokens), 0) AS input_tokens,
			       COALESCE(SUM(ul.output_tokens), 0) AS output_tokens
			FROM usage_logs ul
			WHERE ul.team_id = $1 AND ul.created_at >= $2 AND ul.created_at < $3`+usageActorFilter+`
			GROUP BY ul.user_id, usage_date
		)
		SELECT a.user_id,
		       COALESCE(NULLIF(u.username, ''), NULLIF(u.email, ''), 'User #' || a.user_id::text),
		       CASE WHEN EXISTS (
			   SELECT 1 FROM team_memberships current_m
			   WHERE current_m.team_id = $1 AND current_m.user_id = a.user_id AND current_m.left_at IS NULL
		       ) THEN 'active' ELSE 'left' END,
		       d.usage_date, COALESCE(d.actual_cost, 0), COALESCE(d.request_count, 0),
		       COALESCE(d.input_tokens, 0), COALESCE(d.output_tokens, 0)
		FROM actors a
		LEFT JOIN users u ON u.id = a.user_id
		LEFT JOIN daily d ON d.user_id = a.user_id
		ORDER BY a.user_id, d.usage_date`, args...)
	if err != nil {
		return nil, err
	}
	// 查询结束时关闭结果集，读取阶段的错误统一通过 rows.Err 返回。
	defer func() { _ = rows.Close() }()
	items := make([]service.TeamMemberUsageSeries, 0)
	indexByUser := make(map[int64]int)
	for rows.Next() {
		var userID int64
		var displayName, status string
		var date sql.NullString
		var actualCost float64
		var requestCount, inputTokens, outputTokens int64
		if err := rows.Scan(&userID, &displayName, &status, &date, &actualCost, &requestCount, &inputTokens, &outputTokens); err != nil {
			return nil, err
		}
		index, exists := indexByUser[userID]
		if !exists {
			index = len(items)
			indexByUser[userID] = index
			items = append(items, service.TeamMemberUsageSeries{
				ActorUserID: userID,
				DisplayName: displayName,
				Status:      status,
				Summary:     service.TeamUsageSummary{Daily: make([]service.TeamUsageDaily, 0)},
			})
		}
		item := &items[index]
		if date.Valid {
			item.Summary.Daily = append(item.Summary.Daily, service.TeamUsageDaily{Date: date.String, ActualCost: actualCost, RequestCount: requestCount})
			item.Summary.ActualCost += actualCost
			item.Summary.RequestCount += requestCount
			item.Summary.InputTokens += inputTokens
			item.Summary.OutputTokens += outputTokens
		}
	}
	return items, rows.Err()
}

func (r *teamRepository) ListUsageLogs(ctx context.Context, teamID int64, query service.TeamUsageQuery) ([]service.TeamUsageLogItem, int64, error) {
	where, args := teamUsageWhere(teamID, query)
	var total int64
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_logs ul WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limitPos := len(args) + 1
	offsetPos := len(args) + 2
	args = append(args, query.Limit, query.Offset)
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT ul.id, ul.user_id, COALESCE(u.email, ''), ul.api_key_id, COALESCE(k.name, ''),
		       ul.request_id, COALESCE(NULLIF(ul.requested_model, ''), ul.model), ul.actual_cost,
		       ul.input_tokens, ul.output_tokens, ul.created_at
		FROM usage_logs ul
		LEFT JOIN users u ON u.id = ul.user_id
		LEFT JOIN api_keys k ON k.id = ul.api_key_id
		WHERE %s ORDER BY ul.created_at DESC, ul.id DESC LIMIT $%d OFFSET $%d`, where, limitPos, offsetPos), args...)
	if err != nil {
		return nil, 0, err
	}
	// 查询结束时关闭结果集，读取阶段的错误统一通过 rows.Err 返回。
	defer func() { _ = rows.Close() }()
	items := make([]service.TeamUsageLogItem, 0, query.Limit)
	for rows.Next() {
		var item service.TeamUsageLogItem
		if err := rows.Scan(&item.ID, &item.ActorUserID, &item.ActorEmail, &item.APIKeyID, &item.APIKeyName, &item.RequestID, &item.Model, &item.ActualCost, &item.InputTokens, &item.OutputTokens, &item.CreatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func teamKeyActorCondition(actorUserID *int64, args []any) (string, []any) {
	if actorUserID == nil || *actorUserID <= 0 {
		return "", args
	}
	args = append(args, *actorUserID)
	return fmt.Sprintf(" AND k.user_id = $%d", len(args)), args
}

func (r *teamRepository) ListTeamKeys(ctx context.Context, teamID int64, actorUserID *int64) ([]service.TeamAPIKeyItem, error) {
	args := []any{teamID}
	actorCondition, args := teamKeyActorCondition(actorUserID, args)
	rows, err := r.db.QueryContext(ctx, `
		SELECT k.id, k.user_id, COALESCE(u.email, ''), k.name, k.key, k.status, k.team_owner_disabled, k.group_id,
		       COALESCE(g.name, ''), k.last_used_at, k.created_at
		FROM api_keys k
		LEFT JOIN users u ON u.id = k.user_id
		LEFT JOIN groups g ON g.id = k.group_id
		WHERE k.team_id = $1 AND k.deleted_at IS NULL`+actorCondition+`
		ORDER BY k.created_at DESC, k.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	// 查询结束时关闭结果集，读取阶段的错误统一通过 rows.Err 返回。
	defer func() { _ = rows.Close() }()
	items := make([]service.TeamAPIKeyItem, 0)
	for rows.Next() {
		var item service.TeamAPIKeyItem
		var groupID sql.NullInt64
		var lastUsedAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.UserID, &item.UserEmail, &item.Name, &item.Key, &item.Status, &item.OwnerDisabled, &groupID, &item.GroupName, &lastUsedAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.GroupID = batchImageNullInt64Ptr(groupID)
		item.LastUsedAt = batchImageNullTimePtr(lastUsedAt)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *teamRepository) DisableTeamKey(ctx context.Context, teamID, keyID int64, actorUserID *int64) (string, error) {
	args := []any{teamID, keyID}
	actorCondition, args := teamKeyActorCondition(actorUserID, args)
	var key string
	err := r.db.QueryRowContext(ctx, `
		UPDATE api_keys k SET status = 'disabled', team_owner_disabled = TRUE, updated_at = NOW()
		WHERE k.team_id = $1 AND k.id = $2 AND k.deleted_at IS NULL`+actorCondition+`
		RETURNING k.key`, args...).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", service.ErrAPIKeyNotFound
	}
	return key, err
}

func (r *teamRepository) EnableTeamKey(ctx context.Context, teamID, keyID int64, actorUserID *int64) (string, error) {
	args := []any{teamID, keyID}
	actorCondition, args := teamKeyActorCondition(actorUserID, args)
	var key string
	err := r.db.QueryRowContext(ctx, `
		UPDATE api_keys k SET status = 'active', team_owner_disabled = FALSE, updated_at = NOW()
		WHERE k.team_id = $1 AND k.id = $2 AND k.deleted_at IS NULL`+actorCondition+`
		RETURNING k.key`, args...).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", service.ErrAPIKeyNotFound
	}
	return key, err
}

func (r *teamRepository) DeleteTeamKey(ctx context.Context, teamID, keyID int64, actorUserID *int64) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	args := []any{teamID, keyID}
	actorCondition, args := teamKeyActorCondition(actorUserID, args)
	var key string
	err = tx.QueryRowContext(ctx, `SELECT k.key FROM api_keys k WHERE k.team_id = $1 AND k.id = $2 AND k.deleted_at IS NULL`+actorCondition+` FOR UPDATE`, args...).Scan(&key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", service.ErrAPIKeyNotFound
	}
	if err != nil {
		return "", err
	}
	tombstone := fmt.Sprintf("__deleted__%d__%d", keyID, time.Now().UnixNano())
	if _, err = tx.ExecContext(ctx, `UPDATE api_keys SET key = $2, deleted_at = NOW(), updated_at = NOW() WHERE id = $1`, keyID, tombstone); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return key, nil
}

func normalizeTeamMembershipWindows(member *service.TeamMembership, now time.Time) {
	if member == nil {
		return
	}
	dailyStart := timezone.StartOfDay(now)
	weeklyStart := timezone.StartOfWeek(now)
	monthlyStart := timezone.StartOfMonth(now)
	if member.DailyWindowStart == nil || member.DailyWindowStart.Before(dailyStart) {
		member.DailyUsageUSD = 0
		member.DailyWindowStart = &dailyStart
	}
	if member.WeeklyWindowStart == nil || member.WeeklyWindowStart.Before(weeklyStart) {
		member.WeeklyUsageUSD = 0
		member.WeeklyWindowStart = &weeklyStart
	}
	if member.MonthlyWindowStart == nil || member.MonthlyWindowStart.Before(monthlyStart) {
		member.MonthlyUsageUSD = 0
		member.MonthlyWindowStart = &monthlyStart
	}
}

type teamRowScanner interface {
	Scan(dest ...any) error
}

func scanTeamMembership(row teamRowScanner, member *service.TeamMembership) error {
	return row.Scan(
		&member.ID, &member.TeamID, &member.UserID, &member.Email, &member.Username, &member.Role,
		&member.DailyLimitUSD, &member.WeeklyLimitUSD, &member.MonthlyLimitUSD,
		&member.DailyUsageUSD, &member.WeeklyUsageUSD, &member.MonthlyUsageUSD,
		&member.DailyWindowStart, &member.WeeklyWindowStart, &member.MonthlyWindowStart,
		&member.JoinedAt, &member.LastActiveAt,
	)
}

func requireTeamAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return service.ErrTeamNotFound
	}
	return nil
}

func mapTeamConstraintError(err error) error {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		return service.ErrTeamAlreadyJoined
	}
	return fmt.Errorf("团队数据约束失败: %w", err)
}

var _ service.TeamRepository = (*teamRepository)(nil)
