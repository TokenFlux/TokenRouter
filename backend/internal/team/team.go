// 本文件维护 team 的所属能力；兼容入口复用唯一实现。
package team

import (
	context "context"
	rand "crypto/rand"
	sha256 "crypto/sha256"
	hex "encoding/hex"
	fmt "fmt"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	logredact "github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	slog "log/slog"
	mail "net/mail"
	url "net/url"
	strconv "strconv"
	strings "strings"
	time "time"
)

const (
	TeamStatusActive    = "active"
	TeamStatusSuspended = "suspended"
	TeamRoleOwner       = "owner"
	TeamRoleMember      = "member"
)

// Team 表示团队的公开基础信息。
type Team struct {
	ID                     int64      `json:"id"`
	Name                   string     `json:"name"`
	Status                 string     `json:"status"`
	MemberLimit            int        `json:"member_limit"`
	DefaultDailyLimitUSD   float64    `json:"default_daily_limit_usd"`
	DefaultWeeklyLimitUSD  float64    `json:"default_weekly_limit_usd"`
	DefaultMonthlyLimitUSD float64    `json:"default_monthly_limit_usd"`
	MemberCount            int        `json:"member_count"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
	DeletedAt              *time.Time `json:"-"`
}

// TeamMembership 同时保存角色、限额和当前自然周期用量。
type TeamMembership struct {
	ID                 int64      `json:"id"`
	TeamID             int64      `json:"team_id"`
	UserID             int64      `json:"user_id"`
	Email              string     `json:"email"`
	Username           string     `json:"username"`
	Role               string     `json:"role"`
	DailyLimitUSD      float64    `json:"daily_limit_usd"`
	WeeklyLimitUSD     float64    `json:"weekly_limit_usd"`
	MonthlyLimitUSD    float64    `json:"monthly_limit_usd"`
	DailyUsageUSD      float64    `json:"daily_usage_usd"`
	WeeklyUsageUSD     float64    `json:"weekly_usage_usd"`
	MonthlyUsageUSD    float64    `json:"monthly_usage_usd"`
	DailyWindowStart   *time.Time `json:"daily_window_start"`
	WeeklyWindowStart  *time.Time `json:"weekly_window_start"`
	MonthlyWindowStart *time.Time `json:"monthly_window_start"`
	JoinedAt           time.Time  `json:"joined_at"`
	LastActiveAt       *time.Time `json:"last_active_at"`
}

// TeamContext 是前端初始化团队页面和作用域切换所需的完整上下文。
type TeamContext struct {
	Team       *Team           `json:"team"`
	Membership *TeamMembership `json:"membership"`
	Owner      *TeamMembership `json:"owner"`
}

// TeamInvitation 不返回令牌哈希，只暴露邀请生命周期信息。
type TeamInvitation struct {
	ID            int64      `json:"id"`
	TeamID        int64      `json:"team_id"`
	InviterUserID int64      `json:"inviter_user_id"`
	Email         string     `json:"email"`
	Status        string     `json:"status"`
	ExpiresAt     time.Time  `json:"expires_at"`
	AcceptedAt    *time.Time `json:"accepted_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

// TeamInvitationPreview 是受邀用户确认邀请前可查看的最小必要信息。
type TeamInvitationPreview struct {
	TeamName     string    `json:"team_name"`
	InviterName  string    `json:"inviter_name"`
	InviterEmail string    `json:"inviter_email"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// TeamInvitationLimiter 对邀请和重发执行跨实例邮件频率限制。
type TeamInvitationLimiter interface {
	CheckAndRecord(ctx context.Context, teamID int64, email string) (allowed bool, retryAfter time.Duration, err error)
}

// TeamOwnershipTransfer 表示待目标成员确认的所有权转让。
type TeamOwnershipTransfer struct {
	ID         int64      `json:"id"`
	TeamID     int64      `json:"team_id"`
	FromUserID int64      `json:"from_user_id"`
	ToUserID   int64      `json:"to_user_id"`
	Status     string     `json:"status"`
	ExpiresAt  time.Time  `json:"expires_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

// TeamAdminListItem 为平台管理员提供团队与所有者摘要。
type TeamAdminListItem struct {
	Team
	OwnerUserID int64  `json:"owner_user_id"`
	OwnerEmail  string `json:"owner_email"`
}

// TeamAdminUpdate 表示管理员一次 PATCH 中经过完整校验的可选字段。
type TeamAdminUpdate struct {
	Name        *string
	Status      *string
	MemberLimit *int
}

// TeamUsageQuery 描述团队用量的时间范围与可选成员、Key 筛选条件。
type TeamUsageQuery struct {
	From        time.Time
	To          time.Time
	ActorUserID *int64
	APIKeyID    *int64
	Limit       int
	Offset      int
}

// TeamUsageDaily 是团队用量趋势中的单个自然日。
type TeamUsageDaily struct {
	Date         string  `json:"date"`
	ActualCost   float64 `json:"actual_cost"`
	RequestCount int64   `json:"request_count"`
}

// TeamUsageSummary 汇总团队 Key 在指定时间范围内的消费与令牌数。
type TeamUsageSummary struct {
	ActualCost   float64          `json:"actual_cost"`
	RequestCount int64            `json:"request_count"`
	InputTokens  int64            `json:"input_tokens"`
	OutputTokens int64            `json:"output_tokens"`
	Daily        []TeamUsageDaily `json:"daily"`
}

// TeamMemberUsageSeries 同时覆盖当前成员和查询范围内存在历史消费的离队成员。
type TeamMemberUsageSeries struct {
	ActorUserID int64            `json:"actor_user_id"`
	DisplayName string           `json:"display_name"`
	Status      string           `json:"status"`
	Summary     TeamUsageSummary `json:"summary"`
}

// TeamUsageLogItem 是团队页面可展示的脱敏用量明细。
type TeamUsageLogItem struct {
	ID           int64     `json:"id"`
	ActorUserID  int64     `json:"actor_user_id"`
	ActorEmail   string    `json:"actor_email"`
	APIKeyID     int64     `json:"api_key_id"`
	APIKeyName   string    `json:"api_key_name"`
	RequestID    string    `json:"request_id"`
	Model        string    `json:"model"`
	ActualCost   float64   `json:"actual_cost"`
	InputTokens  int       `json:"input_tokens"`
	OutputTokens int       `json:"output_tokens"`
	CreatedAt    time.Time `json:"created_at"`
}

// TeamUsagePage 返回团队用量明细及分页总数。
type TeamUsagePage struct {
	Items  []TeamUsageLogItem `json:"items"`
	Total  int64              `json:"total"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

// TeamAPIKeyItem 是团队管理视图使用的密钥元数据，永不序列化完整密钥。
type TeamAPIKeyItem struct {
	ID            int64      `json:"id"`
	UserID        int64      `json:"user_id"`
	UserEmail     string     `json:"user_email"`
	Name          string     `json:"name"`
	MaskedKey     string     `json:"masked_key"`
	Status        string     `json:"status"`
	OwnerDisabled bool       `json:"team_owner_disabled"`
	GroupID       *int64     `json:"group_id"`
	GroupName     string     `json:"group_name"`
	LastUsedAt    *time.Time `json:"last_used_at"`
	CreatedAt     time.Time  `json:"created_at"`
	Key           string     `json:"-"`
}

// TeamRepository 定义团队生命周期所需的原子仓储操作。
type TeamRepository interface {
	Create(ctx context.Context, name string, ownerUserID int64, memberLimit int) (*TeamContext, error)
	GetContextByUserID(ctx context.Context, userID int64) (*TeamContext, error)
	GetContextByTeamID(ctx context.Context, teamID int64) (*TeamContext, error)
	UpdateName(ctx context.Context, teamID int64, name string) error
	SetDefaultMemberLimits(ctx context.Context, teamID int64, daily, weekly, monthly float64) error
	ListMembers(ctx context.Context, teamID int64) ([]TeamMembership, error)
	CreateInvitation(ctx context.Context, teamID, inviterUserID int64, email, tokenHash string, expiresAt time.Time) (*TeamInvitation, error)
	ListInvitations(ctx context.Context, teamID int64) ([]TeamInvitation, error)
	GetInvitationByID(ctx context.Context, teamID, invitationID int64) (*TeamInvitation, error)
	ReissueInvitation(ctx context.Context, teamID, invitationID int64, tokenHash string, expiresAt time.Time) (*TeamInvitation, error)
	RevokeInvitation(ctx context.Context, teamID, invitationID int64) error
	PreviewInvitation(ctx context.Context, tokenHash, normalizedEmail string, now time.Time) (*TeamInvitationPreview, error)
	ResolveInvitation(ctx context.Context, tokenHash string, userID int64, normalizedEmail, resolution string, now time.Time) (*TeamContext, error)
	RemoveMember(ctx context.Context, teamID, userID int64, now time.Time) error
	UpdateMemberLimits(ctx context.Context, teamID, userID int64, daily, weekly, monthly float64) error
	ResetMemberUsage(ctx context.Context, teamID, userID int64, resetDaily, resetWeekly, resetMonthly bool, now time.Time) error
	CreateOwnershipTransfer(ctx context.Context, teamID, fromUserID, toUserID int64, tokenHash string, expiresAt time.Time) (*TeamOwnershipTransfer, error)
	ResolveOwnershipTransfer(ctx context.Context, tokenHash string, actorUserID int64, resolution string, now time.Time) (*TeamContext, error)
	CancelOwnershipTransfer(ctx context.Context, teamID, actorUserID int64, now time.Time) error
	Dissolve(ctx context.Context, teamID int64, now time.Time) error
	SetStatus(ctx context.Context, teamID int64, status string) error
	SetMemberLimit(ctx context.Context, teamID int64, limit int) error
	UpdateAdmin(ctx context.Context, teamID int64, update TeamAdminUpdate) error
	ForceTransfer(ctx context.Context, teamID, toUserID int64, now time.Time) (*TeamContext, error)
	ListAdmin(ctx context.Context) ([]TeamAdminListItem, error)
	ListTeamKeyStrings(ctx context.Context, teamID int64) ([]string, error)
	GetUsageSummary(ctx context.Context, teamID int64, query TeamUsageQuery) (*TeamUsageSummary, error)
	ListMemberUsageSeries(ctx context.Context, teamID int64, query TeamUsageQuery) ([]TeamMemberUsageSeries, error)
	ListUsageLogs(ctx context.Context, teamID int64, query TeamUsageQuery) ([]TeamUsageLogItem, int64, error)
	ListTeamKeys(ctx context.Context, teamID int64, actorUserID *int64) ([]TeamAPIKeyItem, error)
	DisableTeamKey(ctx context.Context, teamID, keyID int64, actorUserID *int64) (string, error)
	EnableTeamKey(ctx context.Context, teamID, keyID int64, actorUserID *int64) (string, error)
	DeleteTeamKey(ctx context.Context, teamID, keyID int64, actorUserID *int64) (string, error)
}

// TeamService 编排团队权限、令牌、邮件和缓存失效。
type TeamService struct {
	repo           TeamRepository
	userRepo       UserReader
	emailService   Notifier
	apiKeyCache    KeyInvalidator
	inviteLimiter  TeamInvitationLimiter
	settingService Settings
	options        *Options
}

func (s *TeamService) ensureEnabled() error {
	if s.options != nil && !s.options.Enabled {
		return ErrTeamFeatureDisabled
	}
	return nil
}

func (s *TeamService) Create(ctx context.Context, userID int64, name string) (*TeamContext, error) {
	if err := s.ensureEnabled(); err != nil {
		return nil, err
	}
	if s.options != nil && !s.options.SelfServiceEnabled {
		return nil, ErrTeamSelfServiceDisabled
	}
	name, err := NormalizeTeamName(name)
	if err != nil {
		return nil, err
	}
	limit := 10
	if s.options != nil && s.options.DefaultMemberLimit >= 0 {
		limit = s.options.DefaultMemberLimit
	}
	return s.repo.Create(ctx, name, userID, limit)
}

func (s *TeamService) AdminCreate(ctx context.Context, ownerUserID int64, name string, memberLimit *int) (*TeamContext, error) {
	if err := s.ensureEnabled(); err != nil {
		return nil, err
	}
	name, err := NormalizeTeamName(name)
	if err != nil {
		return nil, err
	}
	limit := 10
	if s.options != nil && s.options.DefaultMemberLimit >= 0 {
		limit = s.options.DefaultMemberLimit
	}
	if memberLimit != nil {
		limit = *memberLimit
	}
	if limit < 0 {
		return nil, infraerrors.BadRequest("TEAM_MEMBER_LIMIT_INVALID", "团队成员上限不能为负数")
	}
	return s.repo.Create(ctx, name, ownerUserID, limit)
}

func (s *TeamService) GetCurrent(ctx context.Context, userID int64) (*TeamContext, error) {
	if err := s.ensureEnabled(); err != nil {
		return nil, err
	}
	return s.repo.GetContextByUserID(ctx, userID)
}

func (s *TeamService) UpdateName(ctx context.Context, userID int64, name string) (*TeamContext, error) {
	teamCtx, err := s.requireOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	name, err = NormalizeTeamName(name)
	if err != nil {
		return nil, err
	}
	if err := s.repo.UpdateName(ctx, teamCtx.Team.ID, name); err != nil {
		return nil, err
	}
	return s.repo.GetContextByTeamID(ctx, teamCtx.Team.ID)
}

// UpdateDefaultMemberLimits 更新后续新成员加入团队时继承的默认限额。
func (s *TeamService) UpdateDefaultMemberLimits(ctx context.Context, userID int64, daily, weekly, monthly float64) (*TeamContext, error) {
	teamCtx, err := s.requireOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	if daily < 0 || weekly < 0 || monthly < 0 {
		return nil, infraerrors.BadRequest("TEAM_DEFAULT_MEMBER_LIMIT_INVALID", "成员默认限额不能为负数")
	}
	if err := s.repo.SetDefaultMemberLimits(ctx, teamCtx.Team.ID, daily, weekly, monthly); err != nil {
		return nil, err
	}
	return s.repo.GetContextByTeamID(ctx, teamCtx.Team.ID)
}

// SetStatus 允许 Owner 暂停或恢复自己的团队，恢复检查不能依赖团队当前为 active。
func (s *TeamService) SetStatus(ctx context.Context, userID int64, status string) (*TeamContext, error) {
	if status != TeamStatusActive && status != TeamStatusSuspended {
		return nil, infraerrors.BadRequest("TEAM_STATUS_INVALID", "无效的团队状态")
	}
	teamCtx, err := s.requireOwnerIncludingSuspended(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := s.repo.SetStatus(ctx, teamCtx.Team.ID, status); err != nil {
		return nil, err
	}
	s.invalidateTeamKeys(ctx, teamCtx.Team.ID)
	return s.repo.GetContextByUserID(ctx, userID)
}

func (s *TeamService) ListMembers(ctx context.Context, userID int64) ([]TeamMembership, error) {
	teamCtx, err := s.requireMembership(ctx, userID)
	if err != nil {
		return nil, err
	}
	members, err := s.repo.ListMembers(ctx, teamCtx.Team.ID)
	if err != nil {
		return nil, err
	}
	if teamCtx.Membership.Role == TeamRoleOwner {
		return members, nil
	}
	filtered := make([]TeamMembership, 0, 2)
	for _, member := range members {
		if member.UserID == userID || member.Role == TeamRoleOwner {
			filtered = append(filtered, member)
		}
	}
	return filtered, nil
}

// GetUsageSummary 根据当前成员角色收紧团队用量的可见范围。
func (s *TeamService) GetUsageSummary(ctx context.Context, userID int64, query TeamUsageQuery) (*TeamUsageSummary, error) {
	teamCtx, err := s.requireMembership(ctx, userID)
	if err != nil {
		return nil, err
	}
	query = NormalizeTeamUsageQueryAt(query, s.now())
	if teamCtx.Membership.Role != TeamRoleOwner {
		query.ActorUserID = &userID
	}
	return s.repo.GetUsageSummary(ctx, teamCtx.Team.ID, query)
}

// ListMemberUsageSeries 返回 Owner 的全团队成员趋势，Member 只能读取自己。
func (s *TeamService) ListMemberUsageSeries(ctx context.Context, userID int64, query TeamUsageQuery) ([]TeamMemberUsageSeries, error) {
	teamCtx, err := s.requireMembership(ctx, userID)
	if err != nil {
		return nil, err
	}
	query = NormalizeTeamUsageQueryAt(query, s.now())
	if teamCtx.Membership.Role != TeamRoleOwner {
		query.ActorUserID = &userID
	}
	return s.repo.ListMemberUsageSeries(ctx, teamCtx.Team.ID, query)
}

// ListUsageLogs 返回当前成员有权查看的团队 Key 用量明细。
func (s *TeamService) ListUsageLogs(ctx context.Context, userID int64, query TeamUsageQuery) (*TeamUsagePage, error) {
	teamCtx, err := s.requireMembership(ctx, userID)
	if err != nil {
		return nil, err
	}
	query = NormalizeTeamUsageQueryAt(query, s.now())
	if teamCtx.Membership.Role != TeamRoleOwner {
		query.ActorUserID = &userID
	}
	items, total, err := s.repo.ListUsageLogs(ctx, teamCtx.Team.ID, query)
	if err != nil {
		return nil, err
	}
	return &TeamUsagePage{Items: items, Total: total, Limit: query.Limit, Offset: query.Offset}, nil
}

// ListTeamKeys 返回角色允许查看的团队 Key，并统一生成脱敏展示值。
func (s *TeamService) ListTeamKeys(ctx context.Context, userID int64) ([]TeamAPIKeyItem, error) {
	teamCtx, err := s.requireMembershipIncludingSuspended(ctx, userID)
	if err != nil {
		return nil, err
	}
	actorFilter := TeamKeyActorFilter(teamCtx, userID)
	items, err := s.repo.ListTeamKeys(ctx, teamCtx.Team.ID, actorFilter)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].MaskedKey = logredact.MaskCredential(items[i].Key)
		items[i].Key = ""
	}
	return items, nil
}

// DisableTeamKey 由 Owner 锁定任意团队 Key，Member 的普通更新不能解除锁定。
func (s *TeamService) DisableTeamKey(ctx context.Context, userID, keyID int64) error {
	teamCtx, err := s.requireOwnerIncludingSuspended(ctx, userID)
	if err != nil {
		return err
	}
	key, err := s.repo.DisableTeamKey(ctx, teamCtx.Team.ID, keyID, nil)
	if err != nil {
		return err
	}
	s.invalidateKey(ctx, key)
	return nil
}

// EnableTeamKey 仅允许 Owner 清除团队锁定并恢复 Key。
func (s *TeamService) EnableTeamKey(ctx context.Context, userID, keyID int64) error {
	teamCtx, err := s.requireOwnerIncludingSuspended(ctx, userID)
	if err != nil {
		return err
	}
	key, err := s.repo.EnableTeamKey(ctx, teamCtx.Team.ID, keyID, nil)
	if err != nil {
		return err
	}
	s.invalidateKey(ctx, key)
	return nil
}

// DeleteTeamKey 软删除团队 Key 并立即清除认证缓存。
func (s *TeamService) DeleteTeamKey(ctx context.Context, userID, keyID int64) error {
	teamCtx, err := s.requireOwnerIncludingSuspended(ctx, userID)
	if err != nil {
		return err
	}
	key, err := s.repo.DeleteTeamKey(ctx, teamCtx.Team.ID, keyID, nil)
	if err != nil {
		return err
	}
	s.invalidateKey(ctx, key)
	return nil
}

func TeamKeyActorFilter(teamCtx *TeamContext, userID int64) *int64 {
	if teamCtx != nil && teamCtx.Membership != nil && teamCtx.Membership.Role == TeamRoleOwner {
		return nil
	}
	return &userID
}

// NormalizeTeamUsageQueryAt 让日期缺省值沿用调用入口的取时点。
func NormalizeTeamUsageQueryAt(query TeamUsageQuery, now time.Time) TeamUsageQuery {
	if query.To.IsZero() {
		query.To = now
	}
	if query.From.IsZero() {
		query.From = query.To.AddDate(0, 0, -30)
	}
	if query.From.After(query.To) {
		query.From, query.To = query.To, query.From
	}
	if query.Limit <= 0 || query.Limit > 100 {
		query.Limit = 20
	}
	if query.Offset < 0 {
		query.Offset = 0
	}
	return query
}

// Invite 创建绑定邮箱的邀请，明文令牌只用于本次邮件发送。
func (s *TeamService) Invite(ctx context.Context, userID int64, email string) (*TeamInvitation, error) {
	teamCtx, err := s.requireOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	email, err = NormalizeTeamEmail(email)
	if err != nil {
		return nil, err
	}
	if err := s.CheckInvitationRate(ctx, teamCtx.Team.ID, email); err != nil {
		return nil, err
	}
	token, tokenHash, err := NewTeamToken()
	if err != nil {
		return nil, err
	}
	link := ""
	if s.emailService != nil {
		link, err = s.FrontendLink(ctx, "/team", "invitation", token)
		if err != nil {
			return nil, err
		}
	}
	expiresAt := s.now().Add(7 * 24 * time.Hour)
	invitation, err := s.repo.CreateInvitation(ctx, teamCtx.Team.ID, userID, email, tokenHash, expiresAt)
	if err != nil {
		return nil, err
	}
	if err := s.SendInvitationEmail(ctx, email, teamCtx.Team.Name, link, expiresAt); err != nil {
		return nil, err
	}
	return invitation, nil
}

func (s *TeamService) ListInvitations(ctx context.Context, userID int64) ([]TeamInvitation, error) {
	teamCtx, err := s.requireOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.repo.ListInvitations(ctx, teamCtx.Team.ID)
}

func (s *TeamService) ReissueInvitation(ctx context.Context, userID, invitationID int64) (*TeamInvitation, error) {
	teamCtx, err := s.requireOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	existing, err := s.repo.GetInvitationByID(ctx, teamCtx.Team.ID, invitationID)
	if err != nil {
		return nil, err
	}
	if err := s.CheckInvitationRate(ctx, teamCtx.Team.ID, existing.Email); err != nil {
		return nil, err
	}
	token, tokenHash, err := NewTeamToken()
	if err != nil {
		return nil, err
	}
	link := ""
	if s.emailService != nil {
		link, err = s.FrontendLink(ctx, "/team", "invitation", token)
		if err != nil {
			return nil, err
		}
	}
	expiresAt := s.now().Add(7 * 24 * time.Hour)
	invitation, err := s.repo.ReissueInvitation(ctx, teamCtx.Team.ID, invitationID, tokenHash, expiresAt)
	if err != nil {
		return nil, err
	}
	if err := s.SendInvitationEmail(ctx, invitation.Email, teamCtx.Team.Name, link, expiresAt); err != nil {
		return nil, err
	}
	return invitation, nil
}

func (s *TeamService) CheckInvitationRate(ctx context.Context, teamID int64, email string) error {
	if s.inviteLimiter == nil {
		return ErrTeamInvitationUnavailable
	}
	allowed, retryAfter, err := s.inviteLimiter.CheckAndRecord(ctx, teamID, email)
	if err != nil {
		slog.Warn("团队邀请限流器不可用", "team_id", teamID, "error", err)
		return ErrTeamInvitationUnavailable
	}
	if allowed {
		return nil
	}
	retrySeconds := int64((retryAfter + time.Second - 1) / time.Second)
	if retrySeconds < 1 {
		retrySeconds = 1
	}
	return ErrTeamInvitationRateLimited.WithMetadata(map[string]string{"retry_after": strconv.FormatInt(retrySeconds, 10)})
}

func (s *TeamService) RevokeInvitation(ctx context.Context, userID, invitationID int64) error {
	teamCtx, err := s.requireOwner(ctx, userID)
	if err != nil {
		return err
	}
	return s.repo.RevokeInvitation(ctx, teamCtx.Team.ID, invitationID)
}

// PreviewInvitation 校验令牌归属后返回邀请弹窗所需的信息。
func (s *TeamService) PreviewInvitation(ctx context.Context, userID int64, token string) (*TeamInvitationPreview, error) {
	if err := s.ensureEnabled(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, ErrTeamInvitationInvalid
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.repo.PreviewInvitation(
		ctx,
		HashTeamToken(token),
		identity.NormalizeRegistrationEmailAddress(user.Email),
		s.now(),
	)
}

func (s *TeamService) ResolveInvitation(ctx context.Context, userID int64, token, resolution string) (*TeamContext, error) {
	if err := s.ensureEnabled(); err != nil {
		return nil, err
	}
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if resolution != "accepted" && resolution != "declined" {
		return nil, ErrTeamInvitationInvalid
	}
	return s.repo.ResolveInvitation(ctx, HashTeamToken(token), userID, identity.NormalizeRegistrationEmailAddress(user.Email), resolution, s.now())
}

func (s *TeamService) RemoveMember(ctx context.Context, ownerUserID, memberUserID int64) error {
	teamCtx, err := s.requireOwner(ctx, ownerUserID)
	if err != nil {
		return err
	}
	if ownerUserID == memberUserID {
		return ErrTeamOwnerCannotLeave
	}
	if err := s.repo.RemoveMember(ctx, teamCtx.Team.ID, memberUserID, s.now()); err != nil {
		return err
	}
	s.invalidateTeamKeys(ctx, teamCtx.Team.ID)
	return nil
}

func (s *TeamService) Leave(ctx context.Context, userID int64) error {
	teamCtx, err := s.requireMembershipIncludingSuspended(ctx, userID)
	if err != nil {
		return err
	}
	if teamCtx.Membership.Role == TeamRoleOwner {
		return ErrTeamOwnerCannotLeave
	}
	if err := s.repo.RemoveMember(ctx, teamCtx.Team.ID, userID, s.now()); err != nil {
		return err
	}
	s.invalidateTeamKeys(ctx, teamCtx.Team.ID)
	return nil
}

func (s *TeamService) UpdateMemberLimits(ctx context.Context, ownerUserID, memberUserID int64, daily, weekly, monthly float64) error {
	teamCtx, err := s.requireOwner(ctx, ownerUserID)
	if err != nil {
		return err
	}
	if daily < 0 || weekly < 0 || monthly < 0 {
		return infraerrors.BadRequest("TEAM_MEMBER_LIMIT_INVALID", "成员限额不能为负数")
	}
	if err := s.repo.UpdateMemberLimits(ctx, teamCtx.Team.ID, memberUserID, daily, weekly, monthly); err != nil {
		return err
	}
	s.invalidateTeamKeys(ctx, teamCtx.Team.ID)
	return nil
}

func (s *TeamService) ResetMemberUsage(ctx context.Context, ownerUserID, memberUserID int64, daily, weekly, monthly bool) error {
	teamCtx, err := s.requireOwner(ctx, ownerUserID)
	if err != nil {
		return err
	}
	if !daily && !weekly && !monthly {
		return infraerrors.BadRequest("TEAM_USAGE_RESET_EMPTY", "至少选择一个需要重置的周期")
	}
	if err := s.repo.ResetMemberUsage(ctx, teamCtx.Team.ID, memberUserID, daily, weekly, monthly, s.now()); err != nil {
		return err
	}
	s.invalidateTeamKeys(ctx, teamCtx.Team.ID)
	return nil
}

func (s *TeamService) StartOwnershipTransfer(ctx context.Context, ownerUserID, targetUserID int64) (*TeamOwnershipTransfer, error) {
	teamCtx, err := s.requireOwner(ctx, ownerUserID)
	if err != nil {
		return nil, err
	}
	if ownerUserID == targetUserID {
		return nil, ErrTeamTransferInvalid
	}
	token, tokenHash, err := NewTeamToken()
	if err != nil {
		return nil, err
	}
	var targetEmail, link string
	if s.emailService != nil {
		target, getErr := s.userRepo.GetByID(ctx, targetUserID)
		if getErr == nil {
			link, err = s.FrontendLink(ctx, "/team", "transfer", token)
			if err != nil {
				return nil, err
			}
			targetEmail = target.Email
		}
	}
	expiresAt := s.now().Add(24 * time.Hour)
	transfer, err := s.repo.CreateOwnershipTransfer(ctx, teamCtx.Team.ID, ownerUserID, targetUserID, tokenHash, expiresAt)
	if err != nil {
		return nil, err
	}
	if targetEmail != "" {
		if sendErr := s.emailService.SendOwnershipTransfer(ctx, targetEmail, teamCtx.Team.Name, link); sendErr != nil {
			return nil, sendErr
		}
	}
	return transfer, nil
}

func (s *TeamService) ResolveOwnershipTransfer(ctx context.Context, userID int64, token, resolution string) (*TeamContext, error) {
	if err := s.ensureEnabled(); err != nil {
		return nil, err
	}
	if resolution != "accepted" && resolution != "declined" {
		return nil, ErrTeamTransferInvalid
	}
	teamCtx, err := s.repo.ResolveOwnershipTransfer(ctx, HashTeamToken(token), userID, resolution, s.now())
	if err == nil && teamCtx != nil {
		s.invalidateTeamKeys(ctx, teamCtx.Team.ID)
	}
	return teamCtx, err
}

func (s *TeamService) CancelOwnershipTransfer(ctx context.Context, ownerUserID int64) error {
	teamCtx, err := s.requireOwner(ctx, ownerUserID)
	if err != nil {
		return err
	}
	return s.repo.CancelOwnershipTransfer(ctx, teamCtx.Team.ID, ownerUserID, s.now())
}

func (s *TeamService) Dissolve(ctx context.Context, ownerUserID int64) error {
	teamCtx, err := s.requireOwnerIncludingSuspended(ctx, ownerUserID)
	if err != nil {
		return err
	}
	if err := s.repo.Dissolve(ctx, teamCtx.Team.ID, s.now()); err != nil {
		return err
	}
	s.invalidateTeamKeys(ctx, teamCtx.Team.ID)
	return nil
}

func (s *TeamService) AdminSetStatus(ctx context.Context, teamID int64, status string) error {
	if status != TeamStatusActive && status != TeamStatusSuspended {
		return infraerrors.BadRequest("TEAM_STATUS_INVALID", "无效的团队状态")
	}
	if err := s.repo.SetStatus(ctx, teamID, status); err != nil {
		return err
	}
	s.invalidateTeamKeys(ctx, teamID)
	return nil
}

// AdminUpdateName 允许平台管理员修正团队名称，并复用统一的名称校验规则。
func (s *TeamService) AdminUpdateName(ctx context.Context, teamID int64, name string) error {
	name, err := NormalizeTeamName(name)
	if err != nil {
		return err
	}
	return s.repo.UpdateName(ctx, teamID, name)
}

func (s *TeamService) AdminSetMemberLimit(ctx context.Context, teamID int64, limit int) error {
	if limit < 0 {
		return infraerrors.BadRequest("TEAM_MEMBER_LIMIT_INVALID", "团队成员上限不能为负数")
	}
	return s.repo.SetMemberLimit(ctx, teamID, limit)
}

// AdminUpdate 在写入前校验所有字段，再由仓储使用一条语句原子更新。
func (s *TeamService) AdminUpdate(ctx context.Context, teamID int64, update TeamAdminUpdate) (*TeamContext, error) {
	if update.Name != nil {
		name, err := NormalizeTeamName(*update.Name)
		if err != nil {
			return nil, err
		}
		update.Name = &name
	}
	if update.Status != nil && *update.Status != TeamStatusActive && *update.Status != TeamStatusSuspended {
		return nil, infraerrors.BadRequest("TEAM_STATUS_INVALID", "无效的团队状态")
	}
	if update.MemberLimit != nil && *update.MemberLimit < 0 {
		return nil, infraerrors.BadRequest("TEAM_MEMBER_LIMIT_INVALID", "团队成员上限不能为负数")
	}
	if update.Name == nil && update.Status == nil && update.MemberLimit == nil {
		return s.repo.GetContextByTeamID(ctx, teamID)
	}
	if err := s.repo.UpdateAdmin(ctx, teamID, update); err != nil {
		return nil, err
	}
	if update.Status != nil {
		s.invalidateTeamKeys(ctx, teamID)
	}
	return s.repo.GetContextByTeamID(ctx, teamID)
}

func (s *TeamService) AdminForceTransfer(ctx context.Context, teamID, targetUserID int64) (*TeamContext, error) {
	teamCtx, err := s.repo.ForceTransfer(ctx, teamID, targetUserID, s.now())
	if err == nil {
		s.invalidateTeamKeys(ctx, teamID)
	}
	return teamCtx, err
}

func (s *TeamService) AdminList(ctx context.Context) ([]TeamAdminListItem, error) {
	return s.repo.ListAdmin(ctx)
}

func (s *TeamService) AdminGet(ctx context.Context, teamID int64) (*TeamContext, error) {
	return s.repo.GetContextByTeamID(ctx, teamID)
}

func (s *TeamService) AdminListMembers(ctx context.Context, teamID int64) ([]TeamMembership, error) {
	if _, err := s.repo.GetContextByTeamID(ctx, teamID); err != nil {
		return nil, err
	}
	return s.repo.ListMembers(ctx, teamID)
}

func (s *TeamService) AdminGetUsageSummary(ctx context.Context, teamID int64, query TeamUsageQuery) (*TeamUsageSummary, error) {
	if _, err := s.repo.GetContextByTeamID(ctx, teamID); err != nil {
		return nil, err
	}
	return s.repo.GetUsageSummary(ctx, teamID, NormalizeTeamUsageQueryAt(query, s.now()))
}

func (s *TeamService) AdminDissolve(ctx context.Context, teamID int64) error {
	if err := s.repo.Dissolve(ctx, teamID, s.now()); err != nil {
		return err
	}
	s.invalidateTeamKeys(ctx, teamID)
	return nil
}

func (s *TeamService) requireMembership(ctx context.Context, userID int64) (*TeamContext, error) {
	teamCtx, err := s.requireMembershipIncludingSuspended(ctx, userID)
	if err != nil {
		return nil, err
	}
	if teamCtx.Team.Status != TeamStatusActive {
		return nil, ErrTeamSuspended
	}
	return teamCtx, nil
}

func (s *TeamService) requireMembershipIncludingSuspended(ctx context.Context, userID int64) (*TeamContext, error) {
	if err := s.ensureEnabled(); err != nil {
		return nil, err
	}
	teamCtx, err := s.repo.GetContextByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if teamCtx == nil || teamCtx.Team == nil || teamCtx.Membership == nil {
		return nil, ErrTeamMembershipRequired
	}
	return teamCtx, nil
}

func (s *TeamService) requireOwner(ctx context.Context, userID int64) (*TeamContext, error) {
	teamCtx, err := s.requireMembership(ctx, userID)
	if err != nil {
		return nil, err
	}
	if teamCtx.Membership.Role != TeamRoleOwner {
		return nil, ErrTeamOwnerRequired
	}
	return teamCtx, nil
}

func (s *TeamService) requireOwnerIncludingSuspended(ctx context.Context, userID int64) (*TeamContext, error) {
	teamCtx, err := s.requireMembershipIncludingSuspended(ctx, userID)
	if err != nil {
		return nil, err
	}
	if teamCtx.Membership.Role != TeamRoleOwner {
		return nil, ErrTeamOwnerRequired
	}
	return teamCtx, nil
}

func (s *TeamService) invalidateTeamKeys(ctx context.Context, teamID int64) {
	if s.apiKeyCache == nil || s.repo == nil {
		return
	}
	keys, err := s.repo.ListTeamKeyStrings(ctx, teamID)
	if err != nil {
		return
	}
	for _, key := range keys {
		s.apiKeyCache.InvalidateAuthCacheByKey(ctx, key)
	}
}

func (s *TeamService) invalidateKey(ctx context.Context, key string) {
	if s.apiKeyCache == nil || strings.TrimSpace(key) == "" {
		return
	}
	s.apiKeyCache.InvalidateAuthCacheByKey(ctx, key)
}

func (s *TeamService) FrontendLink(ctx context.Context, path, parameter, token string) (string, error) {
	base := ""
	// 管理后台保存的动态设置优先，未配置时兼容启动配置中的 server.frontend_url。
	if s.settingService != nil {
		base = strings.TrimRight(strings.TrimSpace(s.settingService.GetFrontendURL(ctx)), "/")
	} else if s.options != nil {
		base = strings.TrimRight(strings.TrimSpace(s.options.FrontendURL), "/")
	}
	if base == "" {
		return "", ErrTeamFrontendURLUnavailable
	}
	return base + path + "?" + url.QueryEscape(parameter) + "=" + url.QueryEscape(token), nil
}

func NormalizeTeamName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 100 {
		return "", infraerrors.BadRequest("TEAM_NAME_INVALID", "团队名称长度必须为 1 到 100 个字符")
	}
	return name, nil
}

func NormalizeTeamEmail(email string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	parsed, err := mail.ParseAddress(email)
	if err != nil || !strings.EqualFold(parsed.Address, email) {
		return "", infraerrors.BadRequest("TEAM_INVITATION_EMAIL_INVALID", "请输入有效的邮箱地址")
	}
	return identity.NormalizeRegistrationEmailAddress(email), nil
}

func NewTeamToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("生成团队令牌失败: %w", err)
	}
	token := hex.EncodeToString(raw)
	return token, HashTeamToken(token), nil
}

func HashTeamToken(token string) string {
	hash := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(hash[:])
}

// Options 保留启动团队开关与默认值，动态前端地址单独读取。
type Options struct {
	// Now 保持原取时点，生产由 app 注入；旧构造器未指定时使用系统时钟。
	Now func() time.Time

	Enabled, SelfServiceEnabled bool
	DefaultMemberLimit          int
	FrontendURL                 string
}
type UserSnapshot struct {
	ID              int64
	Email, Username string
}
type UserReader interface {
	GetByID(context.Context, int64) (*UserSnapshot, error)
	GetByEmail(context.Context, string) (*UserSnapshot, error)
}
type Settings interface{ GetFrontendURL(context.Context) string }
type KeyInvalidator interface{ InvalidateAuthCacheByKey(context.Context, string) }
type Notifier interface {
	SendInvitation(context.Context, string, string, string, time.Time) error
	SendOwnershipTransfer(context.Context, string, string, string) error
}

func NewTeamService(repo TeamRepository, users UserReader, notifier Notifier, keys KeyInvalidator, limiter TeamInvitationLimiter, settings Settings, options *Options) *TeamService {
	return &TeamService{repo: repo, userRepo: users, emailService: notifier, apiKeyCache: keys, inviteLimiter: limiter, settingService: settings, options: options}
}
func (s *TeamService) SendInvitationEmail(ctx context.Context, email, teamName, link string, expiresAt time.Time) error {
	if s.emailService == nil {
		return nil
	}
	if strings.TrimSpace(link) == "" {
		return ErrTeamFrontendURLUnavailable
	}
	return s.emailService.SendInvitation(ctx, email, teamName, link, expiresAt)
}

func (s *TeamService) now() time.Time {
	if s.options != nil && s.options.Now != nil {
		return s.options.Now()
	}
	return time.Now()
}

// NormalizeTeamUsageQuery 为旧用量入口保留系统时钟的缺省行为。
func NormalizeTeamUsageQuery(query TeamUsageQuery) TeamUsageQuery {
	return NormalizeTeamUsageQueryAt(query, time.Now())
}
