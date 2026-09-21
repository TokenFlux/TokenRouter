// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	usagequery "github.com/TokenFlux/TokenRouter/internal/usage/postgres/query"

	context "context"

	sql "database/sql"

	errors "errors"

	fmt "fmt"

	billing "github.com/TokenFlux/TokenRouter/internal/billing"

	timezone "github.com/TokenFlux/TokenRouter/internal/pkg/timezone"

	team "github.com/TokenFlux/TokenRouter/internal/team"

	pq "github.com/lib/pq"

	strings "strings"

	time "time"
)

type TeamRepository struct {
	calendar *timezone.Calendar
	keys     TeamKeys
	usage    MemberUsage
	db       *sql.DB
}

// NewTeamRepository 创建使用事务保证成员和所有权约束的团队仓储。
func NewTeamRepository(db *sql.DB, keys TeamKeys, usage MemberUsage, calendar ...*timezone.Calendar) team.TeamRepository {
	repo := &TeamRepository{db: db, keys: keys, usage: usage}
	if len(calendar) > 0 {
		repo.calendar = calendar[0]
	}
	return repo
}

func (r *TeamRepository) Create(ctx context.Context, name string, ownerUserID int64, memberLimit int) (*team.TeamContext, error) {
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
		return nil, team.ErrTeamAlreadyJoined
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
		return nil, MapTeamConstraintError(err)
	}
	if err = tx.Commit(); err != nil {
		return nil, MapTeamConstraintError(err)
	}
	return r.GetContextByTeamID(ctx, teamID)
}

func (r *TeamRepository) GetContextByUserID(ctx context.Context, userID int64) (*team.TeamContext, error) {
	return r.scanContext(ctx, `WHERE current_membership.user_id = $1 AND current_membership.left_at IS NULL`, userID)
}

func (r *TeamRepository) GetContextByTeamID(ctx context.Context, teamID int64) (*team.TeamContext, error) {
	return r.scanContext(ctx, `WHERE t.id = $1 AND current_membership.role = 'owner'`, teamID)
}

func (r *TeamRepository) scanContext(ctx context.Context, where string, arg int64) (*team.TeamContext, error) {
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
	teamCtx := &team.TeamContext{Team: &team.Team{}, Membership: &team.TeamMembership{}, Owner: &team.TeamMembership{}}
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
		return nil, team.ErrTeamNotFound
	}
	if err == nil {
		r.normalizeMemberWindows(teamCtx.Membership, time.Now())
		r.normalizeMemberWindows(teamCtx.Owner, time.Now())
	}
	return teamCtx, err
}

func (r *TeamRepository) UpdateName(ctx context.Context, teamID int64, name string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE teams SET name = $2, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, teamID, name)
	return RequireTeamAffected(result, err)
}

func (r *TeamRepository) SetDefaultMemberLimits(ctx context.Context, teamID int64, daily, weekly, monthly float64) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE teams SET default_daily_limit_usd = $2, default_weekly_limit_usd = $3,
			default_monthly_limit_usd = $4, updated_at = NOW()
		WHERE id = $1 AND deleted_at IS NULL`, teamID, daily, weekly, monthly)
	return RequireTeamAffected(result, err)
}

func (r *TeamRepository) ListMembers(ctx context.Context, teamID int64) ([]team.TeamMembership, error) {
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
	members := make([]team.TeamMembership, 0)
	for rows.Next() {
		var member team.TeamMembership
		if err := ScanTeamMembership(rows, &member); err != nil {
			return nil, err
		}
		r.normalizeMemberWindows(&member, time.Now())
		members = append(members, member)
	}
	return members, rows.Err()
}

func (r *TeamRepository) CreateInvitation(ctx context.Context, teamID, inviterUserID int64, email, tokenHash string, expiresAt time.Time) (*team.TeamInvitation, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `UPDATE team_invitations SET status = 'revoked', updated_at = NOW() WHERE team_id = $1 AND email = $2 AND status = 'pending'`, teamID, email); err != nil {
		return nil, err
	}
	invitation := &team.TeamInvitation{}
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

func (r *TeamRepository) ListInvitations(ctx context.Context, teamID int64) ([]team.TeamInvitation, error) {
	_, _ = r.db.ExecContext(ctx, `UPDATE team_invitations SET status = 'expired', updated_at = NOW() WHERE team_id = $1 AND status = 'pending' AND expires_at <= NOW()`, teamID)
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, team_id, inviter_user_id, email, status, expires_at, accepted_at, created_at
		FROM team_invitations WHERE team_id = $1 ORDER BY created_at DESC`, teamID)
	if err != nil {
		return nil, err
	}
	// 查询结束时关闭结果集，读取阶段的错误统一通过 rows.Err 返回。
	defer func() { _ = rows.Close() }()
	items := make([]team.TeamInvitation, 0)
	for rows.Next() {
		var item team.TeamInvitation
		if err := rows.Scan(&item.ID, &item.TeamID, &item.InviterUserID, &item.Email, &item.Status, &item.ExpiresAt, &item.AcceptedAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *TeamRepository) GetInvitationByID(ctx context.Context, teamID, invitationID int64) (*team.TeamInvitation, error) {
	item := &team.TeamInvitation{}
	err := r.db.QueryRowContext(ctx, `
		SELECT id, team_id, inviter_user_id, email, status, expires_at, accepted_at, created_at
		FROM team_invitations WHERE team_id = $1 AND id = $2`, teamID, invitationID).
		Scan(&item.ID, &item.TeamID, &item.InviterUserID, &item.Email, &item.Status, &item.ExpiresAt, &item.AcceptedAt, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, team.ErrTeamInvitationInvalid
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (r *TeamRepository) ReissueInvitation(ctx context.Context, teamID, invitationID int64, tokenHash string, expiresAt time.Time) (*team.TeamInvitation, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var email, status string
	err = tx.QueryRowContext(ctx, `SELECT email, status FROM team_invitations WHERE id = $2 AND team_id = $1 FOR UPDATE`, teamID, invitationID).Scan(&email, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, team.ErrTeamInvitationInvalid
	}
	if err != nil {
		return nil, err
	}
	if status != "pending" && status != "expired" {
		return nil, team.ErrTeamInvitationInvalid
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE team_invitations SET status = 'revoked', updated_at = NOW()
		WHERE team_id = $1 AND email = $2 AND id <> $3 AND status = 'pending'`, teamID, email, invitationID); err != nil {
		return nil, err
	}
	item := &team.TeamInvitation{}
	err = tx.QueryRowContext(ctx, `
		UPDATE team_invitations SET token_hash = $3, status = 'pending', expires_at = $4, accepted_by_user_id = NULL, accepted_at = NULL, updated_at = NOW()
		WHERE id = $2 AND team_id = $1 AND status IN ('pending', 'expired')
		RETURNING id, team_id, inviter_user_id, email, status, expires_at, accepted_at, created_at`,
		teamID, invitationID, tokenHash, expiresAt,
	).Scan(&item.ID, &item.TeamID, &item.InviterUserID, &item.Email, &item.Status, &item.ExpiresAt, &item.AcceptedAt, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, team.ErrTeamInvitationInvalid
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return item, nil
}

func (r *TeamRepository) RevokeInvitation(ctx context.Context, teamID, invitationID int64) error {
	result, err := r.db.ExecContext(ctx, `UPDATE team_invitations SET status = 'revoked', updated_at = NOW() WHERE id = $2 AND team_id = $1 AND status = 'pending'`, teamID, invitationID)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return team.ErrTeamInvitationInvalid
	}
	return nil
}

// PreviewInvitation 仅在令牌、状态和受邀邮箱均匹配时返回邀请摘要。
func (r *TeamRepository) PreviewInvitation(ctx context.Context, tokenHash, normalizedEmail string, now time.Time) (*team.TeamInvitationPreview, error) {
	var invitationID int64
	var invitationEmail, status string
	preview := &team.TeamInvitationPreview{}
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
		return nil, team.ErrTeamInvitationInvalid
	}
	if err != nil {
		return nil, err
	}
	if status != "pending" {
		return nil, team.ErrTeamInvitationInvalid
	}
	if !preview.ExpiresAt.After(now) {
		_, _ = r.db.ExecContext(ctx, `UPDATE team_invitations SET status = 'expired', updated_at = $2 WHERE id = $1 AND status = 'pending'`, invitationID, now)
		return nil, team.ErrTeamInvitationExpired
	}
	if !strings.EqualFold(invitationEmail, normalizedEmail) {
		return nil, team.ErrTeamInvitationEmail
	}
	return preview, nil
}

func (r *TeamRepository) ResolveInvitation(ctx context.Context, tokenHash string, userID int64, normalizedEmail, resolution string, now time.Time) (*team.TeamContext, error) {
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
		return nil, team.ErrTeamInvitationInvalid
	}
	if err != nil {
		return nil, err
	}
	if status != "pending" {
		return nil, team.ErrTeamInvitationInvalid
	}
	if !expiresAt.After(now) {
		_, _ = tx.ExecContext(ctx, `UPDATE team_invitations SET status = 'expired', updated_at = $2 WHERE id = $1`, invitationID, now)
		_ = tx.Commit()
		return nil, team.ErrTeamInvitationExpired
	}
	if !strings.EqualFold(email, normalizedEmail) {
		return nil, team.ErrTeamInvitationEmail
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
		return nil, team.ErrTeamNotFound
	}
	if err != nil {
		return nil, err
	}
	if teamStatus != team.TeamStatusActive {
		return nil, team.ErrTeamSuspended
	}
	// 团队行锁获取后另起一条语句统计，确保能看到前一个接受事务刚提交的成员。
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_memberships WHERE team_id = $1 AND left_at IS NULL AND role = 'member'`, teamID).Scan(&memberCount); err != nil {
		return nil, err
	}
	if memberCount >= memberLimit {
		return nil, team.ErrTeamMemberLimitReached
	}
	var alreadyJoined bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM team_memberships WHERE user_id = $1 AND left_at IS NULL)`, userID).Scan(&alreadyJoined); err != nil {
		return nil, err
	}
	if alreadyJoined {
		return nil, team.ErrTeamAlreadyJoined
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO team_memberships (
			team_id, user_id, role, daily_limit_usd, weekly_limit_usd, monthly_limit_usd,
			joined_at, created_at, updated_at
		)
		VALUES ($1, $2, 'member', $3, $4, $5, $6, $6, $6)`,
		teamID, userID, defaultDaily, defaultWeekly, defaultMonthly, now); err != nil {
		return nil, MapTeamConstraintError(err)
	}
	// 再次加入时旧 Membership 生命周期内的 Key 不得自动恢复可用。
	if err = r.keys.DisableHistoricalMemberInTx(ctx, tx, teamID, userID, now); err != nil {
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
		return nil, MapTeamConstraintError(err)
	}
	return r.GetContextByUserID(ctx, userID)
}

func (r *TeamRepository) RemoveMember(ctx context.Context, teamID, userID int64, now time.Time) error {
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
		return team.ErrTeamMembershipRequired
	}
	if err = r.keys.DisableMemberInTx(ctx, tx, teamID, userID, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *TeamRepository) UpdateMemberLimits(ctx context.Context, teamID, userID int64, daily, weekly, monthly float64) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE team_memberships SET daily_limit_usd = $3, weekly_limit_usd = $4, monthly_limit_usd = $5, updated_at = NOW()
		WHERE team_id = $1 AND user_id = $2 AND left_at IS NULL AND role = 'member'`, teamID, userID, daily, weekly, monthly)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return team.ErrTeamMembershipRequired
	}
	return nil
}

func (r *TeamRepository) ResetMemberUsage(ctx context.Context, teamID, userID int64, resetDaily, resetWeekly, resetMonthly bool, now time.Time) error {
	return r.usage.ResetMemberUsage(ctx, teamID, userID, resetDaily, resetWeekly, resetMonthly, now)
}

func (r *TeamRepository) CreateOwnershipTransfer(ctx context.Context, teamID, fromUserID, toUserID int64, tokenHash string, expiresAt time.Time) (*team.TeamOwnershipTransfer, error) {
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
		return nil, team.ErrTeamTransferInvalid
	}
	_, _ = tx.ExecContext(ctx, `UPDATE team_ownership_transfers SET status = 'cancelled', resolved_at = NOW(), updated_at = NOW() WHERE team_id = $1 AND status = 'pending'`, teamID)
	item := &team.TeamOwnershipTransfer{}
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

func (r *TeamRepository) ResolveOwnershipTransfer(ctx context.Context, tokenHash string, actorUserID int64, resolution string, now time.Time) (*team.TeamContext, error) {
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
		return nil, team.ErrTeamTransferInvalid
	}
	if err != nil {
		return nil, err
	}
	if status != "pending" || actorUserID != toUserID {
		return nil, team.ErrTeamTransferInvalid
	}
	if !expiresAt.After(now) {
		_, _ = tx.ExecContext(ctx, `UPDATE team_ownership_transfers SET status = 'expired', resolved_at = $2, updated_at = $2 WHERE id = $1`, id, now)
		_ = tx.Commit()
		return nil, team.ErrTeamTransferExpired
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

func (r *TeamRepository) CancelOwnershipTransfer(ctx context.Context, teamID, actorUserID int64, now time.Time) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE team_ownership_transfers SET status = 'cancelled', resolved_at = $3, updated_at = $3
		WHERE team_id = $1 AND from_user_id = $2 AND status = 'pending'`, teamID, actorUserID, now)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return team.ErrTeamTransferInvalid
	}
	return nil
}

func (r *TeamRepository) Dissolve(ctx context.Context, teamID int64, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if result, execErr := tx.ExecContext(ctx, `UPDATE teams SET status = 'suspended', deleted_at = $2, updated_at = $2 WHERE id = $1 AND deleted_at IS NULL`, teamID, now); execErr != nil {
		return execErr
	} else if affected, _ := result.RowsAffected(); affected == 0 {
		return team.ErrTeamNotFound
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
	if err = r.keys.DisableTeamInTx(ctx, tx, teamID, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *TeamRepository) SetStatus(ctx context.Context, teamID int64, status string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE teams SET status = $2, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, teamID, status)
	return RequireTeamAffected(result, err)
}

func (r *TeamRepository) SetMemberLimit(ctx context.Context, teamID int64, limit int) error {
	result, err := r.db.ExecContext(ctx, `UPDATE teams SET member_limit = $2, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`, teamID, limit)
	return RequireTeamAffected(result, err)
}

func (r *TeamRepository) UpdateAdmin(ctx context.Context, teamID int64, update team.TeamAdminUpdate) error {
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
	return RequireTeamAffected(result, err)
}

func (r *TeamRepository) ForceTransfer(ctx context.Context, teamID, toUserID int64, now time.Time) (*team.TeamContext, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var fromUserID int64
	if err = tx.QueryRowContext(ctx, `SELECT user_id FROM team_memberships WHERE team_id = $1 AND left_at IS NULL AND role = 'owner' FOR UPDATE`, teamID).Scan(&fromUserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, team.ErrTeamNotFound
		}
		return nil, err
	}
	if fromUserID == toUserID {
		return nil, team.ErrTeamTransferInvalid
	}
	var targetAvailable bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL)`, toUserID).Scan(&targetAvailable); err != nil {
		return nil, err
	}
	if !targetAvailable {
		return nil, team.ErrTeamTransferInvalid
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
		return team.ErrTeamTransferInvalid
	}
	promoted, err := tx.ExecContext(ctx, `
		UPDATE team_memberships SET role = 'owner', daily_limit_usd = 0, weekly_limit_usd = 0, monthly_limit_usd = 0, updated_at = $4
		WHERE team_id = $1 AND user_id = $3 AND user_id <> $2 AND left_at IS NULL AND role = 'member'`, teamID, fromUserID, toUserID, now)
	if err != nil {
		return err
	}
	if affected, _ := promoted.RowsAffected(); affected != 1 {
		return team.ErrTeamTransferInvalid
	}
	return nil
}

func (r *TeamRepository) ListAdmin(ctx context.Context) ([]team.TeamAdminListItem, error) {
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
	items := make([]team.TeamAdminListItem, 0)
	for rows.Next() {
		var item team.TeamAdminListItem
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

func (r *TeamRepository) ListTeamKeyStrings(ctx context.Context, teamID int64) ([]string, error) {
	return r.keys.ListTeamKeyStrings(ctx, teamID)
}

func TeamUsageWhere(teamID int64, query team.TeamUsageQuery) (string, []any) {
	return usagequery.TeamUsageWhere(teamID, query)
}

func (r *TeamRepository) GetUsageSummary(ctx context.Context, teamID int64, query team.TeamUsageQuery) (*team.TeamUsageSummary, error) {
	return usagequery.GetUsageSummary(ctx, r.db, teamID, query, r.dateCalendar())
}

func (r *TeamRepository) ListMemberUsageSeries(ctx context.Context, teamID int64, query team.TeamUsageQuery) ([]team.TeamMemberUsageSeries, error) {
	return usagequery.ListMemberUsageSeries(ctx, r.db, teamID, query, r.dateCalendar())
}

func (r *TeamRepository) ListUsageLogs(ctx context.Context, teamID int64, query team.TeamUsageQuery) ([]team.TeamUsageLogItem, int64, error) {
	return usagequery.ListUsageLogs(ctx, r.db, teamID, query)
}

func (r *TeamRepository) ListTeamKeys(ctx context.Context, teamID int64, actorUserID *int64) ([]team.TeamAPIKeyItem, error) {
	return r.keys.ListTeamKeys(ctx, teamID, actorUserID)
}

func (r *TeamRepository) DisableTeamKey(ctx context.Context, teamID, keyID int64, actorUserID *int64) (string, error) {
	return r.keys.DisableTeamKey(ctx, teamID, keyID, actorUserID)
}

func (r *TeamRepository) EnableTeamKey(ctx context.Context, teamID, keyID int64, actorUserID *int64) (string, error) {
	return r.keys.EnableTeamKey(ctx, teamID, keyID, actorUserID)
}

func (r *TeamRepository) DeleteTeamKey(ctx context.Context, teamID, keyID int64, actorUserID *int64) (string, error) {
	return r.keys.DeleteTeamKey(ctx, teamID, keyID, actorUserID)
}

// NormalizeTeamMembershipWindows 为旧存储入口保留系统日期对象，规则只有 billing 一份。
func NormalizeTeamMembershipWindows(member *team.TeamMembership, now time.Time) {
	normalizeMemberQuotaWindows(member, now, timezone.NewCalendar(time.Local))
}
func normalizeMemberQuotaWindows(member *team.TeamMembership, now time.Time, calendar timezone.Calendar) {
	if member == nil {
		return
	}
	projected := billing.NormalizeMemberQuotaWindows(billing.MemberQuotaSnapshot{DailyUsageUSD: member.DailyUsageUSD, WeeklyUsageUSD: member.WeeklyUsageUSD, MonthlyUsageUSD: member.MonthlyUsageUSD, DailyWindowStart: member.DailyWindowStart, WeeklyWindowStart: member.WeeklyWindowStart, MonthlyWindowStart: member.MonthlyWindowStart}, now, calendar)
	member.DailyUsageUSD = projected.DailyUsageUSD
	member.WeeklyUsageUSD = projected.WeeklyUsageUSD
	member.MonthlyUsageUSD = projected.MonthlyUsageUSD
	member.DailyWindowStart = projected.DailyWindowStart
	member.WeeklyWindowStart = projected.WeeklyWindowStart
	member.MonthlyWindowStart = projected.MonthlyWindowStart
}
func (r *TeamRepository) normalizeMemberWindows(member *team.TeamMembership, now time.Time) {
	normalizeMemberQuotaWindows(member, now, r.dateCalendar())
}

// dateCalendar 让团队消费窗口与只读统计使用相同的装配时区。
func (r *TeamRepository) dateCalendar() timezone.Calendar {
	if r.calendar != nil {
		return *r.calendar
	}
	return timezone.NewCalendar(time.Local)
}

type TeamRowScanner interface {
	Scan(dest ...any) error
}

func ScanTeamMembership(row TeamRowScanner, member *team.TeamMembership) error {
	return row.Scan(
		&member.ID, &member.TeamID, &member.UserID, &member.Email, &member.Username, &member.Role,
		&member.DailyLimitUSD, &member.WeeklyLimitUSD, &member.MonthlyLimitUSD,
		&member.DailyUsageUSD, &member.WeeklyUsageUSD, &member.MonthlyUsageUSD,
		&member.DailyWindowStart, &member.WeeklyWindowStart, &member.MonthlyWindowStart,
		&member.JoinedAt, &member.LastActiveAt,
	)
}

func RequireTeamAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return team.ErrTeamNotFound
	}
	return nil
}

func MapTeamConstraintError(err error) error {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" {
		return team.ErrTeamAlreadyJoined
	}
	return fmt.Errorf("团队数据约束失败: %w", err)
}

var _ team.TeamRepository = (*TeamRepository)(nil)

// TeamKeys 的事务参与方法不允许替换调用方连接或提交事务。
type TeamKeys interface {
	ListTeamKeyStrings(context.Context, int64) ([]string, error)
	ListTeamKeys(context.Context, int64, *int64) ([]team.TeamAPIKeyItem, error)
	DisableTeamKey(context.Context, int64, int64, *int64) (string, error)
	EnableTeamKey(context.Context, int64, int64, *int64) (string, error)
	DeleteTeamKey(context.Context, int64, int64, *int64) (string, error)
	DisableMemberInTx(context.Context, *sql.Tx, int64, int64, time.Time) error
	DisableHistoricalMemberInTx(context.Context, *sql.Tx, int64, int64, time.Time) error
	DisableTeamInTx(context.Context, *sql.Tx, int64, time.Time) error
}
type MemberUsage interface {
	ResetMemberUsage(context.Context, int64, int64, bool, bool, bool, time.Time) error
}
