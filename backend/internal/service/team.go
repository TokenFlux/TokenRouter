// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	team "github.com/TokenFlux/TokenRouter/internal/team"
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
		notifier = team.NewEmailNotifications(emailService.Mailer, teamNotificationRecipients(userRepo))
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

// NewTeamNotificationDelivery 保留尚未迁入通知模块的模板及发送规则，S10 改绑。
func NewTeamNotificationDelivery(email *EmailService, users UserRepository) team.Notifier {
	if email == nil {
		return nil
	}
	return team.NewEmailNotifications(email.Mailer, teamNotificationRecipients(users))
}

func teamNotificationRecipients(users UserRepository) team.InvitationRecipientReader {
	if users == nil {
		return nil
	}
	return legacyTeamUsers{Repository: users}
}
