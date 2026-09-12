// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	fmt "fmt"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	team "github.com/TokenFlux/TokenRouter/internal/team"
	html "html"
	slog "log/slog"
	strings "strings"
	time "time"
)

const TeamStatusActive = team.TeamStatusActive

const TeamStatusSuspended = team.TeamStatusSuspended

const TeamRoleOwner = team.TeamRoleOwner

const TeamRoleMember = team.TeamRoleMember

var ErrTeamFeatureDisabled = team.ErrTeamFeatureDisabled

var ErrTeamSelfServiceDisabled = team.ErrTeamSelfServiceDisabled

var ErrTeamNotFound = team.ErrTeamNotFound

var ErrTeamMembershipRequired = team.ErrTeamMembershipRequired

var ErrTeamOwnerRequired = team.ErrTeamOwnerRequired

var ErrTeamAlreadyJoined = team.ErrTeamAlreadyJoined

var ErrTeamMemberLimitReached = team.ErrTeamMemberLimitReached

var ErrTeamInvitationInvalid = team.ErrTeamInvitationInvalid

var ErrTeamInvitationExpired = team.ErrTeamInvitationExpired

var ErrTeamInvitationEmail = team.ErrTeamInvitationEmail

var ErrTeamInvitationRateLimited = team.ErrTeamInvitationRateLimited

var ErrTeamInvitationUnavailable = team.ErrTeamInvitationUnavailable

var ErrTeamFrontendURLUnavailable = team.ErrTeamFrontendURLUnavailable

var ErrTeamOwnerCannotLeave = team.ErrTeamOwnerCannotLeave

var ErrTeamOwnerTransferRequired = team.ErrTeamOwnerTransferRequired

var ErrTeamTransferInvalid = team.ErrTeamTransferInvalid

var ErrTeamTransferExpired = team.ErrTeamTransferExpired

var ErrTeamSuspended = team.ErrTeamSuspended

var ErrTeamMemberDailyExceeded = team.ErrTeamMemberDailyExceeded

var ErrTeamMemberWeeklyExceeded = team.ErrTeamMemberWeeklyExceeded

var ErrTeamMemberMonthlyExceeded = team.ErrTeamMemberMonthlyExceeded

type Team = team.Team

type TeamMembership = team.TeamMembership

type TeamContext = team.TeamContext

type TeamInvitation = team.TeamInvitation

type TeamInvitationPreview = team.TeamInvitationPreview

type TeamInvitationLimiter = team.TeamInvitationLimiter

type TeamOwnershipTransfer = team.TeamOwnershipTransfer

type TeamAdminListItem = team.TeamAdminListItem

type TeamAdminUpdate = team.TeamAdminUpdate

type TeamUsageQuery = team.TeamUsageQuery

type TeamUsageDaily = team.TeamUsageDaily

type TeamUsageSummary = team.TeamUsageSummary

type TeamMemberUsageSeries = team.TeamMemberUsageSeries

type TeamUsageLogItem = team.TeamUsageLogItem

type TeamUsagePage = team.TeamUsagePage

type TeamAPIKeyItem = team.TeamAPIKeyItem

type TeamRepository = team.TeamRepository

type TeamService = team.TeamService

func NewTeamService(repo TeamRepository, userRepo UserRepository, emailService *EmailService, apiKeyCache APIKeyCache, inviteLimiter TeamInvitationLimiter, settingService *SettingService, cfg *config.Config) *TeamService {
	var opts *team.Options
	if cfg != nil {
		opts = &team.Options{Enabled: cfg.Team.Enabled, SelfServiceEnabled: cfg.Team.SelfServiceEnabled, DefaultMemberLimit: cfg.Team.DefaultMemberLimit, FrontendURL: cfg.Server.FrontendURL}
	}
	var notifier team.Notifier
	if emailService != nil {
		notifier = legacyTeamNotifier{emailService: emailService, userRepo: userRepo}
	}
	var settings team.Settings
	if settingService != nil {
		settings = settingService
	}
	var keys team.KeyInvalidator
	if apiKeyCache != nil {
		keys = legacyTeamKeys{cache: apiKeyCache}
	}
	return team.NewTeamService(repo, legacyTeamUsers{Repository: userRepo}, notifier, keys, inviteLimiter, settings, opts)
}

type legacyTeamUsers struct{ Repository UserRepository }

func (p legacyTeamUsers) GetByID(ctx context.Context, id int64) (*team.UserSnapshot, error) {
	u, e := p.Repository.GetByID(ctx, id)
	if u == nil {
		return nil, e
	}
	return &team.UserSnapshot{ID: u.ID, Email: u.Email, Username: u.Username}, e
}
func (p legacyTeamUsers) GetByEmail(ctx context.Context, email string) (*team.UserSnapshot, error) {
	u, e := p.Repository.GetByEmail(ctx, email)
	if u == nil {
		return nil, e
	}
	return &team.UserSnapshot{ID: u.ID, Email: u.Email, Username: u.Username}, e
}

type legacyTeamKeys struct{ cache APIKeyCache }

func (p legacyTeamKeys) InvalidateAuthCacheByKey(ctx context.Context, key string) {
	apikey.PublishedAuthCacheInvalidator{Cache: p.cache}.InvalidateAuthCacheByKey(ctx, key)
}

type legacyTeamNotifier struct {
	emailService *EmailService
	userRepo     UserRepository
}

func (s legacyTeamNotifier) SendInvitation(ctx context.Context, email, teamName, link string, expiresAt time.Time) error {
	if s.emailService == nil {
		return nil
	}
	if strings.TrimSpace(link) == "" {
		return ErrTeamFrontendURLUnavailable
	}
	recipientName := emailRecipientName(email)
	var recipientUserID int64
	if s.userRepo != nil {
		if user, err := s.userRepo.GetByEmail(ctx, email); err == nil && user != nil {
			recipientUserID = user.ID
			if strings.TrimSpace(user.Username) != "" {
				recipientName = strings.TrimSpace(user.Username)
			}
		}
	}

	// 团队邀请优先走统一模板系统，允许管理员在邮件设置中自定义主题和正文。
	if s.emailService.notificationEmailService != nil {
		err := s.emailService.notificationEmailService.Send(ctx, NotificationEmailSendInput{
			Event:          NotificationEmailEventTeamInvitation,
			RecipientEmail: email,
			RecipientName:  recipientName,
			UserID:         recipientUserID,
			Variables: map[string]string{
				"team_name":      teamName,
				"invitation_url": link,
				"expires_at":     expiresAt.Format(time.RFC3339),
			},
		})
		if err == nil {
			return nil
		}
		if !shouldFallbackNotificationEmail(err) {
			return err
		}
		slog.Warn("failed to send templated team invitation email, falling back to legacy template", "recipient_hash", notificationEmailHash(email), "error", err)
	}

	body := fmt.Sprintf("<p>你被邀请加入团队 <strong>%s</strong>。</p><p><a href=\"%s\">查看并处理邀请</a></p><p>邀请有效期至 %s。</p>", html.EscapeString(teamName), html.EscapeString(link), expiresAt.Format(time.RFC3339))
	return s.emailService.SendEmail(ctx, email, "团队邀请", body)
}
func (s legacyTeamNotifier) SendOwnershipTransfer(ctx context.Context, email, teamName, link string) error {
	body := fmt.Sprintf("<p>你收到团队 <strong>%s</strong> 的所有权转让请求。</p><p><a href=\"%s\">确认或拒绝转让</a></p>", html.EscapeString(teamName), html.EscapeString(link))
	return s.emailService.SendEmail(ctx, email, "团队所有权转让", body)
}

// NewTeamNotificationDelivery 保留尚未迁入通知模块的模板及发送规则，S10 改绑。
func NewTeamNotificationDelivery(email *EmailService, users UserRepository) team.Notifier {
	if email == nil {
		return nil
	}
	return legacyTeamNotifier{emailService: email, userRepo: users}
}
